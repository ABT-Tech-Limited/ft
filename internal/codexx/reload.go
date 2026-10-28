package codexx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type recoverServer func(context.Context, io.Writer) error

type daemonLifecycle struct {
	Status           string `json:"status"`
	Backend          string `json:"backend"`
	AppServerVersion string `json:"appServerVersion"`
}

// ReloadDaemon replaces the server for the current CODEX_HOME with a managed
// daemon and verifies readiness. It does not change credentials or run a model.
func ReloadDaemon(ctx context.Context, runner Runner, executable string, stdout, stderr io.Writer) error {
	if err := validateDaemonArguments(ctx, runner, executable); err != nil {
		return err
	}
	socket, err := controlSocketPath()
	if err != nil {
		return err
	}
	if err := checkReloadOrigin(ctx, socket); err != nil {
		return err
	}
	return reloadDaemon(ctx, runner, executable, stdout, stderr, recoverUnmanagedAppServer)
}

func reloadDaemon(ctx context.Context, runner Runner, executable string, stdout, stderr io.Writer, recover recoverServer) error {
	if err := validateDaemonArguments(ctx, runner, executable); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := stopDaemon(ctx, runner, executable, stdout, stderr, recover); err != nil {
		return err
	}
	started, err := runDaemonLifecycle(ctx, runner, executable, "start", stdout, stderr)
	if err != nil {
		return err
	}
	if started.Status != "started" || started.Backend == "" {
		return fmt.Errorf("Codex daemon did not start a fresh managed server (status=%q, backend=%q); another client may have started a server; disconnect it before retrying", started.Status, started.Backend)
	}
	active, err := runDaemonLifecycle(ctx, runner, executable, "version", stdout, stderr)
	if err != nil {
		return fmt.Errorf("verify reloaded Codex daemon: %w", err)
	}
	if active.Status != "running" || active.Backend == "" || active.AppServerVersion == "" {
		return fmt.Errorf("reloaded Codex daemon is not confirmed running and managed (status=%q, backend=%q, version=%q)", active.Status, active.Backend, active.AppServerVersion)
	}
	return nil
}

func stopDaemon(ctx context.Context, runner Runner, executable string, stdout, stderr io.Writer, recover recoverServer) error {
	if err := validateDaemonArguments(ctx, runner, executable); err != nil {
		return err
	}
	// Buffer the first attempt so a recoverable diagnostic is not printed as a
	// fatal Error before recovery has even been attempted.
	var output, diagnostics strings.Builder
	err := runner.Run(ctx, Command{Path: executable, Args: []string{"app-server", "daemon", "stop"}, Stdout: &output, Stderr: &diagnostics})
	if err != nil && ctx.Err() == nil && strings.Contains(strings.ToLower(output.String()+diagnostics.String()), unmanagedDaemonDiagnostic) {
		if stderr != nil {
			fmt.Fprintln(stderr, "The shared Codex app-server is unmanaged; verifying the control socket owner before sending SIGTERM...")
		}
		if recoveryErr := recover(ctx, stderr); recoveryErr != nil {
			return fmt.Errorf("stop Codex app-server daemon: %w: %w: %w", ErrDaemonUnmanaged, err, recoveryErr)
		}
		stopped, retryErr := runDaemonLifecycle(ctx, runner, executable, "stop", stdout, stderr)
		if retryErr != nil {
			return fmt.Errorf("verify stop after unmanaged server exit; if another client respawned it, disconnect that client before retrying: %w", retryErr)
		}
		return checkStopped(stopped)
	}
	forwardDaemonOutput(stderr, diagnostics.String())
	if err != nil {
		return fmt.Errorf("stop Codex app-server daemon: %w", err)
	}
	stopped, err := parseDaemonLifecycle(output.String())
	if err != nil {
		return err
	}
	if err := checkStopped(stopped); err != nil {
		return err
	}
	forwardDaemonOutput(stdout, output.String())
	return nil
}

func checkStopped(result daemonLifecycle) error {
	if result.Status != "stopped" && result.Status != "notRunning" {
		return fmt.Errorf("Codex daemon stop reported unexpected status %q", result.Status)
	}
	return nil
}

func runDaemonLifecycle(ctx context.Context, runner Runner, executable, action string, stdout, stderr io.Writer) (daemonLifecycle, error) {
	if err := ctx.Err(); err != nil {
		return daemonLifecycle{}, err
	}
	var output, diagnostics strings.Builder
	err := runner.Run(ctx, Command{Path: executable, Args: []string{"app-server", "daemon", action}, Stdout: &output, Stderr: &diagnostics})
	forwardDaemonOutput(stderr, diagnostics.String())
	if err != nil {
		return daemonLifecycle{}, fmt.Errorf("%s Codex app-server daemon: %w", action, err)
	}
	result, err := parseDaemonLifecycle(output.String())
	if err != nil {
		return result, fmt.Errorf("%s Codex app-server daemon: %w", action, err)
	}
	forwardDaemonOutput(stdout, output.String())
	return result, nil
}

func parseDaemonLifecycle(output string) (daemonLifecycle, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var result daemonLifecycle
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
		return result, fmt.Errorf("parse Codex daemon status: %w", err)
	}
	return result, nil
}

func forwardDaemonOutput(destination io.Writer, text string) {
	if destination != nil {
		// Match streaming command diagnostics: a broken display must not undo a
		// successfully completed lifecycle operation.
		_, _ = io.WriteString(destination, text)
	}
}

func validateDaemonArguments(ctx context.Context, runner Runner, executable string) error {
	if ctx == nil {
		return fmt.Errorf("nil daemon context")
	}
	if runner == nil {
		return fmt.Errorf("nil command runner")
	}
	if strings.TrimSpace(executable) == "" {
		return fmt.Errorf("empty Codex executable")
	}
	return ctx.Err()
}

func recoverUnmanagedAppServer(ctx context.Context, stderr io.Writer) error {
	socket, err := controlSocketPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return terminateSocketServer(ctx, socket, stderr)
}

func controlSocketPath() (string, error) {
	configPath, err := ConfigPath(os.Getenv("CODEX_HOME"), os.Getenv("HOME"))
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), "app-server-control", "app-server-control.sock"), nil
}
