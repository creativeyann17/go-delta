// Example: Using go-delta as a library

package main

import (
	"fmt"
	"log"

	"github.com/yourusername/go-delta/pkg/compress"
)

func main() {
	// Example 1: Basic compression with default options
	basicExample()

	// Example 2: Compression with custom options
	customExample()

	// Example 3: Compression with progress tracking
	progressExample()
}

func basicExample() {
	opts := compress.DefaultOptions()
	opts.InputPath = "/path/to/files"
	opts.OutputPath = "backup.delta"

	result, err := compress.Compress(opts, nil)
	if err != nil {
		log.Fatalf("Compression failed: %v", err)
	}

	fmt.Printf("Compressed %d files\n", result.FilesProcessed)
	fmt.Printf("Original: %.2f MB, Compressed: %.2f MB (%.1f%%)\n",
		float64(result.OriginalSize)/1024/1024,
		float64(result.CompressedSize)/1024/1024,
		result.CompressionRatio())
}

func customExample() {
	opts := &compress.Options{
		InputPath:  "/path/to/large/dataset",
		OutputPath: "archive.delta",
		MaxThreads: 8,  // Use 8 threads
		Level:      19, // Maximum compression
		DryRun:     false,
		Verbose:    false,
		Quiet:      true,
	}

	result, err := compress.Compress(opts, nil)
	if err != nil {
		log.Fatalf("Compression failed: %v", err)
	}

	if !result.Success() {
		fmt.Printf("Completed with %d errors\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Printf("  - %v\n", e)
		}
	}
}

func progressExample() {
	opts := compress.DefaultOptions()
	opts.InputPath = "/path/to/files"
	opts.OutputPath = "backup.delta"
	opts.Level = 9

	// Custom progress tracking
	progressCb := func(event compress.ProgressEvent) {
		switch event.Type {
		case compress.EventStart:
			fmt.Printf("Starting compression of %d files...\n", event.Total)

		case compress.EventFileStart:
			fmt.Printf("  Compressing: %s\n", event.FilePath)

		case compress.EventFileComplete:
			ratio := float64(event.CompressedSize) / float64(event.Total) * 100
			fmt.Printf("    ✓ %s: %.1f%% compression\n", event.FilePath, ratio)

		case compress.EventError:
			fmt.Printf("    ✗ %s: failed\n", event.FilePath)

		case compress.EventComplete:
			fmt.Printf("\nCompleted: %d/%d files\n", event.Current, event.Total)
			ratio := float64(event.CompressedSize) / float64(event.TotalBytes) * 100
			fmt.Printf("Overall compression: %.1f%%\n", ratio)
		}
	}

	result, err := compress.Compress(opts, progressCb)
	if err != nil {
		log.Fatalf("Compression failed: %v", err)
	}

	fmt.Printf("\nFinal stats:\n")
	fmt.Printf("  Files: %d/%d processed\n", result.FilesProcessed, result.FilesTotal)
	fmt.Printf("  Size: %.2f MB → %.2f MB\n",
		float64(result.OriginalSize)/1024/1024,
		float64(result.CompressedSize)/1024/1024)
}
