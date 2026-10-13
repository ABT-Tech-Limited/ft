package codexx

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// StopDaemon stops the shared Codex app-server daemon of the current
// CODEX_HOME. The daemon keeps the configuration and credentials it loaded at
// startup, so interactive codex sessions keep using stale values after
// config.toml or auth.json change. Stopping it is enough: the next codex
// launch starts a fresh daemon from the current files. Codex exits
// successfully when no daemon is running.
func StopDaemon(ctx context.Context, runner Runner, executable string, stdout, stderr io.Writer) error {
	if ctx == nil {
		return fmt.Errorf("nil daemon stop context")
	}
	if runner == nil {
		return fmt.Errorf("nil command runner")
	}
	if strings.TrimSpace(executable) == "" {
		return fmt.Errorf("empty Codex executable")
	}
	err := runner.Run(ctx, Command{
		Path:   executable,
		Args:   []string{"app-server", "daemon", "stop"},
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return fmt.Errorf("stop Codex app-server daemon: %w", err)
	}
	return nil
}
