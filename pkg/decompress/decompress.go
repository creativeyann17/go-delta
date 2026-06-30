// pkg/decompress/decompress.go
package decompress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/creativeyann17/go-delta/pkg/godelta"
	"github.com/klauspost/compress/zstd"
)

// ProgressCallback is called for various progress events
type ProgressCallback func(event ProgressEvent)

// ProgressEvent contains progress information
type ProgressEvent struct {
	Type             EventType
	FilePath         string
	Current          int64
	Total            int64
	CurrentBytes     uint64
	TotalBytes       uint64
	DecompressedSize uint64
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

// Decompress decompresses an archive from inputPath to outputPath
func Decompress(opts *Options, progressCb ProgressCallback) (*Result, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	result := &Result{}

	// Open archive file
	archiveFile, err := os.Open(opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer archiveFile.Close()

	// Peek at magic to determine format version
	magic := make([]byte, 8)
	if _, err := io.ReadFull(archiveFile, magic); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}

	// Reset to start
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to start: %w", err)
	}

	// Detect and route based on format
	detectedFormat := format.DetectFormat(magic)
	switch detectedFormat {
	case format.FormatZIP:
		archiveFile.Close() // ZIP reader needs file path, not handle
		return result, decompressZip(opts, progressCb, result)

	case format.FormatXZ:
		archiveFile.Close() // XZ reader needs file path, not handle
		return result, decompressXz(opts, progressCb, result)

	case format.FormatGDelta03:
		err := decompressGDelta03(archiveFile, opts, progressCb, result)
		return result, err

	case format.FormatGDelta02:
		err := decompressGDelta02(archiveFile, opts, progressCb, result)
		return result, err

	case format.FormatGDelta01:
		err := decompressGDelta01(archiveFile, opts, progressCb, result)
		return result, err

	default:
		return nil, fmt.Errorf("unknown archive format: %q", magic)
	}
}

// decompressGDelta01 handles the traditional GDELTA01 format.
// Entry headers are read sequentially first, then files are decompressed in
// parallel: every entry stores its data offset, so each worker reads from its
// own archive file handle.
func decompressGDelta01(archiveFile *os.File, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Create archive reader
	reader, err := format.NewArchiveReader(archiveFile)
	if err != nil {
		return fmt.Errorf("read archive header: %w", err)
	}

	fileCount := reader.FileCount()
	result.FilesTotal = fileCount

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:  EventStart,
			Total: int64(fileCount),
		})
	}

	// Create output directory
	if err := os.MkdirAll(opts.OutputPath, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Read all entry headers, skipping over the data sections
	var entries []*format.FileEntry
	var totalCompSize uint64
	for i := 0; i < fileCount; i++ {
		entry, err := reader.ReadFileEntry()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("read entry %d: %w", i, err))
			// Can't continue after a failed read - file position is unknown
			break
		}
		entries = append(entries, entry)
		totalCompSize += entry.CompressedSize

		// Skip the compressed data to reach the next entry header
		if i < fileCount-1 {
			if _, err := archiveFile.Seek(int64(entry.DataOffset+entry.CompressedSize), io.SeekStart); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("seek past entry %d: %w", i, err))
				break
			}
		}
	}

	// Decompress entries in parallel
	workers := opts.MaxThreads
	if workers > len(entries) {
		workers = len(entries)
	}

	var mu sync.Mutex // guards result and totals
	var totalDecompSize uint64
	var wg sync.WaitGroup
	entryCh := make(chan *format.FileEntry, workers*4)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Each worker reads through its own file handle (independent seeks)
			f, err := os.Open(opts.InputPath)
			if err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("open archive: %w", err))
				mu.Unlock()
				return
			}
			defer f.Close()

			decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
			if err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("create zstd decoder: %w", err))
				mu.Unlock()
				return
			}
			defer decoder.Close()

			for entry := range entryCh {
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventFileStart,
						FilePath: entry.Path,
						Total:    int64(entry.OriginalSize),
					})
				}

				decompSize, err := decompressEntryAt(f, entry, decoder, opts, progressCb)

				if err != nil {
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Path, err))
					mu.Unlock()
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:     EventError,
							FilePath: entry.Path,
						})
					}
					continue
				}

				mu.Lock()
				totalDecompSize += decompSize
				result.FilesProcessed++
				mu.Unlock()
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:             EventFileComplete,
						FilePath:         entry.Path,
						Current:          int64(entry.OriginalSize),
						Total:            int64(entry.OriginalSize),
						DecompressedSize: decompSize,
					})
				}
			}
		}()
	}

	for _, entry := range entries {
		entryCh <- entry
	}
	close(entryCh)
	wg.Wait()

	result.CompressedSize = totalCompSize
	result.DecompressedSize = totalDecompSize

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:             EventComplete,
			Current:          int64(result.FilesProcessed),
			Total:            int64(result.FilesTotal),
			TotalBytes:       result.CompressedSize,
			DecompressedSize: result.DecompressedSize,
		})
	}

	return nil
}

// decompressEntryAt decompresses one file entry from its stored data offset.
// The archive handle and decoder are owned by the calling worker.
func decompressEntryAt(
	archiveFile *os.File,
	entry *format.FileEntry,
	decoder *zstd.Decoder,
	opts *Options,
	progressCb ProgressCallback,
) (decompressedSize uint64, err error) {
	// Construct output path, rejecting entries that would escape OutputPath
	outPath, err := safeJoin(opts.OutputPath, entry.Path)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", entry.Path, err)
	}

	// Check if file exists
	if !opts.Overwrite {
		if _, err := os.Stat(outPath); err == nil {
			return 0, ErrFileExists
		}
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return 0, fmt.Errorf("create directories: %w", err)
	}

	// Create output file
	outFile, err := os.Create(outPath)
	if err != nil {
		return 0, fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Seek to this entry's compressed data
	if _, err := archiveFile.Seek(int64(entry.DataOffset), io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek to data: %w", err)
	}

	// Create limited reader for compressed data
	limitedReader := io.LimitReader(archiveFile, int64(entry.CompressedSize))

	// Reset the worker's zstd decoder onto this entry's data
	if err := decoder.Reset(limitedReader); err != nil {
		return 0, fmt.Errorf("reset zstd decoder: %w", err)
	}

	// Progress tracking writer (throttled; EventFileComplete finishes the bar)
	var written, lastReported uint64
	proxy := &godelta.ProgressWriter{
		Writer: outFile,
		OnWrite: func(n int) {
			written += uint64(n)
			if progressCb != nil && written-lastReported >= progressReportStep {
				lastReported = written
				progressCb(ProgressEvent{
					Type:         EventFileProgress,
					FilePath:     entry.Path,
					Current:      int64(written),
					Total:        int64(entry.OriginalSize),
					CurrentBytes: written,
				})
			}
		},
	}

	// Decompress
	_, err = io.Copy(proxy, decoder)
	if err != nil {
		return 0, fmt.Errorf("decompress: %w", err)
	}

	return written, nil
}
