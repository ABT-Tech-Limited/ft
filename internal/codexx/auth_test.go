package codexx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoginPassesTrimmedAPIKeyThroughStdinOnly(t *testing.T) {
	const (
		executable = "/opt/bin/codex"
		apiKey     = "sk-secret-1234567890"
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &recordingRunner{run: func(_ context.Context, command Command) error {
		_, _ = command.Stdout.Write([]byte("logged in"))
		_, _ = command.Stderr.Write([]byte("warning"))
		return nil
	}}

	err := Login(context.Background(), runner, executable, " \t"+apiKey+"\n", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("Login() ran %d commands, want 1", len(runner.commands))
	}
	recorded := runner.commands[0]
	if got, want := recorded.command.Path, executable; got != want {
		t.Errorf("command path = %q, want %q", got, want)
	}
	if got, want := recorded.command.Args, []string{"login", "--with-api-key"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command args = %#v, want %#v", got, want)
	}
	if got, want := recorded.stdin, apiKey+"\n"; got != want {
		t.Errorf("command stdin = %q, want %q", got, want)
	}
	if got := strings.Join(append(append([]string{recorded.command.Path}, recorded.command.Args...), recorded.command.Env...), "\x00"); strings.Contains(got, apiKey) {
		t.Errorf("command path, args, or environment leaked API key: %q", got)
	}
	if got, want := stdout.String(), "logged in"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "warning"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestLoginRejectsEmptyAPIKeyWithoutRunningCommand(t *testing.T) {
	runner := &recordingRunner{}
	err := Login(context.Background(), runner, "codex", " \t\n", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "empty API key") {
		t.Fatalf("Login() error = %v, want empty API key error", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("Login() ran %d commands, want 0", len(runner.commands))
	}
}

func TestLoginDoesNotExposeAPIKeyInCommandError(t *testing.T) {
	const apiKey = "sk-secret-1234567890"
	runErr := errors.New("login process rejected " + apiKey)
	runner := &recordingRunner{run: func(context.Context, Command) error { return runErr }}

	err := Login(context.Background(), runner, "codex", apiKey, nil, nil)
	if err == nil {
		t.Fatal("Login() error = nil, want command error")
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		if strings.Contains(current.Error(), apiKey) {
			t.Fatalf("Login() error chain leaked API key: %v", current)
		}
	}
	if !errors.Is(err, runErr) {
		t.Fatalf("Login() error = %v, want errors.Is(_, runErr)", err)
	}
}

func TestValidateRunsStrictEphemeralReadOnlyCheck(t *testing.T) {
	const executable = "/opt/bin/codex"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &recordingRunner{run: func(_ context.Context, command Command) error {
		_, _ = command.Stdout.Write([]byte("OK\n"))
		_, _ = command.Stderr.Write([]byte("diagnostic"))
		return nil
	}}

	err := Validate(context.Background(), runner, executable, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("Validate() ran %d commands, want 1", len(runner.commands))
	}
	recorded := runner.commands[0]
	if got, want := recorded.command.Path, executable; got != want {
		t.Errorf("command path = %q, want %q", got, want)
	}
	if recorded.command.Dir == "" {
		t.Fatal("command working directory is empty")
	}
	wantArgs := []string{
		"--strict-config",
		"exec",
		"--skip-git-repo-check",
		"--ephemeral",
		"--sandbox", "read-only",
		"--color", "never",
		"-",
	}
	if got := recorded.command.Args; !reflect.DeepEqual(got, wantArgs) {
		t.Errorf("command args = %#v, want %#v", got, wantArgs)
	}
	if got, want := recorded.stdin, "Reply exactly OK. Do not use tools.\n"; got != want {
		t.Errorf("command stdin = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "OK\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "diagnostic"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if _, err := os.Stat(recorded.command.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("validation working directory remains after run: %v", err)
	}
}

func TestValidatePreservesContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &recordingRunner{run: func(ctx context.Context, _ Command) error { return ctx.Err() }}

	err := Validate(ctx, runner, "codex", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Validate() error = %v, want errors.Is(_, context.Canceled)", err)
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "empty", key: "", want: ""},
		{name: "short", key: "abc", want: "***"},
		{name: "eight characters", key: "12345678", want: "********"},
		{name: "long", key: "abcdEFGHijkl", want: "abcd****ijkl"},
		{name: "trim whitespace", key: " \tabcdEFGHijkl\n", want: "abcd****ijkl"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskAPIKey(tt.key); got != tt.want {
				t.Errorf("MaskAPIKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
