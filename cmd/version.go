package cmd

import (
	"fmt"

	"mtsh/internal/manifest"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show application version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(manifest.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
