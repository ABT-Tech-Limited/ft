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
