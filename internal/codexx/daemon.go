package codexx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/user"
	"strconv"
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

func recoverUnmanagedAppServer(ctx context.Context, runner Runner, stderr io.Writer) error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current user: %w", err)
	}

	var processes strings.Builder
	if err := runner.Run(ctx, Command{
		Path:   "ps",
		Args:   []string{"-axo", "pid=,ppid=,user=,args="},
		Stdout: &processes,
	}); err != nil {
		return fmt.Errorf("list current user's app-server processes: %w", err)
	}
	pids := parseUnmanagedAppServerPIDs(processes.String(), currentUser.Username)
	if len(pids) == 0 {
		return fmt.Errorf("could not identify an orphaned unmanaged app-server process owned by %q", currentUser.Username)
	}
	if len(pids) > 1 {
		return fmt.Errorf("found %d orphaned unmanaged app-server processes owned by %q; refusing to stop them automatically", len(pids), currentUser.Username)
	}

	pid := strconv.Itoa(pids[0])
	if err := runner.Run(ctx, Command{
		Path: "kill",
		Args: []string{"-TERM", pid},
	}); err != nil {
		return fmt.Errorf("stop unmanaged app-server process %s: %w", pid, err)
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "Detected unmanaged Codex app-server process %s; sent SIGTERM before retrying daemon update.\n", pid)
	}
	return nil
}

func parseUnmanagedAppServerPIDs(output, currentUser string) []int {
	var pids []int
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != currentUser || fields[1] != "1" {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || !isDirectAppServerCommand(strings.Join(fields[3:], " ")) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func isDirectAppServerCommand(command string) bool {
	fields := strings.Fields(command)
	for index, field := range fields {
		if field != "app-server" || index+2 >= len(fields) {
			continue
		}
		if fields[index+1] == "proxy" || fields[index+1] == "daemon" {
			return false
		}
		return fields[index+1] == "--listen" && strings.HasPrefix(fields[index+2], "unix://")
	}
	return false
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
	var diagnostics strings.Builder
	err := runner.Run(ctx, Command{
		Path:   executable,
		Args:   []string{"app-server", "daemon", "update"},
		Stdout: &output,
		Stderr: &diagnostics,
	})
	if err != nil && strings.Contains(strings.ToLower(diagnostics.String()), unmanagedDaemonDiagnostic) {
		if stderr != nil {
			fmt.Fprintln(stderr, "The shared app-server is unmanaged; locating and stopping the direct app-server process before retrying...")
		}
		if recoverErr := recoverUnmanagedAppServer(ctx, runner, stderr); recoverErr != nil {
			return DaemonUpdate{}, fmt.Errorf("recover unmanaged Codex app-server: %w", recoverErr)
		}
		output.Reset()
		err = runner.Run(ctx, Command{
			Path:   executable,
			Args:   []string{"app-server", "daemon", "update"},
			Stdout: &output,
			Stderr: stderr,
		})
	} else if stderr != nil && diagnostics.Len() > 0 {
		_, _ = io.WriteString(stderr, diagnostics.String())
	}
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
