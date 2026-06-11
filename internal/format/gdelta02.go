// internal/format/gdelta02.go
package format

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
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

// chunkIndexEntrySize is the on-disk size of one chunk index entry:
// Hash(32) + Offset(8) + CompressedSize(8) + OriginalSize(8)
const chunkIndexEntrySize = 56

// WriteChunkIndex writes the chunk index section
// Format: For each chunk: Hash(32) + Offset(8) + CompressedSize(8) + OriginalSize(8)
// Chunks are sorted by hash for deterministic output.
// The whole index is encoded into one buffer and written in a single call
// (per-field binary.Write uses reflection and a syscall per field).
func WriteChunkIndex(w io.Writer, chunks map[[32]byte]ChunkInfo) error {
	// Sort hashes for deterministic output
	hashes := make([][32]byte, 0, len(chunks))
	for hash := range chunks {
		hashes = append(hashes, hash)
	}
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i][:], hashes[j][:]) < 0
	})

	buf := make([]byte, chunkIndexEntrySize*len(hashes))
	pos := 0
	for _, hash := range hashes {
		chunk := chunks[hash]
		copy(buf[pos:], chunk.Hash[:])
		binary.LittleEndian.PutUint64(buf[pos+32:], chunk.Offset)
		binary.LittleEndian.PutUint64(buf[pos+40:], chunk.CompressedSize)
		binary.LittleEndian.PutUint64(buf[pos+48:], chunk.OriginalSize)
		pos += chunkIndexEntrySize
	}

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write chunk index: %w", err)
	}

	return nil
}

// WriteFileMetadata writes a single file metadata entry as one buffered write
// Format: PathLen(2) + Path + OrigSize(8) + ChunkCount(4) + Hashes(32*count)
func WriteFileMetadata(w io.Writer, metadata FileMetadata) error {
	if len(metadata.RelPath) > 65535 {
		return fmt.Errorf("path too long for archive format (%d bytes, max 65535): %s", len(metadata.RelPath), metadata.RelPath)
	}

	pathLen := len(metadata.RelPath)
	buf := make([]byte, 0, 2+pathLen+8+4+32*len(metadata.ChunkHashes))
	buf = binary.LittleEndian.AppendUint16(buf, uint16(pathLen))
	buf = append(buf, metadata.RelPath...)
	buf = binary.LittleEndian.AppendUint64(buf, metadata.OrigSize)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(metadata.ChunkHashes)))
	for _, hash := range metadata.ChunkHashes {
		buf = append(buf, hash[:]...)
	}

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write file metadata: %w", err)
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

// ReadChunkIndex reads the chunk index section in one bulk read
func ReadChunkIndex(r io.Reader, chunkCount uint32) (map[[32]byte]ChunkInfo, error) {
	chunks := make(map[[32]byte]ChunkInfo, chunkCount)

	buf := make([]byte, chunkIndexEntrySize*int(chunkCount))
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read chunk index: %w", err)
	}

	pos := 0
	for i := uint32(0); i < chunkCount; i++ {
		var chunk ChunkInfo
		copy(chunk.Hash[:], buf[pos:])
		chunk.Offset = binary.LittleEndian.Uint64(buf[pos+32:])
		chunk.CompressedSize = binary.LittleEndian.Uint64(buf[pos+40:])
		chunk.OriginalSize = binary.LittleEndian.Uint64(buf[pos+48:])
		pos += chunkIndexEntrySize

		chunks[chunk.Hash] = chunk
	}

	return chunks, nil
}

// ReadFileMetadata reads a single file metadata entry (3 bulk reads instead of
// one read per field/hash)
func ReadFileMetadata(r io.Reader) (FileMetadata, error) {
	var metadata FileMetadata

	// Read path length
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return metadata, fmt.Errorf("read path length: %w", err)
	}
	pathLen := binary.LittleEndian.Uint16(lenBuf[:])

	// Read path + original size + chunk count in one call
	fixedBuf := make([]byte, int(pathLen)+12)
	if _, err := io.ReadFull(r, fixedBuf); err != nil {
		return metadata, fmt.Errorf("read file metadata: %w", err)
	}
	metadata.RelPath = string(fixedBuf[:pathLen])
	metadata.OrigSize = binary.LittleEndian.Uint64(fixedBuf[pathLen:])
	chunkCount := binary.LittleEndian.Uint32(fixedBuf[pathLen+8:])

	// Read all chunk hashes in one call
	hashBuf := make([]byte, 32*int(chunkCount))
	if _, err := io.ReadFull(r, hashBuf); err != nil {
		return metadata, fmt.Errorf("read chunk hashes: %w", err)
	}
	metadata.ChunkHashes = make([][32]byte, chunkCount)
	for i := range metadata.ChunkHashes {
		copy(metadata.ChunkHashes[i][:], hashBuf[i*32:])
	}

	return metadata, nil
}
