package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCodexCmd(deps codexDependencies) *cobra.Command {
	codexCmd := &cobra.Command{
		Use:   "codex",
		Short: "Manage OpenAI Codex CLI configuration",
		Long: `Manage the user-level OpenAI Codex CLI configuration.

Use "ft install codex" to install or update Codex itself.`,
		Args: cobra.NoArgs,
	}
	codexCmd.AddCommand(newCodexConfigCmd(deps))
	return codexCmd
}

func newCodexConfigCmd(deps codexDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Update the Codex API host and key, then refresh the shared app-server",
		Long: `Update the Responses API host and API key used by Codex.

ft asks for the host and key, shows a masked preview, preserves unrelated
config.toml settings, stores the credential through "codex login", stops the
shared app-server daemon so the next codex launch loads the new settings, and
validates the result with a minimal model request.

Editing config.toml or auth.json by hand leaves an already running app-server
daemon on the old credentials, which surfaces as HTTP 401 in interactive codex
sessions. Run without sudo, as the user who uses Codex.`,
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
