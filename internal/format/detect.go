// internal/format/detect.go
package format

// ArchiveFormat represents the detected archive format
type ArchiveFormat int

const (
	FormatUnknown ArchiveFormat = iota
	FormatGDelta01
	FormatGDelta02
	FormatGDelta03
	FormatZIP
	FormatXZ
)

// String returns the string representation of the format
func (f ArchiveFormat) String() string {
	switch f {
	case FormatGDelta01:
		return "GDELTA01"
	case FormatGDelta02:
		return "GDELTA02"
	case FormatGDelta03:
		return "GDELTA03"
	case FormatZIP:
		return "ZIP"
	case FormatXZ:
		return "XZ"
	default:
		return "UNKNOWN"
	}
}

// DetectFormat detects the archive format from magic bytes
// Requires at least 8 bytes to detect all formats
func DetectFormat(magic []byte) ArchiveFormat {
	if len(magic) < 8 {
		return FormatUnknown
	}

	// Check GDELTA formats first (8-byte magic)
	switch string(magic[:8]) {
	case ArchiveMagic:
		return FormatGDelta01
	case ArchiveMagic02:
		return FormatGDelta02
	case ArchiveMagic03:
		return FormatGDelta03
	}

	// Check ZIP (PK signature)
	if magic[0] == 'P' && magic[1] == 'K' {
		return FormatZIP
	}

	// Check XZ (magic: 0xFD377A585A00)
	if magic[0] == 0xFD && magic[1] == '7' && magic[2] == 'z' &&
		magic[3] == 'X' && magic[4] == 'Z' && magic[5] == 0x00 {
		return FormatXZ
	}

	return FormatUnknown
}

// IsZIP returns true if the magic bytes indicate a ZIP file
func IsZIP(magic []byte) bool {
	return len(magic) >= 2 && magic[0] == 'P' && magic[1] == 'K'
}

// IsXZ returns true if the magic bytes indicate an XZ file
func IsXZ(magic []byte) bool {
	return len(magic) >= 6 &&
		magic[0] == 0xFD && magic[1] == '7' && magic[2] == 'z' &&
		magic[3] == 'X' && magic[4] == 'Z' && magic[5] == 0x00
}
