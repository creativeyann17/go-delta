package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "godelta",
	Short:   "go-delta - smart delta compression for backups",
	Long:    "go-delta creates efficient delta archives from similar file sets.",
	Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	// Will add subcommands later: compress, decompress, stats, etc.
	rootCmd.AddCommand(
		versionCmd(),
		compressCmd(),
		// decompressCmd(),    // ← will be added later
		// statsCmd(),         // ← optional later
	)
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("go-delta %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		},
	}
}
