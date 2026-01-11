// internal/format/gdelta02.go
package format

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// GDELTA02 with chunking and deduplication
	ArchiveMagic02 = "GDELTA02"
)

// FileMetadata represents a file with its chunk references
type FileMetadata struct {
	RelPath     string
	OrigSize    uint64
	ChunkHashes [][32]byte // Ordered list of chunk hashes
}

// WriteGDelta02Header writes the GDELTA02 archive header
// Format: Magic(8) + ChunkSize(8) + FileCount(4) + ChunkCount(4)
func WriteGDelta02Header(w io.Writer, chunkSize uint64, fileCount uint32, chunkCount uint32) error {
	// Write magic
	if _, err := w.Write([]byte(ArchiveMagic02)); err != nil {
		return fmt.Errorf("write magic: %w", err)
	}

	// Write chunk size
	if err := binary.Write(w, binary.LittleEndian, chunkSize); err != nil {
		return fmt.Errorf("write chunk size: %w", err)
	}

	// Write file count
	if err := binary.Write(w, binary.LittleEndian, fileCount); err != nil {
		return fmt.Errorf("write file count: %w", err)
	}

	// Write chunk count
	if err := binary.Write(w, binary.LittleEndian, chunkCount); err != nil {
		return fmt.Errorf("write chunk count: %w", err)
	}

	return nil
}

// WriteChunkIndex writes the chunk index section
// Format: For each chunk: Hash(32) + Offset(8) + CompressedSize(8) + OriginalSize(8)
func WriteChunkIndex(w io.Writer, chunks map[[32]byte]ChunkInfo) error {
	for _, chunk := range chunks {
		// Write hash
		if _, err := w.Write(chunk.Hash[:]); err != nil {
			return fmt.Errorf("write chunk hash: %w", err)
		}

		// Write offset
		if err := binary.Write(w, binary.LittleEndian, chunk.Offset); err != nil {
			return fmt.Errorf("write chunk offset: %w", err)
		}

		// Write compressed size
		if err := binary.Write(w, binary.LittleEndian, chunk.CompressedSize); err != nil {
			return fmt.Errorf("write chunk compressed size: %w", err)
		}

		// Write original size
		if err := binary.Write(w, binary.LittleEndian, chunk.OriginalSize); err != nil {
			return fmt.Errorf("write chunk original size: %w", err)
		}
	}

	return nil
}

// WriteFileMetadata writes a single file metadata entry
// Format: PathLen(2) + Path + OrigSize(8) + ChunkCount(4) + Hashes(32*count)
func WriteFileMetadata(w io.Writer, metadata FileMetadata) error {
	// Write path length
	pathLen := uint16(len(metadata.RelPath))
	if err := binary.Write(w, binary.LittleEndian, pathLen); err != nil {
		return fmt.Errorf("write path length: %w", err)
	}

	// Write path
	if _, err := w.Write([]byte(metadata.RelPath)); err != nil {
		return fmt.Errorf("write path: %w", err)
	}

	// Write original size
	if err := binary.Write(w, binary.LittleEndian, metadata.OrigSize); err != nil {
		return fmt.Errorf("write original size: %w", err)
	}

	// Write chunk count
	chunkCount := uint32(len(metadata.ChunkHashes))
	if err := binary.Write(w, binary.LittleEndian, chunkCount); err != nil {
		return fmt.Errorf("write chunk count: %w", err)
	}

	// Write chunk hashes
	for _, hash := range metadata.ChunkHashes {
		if _, err := w.Write(hash[:]); err != nil {
			return fmt.Errorf("write chunk hash: %w", err)
		}
	}

	return nil
}

// ChunkInfo contains metadata about a stored chunk
type ChunkInfo struct {
	Hash           [32]byte
	Offset         uint64
	CompressedSize uint64
	OriginalSize   uint64
}

// WriteArchiveFooter02 writes the GDELTA02 footer
func WriteArchiveFooter02(w io.Writer) error {
	footer := []byte("ENDGDLT2")
	if _, err := w.Write(footer); err != nil {
		return fmt.Errorf("write footer: %w", err)
	}
	return nil
}

// ReadGDelta02Header reads and validates the GDELTA02 header
// Returns chunkSize, fileCount, chunkCount
func ReadGDelta02Header(r io.Reader) (chunkSize uint64, fileCount uint32, chunkCount uint32, err error) {
	// Read and verify magic
	magic := make([]byte, 8)
	if _, err := io.ReadFull(r, magic); err != nil {
		return 0, 0, 0, fmt.Errorf("read magic: %w", err)
	}
	if string(magic) != ArchiveMagic02 {
		return 0, 0, 0, fmt.Errorf("invalid magic: got %q, want %q", magic, ArchiveMagic02)
	}

	// Read chunk size
	if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
		return 0, 0, 0, fmt.Errorf("read chunk size: %w", err)
	}

	// Read file count
	if err := binary.Read(r, binary.LittleEndian, &fileCount); err != nil {
		return 0, 0, 0, fmt.Errorf("read file count: %w", err)
	}

	// Read chunk count
	if err := binary.Read(r, binary.LittleEndian, &chunkCount); err != nil {
		return 0, 0, 0, fmt.Errorf("read chunk count: %w", err)
	}

	return chunkSize, fileCount, chunkCount, nil
}

// ReadChunkIndex reads the chunk index section
func ReadChunkIndex(r io.Reader, chunkCount uint32) (map[[32]byte]ChunkInfo, error) {
	chunks := make(map[[32]byte]ChunkInfo, chunkCount)

	for i := uint32(0); i < chunkCount; i++ {
		var chunk ChunkInfo

		// Read hash
		if _, err := io.ReadFull(r, chunk.Hash[:]); err != nil {
			return nil, fmt.Errorf("read chunk hash %d: %w", i, err)
		}

		// Read offset
		if err := binary.Read(r, binary.LittleEndian, &chunk.Offset); err != nil {
			return nil, fmt.Errorf("read chunk offset %d: %w", i, err)
		}

		// Read compressed size
		if err := binary.Read(r, binary.LittleEndian, &chunk.CompressedSize); err != nil {
			return nil, fmt.Errorf("read chunk compressed size %d: %w", i, err)
		}

		// Read original size
		if err := binary.Read(r, binary.LittleEndian, &chunk.OriginalSize); err != nil {
			return nil, fmt.Errorf("read chunk original size %d: %w", i, err)
		}

		chunks[chunk.Hash] = chunk
	}

	return chunks, nil
}

// ReadFileMetadata reads a single file metadata entry
func ReadFileMetadata(r io.Reader) (FileMetadata, error) {
	var metadata FileMetadata

	// Read path length
	var pathLen uint16
	if err := binary.Read(r, binary.LittleEndian, &pathLen); err != nil {
		return metadata, fmt.Errorf("read path length: %w", err)
	}

	// Read path
	pathBytes := make([]byte, pathLen)
	if _, err := io.ReadFull(r, pathBytes); err != nil {
		return metadata, fmt.Errorf("read path: %w", err)
	}
	metadata.RelPath = string(pathBytes)

	// Read original size
	if err := binary.Read(r, binary.LittleEndian, &metadata.OrigSize); err != nil {
		return metadata, fmt.Errorf("read original size: %w", err)
	}

	// Read chunk count
	var chunkCount uint32
	if err := binary.Read(r, binary.LittleEndian, &chunkCount); err != nil {
		return metadata, fmt.Errorf("read chunk count: %w", err)
	}

	// Read chunk hashes
	metadata.ChunkHashes = make([][32]byte, chunkCount)
	for i := uint32(0); i < chunkCount; i++ {
		if _, err := io.ReadFull(r, metadata.ChunkHashes[i][:]); err != nil {
			return metadata, fmt.Errorf("read chunk hash %d: %w", i, err)
		}
	}

	return metadata, nil
}
