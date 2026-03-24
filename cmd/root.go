// Package cmd implements the cobra command tree for gossm.
package cmd

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	logLevel string
)

// rootCmd is the base command when called without subcommands.
var rootCmd = &cobra.Command{
	Use:   "gossm",
	Short: "AWS SSM session manager and tunnel dashboard",
	Long:  "gossm lists AWS EC2 instances and launches SSM sessions. Run as a daemon for a web-based tunnel management dashboard.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		switch logLevel {
		case "debug":
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		case "info":
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
		default:
			zerolog.SetGlobalLevel(zerolog.WarnLevel)
		}
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&logLevel, "logging", "l", "warn", "set log level (debug, info, warn)")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
