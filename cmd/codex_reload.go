package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCodexReloadCmd(deps codexDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Restart the Codex app-server to load the existing configuration",
		Long: `Restart the server for the current CODEX_HOME and verify it is running
as a managed daemon. Does not edit the host/key, upgrade the CLI, or make a
model request. The first managed start may download a daemon package.

On Linux, an unmanaged server is stopped only after its socket owner and
process identity are verified. ft sends SIGTERM and waits for exit; it never
uses SIGKILL or deletes socket/PID files.

Reloading disconnects clients and may interrupt running tasks. Pause Codex
work and run this command from a separate SSH terminal, not from the server
being restarted. Run without sudo, as the user who uses Codex.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.isSudo() {
				return fmt.Errorf("do not run this command with sudo; run \"ft codex reload\" as the user who uses Codex")
			}
			executable, err := deps.resolveCurrent()
			if err != nil {
				return fmt.Errorf("locating Codex: %w; run \"ft install codex\" first", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Reloading the shared Codex app-server will disconnect clients and may interrupt running tasks...")
			if err := deps.reloadDaemon(cmd.Context(), executable, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("reload Codex app-server: %w; resolve the server issue and retry \"ft codex reload\" from a separate terminal", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Codex app-server 已重载现有配置并确认由 daemon 管理。请重新连接客户端。")
			return nil
		},
	}
}
