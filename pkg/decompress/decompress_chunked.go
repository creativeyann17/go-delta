// pkg/decompress/decompress_chunked.go
package decompress

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

// decompressGDelta02 handles decompression of GDELTA02 archives with chunking
func decompressGDelta02(archiveFile *os.File, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Get archive file size for compressed size stat
	archiveInfo, err := archiveFile.Stat()
	if err != nil {
		return fmt.Errorf("stat archive file: %w", err)
	}
	result.CompressedSize = uint64(archiveInfo.Size())

	// Read GDELTA02 header
	_, fileCount, chunkCount, err := format.ReadGDelta02Header(archiveFile)
	if err != nil {
		return fmt.Errorf("read GDELTA02 header: %w", err)
	}

	result.FilesTotal = int(fileCount)

	if opts.Verbose {
		fmt.Printf("\nReading GDELTA02 archive...\n")
		fmt.Printf("  Files: %d\n", fileCount)
		fmt.Printf("  Unique chunks: %d\n", chunkCount)
	}

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:  EventStart,
			Total: int64(fileCount),
		})
	}

	// Read chunk index
	chunkIndex, err := format.ReadChunkIndex(archiveFile, chunkCount)
	if err != nil {
		return fmt.Errorf("read chunk index: %w", err)
	}

	// Read all file metadata
	fileMetadataList := make([]format.FileMetadata, fileCount)
	for i := uint32(0); i < fileCount; i++ {
		metadata, err := format.ReadFileMetadata(archiveFile)
		if err != nil {
			return fmt.Errorf("read file metadata %d: %w", i, err)
		}
		fileMetadataList[i] = metadata
	}

	// Get current position (start of chunk data section)
	chunkDataStart, err := archiveFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("get chunk data start: %w", err)
	}

	// Create output directory
	if err := os.MkdirAll(opts.OutputPath, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Create a reusable zstd decoder for better performance
	// The decoder is reset for each chunk instead of creating a new one
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return fmt.Errorf("create zstd decoder: %w", err)
	}
	defer decoder.Close()

	// Decompress each file by reassembling its chunks
	var totalDecompSize uint64

	for _, metadata := range fileMetadataList {
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: metadata.RelPath,
				Total:    int64(metadata.OrigSize),
			})
		}

		// Build output path
		outputPath := filepath.Join(opts.OutputPath, metadata.RelPath)

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: create directory: %w", metadata.RelPath, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
			}
			continue
		}

		// Check if file exists
		if !opts.Overwrite {
			if _, err := os.Stat(outputPath); err == nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", metadata.RelPath, ErrFileExists))
				if progressCb != nil {
					progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
				}
				continue
			}
		}

		// Create output file
		outFile, err := os.Create(outputPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: create file: %w", metadata.RelPath, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
			}
			continue
		}

		// Reassemble file from chunks
		var bytesWritten uint64
		if opts.Verbose && len(metadata.ChunkHashes) > 0 {
			fmt.Printf("  %s: reassembling %d chunks\n", metadata.RelPath, len(metadata.ChunkHashes))
		}
		for _, chunkHash := range metadata.ChunkHashes {
			chunkInfo, exists := chunkIndex[chunkHash]
			if !exists {
				outFile.Close()
				os.Remove(outputPath)
				result.Errors = append(result.Errors, fmt.Errorf("%s: chunk not found: %x", metadata.RelPath, chunkHash))
				if progressCb != nil {
					progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
				}
				break
			}

			// Seek to chunk data
			_, err := archiveFile.Seek(chunkDataStart+int64(chunkInfo.Offset), io.SeekStart)
			if err != nil {
				outFile.Close()
				os.Remove(outputPath)
				result.Errors = append(result.Errors, fmt.Errorf("%s: seek chunk: %w", metadata.RelPath, err))
				if progressCb != nil {
					progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
				}
				break
			}

			// Read compressed chunk
			compressedData := make([]byte, chunkInfo.CompressedSize)
			if _, err := io.ReadFull(archiveFile, compressedData); err != nil {
				outFile.Close()
				os.Remove(outputPath)
				result.Errors = append(result.Errors, fmt.Errorf("%s: read chunk: %w", metadata.RelPath, err))
				if progressCb != nil {
					progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
				}
				break
			}

			// Decompress chunk using the reusable decoder
			if err := decoder.Reset(bytes.NewReader(compressedData)); err != nil {
				outFile.Close()
				os.Remove(outputPath)
				result.Errors = append(result.Errors, fmt.Errorf("%s: reset decoder: %w", metadata.RelPath, err))
				if progressCb != nil {
					progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
				}
				break
			}

			// Write decompressed chunk to output file
			n, err := io.Copy(outFile, decoder)

			if err != nil {
				outFile.Close()
				os.Remove(outputPath)
				result.Errors = append(result.Errors, fmt.Errorf("%s: write chunk: %w", metadata.RelPath, err))
				if progressCb != nil {
					progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
				}
				break
			}

			bytesWritten += uint64(n)

			// Report progress
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:         EventFileProgress,
					FilePath:     metadata.RelPath,
					Current:      int64(bytesWritten),
					Total:        int64(metadata.OrigSize),
					CurrentBytes: bytesWritten,
				})
			}
		}

		outFile.Close()

		// Verify complete file was written
		if bytesWritten == metadata.OrigSize {
			result.FilesProcessed++
			totalDecompSize += bytesWritten

			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:             EventFileComplete,
					FilePath:         metadata.RelPath,
					Current:          int64(metadata.OrigSize),
					Total:            int64(metadata.OrigSize),
					DecompressedSize: metadata.OrigSize,
				})
			}
		} else {
			// Incomplete file, remove it
			os.Remove(outputPath)
			result.Errors = append(result.Errors, fmt.Errorf("%s: incomplete (wrote %d, expected %d)", metadata.RelPath, bytesWritten, metadata.OrigSize))
		}

		if opts.Verbose {
			fmt.Printf("Decompressed: %s (%d bytes)\n", metadata.RelPath, metadata.OrigSize)
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
