// internal/format/archive.go
package format

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// Magic signature for go-delta archives
	ArchiveMagic = "GDELTA01"
	MagicSize    = 8

	// File entry header size: path_len(2) + orig_size(8) + comp_size(8) + data_offset(8)
	FileEntryHeaderSize = 26
)

// WriteArchiveHeader writes the magic signature and file count to the beginning of the archive
func WriteArchiveHeader(w io.Writer, fileCount uint32) error {
	// Write magic
	if _, err := w.Write([]byte(ArchiveMagic)); err != nil {
		return fmt.Errorf("write magic: %w", err)
	}

	// Write file count
	if err := binary.Write(w, binary.LittleEndian, fileCount); err != nil {
		return fmt.Errorf("write file count: %w", err)
	}

	return nil
}

// WriteFileEntry writes a file entry header and returns the position where it was written.
// The compressed size and data offset fields are initially zero and must be updated later
// using UpdateFileEntry after compression.
func WriteFileEntry(w io.WriteSeeker, relPath string, origSize uint64) (entryPos int64, err error) {
	// Get current position
	entryPos, err = w.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, fmt.Errorf("get current position: %w", err)
	}

	// Write path length (uint16)
	pathLen := uint16(len(relPath))
	if err := binary.Write(w, binary.LittleEndian, pathLen); err != nil {
		return 0, fmt.Errorf("write path length: %w", err)
	}

	// Write path
	if _, err := w.Write([]byte(relPath)); err != nil {
		return 0, fmt.Errorf("write path: %w", err)
	}

	// Write original size
	if err := binary.Write(w, binary.LittleEndian, origSize); err != nil {
		return 0, fmt.Errorf("write orig size: %w", err)
	}

	// Write placeholder for compressed size (will be updated later)
	if err := binary.Write(w, binary.LittleEndian, uint64(0)); err != nil {
		return 0, fmt.Errorf("write comp size placeholder: %w", err)
	}

	// Write placeholder for data offset (will be updated later)
	if err := binary.Write(w, binary.LittleEndian, uint64(0)); err != nil {
		return 0, fmt.Errorf("write data offset placeholder: %w", err)
	}

	return entryPos, nil
}

// UpdateFileEntry updates the compressed size and data offset fields of a previously written entry
func UpdateFileEntry(w io.WriteSeeker, entryPos int64, compressedSize uint64, dataOffset uint64) error {
	// Save current position
	currentPos, err := w.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("get current position: %w", err)
	}

	// Seek to the entry position
	if _, err := w.Seek(entryPos, io.SeekStart); err != nil {
		return fmt.Errorf("seek to entry: %w", err)
	}

	// Read path length to calculate offset to comp_size field
	pathLenBytes := make([]byte, 2)
	if rw, ok := w.(io.Reader); ok {
		if _, err := io.ReadFull(rw, pathLenBytes); err != nil {
			return fmt.Errorf("read path length: %w", err)
		}
	} else {
		return fmt.Errorf("writer does not support reading")
	}

	pathLen := binary.LittleEndian.Uint16(pathLenBytes)

	// Skip path and orig_size to get to comp_size field
	// offset = 2 (pathLen) + pathLen + 8 (origSize)
	compSizeOffset := entryPos + 2 + int64(pathLen) + 8
	if _, err := w.Seek(compSizeOffset, io.SeekStart); err != nil {
		return fmt.Errorf("seek to comp size: %w", err)
	}

	// Write compressed size
	if err := binary.Write(w, binary.LittleEndian, compressedSize); err != nil {
		return fmt.Errorf("write comp size: %w", err)
	}

	// Write data offset
	if err := binary.Write(w, binary.LittleEndian, dataOffset); err != nil {
		return fmt.Errorf("write data offset: %w", err)
	}

	// Restore original position
	if _, err := w.Seek(currentPos, io.SeekStart); err != nil {
		return fmt.Errorf("restore position: %w", err)
	}

	return nil
}

// WriteArchiveFooter writes any trailing metadata (currently just a simple end marker)
func WriteArchiveFooter(w io.Writer) error {
	// For now, just write an end marker
	if _, err := w.Write([]byte("GDELTAEND")); err != nil {
		return fmt.Errorf("write footer: %w", err)
	}
	return nil
}
