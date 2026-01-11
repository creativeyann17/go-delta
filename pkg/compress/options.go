// pkg/compress/options.go
package compress

import (
	"io"
	"runtime"
)

// Options configures the compression behavior
type Options struct {
	// Input path (file or directory)
	// Ignored if Files is provided
	InputPath string

	// Files allows library users to provide a custom list of files/folders to compress
	// When set, InputPath is ignored
	// Each path can be absolute or relative, file or directory
	// This option is for library use only (not exposed in CLI)
	Files []string

	// Output archive path
	OutputPath string

	// Maximum number of concurrent compression threads
	// Default: runtime.NumCPU()
	MaxThreads int

	// Maximum memory per thread before flushing to disk (bytes)
	// 0 = unlimited (flush only at folder boundaries)
	// Default: 0
	MaxThreadMemory uint64

	// Chunk size for content-based deduplication (bytes)
	// 0 = disabled (traditional file-level compression)
	// Default: 0
	ChunkSize uint64

	// Maximum chunk store size in MB (bounds memory usage for deduplication)
	// Calculated as: maxChunks = ChunkStoreSize / (ChunkSize / 1MB)
	// 0 = unlimited (store all unique chunks)
	// Default: 0
	ChunkStoreSize uint64

	// Compression level (1-22 for zstd, 1-9 for zip deflate)
	// 1=fastest, 9=balanced, 19+=maximum compression (zstd only)
	// Default: 5
	Level int

	// UseZipFormat creates a standard ZIP archive instead of GDELTA format
	// Uses Deflate compression (universally compatible)
	// Cannot be combined with ChunkSize (deduplication not supported in ZIP mode)
	// Default: false
	UseZipFormat bool

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
		ChunkSize:       0, // Chunking disabled by default
		Level:           5,
		DryRun:          false,
		Verbose:         false,
		Quiet:           false,
	}
}

// Validate checks if options are valid
func (o *Options) Validate() error {
	if o.InputPath == "" && len(o.Files) == 0 {
		return ErrInputRequired
	}
	if o.OutputPath == "" {
		o.OutputPath = "archive.delta"
	}
	if o.MaxThreads <= 0 {
		o.MaxThreads = runtime.NumCPU()
	}

	// Set default level if not specified
	if o.Level == 0 {
		o.Level = 5
	}

	// ZIP mode uses deflate compression (1-9 levels)
	if o.UseZipFormat {
		if o.Level < 1 || o.Level > 9 {
			return ErrInvalidLevel
		}
		if o.ChunkSize > 0 {
			return ErrZipNoChunking
		}
	} else {
		// GDELTA mode uses zstd (1-22 levels)
		if o.Level < 1 || o.Level > 22 {
			return ErrInvalidLevel
		}
	}
	if o.Quiet {
		o.Verbose = false
	}
	return nil
}
