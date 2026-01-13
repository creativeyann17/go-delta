// pkg/compress/errors.go
package compress

import "errors"

var (
	// ErrInputRequired is returned when input path is not specified
	ErrInputRequired = errors.New("input path is required")

	// ErrInvalidLevel is returned when compression level is out of range
	ErrInvalidLevel = errors.New("compression level must be between 1 and 22 (zstd) or 1-9 (zip)")

	// ErrNoFiles is returned when no files are found to compress
	ErrNoFiles = errors.New("no regular files found to compress")

	// ErrZipNoChunking is returned when trying to use chunking with ZIP format
	ErrZipNoChunking = errors.New("chunk-based deduplication is not supported in ZIP format")

	// ErrInvalidParallelism is returned when parallelism strategy is invalid
	ErrInvalidParallelism = errors.New("parallelism must be 'auto', 'folder', or 'file'")
)
