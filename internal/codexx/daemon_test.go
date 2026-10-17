package codexx

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestStopDaemonRunsAppServerDaemonStop(t *testing.T) {
	const executable = "/opt/bin/codex"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &recordingRunner{run: func(_ context.Context, command Command) error {
		_, _ = command.Stdout.Write([]byte(`{"status":"stopped"}`))
		_, _ = command.Stderr.Write([]byte("diagnostic"))
		return nil
	}}

	if err := StopDaemon(context.Background(), runner, executable, &stdout, &stderr); err != nil {
		t.Fatalf("StopDaemon() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("StopDaemon() ran %d commands, want 1", len(runner.commands))
	}
	recorded := runner.commands[0].command
	if recorded.Path != executable {
		t.Errorf("command path = %q, want %q", recorded.Path, executable)
	}
	if want := []string{"app-server", "daemon", "stop"}; !reflect.DeepEqual(recorded.Args, want) {
		t.Errorf("command args = %#v, want %#v", recorded.Args, want)
	}
	if recorded.Stdin != nil {
		t.Error("command stdin is set, want none")
	}
	if got, want := stdout.String(), `{"status":"stopped"}`; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "diagnostic"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestStopDaemonWrapsCommandError(t *testing.T) {
	runErr := errors.New("exit status 2")
	runner := &recordingRunner{run: func(context.Context, Command) error { return runErr }}

	err := StopDaemon(context.Background(), runner, "codex", nil, nil)
	if !errors.Is(err, runErr) {
		t.Fatalf("StopDaemon() error = %v, want errors.Is(_, runErr)", err)
	}
	if !strings.Contains(err.Error(), "stop Codex app-server daemon") {
		t.Errorf("StopDaemon() error = %q, want daemon context", err)
	}
}

func TestStopDaemonReportsUnmanagedAppServer(t *testing.T) {
	const diagnostic = "Error: app server is running but is not managed by codex app-server daemon\n"
	runErr := errors.New("exit status 1")
	var stderr bytes.Buffer
	runner := &recordingRunner{run: func(_ context.Context, command Command) error {
		_, _ = command.Stderr.Write([]byte(diagnostic))
		return runErr
	}}

	err := StopDaemon(context.Background(), runner, "codex", nil, &stderr)
	if !errors.Is(err, ErrDaemonUnmanaged) {
		t.Fatalf("StopDaemon() error = %v, want errors.Is(_, ErrDaemonUnmanaged)", err)
	}
	if !errors.Is(err, runErr) {
		t.Fatalf("StopDaemon() error = %v, want errors.Is(_, runErr)", err)
	}
	if got := stderr.String(); got != diagnostic {
		t.Errorf("stderr = %q, want %q", got, diagnostic)
	}
}

func TestStopDaemonRejectsInvalidArgumentsWithoutRunningCommand(t *testing.T) {
	runner := &recordingRunner{}
	tests := []struct {
		name       string
		ctx        context.Context
		runner     Runner
		executable string
	}{
		{name: "nil context", ctx: nil, runner: runner, executable: "codex"},
		{name: "nil runner", ctx: context.Background(), runner: nil, executable: "codex"},
		{name: "empty executable", ctx: context.Background(), runner: runner, executable: " \t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := StopDaemon(tt.ctx, tt.runner, tt.executable, nil, nil); err == nil {
				t.Fatal("StopDaemon() error = nil, want validation error")
			}
		})
	}
	if len(runner.commands) != 0 {
		t.Fatalf("StopDaemon() ran %d commands, want 0", len(runner.commands))
	}
}

func TestUpdateDaemonParsesStatusAndPassesDiagnostics(t *testing.T) {
	const executable = "/opt/bin/codex"
	tests := []struct {
		name   string
		stdout string
		want   DaemonUpdate
	}{
		{
			name:   "updated and restarted",
			stdout: `{"status":"updated","managedCodexPath":"/home/u/.codex/packages/app-server-daemon/current/bin/codex","installedVersion":"0.162.1","runningVersion":"0.162.1","message":"restarted"}`,
			want:   DaemonUpdate{Status: DaemonUpdated, InstalledVersion: "0.162.1", RunningVersion: "0.162.1", Message: "restarted"},
		},
		{
			name:   "updated package while stopped",
			stdout: `{"status":"updated","installedVersion":"0.162.1","runningVersion":null,"message":"ready"}` + "\n",
			want:   DaemonUpdate{Status: DaemonUpdated, InstalledVersion: "0.162.1", Message: "ready"},
		},
		{
			name:   "already current",
			stdout: `{"status":"noUpdate","installedVersion":"0.162.1","runningVersion":"0.162.1","message":"current"}`,
			want:   DaemonUpdate{Status: DaemonNoUpdate, InstalledVersion: "0.162.1", RunningVersion: "0.162.1", Message: "current"},
		},
		{
			name:   "never started",
			stdout: `{"status":"unsupported","installedVersion":null,"runningVersion":null,"message":"select a package"}`,
			want:   DaemonUpdate{Status: DaemonUnsupported, Message: "select a package"},
		},
		{
			name:   "status on last line",
			stdout: "Installing daemon package...\n" + `{"status":"noUpdate","installedVersion":"0.162.1","runningVersion":null,"message":"ok"}` + "\n\n",
			want:   DaemonUpdate{Status: DaemonNoUpdate, InstalledVersion: "0.162.1", Message: "ok"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			runner := &recordingRunner{run: func(_ context.Context, command Command) error {
				_, _ = command.Stdout.Write([]byte(tt.stdout))
				_, _ = command.Stderr.Write([]byte("diagnostic"))
				return nil
			}}

			got, err := UpdateDaemon(context.Background(), runner, executable, &stderr)
			if err != nil {
				t.Fatalf("UpdateDaemon() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("UpdateDaemon() = %+v, want %+v", got, tt.want)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("UpdateDaemon() ran %d commands, want 1", len(runner.commands))
			}
			recorded := runner.commands[0].command
			if recorded.Path != executable {
				t.Errorf("command path = %q, want %q", recorded.Path, executable)
			}
			if want := []string{"app-server", "daemon", "update"}; !reflect.DeepEqual(recorded.Args, want) {
				t.Errorf("command args = %#v, want %#v", recorded.Args, want)
			}
			if recorded.Stdin != nil {
				t.Error("command stdin is set, want none")
			}
			if got, want := stderr.String(), "diagnostic"; got != want {
				t.Errorf("stderr = %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateDaemonRejectsUnexpectedOutput(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		wantErr string
	}{
		{name: "empty", stdout: "", wantErr: "no status"},
		{name: "whitespace", stdout: " \n\t\n", wantErr: "no status"},
		{name: "not json", stdout: "updated\n", wantErr: "parse"},
		{name: "unknown status", stdout: `{"status":"pinned","message":"x"}`, wantErr: `"pinned"`},
		{name: "missing status", stdout: `{"message":"x"}`, wantErr: `""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingRunner{run: func(_ context.Context, command Command) error {
				_, _ = command.Stdout.Write([]byte(tt.stdout))
				return nil
			}}
			_, err := UpdateDaemon(context.Background(), runner, "codex", nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("UpdateDaemon() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestUpdateDaemonWrapsCommandError(t *testing.T) {
	runErr := errors.New("exit status 1")
	runner := &recordingRunner{run: func(context.Context, Command) error { return runErr }}

	_, err := UpdateDaemon(context.Background(), runner, "codex", nil)
	if !errors.Is(err, runErr) {
		t.Fatalf("UpdateDaemon() error = %v, want errors.Is(_, runErr)", err)
	}
	if !strings.Contains(err.Error(), "update Codex app-server daemon") {
		t.Errorf("UpdateDaemon() error = %q, want daemon context", err)
	}
}

func TestUpdateDaemonRejectsInvalidArgumentsWithoutRunningCommand(t *testing.T) {
	runner := &recordingRunner{}
	tests := []struct {
		name       string
		ctx        context.Context
		runner     Runner
		executable string
	}{
		{name: "nil context", ctx: nil, runner: runner, executable: "codex"},
		{name: "nil runner", ctx: context.Background(), runner: nil, executable: "codex"},
		{name: "empty executable", ctx: context.Background(), runner: runner, executable: " \t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := UpdateDaemon(tt.ctx, tt.runner, tt.executable, nil); err == nil {
				t.Fatal("UpdateDaemon() error = nil, want validation error")
			}
		})
	}
	if len(runner.commands) != 0 {
		t.Fatalf("UpdateDaemon() ran %d commands, want 0", len(runner.commands))
	}
}
