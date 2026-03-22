package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X github.com/bovinemagnet/gossm/cmd.Version=..."
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of gossm",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("gossm version %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
