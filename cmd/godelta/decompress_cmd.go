// cmd/godelta/decompress_cmd.go

package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

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
			// Add .gdelta extension if missing
			if inputPath != "" && len(inputPath) >= 7 && inputPath[len(inputPath)-7:] != ".gdelta" {
				inputPath += ".gdelta"
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
			progressCb := func(event decompress.ProgressEvent) {
				if quiet || progress == nil {
					return
				}

				switch event.Type {
				case decompress.EventStart:
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

				case decompress.EventFileStart:
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

				case decompress.EventFileProgress:
					if bar, ok := fileBars.Load(event.FilePath); ok {
						bar.(*mpb.Bar).SetCurrent(event.Current)
					}

				case decompress.EventFileComplete:
					if bar, ok := fileBars.Load(event.FilePath); ok {
						bar.(*mpb.Bar).SetCurrent(event.Total)
						fileBars.Delete(event.FilePath)
					}
					if overallBar != nil {
						overallBar.Increment()
					}

				case decompress.EventError:
					if bar, ok := fileBars.Load(event.FilePath); ok {
						bar.(*mpb.Bar).Abort(true)
						fileBars.Delete(event.FilePath)
					}
					if overallBar != nil {
						overallBar.Increment()
					}

				case decompress.EventComplete:
					// Handled after Decompress returns
				}
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
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing files")

	_ = cmd.MarkFlagRequired("input")

	return cmd
}
