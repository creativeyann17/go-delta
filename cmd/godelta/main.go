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
