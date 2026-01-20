// pkg/compress/options.go
package compress

import (
	"io"
	"runtime"
)

// Parallelism defines the parallelism strategy
type Parallelism string

const (
	// ParallelismAuto auto-detects based on input structure
	// Uses folder mode if enough folders, file mode otherwise
	ParallelismAuto Parallelism = "auto"

	// ParallelismFolder processes whole folders per worker (original behavior)
	// Best when: many folders with few files each
	ParallelismFolder Parallelism = "folder"

	// ParallelismFile processes individual files per worker with folder affinity
	// Files from same folder go to same worker for locality
	// Best when: flat directories or few folders with many files
	ParallelismFile Parallelism = "file"
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

	// Parallelism strategy: "auto", "folder", or "file"
	// Default: "auto"
	Parallelism Parallelism

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

	// UseDictionary enables GDELTA03 dictionary-based compression
	// Trains a zstd dictionary from input files for better compression
	// Especially effective for many small files with common patterns
	// Cannot be combined with ChunkSize or UseZipFormat
	// Default: false
	UseDictionary bool

	// DryRun simulates compression without writing
	DryRun bool

	// Verbose enables detailed logging
	Verbose bool

	// ProgressWriter receives progress updates (optional)
	// If nil and Quiet=false, progress goes to stdout
	ProgressWriter io.Writer

	// Quiet suppresses all output except errors
	Quiet bool

	// UseGitignore respects .gitignore files to exclude matching paths
	UseGitignore bool

	// DisableGC disables garbage collection during compression for maximum
	// throughput. Uses pooled buffers to minimize allocations. GC is re-enabled
	// after compression completes. Only affects ZIP compression mode.
	// Default: false
	DisableGC bool
}

// DefaultOptions returns options with sensible defaults
func DefaultOptions() *Options {
	return &Options{
		MaxThreads:      runtime.NumCPU(),
		Parallelism:     ParallelismAuto,
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

	// Validate parallelism strategy
	if o.Parallelism == "" {
		o.Parallelism = ParallelismAuto
	}
	switch o.Parallelism {
	case ParallelismAuto, ParallelismFolder, ParallelismFile:
		// valid
	default:
		return ErrInvalidParallelism
	}

	// Set default level if not specified
	if o.Level == 0 {
		o.Level = 5
	}

	// ZIP mode uses deflate compression (1-9 levels)
	if o.UseZipFormat {
		if o.Level < 1 || o.Level > 9 {
			return ErrInvalidLevelZip
		}
		if o.ChunkSize > 0 {
			return ErrZipNoChunking
		}
		if o.UseDictionary {
			return ErrZipNoDictionary
		}
	} else {
		// GDELTA mode uses zstd (1-22 levels)
		if o.Level < 1 || o.Level > 22 {
			return ErrInvalidLevelZstd
		}
	}

	// Dictionary mode is mutually exclusive with chunking
	if o.UseDictionary && o.ChunkSize > 0 {
		return ErrDictionaryNoChunking
	}

	// Validate chunk size bounds if chunking is enabled
	if o.ChunkSize > 0 {
		const minChunkSize = 4 * 1024         // 4KB minimum
		const maxChunkSize = 64 * 1024 * 1024 // 64MB maximum
		if o.ChunkSize < minChunkSize {
			return ErrChunkSizeTooSmall
		}
		if o.ChunkSize > maxChunkSize {
			return ErrChunkSizeTooLarge
		}
	}
	if o.Quiet {
		o.Verbose = false
	}
	return nil
}
