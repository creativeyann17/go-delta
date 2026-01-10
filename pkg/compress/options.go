// pkg/compress/options.go
package compress

import (
	"io"
	"runtime"
)

// Options configures the compression behavior
type Options struct {
	// Input path (file or directory)
	InputPath string

	// Output archive path
	OutputPath string

	// Maximum number of concurrent compression threads
	// Default: runtime.NumCPU()
	MaxThreads int

	// Maximum memory per thread before flushing to disk (bytes)
	// 0 = unlimited (flush only at folder boundaries)
	// Default: 0
	MaxThreadMemory uint64

	// Compression level (1-22 for zstd)
	// 1=fastest, 9=balanced, 19+=maximum compression
	// Default: 5
	Level int

	// DryRun simulates compression without writing
	DryRun bool

	// Verbose enables detailed logging
	Verbose bool

	// ProgressWriter receives progress updates (optional)
	// If nil and Quiet=false, progress goes to stdout
	ProgressWriter io.Writer

	// Quiet suppresses all output except errors
	Quiet bool
}

// DefaultOptions returns options with sensible defaults
func DefaultOptions() *Options {
	return &Options{
		MaxThreads:      runtime.NumCPU(),
		MaxThreadMemory: 0, // Unlimited by default
		Level:           5,
		DryRun:          false,
		Verbose:         false,
		Quiet:           false,
	}
}

// Validate checks if options are valid
func (o *Options) Validate() error {
	if o.InputPath == "" {
		return ErrInputRequired
	}
	if o.OutputPath == "" {
		o.OutputPath = "archive.delta"
	}
	if o.MaxThreads <= 0 {
		o.MaxThreads = runtime.NumCPU()
	}
	if o.Level < 1 || o.Level > 22 {
		return ErrInvalidLevel
	}
	if o.Quiet {
		o.Verbose = false
	}
	return nil
}
