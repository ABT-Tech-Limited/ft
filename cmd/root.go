// Package cmd wires up the ft command tree.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ft",
	Short: "ft wraps common Linux ops commands for Ubuntu servers",
	Long: `ft wraps common Linux ops commands (system init, service setup, ...)
into one-shot subcommands. Supported systems: Ubuntu 24.04 / 26.04.`,
	SilenceUsage: true,
}

// SetVersionInfo records build metadata injected by GoReleaser.
func SetVersionInfo(version, commit, date string) {
	rootCmd.Version = fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
}

// Execute runs the root command and returns its error, if any.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print ft version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("ft", rootCmd.Version)
		},
	})
}
