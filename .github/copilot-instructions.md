# Copilot Instructions for go-delta

## Project Overview
go-delta is a smart delta compression tool for backups written in Go. It creates efficient compressed archives from file sets using zstd compression and parallel processing.

**Purpose**: Reduce backup storage through efficient compression with multi-threaded processing and visual progress feedback.

## Architecture

### Package Structure
```
cmd/godelta/           - CLI entry point using Cobra framework
internal/format/       - GDELTA01 archive format (header/entry/footer)
pkg/compress/          - Compression logic with worker pools
pkg/decompress/        - Decompression logic
examples/              - Library usage examples
```

**Design Principle**: Follow Go's standard layout with `internal/` for private packages, `pkg/` for public APIs, and `cmd/` for executables.

### Key Dependencies
- **cobra**: CLI framework for subcommands
- **klauspost/compress**: High-performance zstd compression
- **vbauerster/mpb/v8**: Multi-progress bar library for concurrent task visualization

## Archive Format (GDELTA01)

Sequential binary format:
```
[Archive Header: "GDELTA01" + file count]
[File Entry 1: metadata + compressed data]
[File Entry 2: metadata + compressed data]
...
[Archive Footer: "ENDGDLT1"]
```

Each file entry contains:
- Relative path (variable length, null-terminated)
- Original size (uint64)
- Compressed size (uint64)
- Data offset (uint64)
- Compressed zstd data

## Development Workflow

### Building
```bash
make build          # Build for current platform → bin/godelta
make build-all      # Cross-compile for linux/darwin/windows (amd64/arm64) → dist/
make clean          # Remove bin/ and dist/
make test           # Run all tests with verbose output
make fmt            # Format code using go fmt
make install-hooks  # Install pre-commit hooks for auto-formatting
```

**Version Embedding**: The Makefile automatically injects version metadata via ldflags:
- `VERSION`: git tag or git commit hash
- `COMMIT`: short git hash
- `DATE`: ISO 8601 timestamp

### Testing
Integration tests in `pkg/compress/integration_test.go`:
- Round-trip compress/decompress with MD5 validation
- Subdirectory support
- Multiple compression scenarios
- Error handling

## Implemented Features

### Compression (`godelta compress`)
**Flags:**
- `-i, --input`: Source file or directory (required)
- `-o, --output`: Output archive file (auto-adds `.gdelta` if missing)
- `-t, --threads`: Max concurrent workers (default: CPU count)
- `-l, --level`: zstd compression level 1-22 (default: 5)
- `--dry-run`: Simulate without writing
- `--verbose`: Detailed output
- `--quiet`: Minimal output

**Features:**
- Parallel compression with worker pools
- Folder-based task chunking (files from same directory processed together for better locality)
- Multi-progress bars showing individual file progress + overall progress
- Automatic `.gdelta` extension addition
- Thread-safe archive writing with mutex serialization
- Progress events: Start, FileStart, FileProgress, FileComplete, Error, Complete

**Worker Strategy:**
Files are sorted by directory path before task distribution, ensuring:
- Files from the same folder are processed consecutively
- Better CPU cache locality
- More predictable folder-by-folder completion
- No architectural changes - just smarter task ordering

### Decompression (`godelta decompress`)
**Flags:**
- `-i, --input`: Archive file (required, auto-adds `.gdelta` if missing)
- `-o, --output`: Output directory (default: current dir)
- `--overwrite`: Overwrite existing files
- `--verbose`: Detailed output
- `--quiet`: Minimal output

**Features:**
- Sequential decompression (archive format requires it)
- Multi-progress bars (per-file + overall)
- Automatic `.gdelta` extension addition
- Error handling with position corruption prevention
- Skips compressed data on errors to maintain archive position

### Progress Visualization
Uses `mpb/v8` for sophisticated progress tracking:

**Non-verbose mode:**
- Individual progress bar per file being processed
- Shows: truncated filename, size (KiB), percentage
- Bars auto-remove on completion
- Overall "Total" bar at bottom showing files processed

**Verbose mode:**
- Completion messages per file with compression ratios
- Worker IDs (compression only)
- Detailed error messages

**Progress bar structure:**
```
...compress/file.go 5.2 / 10.5 KiB ▓▓▓▓▓▓░░░░  50 %
...compress/test.go 8.1 / 12.3 KiB ▓▓▓▓▓▓▓░░░  66 %
Total               25 / 30        ▓▓▓▓▓▓▓▓░░  83 %
```

## Code Conventions

### CLI Structure (Cobra)
Each command in `cmd/godelta/`:
- `main.go`: Root command and version
- `compress_cmd.go`: Compression command
- `decompress_cmd.go`: Decompression command

Shared utility: `truncateLeft()` function for path display (preserves filename, truncates from left)

### Progress Events
Standard event pattern in both compress and decompress:
```go
type ProgressEvent struct {
    Type           EventType
    FilePath       string
    Current        int64  // Current file progress
    Total          int64  // Total file size or file count
    CompressedSize uint64 // Compressed bytes
}
```

### Worker Pool Pattern (compress.go)
1. Sort files by directory for locality
2. Create worker goroutines with task channel
3. Serialize archive writes with mutex
4. Track progress with atomic counters
5. Close task channel to signal completion

### Error Handling
- Continue on file errors, collect in `result.Errors`
- Return final error if any errors occurred
- Progress bars handle errors gracefully (abort + increment overall)

## Extension Behavior
Both commands automatically add `.gdelta` extension if missing:
```go
if outputPath != "" && !strings.HasSuffix(outputPath, ".gdelta") {
    outputPath += ".gdelta"
}
```

## Key Files
- [Makefile](Makefile): Build system with cross-compilation and version injection
- [cmd/godelta/main.go](cmd/godelta/main.go): CLI entry point and Cobra setup
- [cmd/godelta/compress_cmd.go](cmd/godelta/compress_cmd.go): Compression command with mpb progress
- [cmd/godelta/decompress_cmd.go](cmd/godelta/decompress_cmd.go): Decompression command with mpb progress
- [pkg/compress/compress.go](pkg/compress/compress.go): Core compression logic with worker pool
- [pkg/decompress/decompress.go](pkg/decompress/decompress.go): Core decompression logic
- [internal/format/](internal/format/): Archive format handlers
- [go.mod](go.mod): Dependencies (cobra, zstd, mpb)

## Important Notes
- **Production Ready**: Core compress/decompress fully implemented and tested
- **Cross-Platform**: Build system supports Linux, macOS, Windows on amd64/arm64
- **Thread-Safe**: Mutex protection for archive writes, atomic counters for stats
- **Progress Feedback**: Sophisticated multi-bar visualization for long operations
- **Automatic Extensions**: User-friendly `.gdelta` extension handling
