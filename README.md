# go-delta

[![Build and Release](https://github.com/creativeyann17/go-delta/actions/workflows/build-and-release.yml/badge.svg)](https://github.com/creativeyann17/go-delta/actions/workflows/build-and-release.yml)
[![GitHub release](https://img.shields.io/github/v/release/creativeyann17/go-delta)](https://github.com/creativeyann17/go-delta/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/creativeyann17/go-delta)](go.mod)
[![License](https://img.shields.io/github/license/creativeyann17/go-delta)](LICENSE)

A smart delta compression tool for backups written in Go.

## Features

- **Content-based deduplication** - Chunk-level deduplication with BLAKE3 hashing (GDELTA02 format)
- **Human-readable sizes** - Use `64KB`, `128MB`, `2GB` instead of raw byte counts
- **Smart memory management** - Auto-calculated thread memory with system RAM detection and safety warnings
- **Bounded chunk store** - LRU eviction prevents memory exhaustion on large datasets
- **Minimum chunk size enforcement** - 4KB minimum prevents metadata overhead from exceeding savings
- **Zstandard compression** - Industry-leading compression with configurable levels (1-22)
- **True parallel compression** - Folder-based worker pool with independent compression (no mutex contention)
- **Streaming architecture** - Temporary file streaming avoids loading compressed data into RAM
- **Robust cleanup** - Automatic temp file deletion on normal exit, errors, and interruptions (Ctrl+C)
- **Cross-platform** - Native system memory detection for Linux, macOS, and Windows
- **Subdirectory support** - Recursively compress directory structures
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
- `--thread-memory`: Max memory per thread (e.g. `128MB`, `1GB`, `0=auto`, default: 0)
- `-l, --level`: Compression level 1-22 (default: 5)
- `--chunk-size`: Chunk size for deduplication (e.g. `64KB`, `512KB`, min: `4KB`, `0=disabled`, default: 0)
- `--chunk-store-size`: Max in-memory dedup cache size (e.g. `1GB`, `500MB`, `0=unlimited`, default: 0)
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

- `-i, --input`: Input archive file (required, `.gdelta` extension auto-added if missing)
- `-o, --output`: Output directory (default: current directory)
- `--overwrite`: Overwrite existing files
- `--verbose`: Show detailed output
- `--quiet`: Minimal output

## Archive Formats

### GDELTA01 (Traditional)
Standard compression without deduplication:
- **Header**: Magic number + file count
- **Entry metadata**: Path, original size, compressed size, data offset
- **Compressed data**: Zstandard-compressed file contents

Files are stored sequentially with entry headers followed immediately by compressed data.

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
- Without `--chunk-size`: GDELTA01 (traditional)
- With `--chunk-size N`: GDELTA02 (chunked deduplication)

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

```go28 * 1024,           // 128 KB chunks
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

if result.TotalChunkscessed,
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

## API Reference

### Compression

#### `compress.Options`
```go
type Options struct {
    InputPath       string  // Source file/directory
    OutputPath      string  // Output archive path
    MaxThreadMemory uint64  // Max memory per thread in bytes (0=auto-calculate from input size)
    Level           int     // Compression level 1-22 (default: 5)
    ChunkSize       uint64  // Chunk size in bytes for dedup (0=disabled, min 4096, uses GDELTA01)
    ChunkStoreSize  uint64  // Max chunk store size in MB (0=unlimited, limits RAM not archive sizeed, uses GDELTA01)
    ChunkStoreSize  uint64  // Max chunk store size in MB (0=unlimited)
    DryRun          bool    // Simulate without writing
    Verbose         bool    // Detailed logging
    Quiet           bool    // Suppress output
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
- Subdirectory handling
- Empty file and directory edge cases
- Overwrite protection
- Duplicate compression/decompression scenarios

### Git Hooks

```bash
make install-hooks  # Install pre-commit hook
```

Th├── sysmem_linux.go   # Linux system memory detection
  ├── sysmem_darwin.go  # macOS system memory detection
  ├── sysmem_windows.go # Windows system memory detection
  └── version_cmd.go  # Version subcommand

pkg/                  # Public API
  ├── compress/       # Compression implementation
  │   ├── compress.go           # Main entry point
  │   ├── compress_chunked.go   # GDELTA02 chunked compression with streaming
  │   ├── options.go
  │   ├── result.go
  │   └── integration_test.go
  └── decompress/     # Decompression implementation
      ├── decompress.go         # Main entry point
      ├── decompress_chunked.go # GDELTA02 decompression
      ├── options.go
      └── result.go

internal/             # Private packages
  ├── format/         # Archive formats
  │   ├── archive.go  # GDELTA01 write
  │   ├── reader.go   # GDELTA01 read
  │   └── gdelta02.go # GDELTA02 read/write
  ├── chunker/        # Fixed-size chunking with BLAKE3
  │   ├── chunker.go
  │   └── chunker_test.go
  └── chunkstore/     # Thread-safe deduplication store with bounded LRU
      ├── store.go
      ├── store_test.go
      └── bounded       # Archive formats
  │   ├── archive.go  # GDELTA01 write
  │   ├── reader.go   # GDELTA01 read
  │   └── gdelta02.go # GDELTA02 read/write
  ├── chunker/        # Fixed-size chunking with BLAKE3
  │   ├── chunker.go
  │   └── chunker_test.go
  └── chunkstore/     # Thread-safe deduplication store
      ├── store.go
      └── store_test.go

hooks/               # Git hooks (tracked)
  └── pre-commit     # Auto-format before commit
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

Run tests with:
```basHuman-readable size parsing (64KB, 128MB, 2GB, etc.)
- [x] Auto-calculated thread memory with system RAM detection
- [x] Cross-platform system memory detection (Linux/macOS/Windows)
- [x] Memory safety warnings when thread allocation exceeds system RAM
- [x] Minimum chunk size enforcement (4KB) to prevent metadata overhead
- [x] Automatic temp file cleanup on interruption (Ctrl+C)
- [x] Correct deduplication statistics (TotalChunks vs UniqueChunks)
- [x] Comprehensive test suite with 35+ tests including bounded store tests

Future planned features:
- [ ] Variable-size chunking (content-defined chunking with FastCDC)
- [ ] Delta encoding between file versions
- [ ] Incremental backups with change detection
- [ ] Archive integrity verification command
- [ ] File filtering (include/exclude patterns)
- [ ] Encryption support with AES-256-GCM visualization
- [x] Content-based deduplication with chunk-level hashing (GDELTA02)
- [x] Bounded chunk store with LRU eviction (prevents OOM)
- [x] Streaming temp file architecture (avoids loading chunks into RAM)
- [x] Comprehensive test suite with 35+ tests including bounded store tests

Future planned features:
- [ ] Variable-size chunking (content-defined chunking)
- [ ] Delta encoding between file versions
- [ ] Incremental backups
- [ ] Archive integrity verification command
- [ ] File filtering (include/exclude patterns)
- [ ] Encryption support

## License

See [LICENSE](LICENSE) file.
