package codexx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrDaemonUnmanaged reports that an app-server owns the control socket but
// was not started by codex app-server daemon, so the CLI must not stop it.
var ErrDaemonUnmanaged = errors.New("Codex app-server is running but is not managed by codex app-server daemon")

const unmanagedDaemonDiagnostic = "app server is running but is not managed by codex app-server daemon"

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
	var stdoutDiagnostics strings.Builder
	var stderrDiagnostics strings.Builder
	err := runner.Run(ctx, Command{
		Path:   executable,
		Args:   []string{"app-server", "daemon", "stop"},
		Stdout: captureWriter(stdout, &stdoutDiagnostics),
		Stderr: captureWriter(stderr, &stderrDiagnostics),
	})
	if err != nil {
		diagnostics := stdoutDiagnostics.String() + stderrDiagnostics.String()
		if strings.Contains(strings.ToLower(diagnostics), unmanagedDaemonDiagnostic) {
			return fmt.Errorf("stop Codex app-server daemon: %w: %w", ErrDaemonUnmanaged, err)
		}
		return fmt.Errorf("stop Codex app-server daemon: %w", err)
	}
	return nil
}

func captureWriter(destination io.Writer, capture *strings.Builder) io.Writer {
	if destination == nil {
		return capture
	}
	return io.MultiWriter(destination, capture)
}

// DaemonUpdateStatus reports what "codex app-server daemon update" did.
type DaemonUpdateStatus string

const (
	// DaemonUpdated reports that the daemon package was installed or refreshed.
	DaemonUpdated DaemonUpdateStatus = "updated"
	// DaemonNoUpdate reports that the daemon package is already current.
	DaemonNoUpdate DaemonUpdateStatus = "noUpdate"
	// DaemonUnsupported reports that no managed daemon package is selected yet,
	// which happens before the first codex launch in a CODEX_HOME.
	DaemonUnsupported DaemonUpdateStatus = "unsupported"
)

// DaemonUpdate holds the result of a daemon package update.
type DaemonUpdate struct {
	Status           DaemonUpdateStatus
	InstalledVersion string
	RunningVersion   string
	Message          string
}

type daemonUpdateOutput struct {
	Status           string `json:"status"`
	InstalledVersion string `json:"installedVersion"`
	RunningVersion   string `json:"runningVersion"`
	Message          string `json:"message"`
}

// UpdateDaemon brings the shared app-server daemon package to the latest
// release. The daemon keeps its own package copy, which the official installer
// and a plain daemon restart do not refresh. Command diagnostics are passed to
// stderr; the single JSON result line is returned as a parsed DaemonUpdate.
func UpdateDaemon(ctx context.Context, runner Runner, executable string, stderr io.Writer) (DaemonUpdate, error) {
	if ctx == nil {
		return DaemonUpdate{}, fmt.Errorf("nil daemon update context")
	}
	if runner == nil {
		return DaemonUpdate{}, fmt.Errorf("nil command runner")
	}
	if strings.TrimSpace(executable) == "" {
		return DaemonUpdate{}, fmt.Errorf("empty Codex executable")
	}

	var output strings.Builder
	err := runner.Run(ctx, Command{
		Path:   executable,
		Args:   []string{"app-server", "daemon", "update"},
		Stdout: &output,
		Stderr: stderr,
	})
	if err != nil {
		return DaemonUpdate{}, fmt.Errorf("update Codex app-server daemon: %w", err)
	}
	return parseDaemonUpdate(output.String())
}

func parseDaemonUpdate(output string) (DaemonUpdate, error) {
	lines := strings.Split(output, "\n")
	lastLine := ""
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			lastLine = line
		}
	}
	if lastLine == "" {
		return DaemonUpdate{}, fmt.Errorf("Codex app-server daemon update reported no status")
	}
	var parsed daemonUpdateOutput
	if err := json.Unmarshal([]byte(lastLine), &parsed); err != nil {
		return DaemonUpdate{}, fmt.Errorf("parse Codex app-server daemon update output: %w", err)
	}
	status := DaemonUpdateStatus(parsed.Status)
	switch status {
	case DaemonUpdated, DaemonNoUpdate, DaemonUnsupported:
	default:
		return DaemonUpdate{}, fmt.Errorf("Codex app-server daemon update reported unexpected status %q", parsed.Status)
	}
	return DaemonUpdate{
		Status:           status,
		InstalledVersion: parsed.InstalledVersion,
		RunningVersion:   parsed.RunningVersion,
		Message:          parsed.Message,
	}, nil
}
