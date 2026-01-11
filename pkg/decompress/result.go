// pkg/decompress/result.go
package decompress

// Result contains statistics about the decompression operation
type Result struct {
	// Total number of files in archive
	FilesTotal int

	// Number of files successfully decompressed
	FilesProcessed int

	// Total compressed size in bytes
	CompressedSize uint64

	// Total decompressed size in bytes
	DecompressedSize uint64

	// List of errors encountered (non-fatal)
	Errors []error
}

// Success returns true if all files were processed without errors
func (r *Result) Success() bool {
	return len(r.Errors) == 0 && r.FilesProcessed == r.FilesTotal
}

// GetFilesTotal returns total files (interface method)
func (r *Result) GetFilesTotal() int {
	return r.FilesTotal
}

// GetFilesProcessed returns processed files (interface method)
func (r *Result) GetFilesProcessed() int {
	return r.FilesProcessed
}

// GetErrors returns the error list (interface method)
func (r *Result) GetErrors() []error {
	return r.Errors
}

// GetOriginalSize returns decompressed size (interface method)
func (r *Result) GetOriginalSize() uint64 {
	return r.DecompressedSize
}

// GetCompressedSize returns compressed size (interface method)
func (r *Result) GetCompressedSize() uint64 {
	return r.CompressedSize
}
