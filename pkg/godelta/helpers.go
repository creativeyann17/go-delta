// pkg/godelta/helpers.go
package godelta

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// OperationType indicates whether the operation is compression or decompression
type OperationType string

const (
	OperationCompress   OperationType = "compress"
	OperationDecompress OperationType = "decompress"
)

// ProgressEvent is a generic progress event that works for both compress and decompress
type ProgressEvent struct {
	Type         EventType
	FilePath     string
	Current      int64
	Total        int64
	CurrentBytes uint64
	TotalBytes   uint64
}

// EventType indicates the type of progress event
type EventType int

const (
	EventStart EventType = iota
	EventFileStart
	EventFileProgress
	EventFileComplete
	EventComplete
	EventError
)

// Result is a generic interface for both compress and decompress results
type Result interface {
	GetFilesTotal() int
	GetFilesProcessed() int
	GetErrors() []error
	GetOriginalSize() uint64
	GetCompressedSize() uint64
	Success() bool
}

// ProgressBarCallback creates a progress callback that displays multi-progress bars
// Works for both compression and decompression
// Returns the callback function and the progress container (call Wait() after operation)
func ProgressBarCallback() (func(ProgressEvent), *mpb.Progress) {
	progress := mpb.New(
		mpb.WithWidth(60),
		mpb.WithRefreshRate(100),
	)

	var overallBar *mpb.Bar
	var fileBars sync.Map // map[string]*mpb.Bar

	callback := func(event ProgressEvent) {
		switch event.Type {
		case EventStart:
			// Create overall progress bar (at bottom via priority)
			overallBar = progress.AddBar(event.Total,
				mpb.PrependDecorators(
					decor.Name("Total", decor.WC{C: decor.DindentRight | decor.DextraSpace}),
					decor.CountersNoUnit("%d / %d", decor.WCSyncWidth),
				),
				mpb.AppendDecorators(
					decor.Percentage(decor.WC{W: 5}),
				),
				mpb.BarPriority(1000), // High priority = bottom
			)

		case EventFileStart:
			// Skip creating bars for empty files (Total=0) - they complete instantly
			if event.Total == 0 {
				return
			}
			// Create a bar for this file
			shortName := TruncateLeft(event.FilePath, 30)
			bar := progress.AddBar(event.Total,
				mpb.PrependDecorators(
					decor.Name(shortName, decor.WC{C: decor.DindentRight | decor.DextraSpace, W: 32}),
				),
				mpb.AppendDecorators(
					decor.CountersKibiByte("% .1f / % .1f", decor.WC{W: 18}),
					decor.Percentage(decor.WC{W: 5}),
				),
				mpb.BarRemoveOnComplete(),
			)
			fileBars.Store(event.FilePath, bar)

		case EventFileProgress:
			if bar, ok := fileBars.Load(event.FilePath); ok {
				bar.(*mpb.Bar).SetCurrent(event.Current)
			}

		case EventFileComplete:
			if bar, ok := fileBars.Load(event.FilePath); ok {
				b := bar.(*mpb.Bar)
				// Ensure bar completes - SetTotal then SetCurrent for proper completion
				if event.Total > 0 {
					b.SetCurrent(event.Total)
				} else {
					// For zero-size files, abort to remove the bar
					b.Abort(true)
				}
				fileBars.Delete(event.FilePath)
			}
			if overallBar != nil {
				overallBar.Increment()
			}

		case EventError:
			if bar, ok := fileBars.Load(event.FilePath); ok {
				bar.(*mpb.Bar).Abort(true)
				fileBars.Delete(event.FilePath)
			}
			if overallBar != nil {
				overallBar.Increment()
			}
		}
	}

	return callback, progress
}

// FormatSummary formats a result into a human-readable summary string
// Works for both compression and decompression
func FormatSummary(result Result, operation OperationType, isDryRun bool) string {
	var sb strings.Builder

	errors := result.GetErrors()
	if len(errors) > 0 {
		fmt.Fprintf(&sb, "Completed with %d errors:\n", len(errors))
		for _, e := range errors {
			fmt.Fprintf(&sb, "  - %v\n", e)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Summary:\n")
	fmt.Fprintf(&sb, "  Files processed: %d / %d\n", result.GetFilesProcessed(), result.GetFilesTotal())

	if operation == OperationCompress {
		fmt.Fprintf(&sb, "  Original size:   %.2f MiB\n", float64(result.GetOriginalSize())/1024/1024)
		if isDryRun {
			fmt.Fprintf(&sb, "  Compressed size: %.2f MiB (estimated)\n", float64(result.GetCompressedSize())/1024/1024)
		} else {
			fmt.Fprintf(&sb, "  Compressed size: %.2f MiB\n", float64(result.GetCompressedSize())/1024/1024)
		}
		if result.GetOriginalSize() > 0 {
			ratio := float64(result.GetCompressedSize()) / float64(result.GetOriginalSize()) * 100
			fmt.Fprintf(&sb, "  Ratio:           %.1f%%\n", ratio)
		}
	} else {
		fmt.Fprintf(&sb, "  Compressed size:   %.2f MiB\n", float64(result.GetCompressedSize())/1024/1024)
		fmt.Fprintf(&sb, "  Decompressed size: %.2f MiB\n", float64(result.GetOriginalSize())/1024/1024)
	}

	if isDryRun {
		sb.WriteString("\nDry run complete - no data written.\n")
	}

	return sb.String()
}

// FormatSize formats bytes into human-readable string
func FormatSize(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
	)

	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// TruncateLeft truncates a path from the left to fit maxLen, preserving the filename
func TruncateLeft(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}

	// Try to preserve at least the filename
	filename := filepath.Base(path)
	if len(filename) >= maxLen-3 {
		return "..." + filename[len(filename)-(maxLen-3):]
	}

	// Truncate from left with ellipsis
	return "..." + path[len(path)-(maxLen-3):]
}
