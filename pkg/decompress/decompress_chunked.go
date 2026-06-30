// pkg/decompress/decompress_chunked.go
package decompress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

// maxChunkCacheBytes bounds the decompressed-chunk cache memory
const maxChunkCacheBytes = 128 * 1024 * 1024

// chunkCache holds decompressed chunks that will be referenced again, bounded
// by maxBytes. Reference counts are precomputed from the file metadata, so
// entries are freed exactly at their last use. Safe for concurrent use.
type chunkCache struct {
	mu       sync.Mutex
	refs     map[[32]byte]int
	data     map[[32]byte][]byte
	bytes    int
	maxBytes int
}

func newChunkCache(metadata []format.FileMetadata, maxBytes int) *chunkCache {
	refs := make(map[[32]byte]int)
	for _, m := range metadata {
		for _, h := range m.ChunkHashes {
			refs[h]++
		}
	}
	return &chunkCache{
		refs:     refs,
		data:     make(map[[32]byte][]byte),
		maxBytes: maxBytes,
	}
}

// take consumes one reference and returns the cached decompressed chunk if
// present. The returned slice is read-only for the caller; it stays valid
// even after the cache drops it at the last reference.
func (c *chunkCache) take(hash [32]byte) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refs[hash]--
	d, ok := c.data[hash]
	if ok && c.refs[hash] <= 0 {
		c.bytes -= len(d)
		delete(c.data, hash)
		delete(c.refs, hash)
	}
	return d, ok
}

// put stores a decompressed chunk if it will be needed again and the budget
// allows. Returns true if stored; the buffer's ownership then moves to the
// cache and the caller must stop writing into it.
func (c *chunkCache) put(hash [32]byte, d []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refs[hash] <= 0 || c.bytes+len(d) > c.maxBytes {
		return false
	}
	if _, exists := c.data[hash]; exists {
		return false
	}
	c.data[hash] = d
	c.bytes += len(d)
	return true
}

// decompressGDelta02 handles decompression of GDELTA02 archives with chunking.
// Files are reassembled in parallel: each worker reads chunk data through its
// own archive handle, and deduplicated chunks are shared via a bounded cache
// of decompressed data.
func decompressGDelta02(archiveFile *os.File, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Get archive file size for compressed size stat
	archiveInfo, err := archiveFile.Stat()
	if err != nil {
		return fmt.Errorf("stat archive file: %w", err)
	}
	result.CompressedSize = uint64(archiveInfo.Size())

	// Read GDELTA02 header
	_, fileCount, chunkCount, err := format.ReadGDelta02Header(archiveFile)
	if err != nil {
		return fmt.Errorf("read GDELTA02 header: %w", err)
	}

	result.FilesTotal = int(fileCount)

	if opts.Verbose {
		fmt.Printf("\nReading GDELTA02 archive...\n")
		fmt.Printf("  Files: %d\n", fileCount)
		fmt.Printf("  Unique chunks: %d\n", chunkCount)
	}

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:  EventStart,
			Total: int64(fileCount),
		})
	}

	// Read chunk index
	chunkIndex, err := format.ReadChunkIndex(archiveFile, chunkCount)
	if err != nil {
		return fmt.Errorf("read chunk index: %w", err)
	}

	// Read all file metadata
	fileMetadataList := make([]format.FileMetadata, fileCount)
	for i := uint32(0); i < fileCount; i++ {
		metadata, err := format.ReadFileMetadata(archiveFile)
		if err != nil {
			return fmt.Errorf("read file metadata %d: %w", i, err)
		}
		fileMetadataList[i] = metadata
	}

	// Get current position (start of chunk data section)
	chunkDataStart, err := archiveFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("get chunk data start: %w", err)
	}

	// Create output directory
	if err := os.MkdirAll(opts.OutputPath, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	cache := newChunkCache(fileMetadataList, maxChunkCacheBytes)

	// Reassemble files in parallel
	workers := opts.MaxThreads
	if workers > len(fileMetadataList) {
		workers = len(fileMetadataList)
	}

	var mu sync.Mutex // guards result and totals
	var totalDecompSize uint64
	var wg sync.WaitGroup
	fileCh := make(chan format.FileMetadata, workers*4)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Each worker reads through its own file handle (independent seeks)
			f, err := os.Open(opts.InputPath)
			if err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("open archive: %w", err))
				mu.Unlock()
				return
			}
			defer f.Close()

			decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
			if err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, fmt.Errorf("create zstd decoder: %w", err))
				mu.Unlock()
				return
			}
			defer decoder.Close()

			// Reusable buffers for compressed reads and decompressed scratch
			var readBuf, scratch []byte

			for metadata := range fileCh {
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventFileStart,
						FilePath: metadata.RelPath,
						Total:    int64(metadata.OrigSize),
					})
				}

				err := decompressChunkedFile(metadata, f, chunkDataStart, chunkIndex, cache, decoder, &readBuf, &scratch, opts, progressCb)

				if err != nil {
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", metadata.RelPath, err))
					mu.Unlock()
					if progressCb != nil {
						progressCb(ProgressEvent{Type: EventError, FilePath: metadata.RelPath})
					}
					continue
				}

				mu.Lock()
				result.FilesProcessed++
				totalDecompSize += metadata.OrigSize
				mu.Unlock()

				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:             EventFileComplete,
						FilePath:         metadata.RelPath,
						Current:          int64(metadata.OrigSize),
						Total:            int64(metadata.OrigSize),
						DecompressedSize: metadata.OrigSize,
					})
				}

				if opts.Verbose {
					fmt.Printf("Decompressed: %s (%d bytes)\n", metadata.RelPath, metadata.OrigSize)
				}
			}
		}()
	}

	for _, metadata := range fileMetadataList {
		fileCh <- metadata
	}
	close(fileCh)
	wg.Wait()

	result.DecompressedSize = totalDecompSize

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:       EventComplete,
			Current:    int64(result.FilesProcessed),
			Total:      int64(result.FilesTotal),
			TotalBytes: result.DecompressedSize,
		})
	}

	return nil
}

// decompressChunkedFile reassembles one file from its chunks. The archive
// handle, decoder and buffers are owned by the calling worker; the chunk
// cache is shared. On error the partial output file is removed.
func decompressChunkedFile(
	metadata format.FileMetadata,
	archiveFile *os.File,
	chunkDataStart int64,
	chunkIndex map[[32]byte]format.ChunkInfo,
	cache *chunkCache,
	decoder *zstd.Decoder,
	readBuf *[]byte,
	scratch *[]byte,
	opts *Options,
	progressCb ProgressCallback,
) error {
	// Build output path, rejecting entries that would escape OutputPath
	outputPath, err := safeJoin(opts.OutputPath, metadata.RelPath)
	if err != nil {
		return fmt.Errorf("%s: %w", metadata.RelPath, err)
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	// Check if file exists
	if !opts.Overwrite {
		if _, err := os.Stat(outputPath); err == nil {
			return ErrFileExists
		}
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	fail := func(err error) error {
		outFile.Close()
		os.Remove(outputPath)
		return err
	}

	reportProgress := func(bytesWritten uint64) {
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:         EventFileProgress,
				FilePath:     metadata.RelPath,
				Current:      int64(bytesWritten),
				Total:        int64(metadata.OrigSize),
				CurrentBytes: bytesWritten,
			})
		}
	}

	var bytesWritten uint64
	for _, chunkHash := range metadata.ChunkHashes {
		// Cached decompressed chunk: skip the read + decompress entirely
		if data, ok := cache.take(chunkHash); ok {
			n, err := outFile.Write(data)
			if err != nil {
				return fail(fmt.Errorf("write chunk: %w", err))
			}
			bytesWritten += uint64(n)
			reportProgress(bytesWritten)
			continue
		}

		chunkInfo, exists := chunkIndex[chunkHash]
		if !exists {
			return fail(fmt.Errorf("chunk not found: %x", chunkHash))
		}

		// Seek to chunk data
		if _, err := archiveFile.Seek(chunkDataStart+int64(chunkInfo.Offset), io.SeekStart); err != nil {
			return fail(fmt.Errorf("seek chunk: %w", err))
		}

		// Read compressed chunk into the reusable buffer
		if uint64(cap(*readBuf)) < chunkInfo.CompressedSize {
			*readBuf = make([]byte, chunkInfo.CompressedSize)
		}
		compressedData := (*readBuf)[:chunkInfo.CompressedSize]
		if _, err := io.ReadFull(archiveFile, compressedData); err != nil {
			return fail(fmt.Errorf("read chunk: %w", err))
		}

		// Decompress chunk in one call (appends into reusable scratch)
		decompressed, err := decoder.DecodeAll(compressedData, (*scratch)[:0])
		if err != nil {
			return fail(fmt.Errorf("decompress chunk: %w", err))
		}

		// Write decompressed chunk to output file
		n, err := outFile.Write(decompressed)
		if err != nil {
			return fail(fmt.Errorf("write chunk: %w", err))
		}
		bytesWritten += uint64(n)

		// Chunk referenced again later: hand the buffer to the cache and
		// drop our scratch reference; otherwise keep it for the next chunk.
		if cache.put(chunkHash, decompressed) {
			*scratch = nil
		} else {
			*scratch = decompressed
		}

		reportProgress(bytesWritten)
	}

	if err := outFile.Close(); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("close file: %w", err)
	}

	// Verify complete file was written
	if bytesWritten != metadata.OrigSize {
		os.Remove(outputPath)
		return fmt.Errorf("incomplete (wrote %d, expected %d)", bytesWritten, metadata.OrigSize)
	}

	return nil
}
