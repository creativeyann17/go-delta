// pkg/decompress/errors.go
package decompress

import "errors"

var (
	// ErrInputRequired is returned when input path is not specified
	ErrInputRequired = errors.New("input archive path is required")

	// ErrInvalidArchive is returned when archive format is invalid
	ErrInvalidArchive = errors.New("invalid archive format")

	// ErrFileExists is returned when output file exists and overwrite is false
	ErrFileExists = errors.New("file exists (use --overwrite to replace)")
)
