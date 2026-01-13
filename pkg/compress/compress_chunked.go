// pkg/compress/compress_chunked.go
package compress

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/creativeyann17/go-delta/internal/chunker"
	"github.com/creativeyann17/go-delta/internal/chunkstore"
	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

// compressWithChunking performs compression with chunk-level deduplication (GDELTA02)
func compressWithChunking(opts *Options, progressCb ProgressCallback, filesToCompress []folderTask, totalFiles int, totalOrigSize uint64, result *Result) error {
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
		chunkDataFile, err = os.CreateTemp("", "godelta-chunks-*.tmp")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}

		// Track temp file path for signal handler
		tempFilePath := chunkDataFile.Name()

		// Cleanup function for temp file
		cleanupTempFile := func() {
			if chunkDataFile != nil {
				chunkDataFile.Close()
			}
			os.Remove(tempFilePath)
		}
		defer cleanupTempFile()

		// Setup signal handler to cleanup temp file on interruption
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigChan
			cleanupTempFile()
			os.Exit(1)
		}()

		chunkDataWriter = chunkDataFile
	}

	// Process files with worker pool
	var processedCount atomic.Uint32
	var errorsMu sync.Mutex

	var wg sync.WaitGroup
	folderCh := make(chan folderTask, len(filesToCompress))

	for i := 0; i < opts.MaxThreads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for folderTask := range folderCh {
				for _, fileTask := range folderTask.Files {
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:     EventFileStart,
							FilePath: fileTask.RelPath,
							Total:    int64(fileTask.OrigSize),
						})
					}

					if opts.DryRun {
						// Dry-run: just chunk the file without storing
						file, err := os.Open(fileTask.AbsPath)
						if err != nil {
							errorsMu.Lock()
							result.Errors = append(result.Errors, fmt.Errorf("%s: %w", fileTask.RelPath, err))
							errorsMu.Unlock()
							continue
						}
						chunks, err := chunkerInstance.Split(file)
						file.Close()

						if err != nil {
							errorsMu.Lock()
							result.Errors = append(result.Errors, fmt.Errorf("%s: %w", fileTask.RelPath, err))
							errorsMu.Unlock()
							continue
						}

						// Just count chunks for dry-run
						for range chunks {
							store.Stats() // Trigger counter increment
						}
					} else {
						// Real compression with chunking
						metadata, err := compressFileChunked(
							fileTask,
							chunkerInstance,
							store,
							chunkDataWriter,
							&chunkOffsetMu,
							&currentChunkOffset,
							opts.Level,
							progressCb,
						)

						if err != nil {
							errorsMu.Lock()
							result.Errors = append(result.Errors, fmt.Errorf("%s: %w", fileTask.RelPath, err))
							errorsMu.Unlock()
							if progressCb != nil {
								progressCb(ProgressEvent{
									Type:     EventError,
									FilePath: fileTask.RelPath,
								})
							}
							continue
						}

						if opts.Verbose && len(metadata.ChunkHashes) > 0 {
							fmt.Printf("  [Worker %d] %s: %d chunks\n", workerID, fileTask.RelPath, len(metadata.ChunkHashes))
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
							FilePath: fileTask.RelPath,
							Current:  int64(fileTask.OrigSize),
							Total:    int64(fileTask.OrigSize),
						})
					}
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

		// Convert chunkstore.ChunkInfo to format.ChunkInfo
		formatChunkIndex := make(map[[32]byte]format.ChunkInfo, len(chunkIndex))
		for hash, info := range chunkIndex {
			formatChunkIndex[hash] = format.ChunkInfo{
				Hash:           info.Hash,
				Offset:         info.Offset,
				CompressedSize: info.CompressedSize,
				OriginalSize:   info.OriginalSize,
			}
		}

		// Write header
		if err := format.WriteGDelta02Header(writer, opts.ChunkSize, uint32(len(fileMetadataList)), uint32(len(chunkIndex))); err != nil {
			return fmt.Errorf("write header: %w", err)
		}

		// Write chunk index
		if err := format.WriteChunkIndex(writer, formatChunkIndex); err != nil {
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
func compressFileChunked(
	task fileTask,
	chunkerInstance *chunker.Chunker,
	store *chunkstore.Store,
	writer io.Writer,
	writerMu *sync.Mutex,
	currentOffset *uint64,
	level int,
	progressCb ProgressCallback,
) (format.FileMetadata, error) {
	// Open file
	file, err := os.Open(task.AbsPath)
	if err != nil {
		return format.FileMetadata{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	// Split into chunks
	chunks, err := chunkerInstance.Split(file)
	if err != nil {
		return format.FileMetadata{}, fmt.Errorf("split chunks: %w", err)
	}

	// Process each chunk
	chunkHashes := make([][32]byte, 0, len(chunks))
	bytesRead := uint64(0)

	for _, chunk := range chunks {
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
		chunkInfo, isNew, err := store.GetOrAdd(chunk.Hash, chunk.OrigSize, func() (offset uint64, comprSize uint64, err error) {
			// Compress the chunk
			var compressed bytes.Buffer
			enc, err := zstd.NewWriter(&compressed,
				zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
				zstd.WithZeroFrames(true),
			)
			if err != nil {
				return 0, 0, fmt.Errorf("create zstd encoder: %w", err)
			}

			if _, err := enc.Write(chunk.Data); err != nil {
				enc.Close()
				return 0, 0, fmt.Errorf("compress chunk: %w", err)
			}

			if err := enc.Close(); err != nil {
				return 0, 0, fmt.Errorf("close encoder: %w", err)
			}

			compressedData := compressed.Bytes()

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
			return format.FileMetadata{}, fmt.Errorf("process chunk: %w", err)
		}

		if isNew {
			// New chunk stored
		} else {
			// Chunk deduplicated!
		}

		chunkHashes = append(chunkHashes, chunkInfo.Hash)
	}

	return format.FileMetadata{
		RelPath:     task.RelPath,
		OrigSize:    task.OrigSize,
		ChunkHashes: chunkHashes,
	}, nil
}
