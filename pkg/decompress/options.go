// pkg/decompress/options.go
package decompress

import (
	"io"
	"runtime"
)

// Options configures the decompression behavior
type Options struct {
	// Input archive path
	InputPath string

	// Output directory path
	OutputPath string

	// Maximum number of concurrent decompression threads
	// Default: runtime.NumCPU()
	MaxThreads int

	// Verify decompressed data integrity (future feature)
	Verify bool

	// Verbose enables detailed logging
	Verbose bool

	// ProgressWriter receives progress updates (optional)
	ProgressWriter io.Writer

	// Quiet suppresses all output except errors
	Quiet bool

	// Overwrite existing files without prompting
	Overwrite bool
}

// DefaultOptions returns options with sensible defaults
func DefaultOptions() *Options {
	return &Options{
		MaxThreads: runtime.NumCPU(),
		Verify:     false,
		Verbose:    false,
		Quiet:      false,
		Overwrite:  false,
	}
}

// Validate checks if options are valid
func (o *Options) Validate() error {
	if o.InputPath == "" {
		return ErrInputRequired
	}
	if o.OutputPath == "" {
		o.OutputPath = "."
	}
	if o.MaxThreads <= 0 {
		o.MaxThreads = runtime.NumCPU()
	}
	if o.Quiet {
		o.Verbose = false
	}
	return nil
}
