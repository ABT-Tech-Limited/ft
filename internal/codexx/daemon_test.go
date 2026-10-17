package codexx

import (
	"bytes"
	"context"
	"errors"
	"os/user"
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

func TestUpdateDaemonRecoversUnmanagedAppServer(t *testing.T) {
	const diagnostic = "Error: app server is running but is not managed by codex app-server daemon\n"
	const updateOutput = `{"status":"updated","installedVersion":"0.163.0","runningVersion":"0.163.0","message":"restarted"}`
	var stderr bytes.Buffer
	var runner *recordingRunner
	runner = &recordingRunner{run: func(_ context.Context, command Command) error {
		switch command.Path + " " + strings.Join(command.Args, " ") {
		case "/opt/bin/codex app-server daemon update":
			updates := 0
			for _, recorded := range runner.commands {
				if reflect.DeepEqual(recorded.command.Args, command.Args) {
					updates++
				}
			}
			if updates == 1 {
				_, _ = command.Stderr.Write([]byte(diagnostic))
				return errors.New("exit status 1")
			}
			_, _ = command.Stdout.Write([]byte(updateOutput))
		case "ps -axo pid=,ppid=,user=,args=":
			current, currentErr := user.Current()
			if currentErr != nil {
				t.Fatalf("user.Current() error = %v", currentErr)
			}
			_, _ = command.Stdout.Write([]byte("22734 1 " + current.Username + " codex -c features.code_mode_host=true app-server --listen unix://\n"))
		case "kill -TERM 22734":
		default:
			t.Fatalf("unexpected command: %#v", command)
		}
		return nil
	}}

	got, err := UpdateDaemon(context.Background(), runner, "/opt/bin/codex", &stderr)
	if err != nil {
		t.Fatalf("UpdateDaemon() error = %v", err)
	}
	if got.Status != DaemonUpdated || got.RunningVersion != "0.163.0" {
		t.Fatalf("UpdateDaemon() = %+v, want updated 0.163.0", got)
	}
	if len(runner.commands) != 4 {
		t.Fatalf("UpdateDaemon() ran %d commands, want 4", len(runner.commands))
	}
	if !strings.Contains(stderr.String(), "unmanaged") {
		t.Errorf("stderr = %q, want unmanaged recovery diagnostic", stderr.String())
	}
	if strings.Contains(stderr.String(), diagnostic) {
		t.Errorf("stderr = %q, want recoverable daemon diagnostic hidden", stderr.String())
	}
}

func TestParseUnmanagedAppServerPIDsOnlyMatchesDirectServers(t *testing.T) {
	output := strings.Join([]string{
		"22734 1 root codex -c features.code_mode_host=true app-server --listen unix://",
		"22735 1 root codex app-server proxy",
		"22736 1 root codex app-server daemon update",
		"22737 1 other codex app-server --listen unix://",
		"22738 1 root codex app-server --listen ws://127.0.0.1:4500",
		"22739 123 root codex app-server --listen unix://",
	}, "\n")

	got := parseUnmanagedAppServerPIDs(output, "root")
	if !reflect.DeepEqual(got, []int{22734}) {
		t.Fatalf("parseUnmanagedAppServerPIDs() = %v, want [22734]", got)
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
