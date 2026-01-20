// pkg/verify/result.go
package verify

import "fmt"

// Format represents the archive format type
type Format string

const (
	FormatGDelta01 Format = "GDELTA01"
	FormatGDelta02 Format = "GDELTA02"
	FormatGDelta03 Format = "GDELTA03"
	FormatZIP      Format = "ZIP"
	FormatUnknown  Format = "UNKNOWN"
)

// Result contains comprehensive verification results
type Result struct {
	// Archive metadata
	Format      Format // Archive format (GDELTA01, GDELTA02, ZIP)
	ArchivePath string // Path to the verified archive
	ArchiveSize uint64 // Total archive file size in bytes

	// Header information
	Magic       string // Raw magic bytes as string
	HeaderValid bool   // Whether header passed validation

	// File statistics
	FileCount     int    // Number of files in archive
	TotalOrigSize uint64 // Sum of original file sizes
	TotalCompSize uint64 // Sum of compressed data sizes
	EmptyFiles    int    // Number of zero-byte files

	// GDELTA02-specific chunk information
	ChunkSize     uint64 // Configured average chunk size (0 for non-chunked)
	ChunkCount    uint64 // Total unique chunks in archive
	TotalChunkRef uint64 // Total chunk references across all files

	// GDELTA03-specific dictionary information
	DictSize uint32 // Dictionary size in bytes (0 for non-dictionary)

	// Data integrity (only populated when VerifyData=true)
	DataVerified   bool // Whether data verification was performed
	FilesVerified  int  // Number of files with verified data
	ChunksVerified int  // Number of chunks with verified data (GDELTA02)
	CorruptFiles   int  // Number of files that failed verification
	CorruptChunks  int  // Number of chunks that failed verification

	// Structural integrity
	StructureValid bool // Overall structure is valid
	FooterValid    bool // Footer marker is valid
	IndexValid     bool // Chunk index is valid (GDELTA02)
	MetadataValid  bool // File metadata is valid
	OrphanedChunks int  // Chunks not referenced by any file (GDELTA02)
	MissingChunks  int  // Chunks referenced but not in index (GDELTA02)
	DuplicatePaths int  // Files with duplicate paths

	// File details (populated during verification)
	Files []FileInfo

	// Errors encountered during verification
	Errors []error
}

// FileInfo contains information about a single file in the archive
type FileInfo struct {
	Path           string // Relative path in archive
	OriginalSize   uint64 // Original uncompressed size
	CompressedSize uint64 // Compressed size in archive
	ChunkCount     int    // Number of chunks (GDELTA02 only)
	DataValid      bool   // Data integrity verified (when VerifyData=true)
	Error          error  // Error if verification failed for this file
}

// CompressionRatio returns the compression ratio as a percentage
func (r *Result) CompressionRatio() float64 {
	if r.TotalOrigSize == 0 {
		return 0
	}
	return float64(r.TotalCompSize) / float64(r.TotalOrigSize) * 100
}

// SpaceSaved returns bytes saved by compression
func (r *Result) SpaceSaved() uint64 {
	if r.TotalCompSize >= r.TotalOrigSize {
		return 0
	}
	return r.TotalOrigSize - r.TotalCompSize
}

// SpaceSavedRatio returns percentage of space saved
func (r *Result) SpaceSavedRatio() float64 {
	if r.TotalOrigSize == 0 {
		return 0
	}
	return float64(r.SpaceSaved()) / float64(r.TotalOrigSize) * 100
}

// ChunkDeduplicationRatio returns dedup ratio for GDELTA02
// (how many chunk references are duplicates)
func (r *Result) ChunkDeduplicationRatio() float64 {
	if r.TotalChunkRef == 0 || r.ChunkCount == 0 {
		return 0
	}
	if r.TotalChunkRef <= r.ChunkCount {
		return 0
	}
	duplicates := r.TotalChunkRef - r.ChunkCount
	return float64(duplicates) / float64(r.TotalChunkRef) * 100
}

// AverageChunksPerFile returns average chunks per file for GDELTA02
func (r *Result) AverageChunksPerFile() float64 {
	if r.FileCount == 0 {
		return 0
	}
	return float64(r.TotalChunkRef) / float64(r.FileCount)
}

// IsValid returns true if the archive passed all validation checks
func (r *Result) IsValid() bool {
	return r.HeaderValid && r.StructureValid && r.FooterValid &&
		len(r.Errors) == 0 && r.MissingChunks == 0 && r.CorruptFiles == 0
}

// Success returns true if verification completed without critical errors
func (r *Result) Success() bool {
	return r.IsValid()
}

// Summary returns a human-readable summary of the verification result
func (r *Result) Summary() string {
	status := "VALID"
	if !r.IsValid() {
		status = "INVALID"
	}

	s := fmt.Sprintf("Archive: %s [%s]\n", r.ArchivePath, status)
	s += fmt.Sprintf("Format:  %s\n", r.Format)
	s += fmt.Sprintf("Size:    %s\n", formatSize(r.ArchiveSize))
	s += fmt.Sprintf("Files:   %d\n", r.FileCount)

	if r.TotalOrigSize > 0 {
		s += fmt.Sprintf("Original:   %s\n", formatSize(r.TotalOrigSize))
		s += fmt.Sprintf("Compressed: %s (%.1f%% ratio)\n",
			formatSize(r.TotalCompSize), r.CompressionRatio())
		s += fmt.Sprintf("Saved:      %s (%.1f%%)\n",
			formatSize(r.SpaceSaved()), r.SpaceSavedRatio())
	}

	if r.Format == FormatGDelta02 {
		s += fmt.Sprintf("\nChunk Info:\n")
		s += fmt.Sprintf("  Chunk Size:  %s\n", formatSize(r.ChunkSize))
		s += fmt.Sprintf("  Unique:      %d chunks\n", r.ChunkCount)
		s += fmt.Sprintf("  References:  %d total\n", r.TotalChunkRef)
		if r.ChunkDeduplicationRatio() > 0 {
			s += fmt.Sprintf("  Dedup Ratio: %.1f%%\n", r.ChunkDeduplicationRatio())
		}
	}

	if r.Format == FormatGDelta03 {
		s += fmt.Sprintf("\nDictionary Info:\n")
		s += fmt.Sprintf("  Dict Size:  %s\n", formatSize(uint64(r.DictSize)))
	}

	if r.DataVerified {
		s += fmt.Sprintf("\nData Integrity:\n")
		s += fmt.Sprintf("  Files Verified:  %d/%d\n", r.FilesVerified, r.FileCount)
		if r.CorruptFiles > 0 {
			s += fmt.Sprintf("  Corrupt Files:   %d\n", r.CorruptFiles)
		}
		if r.Format == FormatGDelta02 && r.ChunksVerified > 0 {
			s += fmt.Sprintf("  Chunks Verified: %d\n", r.ChunksVerified)
			if r.CorruptChunks > 0 {
				s += fmt.Sprintf("  Corrupt Chunks:  %d\n", r.CorruptChunks)
			}
		}
	}

	if len(r.Errors) > 0 {
		s += fmt.Sprintf("\nErrors (%d):\n", len(r.Errors))
		for i, err := range r.Errors {
			if i >= 10 {
				s += fmt.Sprintf("  ... and %d more errors\n", len(r.Errors)-10)
				break
			}
			s += fmt.Sprintf("  - %v\n", err)
		}
	}

	return s
}

// formatSize formats bytes to human-readable string
func formatSize(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
