// pkg/verify/errors.go
package verify

import "errors"

var (
	// ErrInputRequired is returned when input path is not specified
	ErrInputRequired = errors.New("input path is required")

	// ErrInvalidMagic is returned when archive has invalid magic bytes
	ErrInvalidMagic = errors.New("invalid archive magic bytes")

	// ErrInvalidHeader is returned when archive header is malformed
	ErrInvalidHeader = errors.New("invalid archive header")

	// ErrInvalidFooter is returned when archive footer is invalid or missing
	ErrInvalidFooter = errors.New("invalid archive footer")

	// ErrInvalidChunkIndex is returned when chunk index is malformed (GDELTA02)
	ErrInvalidChunkIndex = errors.New("invalid chunk index")

	// ErrMissingChunk is returned when a referenced chunk is not in the index
	ErrMissingChunk = errors.New("referenced chunk not found in index")

	// ErrOrphanedChunk is returned when a chunk is not referenced by any file
	ErrOrphanedChunk = errors.New("chunk not referenced by any file")

	// ErrCorruptData is returned when decompressed data fails integrity check
	ErrCorruptData = errors.New("data corruption detected")

	// ErrTruncatedArchive is returned when archive appears truncated
	ErrTruncatedArchive = errors.New("archive appears truncated")

	// ErrUnsupportedFormat is returned for unknown archive formats
	ErrUnsupportedFormat = errors.New("unsupported archive format")
)
