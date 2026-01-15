// pkg/compress/errors.go
package compress

import "errors"

var (
	// ErrInputRequired is returned when input path is not specified
	ErrInputRequired = errors.New("input path is required")

	// ErrInvalidLevelZstd is returned when zstd compression level is out of range
	ErrInvalidLevelZstd = errors.New("compression level for GDELTA (zstd) must be between 1 and 22")

	// ErrInvalidLevelZip is returned when zip compression level is out of range
	ErrInvalidLevelZip = errors.New("compression level for ZIP (deflate) must be between 1 and 9")

	// ErrNoFiles is returned when no files are found to compress
	ErrNoFiles = errors.New("no regular files found to compress")

	// ErrZipNoChunking is returned when trying to use chunking with ZIP format
	ErrZipNoChunking = errors.New("chunk-based deduplication is not supported in ZIP format")

	// ErrInvalidParallelism is returned when parallelism strategy is invalid
	ErrInvalidParallelism = errors.New("parallelism must be 'auto', 'folder', or 'file'")

	// ErrChunkSizeTooSmall is returned when chunk size is below minimum
	ErrChunkSizeTooSmall = errors.New("chunk size must be at least 4KB (4096 bytes)")

	// ErrChunkSizeTooLarge is returned when chunk size exceeds reasonable maximum
	ErrChunkSizeTooLarge = errors.New("chunk size must not exceed 64MB (67108864 bytes)")
)
