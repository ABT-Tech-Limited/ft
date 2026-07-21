package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/ABT-Tech-Limited/ft/internal/sysx"
	"github.com/ABT-Tech-Limited/ft/internal/updater"
	"github.com/spf13/cobra"
)

const latestReleaseURL = "https://api.github.com/repos/ABT-Tech-Limited/ft/releases/latest"

type updateDependencies struct {
	currentVersion func() string
	goos           string
	goarch         string
	executable     func() (string, error)
	httpClient     *http.Client
	latestURL      string
	privilegedRun  updater.PrivilegedRunner
	update         func(context.Context, updater.Options) (updater.Result, error)
}

func newUpdateCmd(deps updateDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update ft to the latest stable release",
		Long: `Check the official GitHub Releases for a newer stable ft version.

The release archive is verified against checksums.txt before ft atomically
replaces the current executable. Protected installations are elevated with
sudo automatically. Development builds cannot self-update.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Checking for ft updates..."); err != nil {
				return fmt.Errorf("writing update status: %w", err)
			}
			executablePath, err := deps.executable()
			if err != nil {
				return fmt.Errorf("locating current ft executable: %w", err)
			}
			result, err := deps.update(cmd.Context(), updater.Options{
				CurrentVersion: deps.currentVersion(),
				GOOS:           deps.goos,
				GOARCH:         deps.goarch,
				ExecutablePath: executablePath,
				Client: updater.Client{
					HTTPClient: deps.httpClient,
					LatestURL:  deps.latestURL,
				},
				PrivilegedRun: deps.privilegedRun,
			})
			if err != nil {
				updateErr := fmt.Errorf("updating ft: %w", err)
				if result.Updated {
					if _, writeErr := fmt.Fprintf(
						cmd.OutOrStdout(),
						"Updated ft from %s to %s at %s, but final cleanup failed: %v\n",
						result.CurrentVersion,
						result.LatestVersion,
						result.Path,
						err,
					); writeErr != nil {
						return errors.Join(updateErr, fmt.Errorf("writing partial update result: %w", writeErr))
					}
				}
				return updateErr
			}
			if !result.Updated {
				if _, err := fmt.Fprintf(
					cmd.OutOrStdout(),
					"ft %s is already up to date (latest stable: %s).\n",
					result.CurrentVersion,
					result.LatestVersion,
				); err != nil {
					return fmt.Errorf("writing update result: %w", err)
				}
				return nil
			}
			if _, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"Updated ft from %s to %s at %s.\n",
				result.CurrentVersion,
				result.LatestVersion,
				result.Path,
			); err != nil {
				return fmt.Errorf("writing update result: %w", err)
			}
			return nil
		},
	}
}

func init() {
	rootCmd.AddCommand(newUpdateCmd(updateDependencies{
		currentVersion: func() string { return buildVersion },
		goos:           runtime.GOOS,
		goarch:         runtime.GOARCH,
		executable:     os.Executable,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		latestURL:      latestReleaseURL,
		privilegedRun:  sysx.RunInputContext,
		update:         updater.Update,
	}))
}
