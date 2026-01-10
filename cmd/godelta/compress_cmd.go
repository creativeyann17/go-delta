// cmd/godelta/compress_cmd.go

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	var threadMemoryMB uint64
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

			// Prepare options
			opts := &compress.Options{
				InputPath:       inputPath,
				OutputPath:      outputPath,
				MaxThreads:      maxThreads,
				MaxThreadMemory: threadMemoryMB * 1024 * 1024, // Convert MB to bytes
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

			// Logging helper
			log := func(format string, args ...interface{}) {
				if !quiet {
					fmt.Printf(format+"\n", args...)
				}
			}

			log("Starting compression...")
			log("  Input:       %s", opts.InputPath)
			log("  Output:      %s", opts.OutputPath)
			log("  Threads:     %d", opts.MaxThreads)
			log("  Level:       %d", opts.Level)
			if opts.MaxThreadMemory > 0 {
				log("  Thread Mem:  %d MB", opts.MaxThreadMemory/(1024*1024))
			}
			if dryRun {
				log("  Mode:        DRY-RUN (no data written)")
			}
			log("")

			// Multi-progress bar container
			var progress *mpb.Progress
			var overallBar *mpb.Bar
			var fileBars sync.Map // map[string]*mpb.Bar

			if !quiet {
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
	cmd.Flags().Uint64Var(&threadMemoryMB, "thread-memory", 0, "Max memory per thread in MB (0=unlimited)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate without writing anything")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().IntVarP(&compressLevel, "level", "l", 5,
		"zstd compression level (1=fastest, 9=best default, 19=max ratio)")

	_ = cmd.MarkFlagRequired("input")

	return cmd
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
