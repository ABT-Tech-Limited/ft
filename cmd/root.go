// Package cmd wires up the ft command tree.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var buildVersion = "dev"

var rootCmd = &cobra.Command{
	Use:   "ft",
	Short: "ft wraps common ops commands for Linux servers",
	Long: `ft wraps common Linux ops commands (system init, service setup, ...)
into one-shot subcommands.

Designed for Linux servers of all major distributions; currently
supports Ubuntu 24.04 / 26.04, with more distributions to come.`,
	SilenceUsage: true,
}

// SetVersionInfo records build metadata injected by GoReleaser.
func SetVersionInfo(version, commit, date string) {
	buildVersion = version
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
