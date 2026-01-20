// pkg/decompress/decompress.go
package decompress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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

// decompressGDelta01 handles the traditional GDELTA01 format
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

	// Process files sequentially (reading entry headers and data in order)
	var totalDecompSize, totalCompSize uint64

	for i := 0; i < fileCount; i++ {
		entry, err := reader.ReadFileEntry()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("read entry %d: %w", i, err))
			// Can't continue after a failed read - file position is unknown
			break
		}

		totalCompSize += entry.CompressedSize

		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: entry.Path,
				Total:    int64(entry.OriginalSize),
			})
		}

		// Decompress directly from current position (entry data follows entry header)
		decompSize, err := decompressFileFromCurrentPosition(archiveFile, entry, opts, progressCb)

		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Path, err))
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:     EventError,
					FilePath: entry.Path,
				})
			}
		} else {
			totalDecompSize += decompSize
			result.FilesProcessed++
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
	}

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

// decompressFileFromCurrentPosition decompresses a file from the current archive position
// The archive format has entry headers followed immediately by compressed data
func decompressFileFromCurrentPosition(
	archiveFile *os.File,
	entry *format.FileEntry,
	opts *Options,
	progressCb ProgressCallback,
) (decompressedSize uint64, err error) {
	// Construct output path
	outPath := filepath.Join(opts.OutputPath, entry.Path)

	// Check if file exists
	if !opts.Overwrite {
		if _, err := os.Stat(outPath); err == nil {
			// File exists - skip the compressed data in the archive to maintain position
			if _, err := archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent); err != nil {
				return 0, fmt.Errorf("skip compressed data: %w", err)
			}
			return 0, ErrFileExists
		}
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		// Skip compressed data to maintain archive position
		archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent)
		return 0, fmt.Errorf("create directories: %w", err)
	}

	// Create output file
	outFile, err := os.Create(outPath)
	if err != nil {
		// Skip compressed data to maintain archive position
		archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent)
		return 0, fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Create limited reader for compressed data (reads from current position)
	limitedReader := io.LimitReader(archiveFile, int64(entry.CompressedSize))

	// Create zstd decoder
	decoder, err := zstd.NewReader(limitedReader)
	if err != nil {
		return 0, fmt.Errorf("create zstd decoder: %w", err)
	}
	defer decoder.Close()

	// Progress tracking writer
	written := uint64(0)
	proxy := &godelta.ProgressWriter{
		Writer: outFile,
		OnWrite: func(n int) {
			written += uint64(n)
			if progressCb != nil {
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
