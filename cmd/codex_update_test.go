package cmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/codexx"
)

func TestCodexUpdateInstallsLatestAndRefreshesDaemon(t *testing.T) {
	recorder := &codexRecorder{
		versionBefore: "0.150.0",
		versionAfter:  "0.151.0",
		daemonUpdate:  codexx.DaemonUpdate{Status: codexx.DaemonUpdated, InstalledVersion: "0.151.0", RunningVersion: "0.151.0"},
	}
	output, err := executeCodexUpdate(t, context.Background(), recorder)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"Detected Codex CLI 0.150.0", "Updated Codex CLI from 0.150.0 to 0.151.0", "已更新到 0.151.0 并已重启"} {
		if !strings.Contains(output, want) {
			t.Errorf("output %q does not contain %q", output, want)
		}
	}
	want := []string{"install", "updateDaemon"}
	if !reflect.DeepEqual(recorder.events, want) {
		t.Errorf("events = %v, want %v", recorder.events, want)
	}
	if recorder.detectCalls != 2 || recorder.readCalls != 0 || recorder.validateCalls != 0 {
		t.Errorf("calls detect=%d read=%d validate=%d, want 2,0,0", recorder.detectCalls, recorder.readCalls, recorder.validateCalls)
	}
}

func TestCodexUpdateDescribesDaemonOutcome(t *testing.T) {
	tests := []struct {
		name   string
		update codexx.DaemonUpdate
		want   string
	}{
		{name: "running and current", update: codexx.DaemonUpdate{Status: codexx.DaemonNoUpdate, InstalledVersion: "0.151.0", RunningVersion: "0.151.0"}, want: "已是 0.151.0 且未重启"},
		{name: "stopped and current", update: codexx.DaemonUpdate{Status: codexx.DaemonNoUpdate, InstalledVersion: "0.151.0"}, want: "已是 0.151.0。"},
		{name: "package updated while stopped", update: codexx.DaemonUpdate{Status: codexx.DaemonUpdated, InstalledVersion: "0.151.0"}, want: "下次启动 codex 时使用新版本"},
		{name: "never started", update: codexx.DaemonUpdate{Status: codexx.DaemonUnsupported}, want: "尚未启动"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &codexRecorder{versionBefore: "0.151.0", versionAfter: "0.151.0", daemonUpdate: tt.update}
			output, err := executeCodexUpdate(t, context.Background(), recorder)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, want := range []string{"Codex CLI 0.151.0 is already the latest release", tt.want} {
				if !strings.Contains(output, want) {
					t.Errorf("output %q does not contain %q", output, want)
				}
			}
		})
	}
}

func TestCodexUpdateRequiresInstalledCodex(t *testing.T) {
	tests := []struct {
		name     string
		recorder *codexRecorder
	}{
		{name: "not on PATH", recorder: &codexRecorder{resolveCurrentErr: codexx.ErrNotFound}},
		{name: "version probe reports missing", recorder: &codexRecorder{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeCodexUpdate(t, context.Background(), tt.recorder)
			if !errors.Is(err, codexx.ErrNotFound) {
				t.Fatalf("Execute() error = %v, want ErrNotFound", err)
			}
			if !strings.Contains(err.Error(), "ft install codex") {
				t.Errorf("error %q does not point to ft install codex", err)
			}
			if len(tt.recorder.events) != 0 {
				t.Errorf("missing Codex caused side effects: %v", tt.recorder.events)
			}
		})
	}
}

func TestCodexUpdateStopsOnFailures(t *testing.T) {
	tests := []struct {
		name       string
		recorder   *codexRecorder
		wantEvents []string
		wantInErr  []string
	}{
		{name: "version probe", recorder: &codexRecorder{detectErr: errors.New("permission denied")}, wantInErr: []string{"permission denied"}},
		{name: "installer", recorder: &codexRecorder{versionBefore: "0.150.0", installErr: errors.New("download failed")}, wantEvents: []string{"install"}, wantInErr: []string{"download failed"}},
		{name: "daemon update", recorder: &codexRecorder{versionBefore: "0.150.0", updateDaemonErr: errors.New("offline")}, wantEvents: []string{"install", "updateDaemon"}, wantInErr: []string{"offline", "codex app-server daemon update"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeCodexUpdate(t, context.Background(), tt.recorder)
			if err == nil {
				t.Fatal("Execute() error = nil")
			}
			for _, want := range tt.wantInErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if !reflect.DeepEqual(tt.recorder.events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", tt.recorder.events, tt.wantEvents)
			}
		})
	}
}

func TestCodexUpdateRefusesSudoAndHelpHasNoSideEffects(t *testing.T) {
	t.Run("sudo", func(t *testing.T) {
		recorder := &codexRecorder{versionBefore: "0.150.0"}
		deps := recorder.dependencies()
		deps.isSudo = func() bool { return true }
		command := newCodexCmd(deps)
		command.SetArgs([]string{"update"})
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		err := command.Execute()
		if err == nil || !strings.Contains(err.Error(), "sudo") {
			t.Fatalf("Execute() error = %v, want sudo guidance", err)
		}
		if recorder.detectCalls != 0 || len(recorder.events) != 0 {
			t.Fatal("sudo refusal caused side effects")
		}
	})

	t.Run("help", func(t *testing.T) {
		recorder := &codexRecorder{versionBefore: "0.150.0"}
		command := newCodexCmd(recorder.dependencies())
		command.SetArgs([]string{"update", "--help"})
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		if err := command.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if recorder.detectCalls != 0 || len(recorder.events) != 0 {
			t.Fatal("help caused side effects")
		}
	})
}

func TestCodexUpdatePropagatesCommandContext(t *testing.T) {
	type key string
	ctx := context.WithValue(context.Background(), key("ctx"), "value")
	recorder := &codexRecorder{versionBefore: "0.150.0"}
	if _, err := executeCodexUpdate(t, ctx, recorder); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recorder.contexts) == 0 {
		t.Fatal("no dependency received a context")
	}
	for index, got := range recorder.contexts {
		if got != ctx {
			t.Errorf("context[%d] = %v, want command context", index, got)
		}
	}
}

func executeCodexUpdate(t *testing.T, ctx context.Context, recorder *codexRecorder) (string, error) {
	t.Helper()
	command := newCodexCmd(recorder.dependencies())
	command.SetArgs([]string{"update"})
	command.SetContext(ctx)
	var output bytes.Buffer
	command.SetIn(strings.NewReader(""))
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.Execute()
	return output.String(), err
}
