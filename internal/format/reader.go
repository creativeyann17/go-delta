// internal/format/reader.go
package format

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ArchiveReader provides methods to read archive metadata
type ArchiveReader struct {
	r         io.ReadSeeker
	fileCount uint32
}

// FileEntry represents a file entry in the archive
type FileEntry struct {
	Path           string
	OriginalSize   uint64
	CompressedSize uint64
	DataOffset     uint64
}

// NewArchiveReader creates a new archive reader and validates the header
func NewArchiveReader(r io.ReadSeeker) (*ArchiveReader, error) {
	// Read and validate magic
	magic := make([]byte, MagicSize)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}

	if string(magic) != ArchiveMagic {
		return nil, fmt.Errorf("invalid magic: expected %q, got %q", ArchiveMagic, string(magic))
	}

	// Read file count
	var fileCount uint32
	if err := binary.Read(r, binary.LittleEndian, &fileCount); err != nil {
		return nil, fmt.Errorf("read file count: %w", err)
	}

	return &ArchiveReader{
		r:         r,
		fileCount: fileCount,
	}, nil
}

// FileCount returns the number of files in the archive
func (ar *ArchiveReader) FileCount() int {
	return int(ar.fileCount)
}

// ReadFileEntry reads the next file entry from the archive
func (ar *ArchiveReader) ReadFileEntry() (*FileEntry, error) {
	// Read path length
	var pathLen uint16
	if err := binary.Read(ar.r, binary.LittleEndian, &pathLen); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("read path length: %w", err)
	}

	// Read path
	pathBytes := make([]byte, pathLen)
	if _, err := io.ReadFull(ar.r, pathBytes); err != nil {
		return nil, fmt.Errorf("read path: %w", err)
	}

	// Read original size
	var origSize uint64
	if err := binary.Read(ar.r, binary.LittleEndian, &origSize); err != nil {
		return nil, fmt.Errorf("read original size: %w", err)
	}

	// Read compressed size
	var compSize uint64
	if err := binary.Read(ar.r, binary.LittleEndian, &compSize); err != nil {
		return nil, fmt.Errorf("read compressed size: %w", err)
	}

	// Read data offset
	var dataOffset uint64
	if err := binary.Read(ar.r, binary.LittleEndian, &dataOffset); err != nil {
		return nil, fmt.Errorf("read data offset: %w", err)
	}

	return &FileEntry{
		Path:           string(pathBytes),
		OriginalSize:   origSize,
		CompressedSize: compSize,
		DataOffset:     dataOffset,
	}, nil
}

// SeekToData seeks to the compressed data for a file entry
func (ar *ArchiveReader) SeekToData(entry *FileEntry) error {
	_, err := ar.r.Seek(int64(entry.DataOffset), io.SeekStart)
	return err
}

// ReadAllEntries reads all file entries from the archive
func (ar *ArchiveReader) ReadAllEntries() ([]*FileEntry, error) {
	entries := make([]*FileEntry, 0, ar.fileCount)

	for i := uint32(0); i < ar.fileCount; i++ {
		entry, err := ar.ReadFileEntry()
		if err != nil {
			return nil, fmt.Errorf("read entry %d: %w", i, err)
		}
		entries = append(entries, entry)

		// Skip over the compressed data to get to the next entry
		// Data is located at DataOffset and is CompressedSize bytes long
		// We need to position ourselves after the data
		if i < ar.fileCount-1 { // Not the last entry
			nextEntryPos := int64(entry.DataOffset + entry.CompressedSize)
			if _, err := ar.r.Seek(nextEntryPos, io.SeekStart); err != nil {
				return nil, fmt.Errorf("seek to next entry: %w", err)
			}
		}
	}

	return entries, nil
}
