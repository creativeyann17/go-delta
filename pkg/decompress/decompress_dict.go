// pkg/decompress/decompress_dict.go
package decompress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

// decompressGDelta03 handles decompression of GDELTA03 archives with dictionary
func decompressGDelta03(archiveFile *os.File, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Get archive file size for compressed size stat
	archiveInfo, err := archiveFile.Stat()
	if err != nil {
		return fmt.Errorf("stat archive file: %w", err)
	}
	result.CompressedSize = uint64(archiveInfo.Size())

	// Read GDELTA03 header (magic already consumed)
	version, dictSize, fileCount, err := format.ReadGDelta03Header(archiveFile)
	if err != nil {
		return fmt.Errorf("read GDELTA03 header: %w", err)
	}

	if version != format.GDELTA03Version {
		return fmt.Errorf("unsupported GDELTA03 version: %d", version)
	}

	result.FilesTotal = int(fileCount)

	if opts.Verbose {
		fmt.Printf("\nReading GDELTA03 archive...\n")
		fmt.Printf("  Files: %d\n", fileCount)
		fmt.Printf("  Dictionary size: %d bytes\n", dictSize)
	}

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:  EventStart,
			Total: int64(fileCount),
		})
	}

	// Read dictionary
	dictionary := make([]byte, dictSize)
	if dictSize > 0 {
		if _, err := io.ReadFull(archiveFile, dictionary); err != nil {
			return fmt.Errorf("read dictionary: %w", err)
		}
	}

	// Create output directory
	if err := os.MkdirAll(opts.OutputPath, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Create decoder with dictionary
	var decoder *zstd.Decoder
	if len(dictionary) > 0 {
		decoder, err = zstd.NewReader(nil, zstd.WithDecoderDicts(dictionary))
	} else {
		decoder, err = zstd.NewReader(nil)
	}
	if err != nil {
		return fmt.Errorf("create zstd decoder: %w", err)
	}
	defer decoder.Close()

	// Decompress each file
	var totalDecompSize uint64

	for i := uint32(0); i < fileCount; i++ {
		// Read file entry
		entry, err := format.ReadGDelta03FileEntry(archiveFile)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("read entry %d: %w", i, err))
			break
		}

		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: entry.Path,
				Total:    int64(entry.OriginalSize),
			})
		}

		// Build output path
		outputPath := filepath.Join(opts.OutputPath, entry.Path)

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
			// Skip compressed data to maintain position
			archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent)
			result.Errors = append(result.Errors, fmt.Errorf("%s: create directory: %w", entry.Path, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: entry.Path})
			}
			continue
		}

		// Check if file exists
		if !opts.Overwrite {
			if _, err := os.Stat(outputPath); err == nil {
				// Skip compressed data
				archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent)
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Path, ErrFileExists))
				if progressCb != nil {
					progressCb(ProgressEvent{Type: EventError, FilePath: entry.Path})
				}
				continue
			}
		}

		// Create output file
		outFile, err := os.Create(outputPath)
		if err != nil {
			// Skip compressed data
			archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent)
			result.Errors = append(result.Errors, fmt.Errorf("%s: create file: %w", entry.Path, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: entry.Path})
			}
			continue
		}

		// Read compressed data and decompress
		compressedData := make([]byte, entry.CompressedSize)
		if _, err := io.ReadFull(archiveFile, compressedData); err != nil {
			outFile.Close()
			os.Remove(outputPath)
			result.Errors = append(result.Errors, fmt.Errorf("%s: read compressed data: %w", entry.Path, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: entry.Path})
			}
			continue
		}

		// Decompress using the decoder
		decompressed, err := decoder.DecodeAll(compressedData, nil)
		if err != nil {
			outFile.Close()
			os.Remove(outputPath)
			result.Errors = append(result.Errors, fmt.Errorf("%s: decompress: %w", entry.Path, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: entry.Path})
			}
			continue
		}

		// Write decompressed data
		written, err := outFile.Write(decompressed)
		outFile.Close()

		if err != nil {
			os.Remove(outputPath)
			result.Errors = append(result.Errors, fmt.Errorf("%s: write: %w", entry.Path, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: entry.Path})
			}
			continue
		}

		if uint64(written) != entry.OriginalSize {
			result.Errors = append(result.Errors, fmt.Errorf("%s: size mismatch (expected %d, got %d)",
				entry.Path, entry.OriginalSize, written))
		}

		totalDecompSize += uint64(written)
		result.FilesProcessed++

		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:             EventFileComplete,
				FilePath:         entry.Path,
				Current:          int64(entry.OriginalSize),
				Total:            int64(entry.OriginalSize),
				DecompressedSize: uint64(written),
			})
		}

		if opts.Verbose {
			fmt.Printf("Decompressed: %s (%d bytes)\n", entry.Path, written)
		}
	}

	result.DecompressedSize = totalDecompSize

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:       EventComplete,
			Current:    int64(result.FilesProcessed),
			Total:      int64(result.FilesTotal),
			TotalBytes: result.DecompressedSize,
		})
	}

	return nil
}
