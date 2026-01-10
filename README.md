# go-delta

A smart delta compression tool for backups written in Go.

## Features

- **Zstandard compression** - Industry-leading compression with configurable levels (1-22)
- **Multi-threaded compression** - Utilize all CPU cores for fast parallel compression
- **Subdirectory support** - Recursively compress directory structures
- **CLI and Library** - Use as a command-line tool or Go library
- **Compress & Decompress** - Full round-trip support with integrity validation
- **Overwrite protection** - Safe decompression with optional overwrite mode

## Installation

### From source

```bash
git clone https://github.com/creativeyann17/go-delta.git
cd go-delta
make build
```

The binary will be in `bin/godelta`.

### Development setup

```bash
# Install git hooks for automatic code formatting
make install-hooks

# Run tests
make test

# Format code
make fmt
```

## CLI Usage

### Compress files

```bash
# Basic compression
godelta compress -i /path/to/files -o backup.delta

# With custom settings
godelta compress \
  --input /data \
  --output archive.delta \
  --threads 8 \
  --level 9 \
  --verbose

# Dry run to see what would be compressed
godelta compress -i /data -o test.delta --dry-run
```

### Decompress files

```bash
# Basic decompression
godelta decompress -i backup.delta -o /restore/path

# With overwrite (replace existing files)
godelta decompress -i backup.delta -o /restore/path --overwrite

# Verbose output
godelta decompress -i backup.delta -o /restore/path --verbose
```

### Compress Options

- `-i, --input`: Input file or directory (required)
- `-o, --output`: Output archive file (default: "archive.delta")
- `-t, --threads`: Max concurrent threads (default: CPU count)
- `-l, --level`: Compression level 1-22 (default: 5)
- `--dry-run`: Simulate without writing
- `--verbose`: Show detailed output
- `--quiet`: Minimal output

### Decompress Options

- `-i, --input`: Input archive file (required)
- `-o, --output`: Output directory (default: current directory)
- `-t, --threads`: Max concurrent threads (default: CPU count)
- `--overwrite`: Overwrite existing files
- `--verbose`: Show detailed output
- `--quiet`: Minimal output

## Archive Format

go-delta uses the GDELTA01 archive format:
- **Header**: Magic number + file count
- **Entry metadata**: Path, original size, compressed size, data offset
- **Compressed data**: Zstandard-compressed file contents

Files are stored sequentially with entry headers followed immediately by compressed data.

## Library Usage

### Compression Example

```go
package main

import (
    "fmt"
    "log"
    "github.com/creativeyann17/go-delta/pkg/compress"
)

func main() {
    opts := &compress.Options{
        InputPath:  "/path/to/files",
        OutputPath: "backup.delta",
        Level:      5,
        MaxThreads: 4,
    }

    result, err := compress.Compress(opts, nil)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Compressed %d files: %.2f MB -> %.2f MB (%.1f%%)\n",
        result.FilesProcessed,
        float64(result.OriginalSize)/1024/1024,
        float64(result.CompressedSize)/1024/1024,
        result.CompressionRatio())
}
```

### With Progress Callback

```go
progressCb := func(event compress.ProgressEvent) {
    switch event.Type {
    case compress.EventFileStart:
        fmt.Printf("Compressing %s...\n", event.FilePath)
    case compress.EventFileComplete:
        fmt.Printf("Done: %s\n", event.FilePath)
    case compress.EventComplete:
        fmt.Printf("Completed: %d files\n", event.Current)
    }
}

result, err := compress.Compress(opts, progressCb)
```

## API Reference

### Compression

#### `compress.Options`
```go
type Options struct {
    InputPath  string  // Source file/directory
    OutputPath string  // Output archive path
    MaxThreads int     // Concurrent threads (default: CPU count)
    Level      int     // Compression level 1-22 (default: 5)
    DryRun     bool    // Simulate without writing
    Verbose    bool    // Detailed logging
    Quiet      bool    // Suppress output
}
```

#### `compress.Result`
```go
type Result struct {
    FilesTotal     int      // Total files found
    FilesProcessed int      // Successfully compressed
    OriginalSize   uint64   // Total original bytes
    CompressedSize uint64   // Total compressed bytes
    Errors         []error  // Non-fatal errors
}

func (r *Result) CompressionRatio() float64  // Returns ratio as percentage
func (r *Result) Success() bool               // Returns true if no errors
```

### Decompression

#### `decompress.Options`
```go
type Options struct {
    InputPath  string  // Input archive file
    OutputPath string  // Output directory (default: ".")
    MaxThreads int     // Concurrent threads (currently unused, sequential)
    Overwrite  bool    // Overwrite existing files
    Verbose    bool    // Detailed logging
    Quiet      bool    // Suppress output
}
```

#### `decompress.Result`
```go
type Result struct {
    FilesTotal       int      // Total files in archive
    FilesProcessed   int      // Successfully decompressed
    CompressedSize   uint64   // Total compressed bytes
    DecompressedSize uint64   // Total decompressed bytes
    Errors           []error  // Non-fatal errors (e.g., file exists)
}
```

### Error Handling

Both compress and decompress operations return two types of errors:

1. **Fatal errors** - Returned as `error` (operation cannot continue)
2. **Non-fatal errors** - Collected in `result.Errors` (operation continues)

Common non-fatal errors:
- `decompress.ErrFileExists` - File already exists (use `--overwrite`)

## Development

### Build

```bash
make build          # Build for current platform -> bin/godelta
make build-all      # Cross-compile for linux/darwin/windows
make clean          # Remove build artifacts
```

### Testing

```bash
make test           # Run all tests
make fmt            # Format code with go fmt
```

The test suite includes:
- Round-trip compression/decompression with MD5 validation
- Subdirectory handling
- Empty file and directory edge cases
- Overwrite protection
- Duplicate compression/decompression scenarios

### Git Hooks

```bash
make install-hooks  # Install pre-commit hook
```

The pre-commit hook automatically runs `make fmt` before each commit to ensure code is properly formatted.

### Project Structure

```
cmd/godelta/           # CLI entry point
  ├── main.go         # Main entry and root command
  ├── compress_cmd.go # Compress subcommand
  ├── decompress_cmd.go # Decompress subcommand
  └── version_cmd.go  # Version subcommand

pkg/                  # Public API
  ├── compress/       # Compression implementation
  │   ├── compress.go
  │   ├── options.go
  │   ├── result.go
  │   └── integration_test.go
  └── decompress/     # Decompression implementation
      ├── decompress.go
      ├── options.go
      └── result.go

internal/format/      # Archive format (private)
  ├── archive.go      # Write operations
  └── reader.go       # Read operations

hooks/               # Git hooks (tracked)
  └── pre-commit     # Auto-format before commit
```

## CI/CD

The project uses GitHub Actions for continuous integration:

1. **Test** - Run all tests on push/tag
2. **Build** - Build binaries for all platforms (only if tests pass)
3. **Release** - Create GitHub release with binaries (only if build succeeds)

Workflow file: [.github/workflows/build-and-release.yml](.github/workflows/build-and-release.yml)

## Roadmap

Future planned features:
- [ ] Content-based deduplication across files
- [ ] Delta encoding between file versions
- [ ] Incremental backups
- [ ] Archive integrity verification command
- [ ] File filtering (include/exclude patterns)
- [ ] Benchmark suite

## License

See [LICENSE](LICENSE) file.
