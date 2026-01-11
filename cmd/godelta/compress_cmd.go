// cmd/godelta/compress_cmd.go

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"github.com/creativeyann17/go-delta/pkg/compress"
)

func init() {
	rootCmd.AddCommand(compressCmd())
}

func compressCmd() *cobra.Command {
	var inputPath, outputPath string
	var maxThreads int
	var threadMemoryStr string
	var chunkSizeStr string
	var chunkStoreSizeStr string
	var dryRun bool
	var verbose bool
	var quiet bool
	var compressLevel int

	cmd := &cobra.Command{
		Use:   "compress",
		Short: "Compress file or directory into delta archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Add .gdelta extension if missing
			if outputPath != "" && len(outputPath) >= 7 && outputPath[len(outputPath)-7:] != ".gdelta" {
				outputPath += ".gdelta"
			}

			// Parse size strings
			threadMemoryKB, err := parseSize(threadMemoryStr)
			if err != nil {
				return fmt.Errorf("invalid --thread-memory: %w", err)
			}

			chunkSizeKB, err := parseSize(chunkSizeStr)
			if err != nil {
				return fmt.Errorf("invalid --chunk-size: %w", err)
			}

			// Validate minimum chunk size to prevent metadata overhead exceeding savings
			if chunkSizeKB > 0 && chunkSizeKB < minChunkSizeKB {
				return fmt.Errorf("--chunk-size too small: %d KB (minimum: %d KB)\n"+
					"Reason: Each chunk has 56 bytes of metadata overhead in the archive.\n"+
					"Chunks smaller than %d KB would increase archive size instead of reducing it.",
					chunkSizeKB, minChunkSizeKB, minChunkSizeKB)
			}

			chunkStoreSizeKB, err := parseSize(chunkStoreSizeStr)
			if err != nil {
				return fmt.Errorf("invalid --chunk-store-size: %w", err)
			}

			// Get total system memory (cross-platform)
			// If detection fails, just disable the warning (don't fail)
			totalSystemMemoryKB, _ := getTotalSystemMemory()

			// Logging helper
			log := func(format string, args ...interface{}) {
				if !quiet {
					fmt.Printf(format+"\n", args...)
				}
			}

			// If thread-memory is 0, calculate it from total input size
			if threadMemoryKB == 0 {
				// Quick scan to get total file size
				var totalSize uint64
				filepath.Walk(inputPath, func(path string, info os.FileInfo, err error) error {
					if err == nil && info.Mode().IsRegular() {
						totalSize += uint64(info.Size())
					}
					return nil
				})

				if totalSize > 0 {
					// Divide total size by thread count, convert to KB, add small buffer
					perThreadBytes := totalSize / uint64(maxThreads)
					threadMemoryKB = (perThreadBytes / 1024) + (50 * 1024) // +50MB buffer
					if !quiet {
						log("Auto-calculated thread memory: %.2f MB per thread (total input: %.2f MiB / %d threads)",
							float64(threadMemoryKB)/1024, float64(totalSize)/(1024*1024), maxThreads)
					}
				}
			}

			// Memory safety check for --thread-memory
			if threadMemoryKB > 0 {
				totalMemoryKB := uint64(maxThreads) * threadMemoryKB

				// System memory detected successfully
				if totalMemoryKB > totalSystemMemoryKB {
					log("WARNING: Total thread memory (%d threads × %.2f MB = %.2f MB) exceeds system memory (%.2f MB)",
						maxThreads, float64(threadMemoryKB)/1024, float64(totalMemoryKB)/1024, float64(totalSystemMemoryKB)/1024)
					log("         This may cause memory exhaustion. Consider reducing --thread-memory or --threads")
					log("")
				}

			}

			// Prepare options
			opts := &compress.Options{
				InputPath:       inputPath,
				OutputPath:      outputPath,
				MaxThreads:      maxThreads,
				MaxThreadMemory: threadMemoryKB * 1024,   // Convert KB to bytes
				ChunkSize:       chunkSizeKB * 1024,      // Convert KB to bytes
				ChunkStoreSize:  chunkStoreSizeKB / 1024, // Convert KB to MB (ChunkStoreSize is in MB)
				Level:           compressLevel,
				DryRun:          dryRun,
				Verbose:         verbose,
				Quiet:           quiet,
			}

			// Validate and set defaults
			if err := opts.Validate(); err != nil {
				return err
			}

			// Warn about very high compression levels
			if compressLevel >= 15 && !quiet {
				fmt.Println("Note: high compression level (>=15) — this will be slow but can give much better ratio")
			}

			log("Starting compression...")
			log("  Input:       %s", opts.InputPath)
			log("  Output:      %s", opts.OutputPath)
			log("  Threads:     %d", opts.MaxThreads)
			log("  Level:       %d", opts.Level)
			if opts.MaxThreadMemory > 0 {
				log("  Thread Mem:  %.2f MB", float64(opts.MaxThreadMemory)/(1024*1024))
			}
			if opts.ChunkSize > 0 {
				log("  Chunk Size:  %s", formatSize(opts.ChunkSize))
				if opts.ChunkStoreSize > 0 {
					// Calculate max chunks accounting for overhead (same formula as compress_chunked.go)
					const overheadPerChunk = 120
					effectiveBytesPerChunk := opts.ChunkSize + overheadPerChunk
					maxChunks := (opts.ChunkStoreSize * 1024 * 1024) / effectiveBytesPerChunk
					log("  Store Size:  %s (~%d chunks in RAM for dedup lookups)",
						formatSize(opts.ChunkStoreSize*1024*1024), maxChunks)
					log("               Note: Archive size NOT limited by this - all unique chunks are saved")
				}
			}
			if dryRun {
				log("  Mode:        DRY-RUN (no data written)")
			}
			log("")

			// Multi-progress bar container
			var progress *mpb.Progress
			var overallBar *mpb.Bar
			var fileBars sync.Map // map[string]*mpb.Bar

			if !quiet && !verbose {
				progress = mpb.New(
					mpb.WithWidth(60),
					mpb.WithRefreshRate(100),
				)
			}

			// Progress callback
			progressCb := func(event compress.ProgressEvent) {
				if quiet || progress == nil {
					return
				}

				switch event.Type {
				case compress.EventStart:
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

				case compress.EventFileStart:
					// Create a bar for this file
					shortName := truncateLeft(event.FilePath, 30)
					bar := progress.AddBar(event.Total,
						mpb.PrependDecorators(
							decor.Name(shortName, decor.WC{C: decor.DindentRight | decor.DextraSpace, W: 32}),
						),
						mpb.AppendDecorators(
							decor.CountersKibiByte("% .1f / % .1f", decor.WCSyncWidth),
							decor.Percentage(decor.WC{W: 5}),
						),
						mpb.BarRemoveOnComplete(),
					)
					fileBars.Store(event.FilePath, bar)

				case compress.EventFileProgress:
					if bar, ok := fileBars.Load(event.FilePath); ok {
						bar.(*mpb.Bar).SetCurrent(event.Current)
					}

				case compress.EventFileComplete:
					if bar, ok := fileBars.Load(event.FilePath); ok {
						bar.(*mpb.Bar).SetCurrent(event.Total)
						fileBars.Delete(event.FilePath)
					}
					if overallBar != nil {
						overallBar.Increment()
					}

				case compress.EventError:
					if bar, ok := fileBars.Load(event.FilePath); ok {
						bar.(*mpb.Bar).Abort(true)
						fileBars.Delete(event.FilePath)
					}
					if overallBar != nil {
						overallBar.Increment()
					}

				case compress.EventComplete:
					// Handled after Compress returns
				}
			}

			// Perform compression
			result, err := compress.Compress(opts, progressCb)

			// Wait for progress bars to finish rendering
			if progress != nil {
				progress.Wait()
			}

			if err != nil {
				return err
			}

			// Final report
			fmt.Println()

			if len(result.Errors) > 0 {
				fmt.Fprintf(os.Stderr, "Completed with %d errors:\n", len(result.Errors))
				for _, e := range result.Errors {
					fmt.Fprintf(os.Stderr, "  - %v\n", e)
				}
				fmt.Println()
			}

			ratio := result.CompressionRatio()
			fmt.Printf("Summary:\n")
			fmt.Printf("  Files processed: %d / %d\n", result.FilesProcessed, result.FilesTotal)
			fmt.Printf("  Original size:   %.2f MiB\n", float64(result.OriginalSize)/1024/1024)

			if dryRun {
				fmt.Printf("  Compressed size: %.2f MiB (estimated)\n", float64(result.CompressedSize)/1024/1024)
			} else {
				fmt.Printf("  Compressed size: %.2f MiB\n", float64(result.CompressedSize)/1024/1024)
			}

			fmt.Printf("  Ratio:           %.1f%%\n", ratio)

			// Show deduplication stats if chunking was enabled
			if opts.ChunkSize > 0 && result.TotalChunks > 0 {
				fmt.Printf("\nDeduplication:\n")
				fmt.Printf("  Total chunks:    %d\n", result.TotalChunks)
				fmt.Printf("  Unique chunks:   %d\n", result.UniqueChunks)
				fmt.Printf("  Deduped chunks:  %d\n", result.DedupedChunks)
				fmt.Printf("  Dedup ratio:     %.1f%%\n", result.DedupRatio())
				fmt.Printf("  Bytes saved:     %.2f MiB\n", float64(result.BytesSaved)/1024/1024)
			}

			if dryRun {
				fmt.Println("\nDry run complete - no archive written.")
			}

			if len(result.Errors) > 0 {
				return fmt.Errorf("finished with %d errors", len(result.Errors))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Input file or directory (required)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output archive file")
	cmd.Flags().IntVarP(&maxThreads, "threads", "t", runtime.NumCPU(), "Max concurrent threads")
	cmd.Flags().StringVar(&threadMemoryStr, "thread-memory", "0", "Max memory per thread (e.g. 128MB, 1GB, 0=auto)")
	cmd.Flags().StringVar(&chunkSizeStr, "chunk-size", "0", "Chunk size for deduplication (e.g. 64KB, 512KB, min: 4KB, 0=disabled)")
	cmd.Flags().StringVar(&chunkStoreSizeStr, "chunk-store-size", "0", "Max in-memory dedup cache size (e.g. 1GB, 500MB, 0=unlimited, does NOT limit archive size)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate without writing anything")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().IntVarP(&compressLevel, "level", "l", 5,
		"zstd compression level (1=fastest, 9=best default, 19=max ratio)")

	_ = cmd.MarkFlagRequired("input")

	return cmd
}

// parseSize parses a size string (e.g., "64KB", "1MB", "2GB") and returns KB
func parseSize(s string) (uint64, error) {
	if s == "" || s == "0" {
		return 0, nil
	}

	s = strings.ToUpper(strings.TrimSpace(s))

	// Extract number and unit
	var numStr string
	var unit string

	for i, r := range s {
		if r >= '0' && r <= '9' || r == '.' {
			numStr += string(r)
		} else {
			unit = s[i:]
			break
		}
	}

	if numStr == "" {
		return 0, fmt.Errorf("no number found in size: %s", s)
	}

	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", numStr)
	}

	// Convert to KB based on unit
	var kb uint64
	switch unit {
	case "B", "":
		kb = uint64(num / 1024)
	case "KB", "K":
		kb = uint64(num)
	case "MB", "M":
		kb = uint64(num * 1024)
	case "GB", "G":
		kb = uint64(num * 1024 * 1024)
	case "TB", "T":
		kb = uint64(num * 1024 * 1024 * 1024)
	default:
		return 0, fmt.Errorf("unknown unit: %s (use B, KB, MB, GB, TB)", unit)
	}

	return kb, nil
}

const (
	// Chunk metadata overhead in GDELTA02 format:
	// - Chunk index: Hash(32) + Offset(8) + CompressedSize(8) + OriginalSize(8) = 56 bytes
	// - File metadata: Hash reference(32) per chunk
	// Total overhead per chunk: ~88 bytes (56 in index + 32 per file reference)
	// Minimum chunk size should be at least 16x this overhead = ~1.4 KB
	// Setting minimum to 4 KB provides safe margin for meaningful deduplication
	minChunkSizeKB = 4
)

// getTotalSystemMemory is implemented in platform-specific files:
// - sysmem_linux.go (Linux)
// - sysmem_darwin.go (macOS)
// - sysmem_windows.go (Windows)
// formatSize formats bytes into human-readable string
func formatSize(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
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

// truncateLeft truncates a path from the left to fit maxLen, preserving the filename
func truncateLeft(path string, maxLen int) string {
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
