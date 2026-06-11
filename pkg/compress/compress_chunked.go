// pkg/compress/compress_chunked.go
package compress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/creativeyann17/go-delta/internal/chunker"
	"github.com/creativeyann17/go-delta/internal/chunkstore"
	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

// compressWithChunking performs compression with chunk-level deduplication (GDELTA02)
func compressWithChunking(opts *Options, progressCb ProgressCallback, filesToCompress []folderTask, totalFiles int, totalOrigSize uint64, result *Result, parallelism Parallelism) error {
	// Calculate max chunks for bounded store
	maxChunks := 0
	if opts.ChunkStoreSize > 0 && opts.ChunkSize > 0 {
		// ChunkStoreSize is in MB, convert to bytes
		storeSizeBytes := opts.ChunkStoreSize * 1024 * 1024

		// Account for memory overhead per chunk:
		// - Compressed chunk data: ~ChunkSize (varies, but use full size for safety)
		// - ChunkInfo struct: ~56 bytes (Hash + Offset + CompressedSize + OriginalSize)
		// - chunkEntry overhead: ~16 bytes (refCount + lruNode pointer)
		// - list.Element: ~32 bytes (prev/next pointers + value interface)
		// - Map entry: ~16 bytes
		// Total overhead: ~120 bytes per chunk
		const overheadPerChunk = 120
		effectiveBytesPerChunk := opts.ChunkSize + overheadPerChunk

		maxChunks = int(storeSizeBytes / effectiveBytesPerChunk)
		if maxChunks < 1 {
			maxChunks = 1 // At least 1 chunk
		}
	}

	// Create chunk store for deduplication with capacity limit
	store := chunkstore.NewStoreWithCapacity(maxChunks)
	chunkerInstance := chunker.New(opts.ChunkSize)

	// Metadata for files (will be written to archive)
	var fileMetadataList []format.FileMetadata
	var metadataMu sync.Mutex

	// Create archive file and temporary file for chunk data
	var writer io.WriteSeeker
	var chunkDataFile *os.File
	var chunkDataWriter io.Writer
	currentChunkOffset := uint64(0)
	var chunkOffsetMu sync.Mutex

	if !opts.DryRun {
		// Ensure output directory exists
		outputDir := filepath.Dir(opts.OutputPath)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}

		outFile, err := os.Create(opts.OutputPath)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer outFile.Close()
		writer = outFile

		// Create temporary file for chunk data
		// Note: no signal handler here — a library must not call os.Exit or
		// install process-wide handlers; interrupt cleanup is the CLI's job.
		chunkDataFile, err = os.CreateTemp("", "godelta-chunks-*.tmp")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		tempFilePath := chunkDataFile.Name()
		defer func() {
			chunkDataFile.Close()
			os.Remove(tempFilePath)
		}()

		chunkDataWriter = chunkDataFile
	}

	// Process files with worker pool
	var processedCount atomic.Uint32
	var errorsMu sync.Mutex

	var wg sync.WaitGroup

	// Worker function to process a single file task
	processFileTask := func(task fileTask, workerID int, enc *zstd.Encoder) {
		// Skip progress bar for 0-byte files (no progress to show)
		if progressCb != nil && task.OrigSize > 0 {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: task.RelPath,
				Total:    int64(task.OrigSize),
			})
		}

		if opts.DryRun {
			// Dry-run: chunk the file and track dedup stats without writing
			file, err := os.Open(task.AbsPath)
			if err != nil {
				errorsMu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
				errorsMu.Unlock()
				return
			}

			// Use streaming callback to avoid loading all chunks into memory
			err = chunkerInstance.SplitWithCallback(file, func(chunk chunker.Chunk) error {
				// Estimate compressed size as 50% of original (typical for zstd)
				estimatedComprSize := chunk.OrigSize / 2
				if estimatedComprSize == 0 {
					estimatedComprSize = 1
				}
				_, _, err := store.GetOrAdd(chunk.Hash, chunk.OrigSize, func() (uint64, uint64, error) {
					// No-op writeFunc for dry-run - just return estimated values
					chunkOffsetMu.Lock()
					offset := currentChunkOffset
					currentChunkOffset += estimatedComprSize
					chunkOffsetMu.Unlock()
					return offset, estimatedComprSize, nil
				})
				return err
			})
			file.Close()

			if err != nil {
				errorsMu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
				errorsMu.Unlock()
				return
			}
		} else {
			// Real compression with chunking
			metadata, err := compressFileChunked(
				task,
				chunkerInstance,
				store,
				chunkDataWriter,
				&chunkOffsetMu,
				&currentChunkOffset,
				enc,
				progressCb,
			)

			if err != nil {
				errorsMu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
				errorsMu.Unlock()
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventError,
						FilePath: task.RelPath,
					})
				}
				return
			}

			if opts.Verbose && len(metadata.ChunkHashes) > 0 {
				fmt.Printf("  [Worker %d] %s: %d chunks\n", workerID, task.RelPath, len(metadata.ChunkHashes))
			}

			// Store file metadata
			metadataMu.Lock()
			fileMetadataList = append(fileMetadataList, metadata)
			metadataMu.Unlock()
		}

		processedCount.Add(1)
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileComplete,
				FilePath: task.RelPath,
				Current:  int64(task.OrigSize),
				Total:    int64(task.OrigSize),
			})
		}
	}

	// newChunkEncoder creates the per-worker encoder used via EncodeAll on
	// small chunks; internal concurrency of 1 avoids goroutine oversubscription.
	newChunkEncoder := func() (*zstd.Encoder, error) {
		return zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(opts.Level)),
			zstd.WithZeroFrames(true),
			zstd.WithEncoderConcurrency(1),
		)
	}

	if parallelism == ParallelismFolder {
		// Folder-based parallelism: workers grab whole folders
		folderCh := make(chan folderTask, len(filesToCompress))

		for i := 0; i < opts.MaxThreads; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()

				enc, err := newChunkEncoder()
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("create zstd encoder: %w", err))
					errorsMu.Unlock()
					return
				}
				defer enc.Close()

				for folder := range folderCh {
					for _, task := range folder.Files {
						processFileTask(task, workerID, enc)
					}
				}
			}(i + 1)
		}

		// Feed folder tasks
		go func() {
			for _, task := range filesToCompress {
				folderCh <- task
			}
			close(folderCh)
		}()
	} else {
		// File-based parallelism: shared work queue, workers pull as they free up
		taskCh := feedTasks(filesToCompress, opts.MaxThreads*16)

		for i := 0; i < opts.MaxThreads; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()

				enc, err := newChunkEncoder()
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("create zstd encoder: %w", err))
					errorsMu.Unlock()
					return
				}
				defer enc.Close()

				for task := range taskCh {
					processFileTask(task, workerID, enc)
				}
			}(i + 1)
		}
	}

	wg.Wait()

	// Flush temp file to ensure all data is written
	if chunkDataFile != nil {
		if err := chunkDataFile.Sync(); err != nil {
			return fmt.Errorf("sync temp file: %w", err)
		}
	}

	// Write GDELTA02 archive
	if !opts.DryRun && writer != nil {
		chunkIndex := store.All()

		if opts.Verbose {
			fmt.Printf("\nWriting GDELTA02 archive...\n")
			fmt.Printf("  Files: %d\n", len(fileMetadataList))
			fmt.Printf("  Unique chunks: %d\n", len(chunkIndex))
			if chunkDataFile != nil {
				// Get temp file size
				tempFileInfo, err := chunkDataFile.Stat()
				if err == nil {
					tempSizeMB := float64(tempFileInfo.Size()) / (1024 * 1024)
					fmt.Printf("  Temp file size: %.2f MiB (compressed chunks)\n", tempSizeMB)
				}
			}
		}

		// Write header
		if err := format.WriteGDelta02Header(writer, opts.ChunkSize, uint32(len(fileMetadataList)), uint32(len(chunkIndex))); err != nil {
			return fmt.Errorf("write header: %w", err)
		}

		// Write chunk index (chunkstore.ChunkInfo is now an alias for format.ChunkInfo)
		if err := format.WriteChunkIndex(writer, chunkIndex); err != nil {
			return fmt.Errorf("write chunk index: %w", err)
		}

		// Write file metadata
		for _, metadata := range fileMetadataList {
			if err := format.WriteFileMetadata(writer, metadata); err != nil {
				return fmt.Errorf("write file metadata: %w", err)
			}
		}

		// Copy chunk data from temp file to main archive
		if chunkDataFile != nil {
			// Seek to beginning of temp file
			if _, err := chunkDataFile.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("seek temp file: %w", err)
			}

			// Copy all chunk data
			if _, err := io.Copy(writer, chunkDataFile); err != nil {
				return fmt.Errorf("copy chunk data: %w", err)
			}
		}

		// Write footer
		if err := format.WriteArchiveFooter02(writer); err != nil {
			return fmt.Errorf("write footer: %w", err)
		}

		// Get final archive size (includes all metadata + chunk data)
		if file, ok := writer.(*os.File); ok {
			if fileInfo, err := file.Stat(); err == nil {
				result.CompressedSize = uint64(fileInfo.Size())
			}
		}
	}

	// Update result with stats
	result.FilesProcessed = int(processedCount.Load())

	stats := store.Stats()
	result.TotalChunks = stats.TotalChunks
	result.UniqueChunks = stats.UniqueChunks
	result.DedupedChunks = stats.DedupedChunks
	result.BytesSaved = stats.BytesSaved

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:           EventComplete,
			Current:        int64(result.FilesProcessed),
			Total:          int64(result.FilesTotal),
			TotalBytes:     result.OriginalSize,
			CompressedSize: result.CompressedSize,
		})
	}

	return nil
}

// compressFileChunked compresses a file using chunking and deduplication
// Uses streaming processing to avoid loading entire file into memory
func compressFileChunked(
	task fileTask,
	chunkerInstance *chunker.Chunker,
	store *chunkstore.Store,
	writer io.Writer,
	writerMu *sync.Mutex,
	currentOffset *uint64,
	enc *zstd.Encoder,
	progressCb ProgressCallback,
) (format.FileMetadata, error) {
	// Open file
	file, err := os.Open(task.AbsPath)
	if err != nil {
		return format.FileMetadata{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	// Process chunks via streaming callback
	chunkHashes := make([][32]byte, 0, 8)
	bytesRead := uint64(0)
	var chunkErr error

	// Reusable buffer for compressed chunk data (EncodeAll appends into it)
	var compressBuf []byte

	err = chunkerInstance.SplitWithCallback(file, func(chunk chunker.Chunk) error {
		bytesRead += chunk.OrigSize

		// Report progress
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:         EventFileProgress,
				FilePath:     task.RelPath,
				Current:      int64(bytesRead),
				Total:        int64(task.OrigSize),
				CurrentBytes: bytesRead,
			})
		}

		// Try to deduplicate
		chunkInfo, _, err := store.GetOrAdd(chunk.Hash, chunk.OrigSize, func() (offset uint64, comprSize uint64, err error) {
			// Compress the chunk with the worker's reusable encoder
			compressedData := enc.EncodeAll(chunk.Data, compressBuf[:0])
			compressBuf = compressedData // keep grown capacity for next chunk

			// Write directly to file (if writer is provided)
			if writer != nil {
				writerMu.Lock()
				offset = *currentOffset
				if _, err := writer.Write(compressedData); err != nil {
					writerMu.Unlock()
					return 0, 0, fmt.Errorf("write chunk to file: %w", err)
				}
				*currentOffset += uint64(len(compressedData))
				writerMu.Unlock()
			} else {
				// Dry run - just calculate offset
				offset = *currentOffset
				*currentOffset += uint64(len(compressedData))
			}

			return offset, uint64(len(compressedData)), nil
		})

		if err != nil {
			chunkErr = fmt.Errorf("process chunk: %w", err)
			return chunkErr
		}

		chunkHashes = append(chunkHashes, chunkInfo.Hash)
		return nil
	})

	if err != nil {
		return format.FileMetadata{}, fmt.Errorf("split chunks: %w", err)
	}

	return format.FileMetadata{
		RelPath:     task.RelPath,
		OrigSize:    task.OrigSize,
		ChunkHashes: chunkHashes,
	}, nil
}
