package cmd

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/aptx"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the system: apt update, upgrade and common tools",
	Long: `Initialize a fresh Linux server (currently Ubuntu 24.04 / 26.04):

  1. apt-get update
  2. apt-get upgrade -y (non-interactive)
  3. install common tools: ` + strings.Join(aptx.DefaultPackages, ", ") + `

Runs with sudo automatically when not invoked as root.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkOS(); err != nil {
			return err
		}
		steps := []struct {
			title string
			argv  []string
		}{
			{"Updating package index", aptx.Update()},
			{"Upgrading installed packages", aptx.Upgrade()},
			{"Installing common tools", aptx.Install(aptx.DefaultPackages...)},
		}
		for i, step := range steps {
			fmt.Printf("\n==> [%d/%d] %s\n", i+1, len(steps), step.title)
			if err := aptx.RunContext(cmd.Context(), step.argv); err != nil {
				return fmt.Errorf("%s failed: %w", strings.ToLower(step.title[:1])+step.title[1:], err)
			}
		}
		fmt.Println("\nSystem initialized successfully.")
		return nil
	},
}

// detectOS returns the supported distribution before a single system command
// is run.
func detectOS() (osrelease.Info, error) {
	if runtime.GOOS != "linux" {
		return osrelease.Info{}, fmt.Errorf("unsupported system: %s (supported: Ubuntu 24.04, Ubuntu 26.04)", runtime.GOOS)
	}
	data, err := os.ReadFile(osrelease.Path)
	if err != nil {
		return osrelease.Info{}, fmt.Errorf("cannot detect OS from %s: %w (supported: Ubuntu 24.04, Ubuntu 26.04)", osrelease.Path, err)
	}
	info := osrelease.Parse(string(data))
	if err := info.Supported(); err != nil {
		return osrelease.Info{}, err
	}
	return info, nil
}

// checkOS preserves the init command's validation-only interface.
func checkOS() error {
	_, err := detectOS()
	return err
}

func init() {
	rootCmd.AddCommand(initCmd)
}
