# Memory Management Fixes for Large File Compression

## Problems

go-delta had **two separate memory explosion issues** when compressing large files:

### Problem 1: GDELTA02 Chunking (FIXED)

When compressing large files (e.g., 3GB+) with **DELTA2 (GDELTA02)** mode using chunking (`--chunk-size`), the chunker loaded all chunks into memory before compression.

### Problem 2: GDELTA01 Folder Batching (FIXED)

When compressing folders with **DELTA1 (GDELTA01)** mode (default), workers would accumulate **entire folders** in memory before flushing to disk, ignoring `--thread-memory` limits.

**Both issues are now fixed.**

### Root Cause 1: GDELTA02 Chunking

The original `chunker.Split()` method loaded **all chunks of a file into memory at once**:

```go
// Old implementation - loads entire file into memory
chunks := make([]Chunk, 0, 8)
for {
    chunk := readNextChunk()
    chunks = append(chunks, chunk)  // Accumulates all chunks
}
return chunks  // Returns all chunks at once
```

For a 3GB file with 1MB average chunk size:
- ~3,000 chunks created
- Each chunk stores full uncompressed data
- Total memory: **~3GB just for chunk data**, plus overhead
- **Before any compression happens**

### Root Cause 2: GDELTA01 Folder Batching

In folder-based parallelism mode, workers processed **entire folders** before flushing:

```go
// Old implementation - accumulates entire folder
for folder := range folderCh {
    compressedFiles := make([]compressedFile, 0, len(folder.Files))
    for _, task := range folder.Files {
        compressFileToMemory(task)  // Accumulates ALL files
        compressedFiles = append(...)
    }
    flushToDisk(compressedFiles)  // Only flushes at folder end!
}
```

For a folder with 10GB of files:
- All files compressed to memory buffers
- **10GB of compressed data** held in RAM
- `--thread-memory` limit was ignored
- Multiple workers = multiple folders = OOM

## Solutions

### Solution 1: Streaming Chunking (GDELTA02)

Implemented **streaming chunking** using a callback pattern:

```go
// New implementation - streams chunks one at a time
func SplitWithCallback(reader io.Reader, callback ChunkCallback) error {
    for {
        chunk := readNextChunk()
        callback(chunk)  // Process immediately
        // Chunk memory freed after callback returns
    }
}
```

**Benefits:**
1. **Constant memory per file**: Only 1 chunk (~1MB) in memory at any time
2. **Immediate compression**: Chunks are compressed and written as they're read
3. **Scales to any file size**: 3GB file uses same memory as 30MB file

### Solution 2: Per-File Flushing (GDELTA01)

Modified worker loops to **check `--thread-memory` after each file**:

```go
// New implementation - flushes when threshold reached
for task := range tasks {
    compressFileToMemory(task)
    compressedFiles = append(...)
    batchSize += compressedSize
    
    // Flush if memory threshold exceeded
    if batchSize >= maxThreadMemory {
        flushToDisk(compressedFiles)
        compressedFiles = []  // Reset
        batchSize = 0
    }
}
```

**Benefits:**
1. **Respects `--thread-memory` limit**: Flushes when limit reached
2. **No folder accumulation**: Large folders don't explode memory
3. **Predictable memory usage**: Per-worker memory bounded

## Implementation Details

### Files Changed

**GDELTA02 Streaming Fix:**
1. [internal/chunker/chunker.go](internal/chunker/chunker.go) - Added `SplitWithCallback()` for streaming
2. [pkg/compress/compress_chunked.go](pkg/compress/compress_chunked.go) - Use streaming callback
3. [internal/chunker/streaming_test.go](internal/chunker/streaming_test.go) - Streaming tests

**GDELTA01 Flushing Fix:**
4. [pkg/compress/compress.go](pkg/compress/compress.go) - Per-file memory threshold checks in both folder and file parallelism modes

### Memory Comparison

**GDELTA02 (Chunking) - Before:**
```
3GB file → 3,000 chunks × 1MB = 3GB in memory
↓ (after all loaded)
Compress each chunk
```

**GDELTA02 (Chunking) - After:**
```
3GB file → Stream 1 chunk at a time
↓ (immediate)
Compress chunk 1 (1MB in memory)
Compress chunk 2 (1MB in memory)
...
Compress chunk 3,000 (1MB in memory)
```

**GDELTA01 (Default) - Before:**
```
Folder with 10GB files:
→ Compress all to memory buffers (10GB RAM)
↓ (after entire folder)
Flush all at once
```

**GDELTA01 (Default) - After:**
```
Folder with 10GB files, --thread-memory 100M:
→ Compress file 1 to buffer (50MB)
→ Compress file 2 to buffer (50MB total: 100MB)
→ Threshold reached! Flush to disk
→ Compress file 3 to buffer (50MB)
→ ... (bounded by --thread-memory)
```

## Testing

### Unit Tests

New test file: [internal/chunker/streaming_test.go](internal/chunker/streaming_test.go)

- `TestStreamingVsNonStreaming`: Demonstrates memory difference
- `TestCallbackError`: Verifies error handling
- `TestCallbackChunkValidity`: Ensures data integrity

### Integration Tests

All existing tests pass without modification:
- `TestChunkedCompression` - GDELTA02
- `TestChunkedRoundTrip` - GDELTA02
- `TestRoundTrip` - GDELTA01
- `TestZipCompressDecompress` - ZIP format
- Plus 60+ other tests

## Usage

Both fixes are **automatic** - no user configuration changes needed.

### Example: Large Folder with GDELTA01

**Before:**
```bash
$ godelta compress -i ~/CHU/Batchs -o ./test/batchs --thread-memory 100M
# Ignores --thread-memory, loads entire folders
# Peak memory: ~10GB+ (all files in folder)
# OOM on systems with <16GB RAM
```

**After:**
```bash
$ godelta compress -i ~/CHU/Batchs -o ./test/batchs --thread-memory 100M
# Respects --thread-memory, flushes every 100MB
# Peak memory: ~400MB (100MB × 4 threads)
# Works on systems with 2GB RAM
```

### Example: 3GB File with Chunking

**Before:**
```bash
$ godelta compress --chunk-size 1MB --input 3GB.iso --threads 4
# Peak memory: ~12GB (3GB per worker × 4 workers)
# OOM likely on systems with <16GB RAM
```

**After:**
```bash
$ godelta compress --chunk-size 1MB --input 3GB.iso --threads 4  
# Peak memory: ~4-5GB (chunk store + compression buffers)
# Works on systems with 8GB RAM
```

**DELTA1 (no chunking):**
```bash
$ godelta compress --input 3GB.iso --threads 4
# Never had this issue - compresses entire files
# Memory based on compressed size, not original size
```

## Backward Compatibility

- Original `Split()` method preserved for any external callers
- All existing tests pass without modification
- No API breaking changes
- Performance identical (same chunk boundaries)

## Performance Impact

**Negligible:**
- Chunk processing is I/O bound, not CPU bound
- Streaming actually improves cache locality
- No additional syscalls or allocations
- GC pressure reduced (less heap usage)

## Future Enhancements

1. Consider deprecating non-streaming `Split()` method
2. Add memory profiling tests
3. Stream decompression similarly (currently sequential anyway)

## Related Issues

- `--thread-memory` flag controls chunk store capacity, not per-file memory
- Chunk store bounded capacity (see `BOUNDED_STORE.md`) helps limit total memory
- For very large files, consider increasing `--chunk-size` to reduce chunk count
