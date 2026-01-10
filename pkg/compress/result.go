// pkg/compress/result.go
package compress

// Result contains statistics about the compression operation
type Result struct {
	// Total number of files found
	FilesTotal int

	// Number of files successfully compressed
	FilesProcessed int

	// Total original size in bytes
	OriginalSize uint64

	// Total compressed size in bytes
	CompressedSize uint64

	// List of errors encountered (non-fatal)
	Errors []error
}

// CompressionRatio returns the compression ratio as a percentage
func (r *Result) CompressionRatio() float64 {
	if r.OriginalSize == 0 {
		return 0
	}
	return float64(r.CompressedSize) / float64(r.OriginalSize) * 100
}

// Success returns true if all files were processed without errors
func (r *Result) Success() bool {
	return len(r.Errors) == 0 && r.FilesProcessed == r.FilesTotal
}
