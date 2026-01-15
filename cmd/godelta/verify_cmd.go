// cmd/godelta/verify_cmd.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/creativeyann17/go-delta/pkg/verify"
)

func init() {
	rootCmd.AddCommand(verifyCmd())
}

func verifyCmd() *cobra.Command {
	var inputPath string
	var verifyData bool
	var verbose bool
	var quiet bool

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify archive integrity",
		Long: `Verify the integrity of a GDELTA or ZIP archive.

By default, performs structural validation (header, metadata, footer).
Use --data to also verify data integrity by decompressing all content.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := &verify.Options{
				InputPath:  inputPath,
				VerifyData: verifyData,
				Verbose:    verbose,
				Quiet:      quiet,
			}

			if err := opts.Validate(); err != nil {
				return err
			}

			// Logging helper
			log := func(format string, args ...interface{}) {
				if !quiet {
					fmt.Printf(format+"\n", args...)
				}
			}

			log("Verifying archive: %s", inputPath)
			if verifyData {
				log("Mode: Full data integrity check")
			} else {
				log("Mode: Structural validation only")
			}
			log("")

			// Create progress callback
			var progressCb verify.ProgressCallback
			if !quiet && !verbose {
				lastFile := ""
				progressCb = func(event verify.ProgressEvent) {
					switch event.Type {
					case verify.EventStart:
						fmt.Printf("Checking %d files...\n", event.Total)
					case verify.EventFileVerify:
						if event.Current%100 == 0 || event.Current == event.Total {
							fmt.Printf("\r  Progress: %d/%d files", event.Current, event.Total)
						}
						lastFile = event.FilePath
					case verify.EventChunkVerify:
						if event.Current%500 == 0 {
							fmt.Printf("\r  Chunks verified: %d/%d", event.Current, event.Total)
						}
					case verify.EventComplete:
						fmt.Printf("\r  Progress: %d/%d files\n", event.Current, event.Total)
					case verify.EventError:
						fmt.Printf("\n  Error in: %s\n", lastFile)
					}
				}
			} else if verbose {
				progressCb = func(event verify.ProgressEvent) {
					switch event.Type {
					case verify.EventStart:
						fmt.Printf("Starting verification: %s\n", event.Message)
					case verify.EventFileVerify:
						fmt.Printf("  [%d/%d] %s\n", event.Current, event.Total, event.FilePath)
					case verify.EventChunkVerify:
						if event.Current%100 == 0 {
							fmt.Printf("  Chunks: %d/%d verified\n", event.Current, event.Total)
						}
					case verify.EventComplete:
						fmt.Printf("Verification complete\n")
					}
				}
			}

			// Perform verification
			result, err := verify.Verify(opts, progressCb)
			if err != nil && result == nil {
				return err
			}

			// Print summary
			fmt.Println()
			fmt.Print(result.Summary())

			// Return error if invalid
			if !result.IsValid() {
				return fmt.Errorf("archive verification failed")
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Input archive file (required)")
	cmd.Flags().BoolVar(&verifyData, "data", false, "Verify data integrity by decompressing all content")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed output")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Minimal output (overrides verbose)")

	_ = cmd.MarkFlagRequired("input")

	return cmd
}
