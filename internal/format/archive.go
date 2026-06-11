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
	if len(relPath) > 65535 {
		return 0, fmt.Errorf("path too long for archive format (%d bytes, max 65535): %s", len(relPath), relPath)
	}

	// Get current position
	entryPos, err = w.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, fmt.Errorf("get current position: %w", err)
	}

	// Entry header in one write: PathLen(2) + Path + OrigSize(8) +
	// CompSize placeholder(8) + DataOffset placeholder(8)
	buf := make([]byte, 0, 2+len(relPath)+24)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(relPath)))
	buf = append(buf, relPath...)
	buf = binary.LittleEndian.AppendUint64(buf, origSize)
	buf = binary.LittleEndian.AppendUint64(buf, 0) // compressed size, updated later
	buf = binary.LittleEndian.AppendUint64(buf, 0) // data offset, updated later

	if _, err := w.Write(buf); err != nil {
		return 0, fmt.Errorf("write file entry: %w", err)
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

	// Write compressed size + data offset in one call
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[:8], compressedSize)
	binary.LittleEndian.PutUint64(buf[8:], dataOffset)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("write comp size and data offset: %w", err)
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
