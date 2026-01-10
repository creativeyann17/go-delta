// cmd/godelta/decompress_cmd.go

package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/cheggaaa/pb/v3"
	"github.com/spf13/cobra"

	"github.com/creativeyann17/go-delta/pkg/decompress"
)

func init() {
	rootCmd.AddCommand(decompressCmd())
}

func decompressCmd() *cobra.Command {
	var inputPath, outputPath string
	var maxThreads int
	var verbose bool
	var quiet bool
	var overwrite bool

	cmd := &cobra.Command{
		Use:   "decompress",
		Short: "Decompress delta archive to files",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Add .gdelta extension if missing
			if inputPath != "" && len(inputPath) >= 7 && inputPath[len(inputPath)-7:] != ".gdelta" {
				inputPath += ".gdelta"
			}

			// Prepare options
			opts := &decompress.Options{
				InputPath:  inputPath,
				OutputPath: outputPath,
				MaxThreads: maxThreads,
				Verbose:    verbose,
				Quiet:      quiet,
				Overwrite:  overwrite,
			}

			// Validate and set defaults
			if err := opts.Validate(); err != nil {
				return err
			}

			// Logging helper
			log := func(format string, args ...interface{}) {
				if !quiet {
					fmt.Printf(format+"\n", args...)
				}
			}

			log("Starting decompression...")
			log("  Input:       %s", opts.InputPath)
			log("  Output:      %s", opts.OutputPath)
			log("  Max threads: %d", opts.MaxThreads)
			if overwrite {
				log("  Mode:        OVERWRITE (replacing existing files)")
			}
			if verbose {
				log("  Mode:        VERBOSE (detailed output)")
			}
			log("")

			// Setup progress bar
			var overallBar *pb.ProgressBar

			if !quiet {
				overallBar = pb.New(0)
				overallBar.SetTemplateString(`{{counters}} {{bar}} {{percent | green}} | {{time .}}`)
				overallBar.SetMaxWidth(80)
			}

			processedFiles := 0

			// Progress callback
			progressCb := func(event decompress.ProgressEvent) {
				if quiet {
					return
				}

				switch event.Type {
				case decompress.EventStart:
					if overallBar != nil {
						overallBar.SetTotal(event.Total)
						overallBar.Start()
					}

				case decompress.EventFileStart:
					if verbose {
						fmt.Printf("  Decompressing %s (%.1f MiB)...\n",
							event.FilePath,
							float64(event.Total)/1024/1024)
					}

				case decompress.EventFileComplete:
					processedFiles++
					if overallBar != nil {
						overallBar.Increment()
					}
					if verbose {
						fmt.Printf("  Finished %s → %.1f MiB\n",
							event.FilePath,
							float64(event.DecompressedSize)/1024/1024)
					}

				case decompress.EventError:
					if verbose {
						fmt.Fprintf(os.Stderr, "  Error on %s\n", event.FilePath)
					}
					processedFiles++
					if overallBar != nil {
						overallBar.Increment()
					}

				case decompress.EventComplete:
					// Final progress update handled after Decompress() returns
				}
			}

			// Perform decompression
			result, err := decompress.Decompress(opts, progressCb)

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

			fmt.Printf("Summary:\n")
			fmt.Printf("  Files successfully processed: %d / %d\n", result.FilesProcessed, result.FilesTotal)
			fmt.Printf("  Compressed size:              %.2f MiB\n", float64(result.CompressedSize)/1024/1024)
			fmt.Printf("  Decompressed size:            %.2f MiB\n", float64(result.DecompressedSize)/1024/1024)

			if len(result.Errors) > 0 {
				return fmt.Errorf("finished with %d errors", len(result.Errors))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Input archive file (required)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", ".", "Output directory")
	cmd.Flags().IntVarP(&maxThreads, "threads", "t", runtime.NumCPU(), "Max concurrent threads")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing files")

	_ = cmd.MarkFlagRequired("input")

	return cmd
}
