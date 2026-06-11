// internal/format/gdelta03.go
package format

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// GDELTA03 with dictionary compression
	ArchiveMagic03  = "GDELTA03"
	ArchiveFooter03 = "ENDGDLT3"
	GDELTA03Version = 0x01
)

// GDELTA03 Header Structure (21 bytes):
//   Magic (8):       "GDELTA03"
//   Version (1):     0x01
//   Dict Size (4):   uint32
//   File Count (4):  uint32
//   Reserved (4):    0x00000000

// GDELTA03 File Entry Structure:
//   Path Length (2):    uint16
//   Path (variable):    string
//   Original Size (8):  uint64
//   Compressed Size (8): uint64
//   [Compressed data follows immediately]

// WriteGDelta03Header writes the GDELTA03 archive header
func WriteGDelta03Header(w io.Writer, dictSize uint32, fileCount uint32) error {
	// Write magic
	if _, err := w.Write([]byte(ArchiveMagic03)); err != nil {
		return fmt.Errorf("write magic: %w", err)
	}

	// Write version
	if err := binary.Write(w, binary.LittleEndian, byte(GDELTA03Version)); err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	// Write dictionary size
	if err := binary.Write(w, binary.LittleEndian, dictSize); err != nil {
		return fmt.Errorf("write dict size: %w", err)
	}

	// Write file count
	if err := binary.Write(w, binary.LittleEndian, fileCount); err != nil {
		return fmt.Errorf("write file count: %w", err)
	}

	// Write reserved bytes
	reserved := uint32(0)
	if err := binary.Write(w, binary.LittleEndian, reserved); err != nil {
		return fmt.Errorf("write reserved: %w", err)
	}

	return nil
}

// ReadGDelta03Header reads the GDELTA03 header including magic
// Returns version, dictionary size, and file count
func ReadGDelta03Header(r io.Reader) (version byte, dictSize uint32, fileCount uint32, err error) {
	// Read and verify magic
	magic := make([]byte, 8)
	if _, err := io.ReadFull(r, magic); err != nil {
		return 0, 0, 0, fmt.Errorf("read magic: %w", err)
	}
	if string(magic) != ArchiveMagic03 {
		return 0, 0, 0, fmt.Errorf("invalid magic: got %q, want %q", magic, ArchiveMagic03)
	}

	return ReadGDelta03HeaderAfterMagic(r)
}

// ReadGDelta03HeaderAfterMagic reads the GDELTA03 header after the magic has been consumed
// Returns version, dictionary size, and file count
func ReadGDelta03HeaderAfterMagic(r io.Reader) (version byte, dictSize uint32, fileCount uint32, err error) {
	// Read version
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return 0, 0, 0, fmt.Errorf("read version: %w", err)
	}

	// Read dictionary size
	if err := binary.Read(r, binary.LittleEndian, &dictSize); err != nil {
		return 0, 0, 0, fmt.Errorf("read dict size: %w", err)
	}

	// Read file count
	if err := binary.Read(r, binary.LittleEndian, &fileCount); err != nil {
		return 0, 0, 0, fmt.Errorf("read file count: %w", err)
	}

	// Read and discard reserved bytes
	var reserved uint32
	if err := binary.Read(r, binary.LittleEndian, &reserved); err != nil {
		return 0, 0, 0, fmt.Errorf("read reserved: %w", err)
	}

	return version, dictSize, fileCount, nil
}

// WriteGDelta03FileEntry writes a file entry for GDELTA03 as one write
// Format: PathLen(2) + Path + OrigSize(8) + CompSize(8)
func WriteGDelta03FileEntry(w io.Writer, relPath string, origSize, compSize uint64) error {
	if len(relPath) > 65535 {
		return fmt.Errorf("path too long for archive format (%d bytes, max 65535): %s", len(relPath), relPath)
	}

	buf := make([]byte, 0, 2+len(relPath)+16)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(relPath)))
	buf = append(buf, relPath...)
	buf = binary.LittleEndian.AppendUint64(buf, origSize)
	buf = binary.LittleEndian.AppendUint64(buf, compSize)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write file entry: %w", err)
	}

	return nil
}

// GDelta03FileEntry represents a file entry in GDELTA03 format
type GDelta03FileEntry struct {
	Path           string
	OriginalSize   uint64
	CompressedSize uint64
}

// ReadGDelta03FileEntry reads a file entry from GDELTA03 archive (2 bulk reads)
func ReadGDelta03FileEntry(r io.Reader) (*GDelta03FileEntry, error) {
	entry := &GDelta03FileEntry{}

	// Read path length
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read path length: %w", err)
	}
	pathLen := binary.LittleEndian.Uint16(lenBuf[:])

	// Read path + original size + compressed size in one call
	buf := make([]byte, int(pathLen)+16)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read file entry: %w", err)
	}
	entry.Path = string(buf[:pathLen])
	entry.OriginalSize = binary.LittleEndian.Uint64(buf[pathLen:])
	entry.CompressedSize = binary.LittleEndian.Uint64(buf[pathLen+8:])

	return entry, nil
}

// WriteArchiveFooter03 writes the GDELTA03 footer
func WriteArchiveFooter03(w io.Writer) error {
	if _, err := w.Write([]byte(ArchiveFooter03)); err != nil {
		return fmt.Errorf("write footer: %w", err)
	}
	return nil
}
