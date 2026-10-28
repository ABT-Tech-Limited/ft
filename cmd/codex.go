package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCodexCmd(deps codexDependencies) *cobra.Command {
	codexCmd := &cobra.Command{
		Use:   "codex",
		Short: "Manage the OpenAI Codex CLI installation and configuration",
		Long: `Manage the user-level OpenAI Codex CLI installation and configuration.

Use "ft install codex" for first-time setup, "ft codex update" to move to the
latest release, "ft codex config" to change the API host or key, and
"ft codex reload" to restart the server with the existing configuration.`,
		Args: cobra.NoArgs,
	}
	codexCmd.AddCommand(newCodexConfigCmd(deps))
	codexCmd.AddCommand(newCodexUpdateCmd(deps))
	codexCmd.AddCommand(newCodexReloadCmd(deps))
	return codexCmd
}

func newCodexConfigCmd(deps codexDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Update the Codex API host and key, then refresh the shared app-server",
		Long: `Update the Responses API host and API key used by Codex.

ft asks for the host and key, shows a masked preview, preserves unrelated
config.toml settings, stores the credential through "codex login", reloads the
shared app-server with the new settings, and
validates the result with a minimal model request.

Editing config.toml or auth.json by hand leaves an already running app-server
daemon on the old credentials, which surfaces as HTTP 401 in interactive codex
sessions. On Linux, ft can safely terminate a verified same-user direct server
owning the current CODEX_HOME control socket and replace it with a managed daemon.
Reloading disconnects clients and may interrupt tasks: pause work and use a
separate SSH terminal. Run without sudo, as the user who uses Codex.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCodexConfig(cmd, deps)
		},
	}
}

func runCodexConfig(cmd *cobra.Command, deps codexDependencies) error {
	if deps.isSudo() {
		return fmt.Errorf("do not run this command with sudo; run \"ft codex config\" as the user who will use Codex")
	}
	executable, err := deps.resolveCurrent()
	if err != nil {
		return fmt.Errorf("locating Codex: %w; run \"ft install codex\" first", err)
	}
	configPath, err := deps.configPath()
	if err != nil {
		return fmt.Errorf("resolving Codex configuration path: %w", err)
	}
	existing, err := deps.readConfig(cmd.Context(), configPath)
	if err != nil {
		return fmt.Errorf("reading Codex configuration: %w", err)
	}
	return configureCodex(cmd, deps, executable, configPath, existing)
}

func init() {
	rootCmd.AddCommand(newCodexCmd(productionCodexDependencies()))
}
