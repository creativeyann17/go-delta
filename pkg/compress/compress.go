// pkg/compress/compress.go
package compress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
	"github.com/yourusername/go-delta/internal/format"
)

type fileTask struct {
	AbsPath  string
	RelPath  string
	Info     os.FileInfo
	OrigSize uint64
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

	// Collect all files to process
	filesToCompress := make([]fileTask, 0, 1024)
	var totalOrigSize uint64

	walkErr := filepath.Walk(opts.InputPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", path, err))
			return nil // continue
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}

		relPath, err := filepath.Rel(opts.InputPath, path)
		if err != nil {
			relPath = path
		}

		filesToCompress = append(filesToCompress, fileTask{
			AbsPath:  path,
			RelPath:  relPath,
			Info:     info,
			OrigSize: uint64(info.Size()),
		})

		totalOrigSize += uint64(info.Size())
		return nil
	})

	if walkErr != nil {
		return nil, fmt.Errorf("directory walk failed: %w", walkErr)
	}

	if len(filesToCompress) == 0 {
		return nil, ErrNoFiles
	}

	result.FilesTotal = len(filesToCompress)
	result.OriginalSize = totalOrigSize

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:       EventStart,
			Total:      int64(len(filesToCompress)),
			TotalBytes: totalOrigSize,
		})
	}

	// Create archive file (if not dry-run)
	var writer io.WriteSeeker
	var writerMu sync.Mutex

	if !opts.DryRun {
		outFile, err := os.Create(opts.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("create output file: %w", err)
		}
		defer outFile.Close()

		writer = outFile

		// Write archive header
		if err := format.WriteArchiveHeader(writer, uint32(len(filesToCompress))); err != nil {
			return nil, fmt.Errorf("write archive header: %w", err)
		}
	}

	// Process files with worker pool
	var totalComprSize uint64
	var processedCount atomic.Uint32
	var errorsMu sync.Mutex

	var wg sync.WaitGroup
	taskCh := make(chan fileTask, len(filesToCompress))

	for i := 0; i < opts.MaxThreads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskCh {
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventFileStart,
						FilePath: task.RelPath,
						Total:    int64(task.OrigSize),
					})
				}

				var comprSize uint64
				var err error

				if opts.DryRun {
					// Dry-run mode (no writer)
					comprSize, err = compressFile(task, nil, opts.Level, opts.Verbose, progressCb)
				} else {
					// Real mode: write file entry header, compress data, update entry
					// Serialize archive writes to maintain correct data offsets
					var entryStart int64
					var dataStart int64

					writerMu.Lock()
					// Write file entry header
					entryStart, err = format.WriteFileEntry(writer, task.RelPath, task.OrigSize)
					if err == nil {
						// Get current position as data offset
						dataStart, err = writer.Seek(0, io.SeekCurrent)
					}
					if err == nil {
						// Compress and write data immediately while holding lock
						comprSize, err = compressFile(task, writer, opts.Level, opts.Verbose, progressCb)
					}
					if err == nil {
						// Update file entry with actual compressed size and data offset
						err = format.UpdateFileEntry(writer, entryStart, comprSize, uint64(dataStart))
					}
					writerMu.Unlock()

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
						continue
					}
				}

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
				} else {
					atomic.AddUint64(&totalComprSize, comprSize)
					processedCount.Add(1)
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:           EventFileComplete,
							FilePath:       task.RelPath,
							Current:        int64(task.OrigSize),
							Total:          int64(task.OrigSize),
							CompressedSize: comprSize,
						})
					}
				}
			}
		}(i + 1)
	}

	// Feed tasks
	go func() {
		for _, task := range filesToCompress {
			taskCh <- task
		}
		close(taskCh)
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

// compressFile compresses a single file and returns the number of compressed bytes written.
func compressFile(
	task fileTask,
	writer io.WriteSeeker,
	level int,
	verbose bool,
	progressCb ProgressCallback,
) (compressedSize uint64, err error) {
	src, err := os.Open(task.AbsPath)
	if err != nil {
		return 0, fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	// Determine target writer based on mode
	var targetWriter io.Writer
	var compressedBytes uint64 // Track actual compressed bytes written

	if writer == nil {
		// Dry-run mode: discard output
		targetWriter = io.Discard
	} else {
		// Real mode: wrap writer to track compressed bytes
		targetWriter = &progressWriter{
			Writer: writer,
			onWrite: func(n int) {
				compressedBytes += uint64(n)
			},
		}
	}

	// Create zstd encoder with requested level
	enc, err := zstd.NewWriter(targetWriter,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
		zstd.WithZeroFrames(true),
	)
	if err != nil {
		return 0, fmt.Errorf("create zstd writer: %w", err)
	}

	// Progress tracking reader (for source file progress, not compressed size)
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
