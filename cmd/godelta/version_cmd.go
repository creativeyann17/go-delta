// cmd/godelta/version_cmd.go

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd())
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
