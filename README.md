# go-delta

[![Build and Release](https://github.com/creativeyann17/go-delta/actions/workflows/build-and-release.yml/badge.svg)](https://github.com/creativeyann17/go-delta/actions/workflows/build-and-release.yml)
[![GitHub release](https://img.shields.io/github/v/release/creativeyann17/go-delta)](https://github.com/creativeyann17/go-delta/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/creativeyann17/go-delta)](go.mod)
[![License](https://img.shields.io/github/license/creativeyann17/go-delta)](LICENSE)

A smart delta compression tool for backups written in Go.

## Features

- **Multiple compression formats** - GDELTA (custom format with optional deduplication) or standard ZIP (universal compatibility)
- **Content-based deduplication** - Chunk-level deduplication with BLAKE3 hashing (GDELTA02 format)
- **Human-readable sizes** - Use `64KB`, `128MB`, `2GB` instead of raw byte counts
- **Smart memory management** - Auto-calculated thread memory with system RAM detection and safety warnings
- **Bounded chunk store** - LRU eviction prevents memory exhaustion on large datasets
- **Minimum chunk size enforcement** - 4KB minimum prevents metadata overhead from exceeding savings
- **Zstandard compression** - Industry-leading compression with configurable levels (1-22) for GDELTA
- **Deflate compression** - Standard ZIP deflate compression (levels 1-9) for universal compatibility
- **True parallel compression** - Folder-based worker pool with independent compression (no mutex contention)
- **Streaming architecture** - Temporary file streaming avoids loading compressed data into RAM
- **Robust cleanup** - Automatic temp file deletion on normal exit, errors, and interruptions (Ctrl+C)
- **Cross-platform** - Native system memory detection for Linux, macOS, and Windows
- **Subdirectory support** - Recursively compress directory structures
- **Custom file selection** - Library API supports custom file/folder lists (independent of directory structure)
- **Progress visualization** - Multi-bar progress tracking for concurrent operations
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

# Enable chunk-based deduplication (64KB chunks recommended)
godelta compress \
  --input /data \
  --output archive.delta \
  --chunk-size 64KB \
  --verbose

# Deduplication with bounded memory (5GB chunk store limit)
# Store keeps metadata for all chunks but evicts LRU chunk data
godelta compress \
  --input /data \
  --output archive.delta \
  --chunk-size 128KB \
  --chunk-store-size 5GB \
  --thread-memory 2GB \
  --verbose

# Auto-calculate thread memory from input size
godelta compress \
  --input /large/dataset \
  --output backup.delta \
  --threads 16 \
  --thread-memory 0

# Dry run to see what would be compressed
godelta compress -i /data -o test.delta --dry-run

# Create standard ZIP archive (universal compatibility)
# Multi-threaded ZIP creates multiple archive files for true parallelism
# Example: --threads 8 creates archive_01.zip through archive_08.zip
godelta compress \
  --input /data \
  --output archive.zip \
  --zip \
  --level 9 \
  --threads 8
```

**Note**: ZIP format with multiple threads creates one archive file per thread (e.g., `archive_01.zip`, `archive_02.zip`, etc.) for true parallel compression without mutex contention. Decompression auto-detects and extracts all parts.

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
- `--thread-memory`: Max memory per thread (e.g. `128MB`, `1GB`, `0=auto`, default: 0)
- `-l, --level`: Compression level 1-9 for ZIP, 1-22 for GDELTA (default: 5)
- `--chunk-size`: Chunk size for deduplication (e.g. `64KB`, `512KB`, min: `4KB`, `0=disabled`, default: 0, GDELTA only)
- `--chunk-store-size`: Max in-memory dedup cache size (e.g. `1GB`, `500MB`, `0=unlimited`, default: 0, GDELTA only)
- `--zip`: Create standard ZIP archive instead of GDELTA format (universally compatible, no deduplication)
- `--dry-run`: Simulate without writing
- `--verbose`: Show detailed output including chunk statistics
- `--quiet`: Minimal output

**Size format**: All size parameters accept human-readable formats:
- Bytes: `1024B` or `1024`
- Kilobytes: `64KB` or `64K`
- Megabytes: `128MB` or `128M`
- Gigabytes: `2GB` or `2G`
- Terabytes: `1TB` or `1T`

### Decompress Options

- `-i, --input`: Input archive file (required, auto-detects `.gdelta` or `.zip` format)
- `-o, --output`: Output directory (default: current directory)
- `--overwrite`: Overwrite existing files
- `--verbose`: Show detailed output
- `--quiet`: Minimal output

**Note**: Decompression automatically detects the archive format (GDELTA01, GDELTA02, or ZIP) by reading the file signature.

## Archive Formats

### ZIP (Standard)
Standard ZIP archive format with deflate compression:
- **Universal compatibility**: Works with any ZIP tool (unzip, 7zip, WinZip, etc.)
- **Deflate compression**: Industry-standard compression (levels 1-9)
- **Multi-part parallel compression**: Each worker thread creates its own ZIP file for true parallelism (no mutex bottleneck)
- **No deduplication**: Each file compressed independently
- **Use case**: Maximum portability, sharing archives, integration with existing tools

**Multi-threaded behavior**: When using multiple threads (e.g., `--threads 8`), godelta creates one ZIP file per thread:
- Single thread: `backup.zip`
- Multi-threaded: `backup_01.zip`, `backup_02.zip`, ..., `backup_08.zip`
- Files are distributed evenly across worker ZIPs
- True parallel writes (no serialization bottleneck)
- Decompression auto-detects and extracts all parts

**Performance**: Slightly slower than GDELTA01 (deflate vs zstd), but universally compatible.

```bash
# Create ZIP archive (creates backup_01.zip through backup_08.zip with 8 threads)
godelta compress -i /data -o backup.zip --zip --level 9 --threads 8

# Extract with godelta (auto-detects all parts)
godelta decompress -i backup_01.zip -o /restore

# Or extract individual parts with standard tools
unzip -d /restore backup_01.zip
unzip -d /restore backup_02.zip
# ... etc
```

### GDELTA01 (Traditional)
Custom format with zstandard compression (no deduplication):
- **Header**: Magic number + file count
- **Entry metadata**: Path, original size, compressed size, data offset
- **Compressed data**: Zstandard-compressed file contents

Files are stored sequentially with entry headers followed immediately by compressed data.

**Performance**: Fastest compression, best compression ratio (zstd), no deduplication overhead.

### GDELTA02 (Chunked with Deduplication)
Content-based deduplication using fixed-size chunks:
- **Header**: Magic number + chunk size + counts
- **Chunk Index**: Hash → offset mapping for all unique chunks
- **File Metadata**: Path + chunk hash list for each file
- **Chunk Data**: Deduplicated compressed chunks
- **Footer**: End marker

**Deduplication benefits:**
- Shared content across files stored once
- BLAKE3 hashing for chunk identification
- Configurable chunk size (larger = less overhead, smaller = more dedup)
- **Bounded chunk store with LRU eviction** (prevents OOM on large datasets)
- **Streaming temp file architecture** (compressed chunks written to disk, not RAM)
- Statistics: Total chunks, unique chunks, deduplication ratio, bytes saved, evictions

**Memory management:**
- **Chunk metadata** (~56 bytes per chunk in archive index + ~32 bytes per file reference)
- **In-memory overhead** (~120 bytes per chunk: metadata + LRU structures)
- **Deduplication cache** (LRU): Evicts least-recently-used chunks when `--chunk-store-size` limit reached
- **Compressed chunk data**: Written to temporary file during compression, streamed to final archive
- **Temp file cleanup**: Automatic cleanup on normal exit, errors, and user interruption (Ctrl+C)
- **Thread memory**: Auto-calculated from input size when `--thread-memory 0`, with safety warnings if exceeding system RAM
- **Cross-platform memory detection**: Linux (sysinfo), macOS (sysctl), Windows (GlobalMemoryStatusEx)

**Minimum chunk size: 4 KB**
- Chunks smaller than 4KB have metadata overhead that exceeds compression benefits
- Each chunk requires 56 bytes in the archive index + 32 bytes per file reference
- Recommended range: **64KB - 512KB** for optimal balance

**⚠️ IMPORTANT: Chunk deduplication only benefits repetitive data**
- **Use chunking for**: VM images, database backups, log files, incremental backups, source code repositories
- **DON'T use chunking for**: Unique media files (photos, videos, music), compressed archives, encrypted data, random data
- **Why**: Metadata overhead (56 bytes per chunk) can make archive LARGER if there's little duplication
- **Example**: 5 million unique 10KB chunks = ~421 MB of pure metadata overhead
- **Rule of thumb**: If you don't expect at least 10% duplication, disable chunking (`--chunk-size 0`)

**When to use GDELTA02:**
- Backups with duplicate files (e.g., VM images, database dumps, logs with repeated patterns)
- Similar files with repeated content (e.g., source code with shared libraries, config files)
- Large datasets with redundant blocks (e.g., incremental backups, version-controlled data)
- **NOT recommended for**: Collections of unique compressed files, media libraries, encrypted archives

**Format selection:**
- With `--zip`: ZIP format (deflate compression, universal compatibility)
- Without `--chunk-size` or `--zip`: GDELTA01 (zstd compression, fastest)
- With `--chunk-size N`: GDELTA02 (zstd + deduplication)

**Note**: `--zip` and `--chunk-size` cannot be combined (ZIP does not support deduplication).

## Architecture

### Folder-Based Parallelism

go-delta achieves true parallel compression by grouping files by their parent directory:

1. **File Grouping**: Files are organized into folder-based tasks
2. **Parallel Compression**: Workers compress files independently (no locks during compression)
3. **Minimal Mutex Locking**: Lock only during quick archive writes or chunk store updates
4. **Streaming Architecture**: Compressed chunks written to temporary file, then streamed to archive

**Example workflow with 4 threads:**
```
Worker 1: Compress /src/utils/* → Write chunks to temp file → Update chunk store
Worker 2: Compress /src/models/* → Write chunks to temp file → Update chunk store (parallel!)
Worker 3: Compress /docs/* → Write chunks to temp file → Update chunk store
Worker 4: Compress /tests/* → Write chunks to temp file → Update chunk store
```

**Bounded memory** (when `--chunk-store-size` is set):
- LRU eviction keeps only most-recently-used chunks in deduplication cache
- Evicted chunks remain in archive (metadata preserved, just removed from cache)
- Prevents OOM on large datasets while maintaining full deduplication capability

### Progress Tracking

Multi-progress bar visualization using [mpb/v8](https://github.com/vbauerster/mpb):
- Individual progress bar per file being compressed
- Overall progress bar showing total completion
- Bars auto-remove on completion for clean output

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

### With Chunk-Based Deduplication

```go
opts := &compress.Options{
    InputPath:       "/path/to/files",
    OutputPath:      "backup.delta",
    MaxThreads:      4,
    Level:           5,
    ChunkSize:       128 * 1024,           // 128 KB chunks
    ChunkStoreSize:  5 * 1024,             // 5 GB chunk store limit (in MB)
    MaxThreadMemory: 2 * 1024 * 1024 * 1024, // 2 GB per thread
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

if result.ChunkSize > 0 {
    fmt.Printf("Deduplication: %d/%d chunks deduplicated (%.1f%%), %.2f MB saved\n",
        result.DedupedChunks,
        result.TotalChunks,
        result.DedupRatio(),
        float64(result.BytesSaved)/1024/1024)
}
```

### With Custom File List (Library Only)

```go
// Compress specific files/folders without using InputPath
opts := &compress.Options{
    Files: []string{
        "/path/to/file1.txt",
        "/path/to/folder1",
        "/another/path/file2.log",
        "relative/path/to/folder",
    },
    OutputPath: "custom.delta",
    MaxThreads: 4,
    Level:      9,
}

result, err := compress.Compress(opts, nil)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Compressed %d files from custom list\n", result.FilesProcessed)
```

**Note**: When using `Files`, the `InputPath` option is ignored. Each path in `Files` can be absolute or relative, and can point to files or directories. This option is designed for library use only and is not exposed in the CLI.

## API Reference

### Compression

#### `compress.Options`
```go
type Options struct {
    InputPath       string   // Source file/directory (ignored if Files is provided)
    Files           []string // Custom list of files/folders to compress (library only, overrides InputPath)
    OutputPath      string   // Output archive path
    MaxThreads      int      // Max concurrent threads (default: CPU count)
    MaxThreadMemory uint64   // Max memory per thread in bytes (0=auto-calculate from input size)
    Level           int      // Compression level 1-22 for GDELTA, 1-9 for ZIP (default: 5)
    ChunkSize       uint64   // Chunk size in bytes for dedup (0=disabled, min 4096, GDELTA only)
    ChunkStoreSize  uint64   // Max chunk store size in MB (0=unlimited, GDELTA only)
    UseZipFormat    bool     // Create ZIP archive instead of GDELTA (no deduplication)
    DryRun          bool     // Simulate without writing
    Verbose         bool     // Detailed logging
    Quiet           bool     // Suppress output
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
    
    // Deduplication statistics (GDELTA02 only)
    TotalChunks    uint64   // Total chunks processed (including duplicates)
    UniqueChunks   uint64   // Unique chunks stored in archive
    DedupedChunks  uint64   // Chunks deduplicated (found in cache, not re-written)
    BytesSaved     uint64   // Compressed bytes saved by deduplication
    Evictions      uint64   // Chunks evicted from bounded store (only affects RAM, not archive)
}

func (r *Result) CompressionRatio() float64  // Returns ratio as percentage
func (r *Result) DedupRatio() float64        // Returns dedup ratio as percentage (DedupedChunks/TotalChunks)
func (r *Result) DedupRatio() float64        // Returns dedup ratio as percentage
func (r *Result) Success() bool               // Returns true if no errors
```

### Decompression

#### `decompress.Options`
```go
type Options struct {
    InputPath  string  // Input archive file
    OutputPath string  // Output directory (default: ".")
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
    CompressedSize   uint64   // Archive file size in bytes
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
- ZIP format with multi-part archive creation and extraction
- Subdirectory handling
- Empty file and directory edge cases
- Overwrite protection
- Duplicate compression/decompression scenarios
- Thread safety and parallel processing

### Git Hooks

```bash
make install-hooks  # Install pre-commit hook
```

## CI/CD

The project uses GitHub Actions for continuous integration:

1. **Test** - Run all tests on push/tag
2. **Build** - Build binaries for all platforms (only if tests pass)
3. **Release** - Create GitHub release with binaries (only if build succeeds)

Workflow file: [.github/workflows/build-and-release.yml](.github/workflows/build-and-release.yml)

## Testing

Comprehensive test suite with 35+ tests covering:
- Fixed-size chunking with BLAKE3 hashing
- Thread-safe deduplication with bounded LRU store
- LRU eviction under capacity pressure
- Round-trip compression/decompression with integrity checks
- Cross-directory deduplication
- Concurrent operations
- Error handling and edge cases


## License

See [LICENSE](LICENSE) file.
