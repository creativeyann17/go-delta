// pkg/compress/compress.go
package compress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

type fileTask struct {
	AbsPath  string
	RelPath  string
	Info     os.FileInfo
	OrigSize uint64
}

type folderTask struct {
	FolderPath string     // Relative folder path
	Files      []fileTask // Files in this folder
}

type compressedFile struct {
	RelPath        string
	OrigSize       uint64
	CompressedData []byte
}

// ProgressCallback is called for various progress events
type ProgressCallback func(event ProgressEvent)

// ProgressEvent contains progress information
type ProgressEvent struct {
	Type           EventType
	FilePath       string
	Current        int64
	Total          int64
	CurrentBytes   uint64
	TotalBytes     uint64
	CompressedSize uint64
}

// EventType indicates the type of progress event
type EventType int

const (
	EventStart EventType = iota
	EventFileStart
	EventFileProgress
	EventFileComplete
	EventComplete
	EventError
)

// Compress compresses files from inputPath into an archive at outputPath
func Compress(opts *Options, progressCb ProgressCallback) (*Result, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	result := &Result{}

	// Collect all files from either Files list or InputPath
	foldersToCompress, totalFiles, totalOrigSize, err := collectFiles(opts, result)
	if err != nil {
		return nil, err
	}

	if totalFiles == 0 {
		return nil, ErrNoFiles
	}

	result.FilesTotal = totalFiles
	result.OriginalSize = totalOrigSize

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:       EventStart,
			Total:      int64(totalFiles),
			TotalBytes: totalOrigSize,
		})
	}

	// Route to ZIP compression if UseZipFormat is enabled
	if opts.UseZipFormat {
		return result, compressToZip(opts, progressCb, foldersToCompress, totalFiles, totalOrigSize, result)
	}

	// Route to chunked compression if ChunkSize > 0
	if opts.ChunkSize > 0 {
		return result, compressWithChunking(opts, progressCb, foldersToCompress, totalFiles, totalOrigSize, result)
	}

	// Traditional GDELTA01 compression (file-level)

	// Create archive file (if not dry-run)
	var writer io.WriteSeeker
	var writerMu sync.Mutex

	if !opts.DryRun {
		// Ensure output directory exists
		outputDir := filepath.Dir(opts.OutputPath)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return nil, fmt.Errorf("create output directory: %w", err)
		}

		outFile, err := os.Create(opts.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("create output file: %w", err)
		}
		defer outFile.Close()

		writer = outFile

		// Write archive header
		if err := format.WriteArchiveHeader(writer, uint32(totalFiles)); err != nil {
			return nil, fmt.Errorf("write archive header: %w", err)
		}
	}

	// Process folders with worker pool
	var totalComprSize uint64
	var processedCount atomic.Uint32
	var errorsMu sync.Mutex

	var wg sync.WaitGroup
	folderCh := make(chan folderTask, len(foldersToCompress))

	for i := 0; i < opts.MaxThreads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Helper function to flush compressed files to disk
			flushToDisk := func(files []compressedFile) error {
				if len(files) == 0 {
					return nil
				}

				writerMu.Lock()
				defer writerMu.Unlock()

				for _, cf := range files {
					// Write file entry header
					entryStart, err := format.WriteFileEntry(writer, cf.RelPath, cf.OrigSize)
					if err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: write entry: %w", cf.RelPath, err))
						errorsMu.Unlock()
						continue
					}

					// Get data offset
					dataStart, err := writer.Seek(0, io.SeekCurrent)
					if err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: seek: %w", cf.RelPath, err))
						errorsMu.Unlock()
						continue
					}

					// Write compressed data
					_, err = writer.Write(cf.CompressedData)
					if err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: write data: %w", cf.RelPath, err))
						errorsMu.Unlock()
						continue
					}

					// Update entry with compressed size and offset
					err = format.UpdateFileEntry(writer, entryStart, uint64(len(cf.CompressedData)), uint64(dataStart))
					if err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: update entry: %w", cf.RelPath, err))
						errorsMu.Unlock()
					}
				}
				return nil
			}

			for folderTask := range folderCh {
				// Compress all files in this folder to memory (parallel work)
				compressedFiles := make([]compressedFile, 0, len(folderTask.Files))
				var folderComprSize uint64

				for _, fileTask := range folderTask.Files {
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:     EventFileStart,
							FilePath: fileTask.RelPath,
							Total:    int64(fileTask.OrigSize),
						})
					}

					var compressedData []byte
					var err error

					if opts.DryRun {
						// Dry-run mode: just compress to discard
						_, err = compressFileToWriter(fileTask, io.Discard, opts.Level, progressCb)
					} else {
						// Compress to memory buffer
						compressedData, err = compressFileToMemory(fileTask, opts.Level, progressCb)
					}

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

					if !opts.DryRun {
						compressedFiles = append(compressedFiles, compressedFile{
							RelPath:        fileTask.RelPath,
							OrigSize:       fileTask.OrigSize,
							CompressedData: compressedData,
						})
						folderComprSize += uint64(len(compressedData))

						// Check if we should flush due to memory threshold
						if opts.MaxThreadMemory > 0 && folderComprSize >= opts.MaxThreadMemory {
							// Flush current batch to disk
							flushToDisk(compressedFiles)
							atomic.AddUint64(&totalComprSize, folderComprSize)

							// Reset batch
							compressedFiles = make([]compressedFile, 0, len(folderTask.Files))
							folderComprSize = 0
						}
					}

					processedCount.Add(1)
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:           EventFileComplete,
							FilePath:       fileTask.RelPath,
							Current:        int64(fileTask.OrigSize),
							Total:          int64(fileTask.OrigSize),
							CompressedSize: uint64(len(compressedData)),
						})
					}
				}

				// Final flush for remaining files in this folder
				if !opts.DryRun && len(compressedFiles) > 0 {
					flushToDisk(compressedFiles)
				}

				atomic.AddUint64(&totalComprSize, folderComprSize)
			}
		}(i + 1)
	}

	// Feed folder tasks
	go func() {
		for _, task := range foldersToCompress {
			folderCh <- task
		}
		close(folderCh)
	}()

	wg.Wait()

	// Write archive footer (if not dry-run)
	if !opts.DryRun && writer != nil {
		if err := format.WriteArchiveFooter(writer); err != nil {
			return nil, fmt.Errorf("write archive footer: %w", err)
		}
	}

	result.FilesProcessed = int(processedCount.Load())
	result.CompressedSize = totalComprSize

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:           EventComplete,
			Current:        int64(result.FilesProcessed),
			Total:          int64(result.FilesTotal),
			TotalBytes:     result.OriginalSize,
			CompressedSize: result.CompressedSize,
		})
	}

	return result, nil
}

// compressFileToMemory compresses a file to a memory buffer and returns the compressed data
func compressFileToMemory(
	task fileTask,
	level int,
	progressCb ProgressCallback,
) ([]byte, error) {
	src, err := os.Open(task.AbsPath)
	if err != nil {
		return nil, fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	// Create buffer to hold compressed data
	var buf []byte
	bufWriter := &bytesWriter{data: &buf}

	// Create zstd encoder
	enc, err := zstd.NewWriter(bufWriter,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
		zstd.WithZeroFrames(true),
	)
	if err != nil {
		return nil, fmt.Errorf("create zstd writer: %w", err)
	}

	// Progress tracking reader
	uncompressedRead := uint64(0)
	proxy := &progressReader{
		Reader: src,
		onRead: func(n int) {
			uncompressedRead += uint64(n)
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:         EventFileProgress,
					FilePath:     task.RelPath,
					Current:      int64(uncompressedRead),
					Total:        int64(task.OrigSize),
					CurrentBytes: uncompressedRead,
				})
			}
		},
	}

	// Perform compression
	_, err = io.Copy(enc, proxy)
	if err != nil {
		enc.Close()
		return nil, fmt.Errorf("copy/compress failed: %w", err)
	}

	// Flush and close encoder
	if err = enc.Close(); err != nil {
		return nil, fmt.Errorf("close zstd encoder: %w", err)
	}

	return buf, nil
}

// compressFileToWriter compresses a file directly to a writer (for dry-run mode)
func compressFileToWriter(
	task fileTask,
	writer io.Writer,
	level int,
	progressCb ProgressCallback,
) (uint64, error) {
	src, err := os.Open(task.AbsPath)
	if err != nil {
		return 0, fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	// Track compressed bytes
	var compressedBytes uint64
	targetWriter := &progressWriter{
		Writer: writer,
		onWrite: func(n int) {
			compressedBytes += uint64(n)
		},
	}

	// Create zstd encoder
	enc, err := zstd.NewWriter(targetWriter,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
		zstd.WithZeroFrames(true),
	)
	if err != nil {
		return 0, fmt.Errorf("create zstd writer: %w", err)
	}

	// Progress tracking reader
	uncompressedRead := uint64(0)
	proxy := &progressReader{
		Reader: src,
		onRead: func(n int) {
			uncompressedRead += uint64(n)
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:         EventFileProgress,
					FilePath:     task.RelPath,
					Current:      int64(uncompressedRead),
					Total:        int64(task.OrigSize),
					CurrentBytes: uncompressedRead,
				})
			}
		},
	}

	// Perform compression
	_, err = io.Copy(enc, proxy)
	if err != nil {
		enc.Close()
		return 0, fmt.Errorf("copy/compress failed: %w", err)
	}

	// Flush and close encoder
	if err = enc.Close(); err != nil {
		return 0, fmt.Errorf("close zstd encoder: %w", err)
	}

	return compressedBytes, nil
}

type bytesWriter struct {
	data *[]byte
}

func (bw *bytesWriter) Write(p []byte) (n int, err error) {
	*bw.data = append(*bw.data, p...)
	return len(p), nil
}

type progressWriter struct {
	io.Writer
	onWrite func(int)
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.Writer.Write(p)
	if n > 0 && pw.onWrite != nil {
		pw.onWrite(n)
	}
	return n, err
}

type progressReader struct {
	io.Reader
	onRead func(int)
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	if n > 0 && pr.onRead != nil {
		pr.onRead(n)
	}
	return n, err
}

// collectFiles gathers all files from either the Files list or InputPath
// Returns folder tasks, total file count, total size, and any error
func collectFiles(opts *Options, result *Result) ([]folderTask, int, uint64, error) {
	folderMap := make(map[string][]fileTask)
	seenRelPaths := make(map[string]string) // relPath -> original source (for overlap detection)
	var totalOrigSize uint64
	var totalFiles int

	// Function to add a file task with overlap checking
	addFile := func(absPath, relPath string, info os.FileInfo, source string) error {
		// Check for overlapping relative paths
		if existingSource, exists := seenRelPaths[relPath]; exists {
			return fmt.Errorf("path overlap: %q from %q conflicts with %q", relPath, source, existingSource)
		}
		seenRelPaths[relPath] = source

		// Group by immediate parent folder
		folderPath := filepath.Dir(relPath)
		if folderPath == "." {
			folderPath = "" // Root level files
		}

		task := fileTask{
			AbsPath:  absPath,
			RelPath:  relPath,
			Info:     info,
			OrigSize: uint64(info.Size()),
		}

		folderMap[folderPath] = append(folderMap[folderPath], task)
		totalOrigSize += uint64(info.Size())
		totalFiles++
		return nil
	}

	if len(opts.Files) > 0 {
		// Custom file list mode: use paths as provided by the user
		for _, inputPath := range opts.Files {
			cleanPath := filepath.Clean(inputPath)
			info, err := os.Stat(cleanPath)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", inputPath, err))
				continue
			}

			if info.IsDir() {
				// Walk directory, paths are relative to this directory
				dirBase := filepath.Base(cleanPath)
				err := filepath.Walk(cleanPath, func(path string, finfo os.FileInfo, err error) error {
					if err != nil {
						result.Errors = append(result.Errors, fmt.Errorf("%s: %w", path, err))
						return nil
					}
					if finfo.IsDir() || !finfo.Mode().IsRegular() {
						return nil
					}

					// RelPath = dirBase + path relative to cleanPath
					relToDir, _ := filepath.Rel(cleanPath, path)
					relPath := filepath.Join(dirBase, relToDir)

					if err := addFile(path, relPath, finfo, inputPath); err != nil {
						return err
					}
					return nil
				})
				if err != nil {
					return nil, 0, 0, err
				}
			} else if info.Mode().IsRegular() {
				// Single file: use just the filename
				relPath := filepath.Base(cleanPath)
				if err := addFile(cleanPath, relPath, info, inputPath); err != nil {
					return nil, 0, 0, err
				}
			}
		}
	} else {
		// InputPath mode: walk and use paths relative to InputPath
		baseDir := opts.InputPath
		err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", path, err))
				return nil
			}
			if info.IsDir() || !info.Mode().IsRegular() {
				return nil
			}

			relPath, err := filepath.Rel(baseDir, path)
			if err != nil {
				relPath = filepath.Base(path)
			}

			if err := addFile(path, relPath, info, baseDir); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return nil, 0, 0, fmt.Errorf("directory walk failed: %w", err)
		}
	}

	// Convert folder map to task list
	foldersToCompress := make([]folderTask, 0, len(folderMap))
	for folderPath, files := range folderMap {
		foldersToCompress = append(foldersToCompress, folderTask{
			FolderPath: folderPath,
			Files:      files,
		})
	}

	return foldersToCompress, totalFiles, totalOrigSize, nil
}
