// pkg/compress/progress.go
package compress

import (
	"fmt"
	"strings"

	"github.com/creativeyann17/go-delta/pkg/godelta"
	"github.com/vbauerster/mpb/v8"
)

// ProgressBarCallback creates a progress callback that displays multi-progress bars
// Returns the callback function and the progress container (call Wait() after compression)
func ProgressBarCallback() (ProgressCallback, *mpb.Progress) {
	genericCb, progress := godelta.ProgressBarCallback()

	// Wrap the generic callback to adapt compress.ProgressEvent to godelta.ProgressEvent
	callback := func(event ProgressEvent) {
		genericCb(godelta.ProgressEvent{
			Type:         godelta.EventType(event.Type),
			FilePath:     event.FilePath,
			Current:      event.Current,
			Total:        event.Total,
			CurrentBytes: event.CurrentBytes,
			TotalBytes:   event.TotalBytes,
		})
	}

	return callback, progress
}

// FormatSummary formats a compression result into a human-readable summary string
func FormatSummary(result *Result, opts *Options) string {
	var sb strings.Builder

	// Use generic formatter for basic stats
	isDryRun := opts != nil && opts.DryRun
	sb.WriteString(godelta.FormatSummary(result, godelta.OperationCompress, isDryRun))

	// Add deduplication stats if chunking was enabled
	if result.TotalChunks > 0 {
		sb.WriteString("\nDeduplication:\n")
		fmt.Fprintf(&sb, "  Total chunks:    %d\n", result.TotalChunks)
		fmt.Fprintf(&sb, "  Unique chunks:   %d\n", result.UniqueChunks)
		fmt.Fprintf(&sb, "  Deduped chunks:  %d\n", result.DedupedChunks)
		fmt.Fprintf(&sb, "  Dedup ratio:     %.1f%%\n", result.DedupRatio())
		fmt.Fprintf(&sb, "  Bytes saved:     %.2f MiB\n", float64(result.BytesSaved)/1024/1024)
		if result.Evictions > 0 {
			fmt.Fprintf(&sb, "  Evictions:       %d (LRU cache)\n", result.Evictions)
		}
	}

	if isDryRun {
		sb.WriteString("\nDry run complete - no archive written.\n")
	}

	return sb.String()
}

// FormatSize formats bytes into human-readable string
func FormatSize(bytes uint64) string {
	return godelta.FormatSize(bytes)
}

// TruncateLeft truncates a path from the left to fit maxLen, preserving the filename
func TruncateLeft(path string, maxLen int) string {
	return godelta.TruncateLeft(path, maxLen)
}
