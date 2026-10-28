package codexx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReloadDaemonLifecycle(t *testing.T) {
	unmanaged := "Error: " + unmanagedDaemonDiagnostic + "\n"
	runErr := errors.New("exit status 1")
	for _, initialStatus := range []string{"stopped", "notRunning", "unmanaged"} {
		t.Run(initialStatus, func(t *testing.T) {
			var events []string
			var stdout, stderr bytes.Buffer
			stops := 0
			runner := &recordingRunner{run: func(_ context.Context, c Command) error {
				if c.Path != "codex" {
					t.Fatalf("unexpected executable: %s", c.Path)
				}
				action := strings.Join(c.Args, " ")
				events = append(events, action)
				switch action {
				case "app-server daemon stop":
					stops++
					if initialStatus == "unmanaged" && stops == 1 {
						io.WriteString(c.Stderr, unmanaged)
						return runErr
					}
					status := initialStatus
					if status == "unmanaged" {
						status = "notRunning"
					}
					io.WriteString(c.Stdout, `{"status":"`+status+`"}`)
				case "app-server daemon start":
					io.WriteString(c.Stdout, `{"status":"started","backend":"pid","pid":1234}`)
				case "app-server daemon version":
					io.WriteString(c.Stdout, `{"status":"running","backend":"pid","appServerVersion":"0.165.0"}`)
				default:
					t.Fatalf("unexpected command: %s", action)
				}
				return nil
			}}
			recover := func(ctx context.Context, out io.Writer) error {
				events = append(events, "recover")
				return nil
			}
			if err := reloadDaemon(context.Background(), runner, "codex", &stdout, &stderr, recover); err != nil {
				t.Fatal(err)
			}
			want := []string{"app-server daemon stop", "app-server daemon start", "app-server daemon version"}
			if initialStatus == "unmanaged" {
				want = append([]string{"app-server daemon stop", "recover"}, want...)
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
			if strings.Contains(stderr.String(), "Error:") {
				t.Fatalf("recoverable error leaked: %s", stderr.String())
			}
			if !strings.Contains(stdout.String(), "0.165.0") {
				t.Fatal("missing running version")
			}
		})
	}
}

func TestReloadDaemonFailures(t *testing.T) {
	commandErr := errors.New("command failed")
	recoveryErr := errors.New("cannot verify socket owner")
	for _, tt := range []struct {
		name          string
		stopOutput    string
		stopErr       error
		unmanaged     bool
		recoveryErr   error
		respawn       bool
		startOutput   string
		startErr      error
		versionOutput string
		versionErr    error
		wantIs        error
		wantCalls     int
		wantRecover   int
	}{
		{name: "stop error", stopErr: commandErr, wantIs: commandErr, wantCalls: 1},
		{name: "malformed stop", stopOutput: "not JSON", wantCalls: 1},
		{name: "unknown stop status", stopOutput: `{"status":"running"}`, wantCalls: 1},
		{name: "recovery refusal", unmanaged: true, recoveryErr: recoveryErr, wantIs: recoveryErr, wantCalls: 1, wantRecover: 1},
		{name: "recovery cancellation", unmanaged: true, recoveryErr: context.Canceled, wantIs: context.Canceled, wantCalls: 1, wantRecover: 1},
		{name: "recovery timeout", unmanaged: true, recoveryErr: context.DeadlineExceeded, wantIs: context.DeadlineExceeded, wantCalls: 1, wantRecover: 1},
		{name: "respawn during stop", unmanaged: true, respawn: true, wantCalls: 2, wantRecover: 1},
		{name: "start error", startErr: commandErr, wantIs: commandErr, wantCalls: 2},
		{name: "malformed start", startOutput: "not JSON", wantCalls: 2},
		{name: "racing launch", startOutput: `{"status":"alreadyRunning","backend":"pid"}`, wantCalls: 2},
		{name: "unmanaged start", startOutput: `{"status":"started"}`, wantCalls: 2},
		{name: "version error", versionErr: commandErr, wantIs: commandErr, wantCalls: 3},
		{name: "malformed version", versionOutput: "not JSON", wantCalls: 3},
		{name: "unmanaged version", versionOutput: `{"status":"running","appServerVersion":"0.165.0"}`, wantCalls: 3},
		{name: "missing version", versionOutput: `{"status":"running","backend":"pid"}`, wantCalls: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stops, recoveries := 0, 0
			runner := &recordingRunner{run: func(_ context.Context, c Command) error {
				var output string
				var err error
				switch c.Args[len(c.Args)-1] {
				case "stop":
					stops++
					if tt.unmanaged && (stops == 1 || tt.respawn) {
						io.WriteString(c.Stderr, "Error: "+unmanagedDaemonDiagnostic)
						return commandErr
					}
					output, err = tt.stopOutput, tt.stopErr
					if output == "" {
						output = `{"status":"stopped"}`
					}
				case "start":
					output, err = tt.startOutput, tt.startErr
					if output == "" {
						output = `{"status":"started","backend":"pid"}`
					}
				case "version":
					output, err = tt.versionOutput, tt.versionErr
					if output == "" {
						output = `{"status":"running","backend":"pid","appServerVersion":"0.165.0"}`
					}
				default:
					t.Fatalf("unexpected command: %+v", c)
				}
				io.WriteString(c.Stdout, output)
				return err
			}}
			err := reloadDaemon(context.Background(), runner, "codex", nil, nil, func(context.Context, io.Writer) error { recoveries++; return tt.recoveryErr })
			if err == nil {
				t.Fatal("expected failure")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("error = %v, want %v", err, tt.wantIs)
			}
			if len(runner.commands) != tt.wantCalls || recoveries != tt.wantRecover {
				t.Fatalf("commands=%d recoveries=%d, want %d/%d", len(runner.commands), recoveries, tt.wantCalls, tt.wantRecover)
			}
		})
	}
}

func TestReloadDaemonInvalidArguments(t *testing.T) {
	runner := &recordingRunner{}
	for _, tt := range []struct {
		ctx        context.Context
		runner     Runner
		executable string
	}{
		{nil, runner, "codex"}, {context.Background(), nil, "codex"}, {context.Background(), runner, " "},
	} {
		if err := ReloadDaemon(tt.ctx, tt.runner, tt.executable, nil, nil); err == nil {
			t.Fatal("expected argument validation failure")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ReloadDaemon(ctx, runner, "codex", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("invalid input executed commands")
	}
}
