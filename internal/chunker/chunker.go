// internal/chunker/chunker.go
package chunker

import (
	"io"

	"github.com/zeebo/blake3"
)

// Chunker splits data into fixed-size chunks
type Chunker struct {
	chunkSize uint64
}

// New creates a new chunker with the specified chunk size
func New(chunkSize uint64) *Chunker {
	return &Chunker{
		chunkSize: chunkSize,
	}
}

// Chunk represents a piece of data with its hash
type Chunk struct {
	Data     []byte
	Hash     [32]byte
	OrigSize uint64
}

// Split reads from reader and splits into fixed-size chunks
// Returns all chunks with their BLAKE3 hashes
func (c *Chunker) Split(reader io.Reader) ([]Chunk, error) {
	chunks := make([]Chunk, 0, 8)
	buffer := make([]byte, c.chunkSize)

	for {
		n, err := io.ReadFull(reader, buffer)
		if n > 0 {
			// Create chunk with actual data read
			chunkData := make([]byte, n)
			copy(chunkData, buffer[:n])

			// Calculate BLAKE3 hash
			hash := blake3.Sum256(chunkData)

			chunks = append(chunks, Chunk{
				Data:     chunkData,
				Hash:     hash,
				OrigSize: uint64(n),
			})
		}

		// Handle end of stream
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		// Any other error is a real failure
		if err != nil {
			return nil, err
		}
	}

	return chunks, nil
}

// ChunkSize returns the configured chunk size
func (c *Chunker) ChunkSize() uint64 {
	return c.chunkSize
}
