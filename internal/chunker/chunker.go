// internal/chunker/chunker.go
package chunker

import (
	"io"

	"github.com/jotfs/fastcdc-go"
	"github.com/zeebo/blake3"
)

// Chunker splits data into content-defined chunks using FastCDC
type Chunker struct {
	avgSize uint64
	minSize uint64
	maxSize uint64
}

// New creates a new chunker with the specified average chunk size.
// Actual chunks will vary between avgSize/4 and avgSize*4.
func New(avgSize uint64) *Chunker {
	return &Chunker{
		avgSize: avgSize,
		minSize: avgSize / 4,
		maxSize: avgSize * 4,
	}
}

// Chunk represents a piece of data with its hash
type Chunk struct {
	Data     []byte
	Hash     [32]byte
	OrigSize uint64
}

// Split reads from reader and splits into content-defined chunks using FastCDC.
// Returns all chunks with their BLAKE3 hashes.
// Chunk boundaries are determined by content patterns, not fixed positions,
// which enables effective deduplication even when data is shifted.
func (c *Chunker) Split(reader io.Reader) ([]Chunk, error) {
	opts := fastcdc.Options{
		AverageSize: int(c.avgSize),
		MinSize:     int(c.minSize),
		MaxSize:     int(c.maxSize),
	}

	chunker, err := fastcdc.NewChunker(reader, opts)
	if err != nil {
		return nil, err
	}

	chunks := make([]Chunk, 0, 8)

	for {
		fc, err := chunker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		// Copy data (FastCDC reuses buffer)
		data := make([]byte, len(fc.Data))
		copy(data, fc.Data)

		// Calculate BLAKE3 hash
		hash := blake3.Sum256(data)

		chunks = append(chunks, Chunk{
			Data:     data,
			Hash:     hash,
			OrigSize: uint64(len(data)),
		})
	}

	return chunks, nil
}

// ChunkSize returns the configured average chunk size
func (c *Chunker) ChunkSize() uint64 {
	return c.avgSize
}

// MinSize returns the minimum chunk size
func (c *Chunker) MinSize() uint64 {
	return c.minSize
}

// MaxSize returns the maximum chunk size
func (c *Chunker) MaxSize() uint64 {
	return c.maxSize
}
