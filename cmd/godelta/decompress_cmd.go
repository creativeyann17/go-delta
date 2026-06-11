// cmd/godelta/decompress_cmd.go

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"

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
			// Add extension if missing
			if inputPath != "" {
				hasZip := strings.HasSuffix(inputPath, ".zip")
				hasGdelta := strings.HasSuffix(inputPath, ".gdelta")
				hasXz := strings.HasSuffix(inputPath, ".xz")

				if !hasZip && !hasGdelta && !hasXz {
					// Check for multi-part ZIP first (e.g., archive_01.zip)
					multiPartZip := inputPath + "_01.zip"
					if _, err := os.Stat(multiPartZip); err == nil {
						inputPath = multiPartZip
					} else if _, err := os.Stat(inputPath + ".zip"); err == nil {
						// Check for single ZIP file
						inputPath += ".zip"
					} else {
						// Default to .gdelta
						inputPath += ".gdelta"
					}
				}
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
			if overwrite {
				log("  Mode:        OVERWRITE (replacing existing files)")
			}
			log("")

			// Create progress callback and progress container
			var progressCb decompress.ProgressCallback
			var progress *mpb.Progress

			if !quiet && !verbose {
				progressCb, progress = decompress.ProgressBarCallback()
			}

			// Perform decompression
			result, err := decompress.Decompress(opts, progressCb)

			// Wait for progress bars to finish rendering
			if progress != nil {
				progress.Wait()
			}

			if err != nil {
				return err
			}

			// Final report
			fmt.Println()
			fmt.Print(decompress.FormatSummary(result))

			if len(result.Errors) > 0 {
				return fmt.Errorf("finished with %d errors", len(result.Errors))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Input archive file (required)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", ".", "Output directory")
	cmd.Flags().IntVarP(&maxThreads, "threads", "t", 0, "Max concurrent threads (0 = number of CPUs)")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing files")

	_ = cmd.MarkFlagRequired("input")

	return cmd
}
