package cmd

import (
	"errors"
	"fmt"

	"github.com/ABT-Tech-Limited/ft/internal/codexx"
	"github.com/spf13/cobra"
)

func newCodexUpdateCmd(deps codexDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update Codex CLI to the latest release and refresh the shared app-server",
		Long: `Update Codex with OpenAI's official standalone installer.

The shared app-server daemon keeps its own copy of Codex, so an installer run
alone leaves a running daemon on the previous release. After the CLI update, ft
runs "codex app-server daemon update": it fetches the matching daemon package
and restarts the daemon only when it is running an older release. A current
daemon is left running. Configuration and credentials are not changed; use
"ft codex config" for that. Run without sudo, as the user who uses Codex.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCodexUpdate(cmd, deps)
		},
	}
}

func runCodexUpdate(cmd *cobra.Command, deps codexDependencies) error {
	if deps.isSudo() {
		return fmt.Errorf("do not run this command with sudo; run \"ft codex update\" as the user who will use Codex")
	}
	executable, err := deps.resolveCurrent()
	if err != nil {
		return fmt.Errorf("locating Codex: %w; run \"ft install codex\" first", err)
	}
	before, err := deps.detectVersion(cmd.Context(), executable)
	if err != nil {
		if errors.Is(err, codexx.ErrNotFound) {
			return fmt.Errorf("locating Codex: %w; run \"ft install codex\" first", err)
		}
		return fmt.Errorf("checking installed Codex version: %w", err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Detected Codex CLI %s; checking for the latest release...\n", before)
	_, err = updateCodexCLI(cmd, deps, out, cmd.ErrOrStderr(), before, "already the latest release")
	return err
}
