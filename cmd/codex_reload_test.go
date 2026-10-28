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

func TestCodexReloadOnlyReloadsExistingConfiguration(t *testing.T) {
	recorder := &codexRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newCodexCmd(recorder.dependencies())
	command.SetArgs([]string{"reload"})
	command.SetContext(ctx)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !reflect.DeepEqual(recorder.events, []string{"reloadDaemon"}) {
		t.Fatalf("events = %v, want only reloadDaemon", recorder.events)
	}
	if recorder.readCalls != 0 || recorder.detectCalls != 0 || recorder.installCalls != 0 || recorder.validateCalls != 0 {
		t.Fatal("reload must not read/write credentials, install Codex or make a model request")
	}
	if !strings.Contains(output.String(), "已重载") || !strings.Contains(output.String(), "interrupt") {
		t.Fatalf("output must warn about interruption and report reload: %s", output.String())
	}
	for _, received := range recorder.contexts {
		if received != ctx {
			t.Fatal("reload must propagate command context")
		}
	}
}

func TestCodexReloadGuardsAndFailures(t *testing.T) {
	reloadErr := errors.New("server did not exit")
	for _, tt := range []struct {
		name       string
		args       []string
		sudo       bool
		resolveErr error
		reloadErr  error
		wantErr    string
		wantEvents []string
	}{
		{name: "help", args: []string{"reload", "--help"}},
		{name: "arguments", args: []string{"reload", "unexpected"}, wantErr: "unknown"},
		{name: "sudo", args: []string{"reload"}, sudo: true, wantErr: "sudo"},
		{name: "not installed", args: []string{"reload"}, resolveErr: codexx.ErrNotFound, wantErr: "ft install codex"},
		{name: "failure", args: []string{"reload"}, reloadErr: reloadErr, wantErr: "server did not exit", wantEvents: []string{"reloadDaemon"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &codexRecorder{resolveCurrentErr: tt.resolveErr, reloadDaemonErr: tt.reloadErr}
			deps := recorder.dependencies()
			deps.isSudo = func() bool { return tt.sudo }
			command := newCodexCmd(deps)
			command.SetArgs(tt.args)
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			err := command.Execute()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Execute() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Execute() error = %v, want %q", err, tt.wantErr)
			}
			if tt.reloadErr != nil && !errors.Is(err, tt.reloadErr) {
				t.Fatal("lost reload error")
			}
			if !reflect.DeepEqual(recorder.events, tt.wantEvents) {
				t.Fatalf("events = %v, want %v", recorder.events, tt.wantEvents)
			}
			if strings.Contains(output.String(), "已重载") {
				t.Fatal("unexpected success message")
			}
		})
	}
}
