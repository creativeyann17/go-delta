// Example: Using go-delta as a library

package main

import (
	"fmt"
	"log"

	"github.com/creativeyann17/go-delta/pkg/compress"
	"github.com/creativeyann17/go-delta/pkg/decompress"
)

func main() {
	// Example 1: Basic compression with default options
	basicExample()

	// Example 2: Compression with custom options
	customExample()

	// Example 3: Custom file list (library only)
	customFilesExample()

	// Example 4: Built-in progress helpers
	helpersExample()

	// Example 5: Chunk-based deduplication
	chunkExample()

	// Example 6: Decompression with helpers
	decompressExample()

	// Example 7: Custom progress tracking (advanced)
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

func customFilesExample() {
	// Compress specific files/folders without InputPath
	// This is a library-only feature not exposed in CLI
	opts := &compress.Options{
		Files: []string{
			"/path/to/file1.txt",
			"/path/to/folder1",
			"/another/path/file2.log",
			"relative/path/to/folder",
		},
		OutputPath: "custom.delta",
		MaxThreads: 4,
		Level:      9,
	}

	result, err := compress.Compress(opts, nil)
	if err != nil {
		log.Fatalf("Compression failed: %v", err)
	}

	fmt.Printf("Compressed %d files from custom list\n", result.FilesProcessed)
}

func helpersExample() {
	// Use built-in progress bar and summary helpers
	progressCb, progress := compress.ProgressBarCallback()

	opts := &compress.Options{
		InputPath:  "/path/to/files",
		OutputPath: "backup.delta",
		Level:      9,
	}

	result, err := compress.Compress(opts, progressCb)

	// Wait for progress bars to complete
	progress.Wait()

	if err != nil {
		log.Fatalf("Compression failed: %v", err)
	}

	// Print formatted summary
	fmt.Print(compress.FormatSummary(result, opts))

	if !result.Success() {
		log.Fatalf("Completed with %d errors", len(result.Errors))
	}
}

func chunkExample() {
	opts := &compress.Options{
		InputPath:       "/path/to/files",
		OutputPath:      "deduplicated.delta",
		MaxThreads:      4,
		Level:           5,
		ChunkSize:       128 * 1024,             // 128 KB chunks
		ChunkStoreSize:  5 * 1024,               // 5 GB chunk store limit (in MB)
		MaxThreadMemory: 2 * 1024 * 1024 * 1024, // 2 GB per thread
	}

	result, err := compress.Compress(opts, nil)
	if err != nil {
		log.Fatalf("Compression failed: %v", err)
	}

	fmt.Printf("Compressed %d files: %.2f MB -> %.2f MB (%.1f%%)\n",
		result.FilesProcessed,
		float64(result.OriginalSize)/1024/1024,
		float64(result.CompressedSize)/1024/1024,
		result.CompressionRatio())

	if result.TotalChunks > 0 {
		fmt.Printf("Deduplication: %d/%d chunks deduplicated (%.1f%%), %.2f MB saved\n",
			result.DedupedChunks,
			result.TotalChunks,
			result.DedupRatio(),
			float64(result.BytesSaved)/1024/1024)

		if result.Evictions > 0 {
			fmt.Printf("Evictions: %d chunks evicted from cache\n", result.Evictions)
		}
	}
}

func decompressExample() {
	// Use built-in progress bar and summary helpers
	progressCb, progress := decompress.ProgressBarCallback()

	opts := &decompress.Options{
		InputPath:  "backup.delta",
		OutputPath: "/restore/location",
		Overwrite:  true,
	}

	result, err := decompress.Decompress(opts, progressCb)

	// Wait for progress bars to complete
	progress.Wait()

	if err != nil {
		log.Fatalf("Decompression failed: %v", err)
	}

	// Print formatted summary
	fmt.Print(decompress.FormatSummary(result))

	if !result.Success() {
		log.Fatalf("Decompression completed with %d errors", len(result.Errors))
	}
}

func progressExample() {
	// Advanced: Custom progress tracking with your own callback
	opts := compress.DefaultOptions()
	opts.InputPath = "/path/to/files"
	opts.OutputPath = "backup.delta"
	opts.Level = 9

	// Custom progress callback for advanced use cases
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
