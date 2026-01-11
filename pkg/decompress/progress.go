// pkg/decompress/progress.go
package decompress

import (
	"github.com/creativeyann17/go-delta/pkg/godelta"
	"github.com/vbauerster/mpb/v8"
)

// ProgressBarCallback creates a progress callback that displays multi-progress bars
// Returns the callback function and the progress container (call Wait() after decompression)
func ProgressBarCallback() (ProgressCallback, *mpb.Progress) {
	genericCb, progress := godelta.ProgressBarCallback()

	// Wrap the generic callback to adapt decompress.ProgressEvent to godelta.ProgressEvent
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

// FormatSummary formats a decompression result into a human-readable summary string
func FormatSummary(result *Result) string {
	return godelta.FormatSummary(result, godelta.OperationDecompress, false)
}

// FormatSize formats bytes into human-readable string
func FormatSize(bytes uint64) string {
	return godelta.FormatSize(bytes)
}

// TruncateLeft truncates a path from the left to fit maxLen, preserving the filename
func TruncateLeft(path string, maxLen int) string {
	return godelta.TruncateLeft(path, maxLen)
}
