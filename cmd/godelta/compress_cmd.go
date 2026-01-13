// cmd/godelta/compress_cmd.go

package main

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"

	"github.com/creativeyann17/go-delta/pkg/compress"
)

func init() {
	rootCmd.AddCommand(compressCmd())
}

func compressCmd() *cobra.Command {
	var inputPath, outputPath string
	var maxThreads int
	var parallelism string
	var threadMemoryStr string
	var chunkSizeStr string
	var chunkStoreSizeStr string
	var dryRun bool
	var verbose bool
	var quiet bool
	var compressLevel int
	var useZipFormat bool

	cmd := &cobra.Command{
		Use:   "compress",
		Short: "Compress file or directory into delta archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine output extension based on format
			if outputPath == "" {
				outputPath = "archive"
			}
			if useZipFormat {
				// For ZIP, remove .zip if present - compress_zip will add _01.zip, _02.zip, etc.
				if strings.HasSuffix(outputPath, ".zip") {
					outputPath = outputPath[:len(outputPath)-4]
				}
			} else {
				// Add .gdelta extension if missing
				if !strings.HasSuffix(outputPath, ".gdelta") {
					outputPath += ".gdelta"
				}
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

			// Auto-calculate thread memory if not specified
			if threadMemoryKB == 0 {
				threadMemoryKB = autoSizeFromSystemMemory(totalSystemMemoryKB)
				if threadMemoryKB > 0 {
					log("Auto-calculated thread memory: %.0f MB (%d%% of system memory, capped at %.0f GB)",
						float64(threadMemoryKB)/1024, autoSizePercent, float64(autoSizeMaxKB)/(1024*1024))
				}
			}

			// Auto-calculate chunk store size if chunking is enabled but store size not specified
			if chunkSizeKB > 0 && chunkStoreSizeKB == 0 {
				chunkStoreSizeKB = autoSizeFromSystemMemory(totalSystemMemoryKB)
				if chunkStoreSizeKB > 0 {
					log("Auto-calculated chunk store size: %.0f MB (%d%% of system memory, capped at %.0f GB)",
						float64(chunkStoreSizeKB)/1024, autoSizePercent, float64(autoSizeMaxKB)/(1024*1024))
				}
			}

			// Prepare options
			opts := &compress.Options{
				InputPath:       inputPath,
				OutputPath:      outputPath,
				MaxThreads:      maxThreads,
				Parallelism:     compress.Parallelism(parallelism),
				MaxThreadMemory: threadMemoryKB * 1024,   // Convert KB to bytes
				ChunkSize:       chunkSizeKB * 1024,      // Convert KB to bytes
				ChunkStoreSize:  chunkStoreSizeKB / 1024, // Convert KB to MB (ChunkStoreSize is in MB)
				Level:           compressLevel,
				UseZipFormat:    useZipFormat,
				DryRun:          dryRun,
				Verbose:         verbose,
				Quiet:           quiet,
			}

			// Validate and set defaults
			if err := opts.Validate(); err != nil {
				return err
			}

			// Warn about very high compression levels
			if !useZipFormat && compressLevel >= 15 && !quiet {
				fmt.Println("Note: high compression level (>=15) — this will be slow but can give much better ratio")
			}

			formatType := "GDELTA01"
			if useZipFormat {
				formatType = "ZIP"
			} else if opts.ChunkSize > 0 {
				formatType = "GDELTA02"
			}

			log("Starting compression...")
			log("  Format:      %s", formatType)
			log("  Input:       %s", opts.InputPath)
			log("  Output:      %s", opts.OutputPath)
			log("  Threads:     %d", opts.MaxThreads)
			log("  Parallelism: %s", opts.Parallelism)
			log("  Level:       %d", opts.Level)
			if opts.MaxThreadMemory > 0 {
				log("  Thread Mem:  %.2f MB", float64(opts.MaxThreadMemory)/(1024*1024))
			}
			if opts.ChunkSize > 0 {
				log("  Chunk Size:  %s", compress.FormatSize(opts.ChunkSize))
				if opts.ChunkStoreSize > 0 {
					// Calculate max chunks accounting for overhead (same formula as compress_chunked.go)
					const overheadPerChunk = 120
					effectiveBytesPerChunk := opts.ChunkSize + overheadPerChunk
					maxChunks := (opts.ChunkStoreSize * 1024 * 1024) / effectiveBytesPerChunk
					log("  Store Size:  %s (~%d chunks in RAM for dedup lookups)",
						compress.FormatSize(opts.ChunkStoreSize*1024*1024), maxChunks)
					log("               Note: Archive size NOT limited by this - all unique chunks are saved")
				}
			}
			if dryRun {
				log("  Mode:        DRY-RUN (no data written)")
			}
			log("")

			// Create progress callback and progress container
			var progressCb compress.ProgressCallback
			var progress *mpb.Progress

			if !quiet && !verbose {
				progressCb, progress = compress.ProgressBarCallback()
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
			fmt.Print(compress.FormatSummary(result, opts))

			if len(result.Errors) > 0 {
				return fmt.Errorf("finished with %d errors", len(result.Errors))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Input file or directory (required)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output archive file")
	cmd.Flags().IntVarP(&maxThreads, "threads", "t", runtime.NumCPU(), "Max concurrent threads")
	cmd.Flags().StringVarP(&parallelism, "parallelism", "p", "auto", "Parallelism strategy: auto, folder, file (auto=detect based on input structure)")
	cmd.Flags().StringVar(&threadMemoryStr, "thread-memory", "0", "Max memory per thread (e.g. 128MB, 1GB, 0=auto ~25% RAM capped at 4GB)")
	cmd.Flags().StringVar(&chunkSizeStr, "chunk-size", "0", "Average chunk size for content-defined dedup (e.g. 64KB, 512KB, actual chunks vary 1/4x to 4x, 0=disabled)")
	cmd.Flags().StringVar(&chunkStoreSizeStr, "chunk-store-size", "0", "Max in-memory dedup cache size (e.g. 1GB, 500MB, 0=auto ~25% RAM, does NOT limit archive size)")
	cmd.Flags().BoolVar(&useZipFormat, "zip", false, "Create standard ZIP archive instead of GDELTA format (universally compatible)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate without writing anything")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().IntVarP(&compressLevel, "level", "l", 5,
		"Compression level: 1-9 for ZIP deflate, 1-22 for zstd (1=fastest, 9=best default, 19=max ratio for zstd)")

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

	// Auto-size calculation constants (in KB)
	autoSizeMaxKB   = 4 * 1024 * 1024 // 4GB cap
	autoSizeMinKB   = 256 * 1024      // 256MB minimum
	autoSizePercent = 25              // Use 25% of system memory
)

// autoSizeFromSystemMemory calculates a bounded size based on system memory.
// Returns size in KB: 25% of system RAM, capped at 4GB, minimum 256MB.
// Returns 0 if systemMemoryKB is 0 (unknown).
func autoSizeFromSystemMemory(systemMemoryKB uint64) uint64 {
	if systemMemoryKB == 0 {
		return 0
	}
	sizeKB := systemMemoryKB * autoSizePercent / 100
	if sizeKB > autoSizeMaxKB {
		sizeKB = autoSizeMaxKB
	}
	if sizeKB < autoSizeMinKB {
		sizeKB = autoSizeMinKB
	}
	return sizeKB
}

// getTotalSystemMemory is implemented in platform-specific files:
// - sysmem_linux.go (Linux)
// - sysmem_darwin.go (macOS)
// - sysmem_windows.go (Windows)
