// pkg/verify/verify.go
package verify

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

// ProgressCallback is called for progress updates during verification
type ProgressCallback func(event ProgressEvent)

// ProgressEvent contains progress information
type ProgressEvent struct {
	Type     EventType
	FilePath string
	Current  int
	Total    int
	Message  string
}

// EventType indicates the type of progress event
type EventType int

const (
	EventStart EventType = iota
	EventFileVerify
	EventChunkVerify
	EventComplete
	EventError
)

// Verify verifies an archive and returns comprehensive results
func Verify(opts *Options, progressCb ProgressCallback) (*Result, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	result := &Result{
		ArchivePath: opts.InputPath,
	}

	// Open archive file
	archiveFile, err := os.Open(opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer archiveFile.Close()

	// Get archive size
	stat, err := archiveFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat archive: %w", err)
	}
	result.ArchiveSize = uint64(stat.Size())

	// Read magic to determine format
	magic := make([]byte, 8)
	if _, err := io.ReadFull(archiveFile, magic); err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("read magic: %w", err))
		return result, ErrTruncatedArchive
	}
	result.Magic = string(magic)

	// Reset to start
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to start: %w", err)
	}

	// Route based on format
	switch {
	case string(magic) == format.ArchiveMagic:
		result.Format = FormatGDelta01
		return result, verifyGDelta01(archiveFile, opts, progressCb, result)

	case string(magic) == format.ArchiveMagic02:
		result.Format = FormatGDelta02
		return result, verifyGDelta02(archiveFile, opts, progressCb, result)

	case string(magic) == format.ArchiveMagic03:
		result.Format = FormatGDelta03
		return result, verifyGDelta03(archiveFile, opts, progressCb, result)

	case magic[0] == 'P' && magic[1] == 'K':
		result.Format = FormatZIP
		// ZIP verification not implemented yet
		result.HeaderValid = true
		result.StructureValid = true
		result.FooterValid = true
		result.Errors = append(result.Errors, fmt.Errorf("ZIP verification not yet implemented"))
		return result, nil

	default:
		result.Format = FormatUnknown
		result.Errors = append(result.Errors, ErrInvalidMagic)
		return result, ErrUnsupportedFormat
	}
}

// verifyGDelta01 verifies a GDELTA01 archive
func verifyGDelta01(archiveFile *os.File, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Create archive reader
	reader, err := format.NewArchiveReader(archiveFile)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("read header: %w", err))
		return ErrInvalidHeader
	}

	result.HeaderValid = true
	result.FileCount = reader.FileCount()
	result.MetadataValid = true

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventStart,
			Total:   result.FileCount,
			Message: fmt.Sprintf("Verifying %d files", result.FileCount),
		})
	}

	// Track seen paths for duplicate detection
	seenPaths := make(map[string]bool)

	// Read and verify each file entry
	for i := 0; i < result.FileCount; i++ {
		entry, err := reader.ReadFileEntry()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("file %d: %w", i, err))
			result.MetadataValid = false
			continue
		}

		fileInfo := FileInfo{
			Path:           entry.Path,
			OriginalSize:   entry.OriginalSize,
			CompressedSize: entry.CompressedSize,
		}

		// Check for duplicates
		if seenPaths[entry.Path] {
			result.DuplicatePaths++
			result.Errors = append(result.Errors, fmt.Errorf("duplicate path: %s", entry.Path))
		}
		seenPaths[entry.Path] = true

		// Track stats
		result.TotalOrigSize += entry.OriginalSize
		result.TotalCompSize += entry.CompressedSize
		if entry.OriginalSize == 0 {
			result.EmptyFiles++
		}

		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileVerify,
				FilePath: entry.Path,
				Current:  i + 1,
				Total:    result.FileCount,
			})
		}

		// Verify data if requested
		if opts.VerifyData {
			err := verifyGDelta01FileData(archiveFile, entry)
			if err != nil {
				fileInfo.Error = err
				result.CorruptFiles++
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Path, err))
			} else {
				fileInfo.DataValid = true
				result.FilesVerified++
			}
			result.DataVerified = true
		} else {
			// Skip over compressed data
			if _, err := archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("skip data for %s: %w", entry.Path, err))
			}
		}

		result.Files = append(result.Files, fileInfo)
	}

	// Verify footer
	footer := make([]byte, 9) // "GDELTAEND"
	n, err := archiveFile.Read(footer)
	if err != nil && err != io.EOF {
		result.Errors = append(result.Errors, fmt.Errorf("read footer: %w", err))
	}
	if n == 9 && string(footer) == "GDELTAEND" {
		result.FooterValid = true
	} else {
		result.FooterValid = false
		result.Errors = append(result.Errors, ErrInvalidFooter)
	}

	result.StructureValid = result.HeaderValid && result.MetadataValid && result.DuplicatePaths == 0

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventComplete,
			Current: result.FileCount,
			Total:   result.FileCount,
			Message: "Verification complete",
		})
	}

	return nil
}

// verifyGDelta01FileData verifies data integrity for a single file
func verifyGDelta01FileData(archiveFile *os.File, entry *format.FileEntry) error {
	// Read compressed data
	compressedData := make([]byte, entry.CompressedSize)
	if _, err := io.ReadFull(archiveFile, compressedData); err != nil {
		return fmt.Errorf("read compressed data: %w", err)
	}

	// Try to decompress
	decoder, err := zstd.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}
	defer decoder.Close()

	// Decompress to /dev/null equivalent, counting bytes
	decompressed, err := io.Copy(io.Discard, decoder)
	if err != nil {
		return fmt.Errorf("decompress: %w", err)
	}

	// Verify size matches
	if uint64(decompressed) != entry.OriginalSize {
		return fmt.Errorf("size mismatch: expected %d, got %d", entry.OriginalSize, decompressed)
	}

	return nil
}

// verifyGDelta02 verifies a GDELTA02 archive
func verifyGDelta02(archiveFile *os.File, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Read header
	chunkSize, fileCount, chunkCount, err := format.ReadGDelta02Header(archiveFile)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("read header: %w", err))
		return ErrInvalidHeader
	}

	result.HeaderValid = true
	result.ChunkSize = chunkSize
	result.FileCount = int(fileCount)
	result.ChunkCount = uint64(chunkCount)

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventStart,
			Total:   result.FileCount,
			Message: fmt.Sprintf("Verifying %d files, %d chunks", fileCount, chunkCount),
		})
	}

	// Read chunk index
	chunkIndex, err := format.ReadChunkIndex(archiveFile, chunkCount)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("read chunk index: %w", err))
		result.IndexValid = false
		return ErrInvalidChunkIndex
	}
	result.IndexValid = true

	// Track chunk references
	chunkRefs := make(map[[32]byte]int)

	// Track seen paths for duplicate detection
	seenPaths := make(map[string]bool)
	result.MetadataValid = true

	// Read file metadata
	for i := uint32(0); i < fileCount; i++ {
		metadata, err := format.ReadFileMetadata(archiveFile)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("file %d: %w", i, err))
			result.MetadataValid = false
			continue
		}

		fileInfo := FileInfo{
			Path:         metadata.RelPath,
			OriginalSize: metadata.OrigSize,
			ChunkCount:   len(metadata.ChunkHashes),
		}

		// Check for duplicates
		if seenPaths[metadata.RelPath] {
			result.DuplicatePaths++
			result.Errors = append(result.Errors, fmt.Errorf("duplicate path: %s", metadata.RelPath))
		}
		seenPaths[metadata.RelPath] = true

		// Track stats
		result.TotalOrigSize += metadata.OrigSize
		result.TotalChunkRef += uint64(len(metadata.ChunkHashes))
		if metadata.OrigSize == 0 {
			result.EmptyFiles++
		}

		// Verify all chunks exist in index
		var fileCompSize uint64
		for _, hash := range metadata.ChunkHashes {
			chunkRefs[hash]++
			if info, exists := chunkIndex[hash]; exists {
				fileCompSize += info.CompressedSize
			} else {
				result.MissingChunks++
				result.Errors = append(result.Errors, fmt.Errorf("%s: missing chunk %x", metadata.RelPath, hash[:8]))
			}
		}
		fileInfo.CompressedSize = fileCompSize
		result.TotalCompSize += fileCompSize

		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileVerify,
				FilePath: metadata.RelPath,
				Current:  int(i) + 1,
				Total:    result.FileCount,
			})
		}

		result.Files = append(result.Files, fileInfo)
	}

	// Check for orphaned chunks (chunks not referenced by any file)
	for hash := range chunkIndex {
		if chunkRefs[hash] == 0 {
			result.OrphanedChunks++
			if opts.Verbose {
				result.Errors = append(result.Errors, fmt.Errorf("orphaned chunk: %x", hash[:8]))
			}
		}
	}

	// Get chunk data start position
	chunkDataStart, err := archiveFile.Seek(0, io.SeekCurrent)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("get chunk data position: %w", err))
	}

	// Verify chunk data if requested
	if opts.VerifyData && chunkDataStart > 0 {
		result.DataVerified = true
		chunksVerified := 0

		for hash, info := range chunkIndex {
			// Seek to chunk
			if _, err := archiveFile.Seek(chunkDataStart+int64(info.Offset), io.SeekStart); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("seek to chunk %x: %w", hash[:8], err))
				result.CorruptChunks++
				continue
			}

			// Read compressed chunk
			compressedData := make([]byte, info.CompressedSize)
			if _, err := io.ReadFull(archiveFile, compressedData); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("read chunk %x: %w", hash[:8], err))
				result.CorruptChunks++
				continue
			}

			// Try to decompress
			decoder, err := zstd.NewReader(bytes.NewReader(compressedData))
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("decompress chunk %x: %w", hash[:8], err))
				result.CorruptChunks++
				continue
			}

			decompressed, err := io.Copy(io.Discard, decoder)
			decoder.Close()

			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("decompress chunk %x: %w", hash[:8], err))
				result.CorruptChunks++
				continue
			}

			if uint64(decompressed) != info.OriginalSize {
				result.Errors = append(result.Errors, fmt.Errorf("chunk %x size mismatch: expected %d, got %d",
					hash[:8], info.OriginalSize, decompressed))
				result.CorruptChunks++
				continue
			}

			chunksVerified++

			if progressCb != nil && chunksVerified%100 == 0 {
				progressCb(ProgressEvent{
					Type:    EventChunkVerify,
					Current: chunksVerified,
					Total:   int(chunkCount),
				})
			}
		}

		result.ChunksVerified = chunksVerified
		result.FilesVerified = result.FileCount - result.CorruptFiles
	}

	// Verify footer
	// Seek to end - 8 bytes
	if _, err := archiveFile.Seek(-8, io.SeekEnd); err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("seek to footer: %w", err))
	} else {
		footer := make([]byte, 8)
		if _, err := io.ReadFull(archiveFile, footer); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("read footer: %w", err))
		} else if string(footer) == "ENDGDLT2" {
			result.FooterValid = true
		} else {
			result.FooterValid = false
			result.Errors = append(result.Errors, fmt.Errorf("invalid footer: %q", footer))
		}
	}

	result.StructureValid = result.HeaderValid && result.IndexValid && result.MetadataValid &&
		result.MissingChunks == 0 && result.DuplicatePaths == 0

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventComplete,
			Current: result.FileCount,
			Total:   result.FileCount,
			Message: "Verification complete",
		})
	}

	return nil
}

// verifyGDelta03 verifies a GDELTA03 archive with dictionary compression
func verifyGDelta03(archiveFile *os.File, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Read header (file position is at start, magic not consumed)
	version, dictSize, fileCount, err := format.ReadGDelta03Header(archiveFile)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("read header: %w", err))
		return ErrInvalidHeader
	}

	if version != format.GDELTA03Version {
		result.Errors = append(result.Errors, fmt.Errorf("unsupported version: %d", version))
		return ErrInvalidHeader
	}

	result.HeaderValid = true
	result.DictSize = dictSize
	result.FileCount = int(fileCount)
	result.MetadataValid = true

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventStart,
			Total:   result.FileCount,
			Message: fmt.Sprintf("Verifying %d files (dict: %d bytes)", fileCount, dictSize),
		})
	}

	// Skip dictionary data
	if dictSize > 0 {
		if _, err := archiveFile.Seek(int64(dictSize), io.SeekCurrent); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("skip dictionary: %w", err))
			return ErrTruncatedArchive
		}
	}

	// Track seen paths for duplicate detection
	seenPaths := make(map[string]bool)

	// Header size: magic(8) + version(1) + dictSize(4) + fileCount(4) + reserved(4) = 21 bytes
	const headerSize = 21

	// Create decoder for data verification if needed
	var decoder *zstd.Decoder
	if opts.VerifyData && dictSize > 0 {
		// Need to read the dictionary for verification
		// Seek back to dictionary start (right after header)
		dictStart := int64(headerSize)
		if _, err := archiveFile.Seek(dictStart, io.SeekStart); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("seek to dictionary: %w", err))
		} else {
			dictionary := make([]byte, dictSize)
			if _, err := io.ReadFull(archiveFile, dictionary); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("read dictionary: %w", err))
			} else {
				decoder, _ = zstd.NewReader(nil, zstd.WithDecoderDicts(dictionary))
				if decoder != nil {
					defer decoder.Close()
				}
			}
		}
	} else if opts.VerifyData {
		decoder, _ = zstd.NewReader(nil)
		if decoder != nil {
			defer decoder.Close()
		}
	}

	// Seek to file entries (after header and dictionary)
	fileEntriesStart := int64(headerSize + int64(dictSize)) // header + dictionary
	if _, err := archiveFile.Seek(fileEntriesStart, io.SeekStart); err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("seek to file entries: %w", err))
		return ErrTruncatedArchive
	}

	// Read and verify each file entry
	for i := 0; i < result.FileCount; i++ {
		entry, err := format.ReadGDelta03FileEntry(archiveFile)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("file %d: %w", i, err))
			result.MetadataValid = false
			break
		}

		fileInfo := FileInfo{
			Path:           entry.Path,
			OriginalSize:   entry.OriginalSize,
			CompressedSize: entry.CompressedSize,
		}

		// Check for duplicates
		if seenPaths[entry.Path] {
			result.DuplicatePaths++
			result.Errors = append(result.Errors, fmt.Errorf("duplicate path: %s", entry.Path))
		}
		seenPaths[entry.Path] = true

		// Track stats
		result.TotalOrigSize += entry.OriginalSize
		result.TotalCompSize += entry.CompressedSize
		if entry.OriginalSize == 0 {
			result.EmptyFiles++
		}

		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileVerify,
				FilePath: entry.Path,
				Current:  i + 1,
				Total:    result.FileCount,
			})
		}

		// Verify data if requested
		if opts.VerifyData && decoder != nil {
			// Read compressed data
			compressedData := make([]byte, entry.CompressedSize)
			if _, err := io.ReadFull(archiveFile, compressedData); err != nil {
				fileInfo.Error = fmt.Errorf("read compressed data: %w", err)
				result.CorruptFiles++
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Path, fileInfo.Error))
			} else {
				// Try to decompress
				decompressed, err := decoder.DecodeAll(compressedData, nil)
				if err != nil {
					fileInfo.Error = fmt.Errorf("decompress: %w", err)
					result.CorruptFiles++
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Path, fileInfo.Error))
				} else if uint64(len(decompressed)) != entry.OriginalSize {
					fileInfo.Error = fmt.Errorf("size mismatch: expected %d, got %d", entry.OriginalSize, len(decompressed))
					result.CorruptFiles++
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", entry.Path, fileInfo.Error))
				} else {
					fileInfo.DataValid = true
					result.FilesVerified++
				}
			}
			result.DataVerified = true
		} else {
			// Skip over compressed data
			if _, err := archiveFile.Seek(int64(entry.CompressedSize), io.SeekCurrent); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("skip data for %s: %w", entry.Path, err))
			}
		}

		result.Files = append(result.Files, fileInfo)
	}

	// Verify footer
	footer := make([]byte, 8) // "ENDGDLT3"
	n, err := archiveFile.Read(footer)
	if err != nil && err != io.EOF {
		result.Errors = append(result.Errors, fmt.Errorf("read footer: %w", err))
	}
	if n == 8 && string(footer) == format.ArchiveFooter03 {
		result.FooterValid = true
	} else {
		result.FooterValid = false
		result.Errors = append(result.Errors, fmt.Errorf("invalid footer: got %q, want %q", footer[:n], format.ArchiveFooter03))
	}

	result.StructureValid = result.HeaderValid && result.MetadataValid && result.DuplicatePaths == 0

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventComplete,
			Current: result.FileCount,
			Total:   result.FileCount,
			Message: "Verification complete",
		})
	}

	return nil
}
