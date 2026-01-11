# Testing Documentation

## Test Coverage for GDELTA02

This document describes the comprehensive test suite for the chunked compression and deduplication (GDELTA02) functionality.

## Test Files

### 1. internal/chunker/chunker_test.go

Tests for the chunking engine that splits data into fixed-size blocks with BLAKE3 hashing.

**Tests:**
- `TestChunkerBasic`: Verifies basic chunking (28 bytes → 3 chunks of 10 bytes)
- `TestChunkerExactSize`: Single chunk when data equals chunk size
- `TestChunkerMultipleExactChunks`: Multiple exact-sized chunks
- `TestChunkerPartialLastChunk`: Handles partial final chunk correctly
- `TestChunkerEmptyData`: Zero chunks for empty input
- `TestChunkerSmallerThanChunkSize`: Small files fit in single chunk
- `TestChunkerHashUniqueness`: Different data produces different hashes
- `TestChunkerHashConsistency`: Same data produces same hashes
- `TestChunkerLargeData`: 1MB data with 64KB chunks, verifies reassembly
- `TestChunkerReadError`: Error handling for I/O failures

**Benchmarks:**
- `BenchmarkChunker1MB`: Performance for 1MB data (64KB chunks)
- `BenchmarkChunker10MB`: Performance for 10MB data (64KB chunks)

### 2. internal/chunkstore/store_test.go

Tests for the thread-safe deduplication store with bounded capacity and LRU eviction.

**Tests:**
- `TestStoreBasic`: Add chunks, verify deduplication works
- `TestStoreGet`: Retrieve chunks by hash
- `TestStoreStats`: TotalChunks, UniqueChunks, DedupedChunks, BytesSaved
- `TestStoreDedupRatio`: Deduplication ratio calculation (0-100%)
- `TestStoreAll`: Retrieve all stored chunks (including evicted metadata)
- `TestStoreCount`: Count of unique chunks
- `TestStoreConcurrency`: 100 goroutines × 10 chunks = 1000 chunks
- `TestStoreConcurrentDuplicates`: 50 goroutines adding same chunk
- `TestStoreWriteFunctionError`: Error handling in write function

**Bounded Store Tests:**
- `TestBoundedStoreCapacity`: Verifies capacity enforcement and eviction
- `TestBoundedStoreLRU`: Validates LRU eviction order
- `TestBoundedStoreUnlimited`: Confirms 0 = unlimited behavior
- `TestBoundedStoreRefCount`: Reference counting accuracy
- `TestBoundedStoreEvictionStats`: Eviction statistics tracking

**Benchmarks:**
- `BenchmarkStoreGetOrAddNew`: Add new chunks
- `BenchmarkStoreGetOrAddDuplicate`: Hit deduplication path
- `BenchmarkStoreGet`: Read-only lookups
- `BenchmarkBoundedStoreWithEviction`: Performance with LRU eviction
- `BenchmarkUnboundedStore`: Baseline performance

### 3. pkg/compress/compress_chunked_test.go

Integration tests for GDELTA02 compression and decompression.

**Tests:**
- `TestChunkedCompression`: Basic chunked compression with duplicate content
  - Creates 3 files: file1.txt="hello world", file2.txt="hello world", file3.txt="goodbye world"
  - Verifies deduplication stats populated (50% dedup ratio expected)
  
- `TestChunkedRoundTrip`: Full compress→decompress cycle
  - 5 files including dup1.txt==dup2.txt (identical content)
  - 16KB chunk size
  - Verifies byte-for-byte file integrity
  - Checks deduplication percentage (30% expected)
  
- `TestChunkedWithSubdirectories`: Cross-directory deduplication
  - Nested structure: dir1/file.txt, dir2/file.txt with identical content
  - Verifies chunks are deduplicated across directories
  - Confirms 6 chunks saved
  
- `TestChunkerSplitting`: Table-driven tests for various scenarios
  - Small file (50 bytes) with large chunks (100 bytes)
  - Exact chunk size (100 bytes with 100-byte chunks)
  - Multiple chunks (300 bytes with 100-byte chunks)
  - Large file (1MB with 64KB chunks)
  
- `TestChunkStoreDeduplication`: Direct store behavior testing
  - Creates 2 unique chunks + 1 duplicate
  - Verifies stats: 3 total, 2 unique, 1 deduped
  
- `TestEmptyFileWithChunking`: Edge case for zero-byte files
  - Ensures empty files compress/decompress without errors

### 4. pkg/compress/compress_zip_test.go

Integration tests for ZIP format compression and decompression.

**Tests:**
- `TestZipCompressDecompress`: Full round-trip compress→decompress cycle
  - Creates 3 test files with nested subdirectory
  - Compresses to ZIP format
  - Decompresses with godelta
  - Verifies MD5 hashes match original files
  - Validates ZIP archive can be opened with standard tools
  
- `TestZipCompressionLevels`: Compression level effectiveness
  - Tests levels 1, 5, and 9 with repetitive data (100KB)
  - Level 1 uses Store method (no compression)
  - Levels 5 and 9 use deflate compression
  - Verifies higher levels produce smaller or equal sizes
  
- `TestZipDryRun`: Dry-run mode validation
  - Ensures no output file is created
  - Stats are still calculated
  
- `TestZipWithChunkingShouldFail`: Validation test
  - Confirms `--zip` and `--chunk-size` cannot be combined
  - Returns `ErrZipNoChunking` error
  
- `TestZipThreadSafety`: Concurrent compression stress test
  - 100 files compressed with 8 worker threads
  - Validates thread-safe ZIP writes (mutex-protected)
  - Verifies all files present in final archive

## Test Statistics

### Total Coverage
- **10 tests** for chunker (+ 2 benchmarks)
- **14 tests** for chunkstore (+ 5 benchmarks) - includes bounded store tests
- **6 integration tests** for chunked compress/decompress (GDELTA02)
- **5 integration tests** for ZIP compress/decompress
- **Total: 35+ tests + 7 benchmarks**

### Key Scenarios Covered
✅ Fixed-size chunking with various data sizes  
✅ BLAKE3 hash uniqueness and consistency  
✅ Thread-safe deduplication with concurrent writes  
✅ **Bounded chunk store with LRU eviction**  
✅ **Chunk metadata preservation after eviction**  
✅ Round-trip integrity (compress → decompress → verify)  
✅ Cross-directory deduplication  
✅ Empty file handling  
✅ Error propagation  
✅ Statistics tracking (TotalChunks, UniqueChunks, DedupedChunks, BytesSaved, Evictions)  
✅ Performance benchmarks including eviction overhead  
✅ **ZIP format with deflate compression (levels 1-9)**  
✅ **ZIP round-trip integrity with MD5 validation**  
✅ **ZIP thread-safe concurrent writes**  
✅ **ZIP format validation (--zip + --chunk-size error)**  
✅ **Auto-detection of archive format (GDELTA vs ZIP)**  

## Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
go test ./... -cover

# Run specific package tests
go test ./internal/chunker -v
go test ./internal/chunkstore -v
go test ./pkg/compress -v

# Run benchmarks
go test ./internal/chunker -bench=. -benchmem
go test ./internal/chunkstore -bench=. -benchmem

# Run with race detector
go test ./... -race
```

## Expected Test Output

All tests should pass:
```
ok  github.com/creativeyann17/go-delta/internal/chunker     0.006s
ok  github.com/creativeyann17/go-delta/internal/chunkstore  0.002s
ok  github.com/creativeyann17/go-delta/pkg/compress        0.053s  (includes ZIP tests)
```

## Performance Benchmarks

Example benchmark results (vary by hardware):
```
BenchmarkChunker1MB             100 allocs/op
BenchmarkChunker10MB           1000 allocs/op
BenchmarkStoreGetOrAddNew      5000 ns/op
BenchmarkStoreGetOrAddDuplicate 100 ns/op
```

## Test Design Principles

1. **Isolation**: Each test creates its own temp directory via `t.TempDir()`
2. **Cleanup**: Automatic cleanup, no manual file removal needed
3. **Determinism**: Fixed data patterns ensure reproducible results
4. **Coverage**: Edge cases (empty, exact size, partial chunks) explicitly tested
5. **Concurrency**: Race conditions tested with 50-100 goroutines
6. **Integration**: Full round-trip tests validate entire pipeline

## Adding New Tests

When adding tests for GDELTA02:

1. Use `t.TempDir()` for file operations
2. Test both success and error paths
3. Verify deduplication statistics
4. Check round-trip integrity with different chunk sizes
5. Add benchmarks for performance-critical paths
6. Use table-driven tests for multiple scenarios

## Related Files

- [pkg/compress/compress_chunked.go](pkg/compress/compress_chunked.go) - GDELTA02 implementation
- [pkg/compress/compress_zip.go](pkg/compress/compress_zip.go) - ZIP implementation
- [pkg/decompress/decompress_chunked.go](pkg/decompress/decompress_chunked.go) - GDELTA02 decompression
- [pkg/decompress/decompress_zip.go](pkg/decompress/decompress_zip.go) - ZIP decompression
- [internal/chunker/chunker.go](internal/chunker/chunker.go) - Chunking logic
- [internal/chunkstore/store.go](internal/chunkstore/store.go) - Dedup store
- [internal/format/gdelta02.go](internal/format/gdelta02.go) - GDELTA02 archive format
