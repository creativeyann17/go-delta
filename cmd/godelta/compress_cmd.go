// cmd/godelta/compress.go

package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/cheggaaa/pb/v3"
	"github.com/spf13/cobra"

	"github.com/creativeyann17/go-delta/pkg/compress"
)

func init() {
	rootCmd.AddCommand(compressCmd())
}

func compressCmd() *cobra.Command {
	var inputPath, outputPath string
	var maxThreads int
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
				InputPath:  inputPath,
				OutputPath: outputPath,
				MaxThreads: maxThreads,
				Level:      compressLevel,
				DryRun:     dryRun,
				Verbose:    verbose,
				Quiet:      quiet,
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
			log("  Max threads: %d", opts.MaxThreads)
			log("  Level:       %d", opts.Level)
			if dryRun {
				log("  Mode:        DRY-RUN (no data written)")
			}
			if verbose {
				log("  Mode:        VERBOSE (detailed output)")
			}
			log("")

			// Setup progress bars
			var overallBar *pb.ProgressBar

			if !quiet {
				overallBar = pb.New(0) // Will set max when we know file count
				overallBar.SetTemplateString(`{{counters}} {{bar}} {{percent | green}} | {{time .}}`)
				overallBar.SetMaxWidth(80)
			}

			processedFiles := 0

			// Progress callback
			progressCb := func(event compress.ProgressEvent) {
				if quiet {
					return
				}

				switch event.Type {
				case compress.EventStart:
					if overallBar != nil {
						overallBar.SetTotal(event.Total)
						overallBar.Start()
					}

				case compress.EventFileStart:
					if verbose {
						fmt.Printf("  Compressing %s (%.1f MiB)...\n",
							event.FilePath,
							float64(event.Total)/1024/1024)
					}
					// Create per-file progress bar (optional - can be overwhelming with many threads)
					// For now, we'll skip per-file bars to avoid clutter

				case compress.EventFileComplete:
					processedFiles++
					if overallBar != nil {
						overallBar.Increment()
					}
					if verbose {
						ratio := float64(event.CompressedSize) / float64(event.Total) * 100
						fmt.Printf("  Finished %s → %.1f MiB (%.1f%%)\n",
							event.FilePath,
							float64(event.CompressedSize)/1024/1024,
							ratio)
					}

				case compress.EventError:
					if verbose {
						fmt.Fprintf(os.Stderr, "  Error on %s\n", event.FilePath)
					}
					processedFiles++
					if overallBar != nil {
						overallBar.Increment()
					}

				case compress.EventComplete:
					// Final progress update handled after Compress() returns
				}
			}

			// Perform compression
			result, err := compress.Compress(opts, progressCb)

			// Finish progress bar before printing summary
			if overallBar != nil {
				overallBar.Finish()
			}

			if err != nil {
				return err
			}

			// Final report
			fmt.Printf("\n")

			if len(result.Errors) > 0 {
				fmt.Fprintf(os.Stderr, "Completed with %d errors:\n", len(result.Errors))
				for _, e := range result.Errors {
					fmt.Fprintf(os.Stderr, "  - %v\n", e)
				}
				fmt.Println()
			}

			ratio := result.CompressionRatio()
			fmt.Printf("Summary:\n")
			fmt.Printf("  Files successfully processed: %d / %d\n", result.FilesProcessed, result.FilesTotal)
			fmt.Printf("  Original size:                %.2f MiB\n", float64(result.OriginalSize)/1024/1024)

			if dryRun {
				fmt.Printf("  Estimated compressed size:    %.2f MiB (rough)\n", float64(result.CompressedSize)/1024/1024)
			} else {
				fmt.Printf("  Compressed size:              %.2f MiB\n", float64(result.CompressedSize)/1024/1024)
			}

			fmt.Printf("  Compression ratio:            %.1f%%\n", ratio)

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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate without writing anything")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().IntVarP(&compressLevel, "level", "l", 5,
		"zstd compression level (1=fastest, 9=best default, 19=max ratio)")

	_ = cmd.MarkFlagRequired("input")

	return cmd
}
