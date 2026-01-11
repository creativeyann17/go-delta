// cmd/godelta/decompress_cmd.go

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"

	"github.com/creativeyann17/go-delta/pkg/decompress"
)

func init() {
	rootCmd.AddCommand(decompressCmd())
}

func decompressCmd() *cobra.Command {
	var inputPath, outputPath string
	var verbose bool
	var quiet bool
	var overwrite bool

	cmd := &cobra.Command{
		Use:   "decompress",
		Short: "Decompress delta archive to files",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Add extension if missing
			if inputPath != "" {
				hasZip := len(inputPath) >= 4 && inputPath[len(inputPath)-4:] == ".zip"
				hasGdelta := len(inputPath) >= 7 && inputPath[len(inputPath)-7:] == ".gdelta"

				if !hasZip && !hasGdelta {
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
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing files")

	_ = cmd.MarkFlagRequired("input")

	return cmd
}
