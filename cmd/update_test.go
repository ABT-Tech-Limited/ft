package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/updater"
)

type updateRecorder struct {
	result          updater.Result
	err             error
	executablePath  string
	executableErr   error
	updateCalls     int
	executableCalls int
	options         updater.Options
	context         context.Context
}

func (r *updateRecorder) dependencies() updateDependencies {
	return updateDependencies{
		currentVersion: func() string { return "0.1.0" },
		goos:           "linux",
		goarch:         "amd64",
		executable: func() (string, error) {
			r.executableCalls++
			return r.executablePath, r.executableErr
		},
		httpClient:    http.DefaultClient,
		latestURL:     "https://api.github.com/repos/ABT-Tech-Limited/ft/releases/latest",
		privilegedRun: func(context.Context, []string, io.Reader) error { return nil },
		update: func(ctx context.Context, options updater.Options) (updater.Result, error) {
			r.updateCalls++
			r.context = ctx
			r.options = options
			return r.result, r.err
		},
	}
}

func TestUpdateCommandReportsAlreadyCurrent(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	recorder := &updateRecorder{
		executablePath: "/usr/local/bin/ft",
		result: updater.Result{
			CurrentVersion: "v0.1.0",
			LatestVersion:  "v0.1.0",
			Path:           "/usr/local/bin/ft",
		},
	}
	output, err := executeUpdate(t, ctx, recorder)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"Checking for ft updates", "v0.1.0", "already up to date"} {
		if !strings.Contains(output, want) {
			t.Errorf("output %q does not contain %q", output, want)
		}
	}
	if recorder.updateCalls != 1 || recorder.executableCalls != 1 {
		t.Errorf("calls update=%d executable=%d, want 1 each", recorder.updateCalls, recorder.executableCalls)
	}
	if recorder.context != ctx {
		t.Errorf("update context = %v, want command context", recorder.context)
	}
	if recorder.options.CurrentVersion != "0.1.0" || recorder.options.GOOS != "linux" || recorder.options.GOARCH != "amd64" {
		t.Errorf("update options = %#v", recorder.options)
	}
	if recorder.options.ExecutablePath != recorder.executablePath {
		t.Errorf("executable option = %q, want %q", recorder.options.ExecutablePath, recorder.executablePath)
	}
}

func TestUpdateCommandReportsSuccessfulReplacement(t *testing.T) {
	recorder := &updateRecorder{
		executablePath: "/usr/local/bin/ft",
		result: updater.Result{
			CurrentVersion: "v0.1.0",
			LatestVersion:  "v0.2.0",
			Path:           "/usr/local/bin/ft",
			Updated:        true,
		},
	}
	output, err := executeUpdate(t, context.Background(), recorder)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"Updated ft", "v0.1.0", "v0.2.0", "/usr/local/bin/ft"} {
		if !strings.Contains(output, want) {
			t.Errorf("output %q does not contain %q", output, want)
		}
	}
}

func TestUpdateCommandReturnsFailureWithoutSuccess(t *testing.T) {
	wantErr := errors.New("release request failed")
	recorder := &updateRecorder{executablePath: "/usr/local/bin/ft", err: wantErr}
	output, err := executeUpdate(t, context.Background(), recorder)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if strings.Contains(output, "already up to date") || strings.Contains(output, "Updated ft") {
		t.Errorf("failure output contains success text: %q", output)
	}
}

func TestUpdateCommandReportsReplacementWhenCleanupFails(t *testing.T) {
	wantErr := errors.New("removing update workspace failed")
	recorder := &updateRecorder{
		executablePath: "/usr/local/bin/ft",
		result: updater.Result{
			CurrentVersion: "v0.1.0",
			LatestVersion:  "v0.2.0",
			Path:           "/usr/local/bin/ft",
			Updated:        true,
		},
		err: wantErr,
	}

	output, err := executeUpdate(t, context.Background(), recorder)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	for _, want := range []string{"Updated ft", "v0.1.0", "v0.2.0", "/usr/local/bin/ft", "cleanup failed"} {
		if !strings.Contains(output, want) {
			t.Errorf("output %q does not contain %q", output, want)
		}
	}
}

func TestUpdateCommandStopsWhenOutputFails(t *testing.T) {
	wantErr := errors.New("output failed")
	recorder := &updateRecorder{}
	command := newUpdateCmd(recorder.dependencies())
	command.SetOut(errorWriter{err: wantErr})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs(nil)

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if recorder.updateCalls != 0 || recorder.executableCalls != 0 {
		t.Errorf("side effects update=%d executable=%d, want none", recorder.updateCalls, recorder.executableCalls)
	}
}

func TestUpdateCommandReturnsFinalOutputFailure(t *testing.T) {
	wantErr := errors.New("final output failed")
	tests := []struct {
		name   string
		result updater.Result
	}{
		{
			name: "already current",
			result: updater.Result{
				CurrentVersion: "v0.2.0",
				LatestVersion:  "v0.2.0",
			},
		},
		{
			name: "updated",
			result: updater.Result{
				CurrentVersion: "v0.1.0",
				LatestVersion:  "v0.2.0",
				Path:           "/usr/local/bin/ft",
				Updated:        true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &updateRecorder{executablePath: "/usr/local/bin/ft", result: tt.result}
			writer := &failOnWrite{failAt: 2, err: wantErr}
			command := newUpdateCmd(recorder.dependencies())
			command.SetOut(writer)
			command.SetErr(&bytes.Buffer{})
			command.SetArgs(nil)

			err := command.Execute()
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			if recorder.updateCalls != 1 {
				t.Errorf("update calls = %d, want 1", recorder.updateCalls)
			}
		})
	}
}

func TestUpdateCommandHelpAndArgumentsHaveNoSideEffects(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "help", args: []string{"--help"}},
		{name: "argument", args: []string{"unexpected"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &updateRecorder{}
			command := newUpdateCmd(recorder.dependencies())
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			command.SetArgs(tt.args)
			err := command.Execute()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Execute() error = %v, wantErr=%v", err, tt.wantErr)
			}
			if recorder.updateCalls != 0 || recorder.executableCalls != 0 {
				t.Errorf("side effects update=%d executable=%d", recorder.updateCalls, recorder.executableCalls)
			}
		})
	}
}

func TestSetVersionInfoRetainsRawVersion(t *testing.T) {
	oldBuildVersion := buildVersion
	oldDisplayVersion := rootCmd.Version
	t.Cleanup(func() {
		buildVersion = oldBuildVersion
		rootCmd.Version = oldDisplayVersion
	})

	SetVersionInfo("0.2.0", "abc123", "2026-07-21")
	if buildVersion != "0.2.0" {
		t.Errorf("buildVersion = %q, want 0.2.0", buildVersion)
	}
	if want := "0.2.0 (commit abc123, built 2026-07-21)"; rootCmd.Version != want {
		t.Errorf("root version = %q, want %q", rootCmd.Version, want)
	}
}

func executeUpdate(t *testing.T, ctx context.Context, recorder *updateRecorder) (string, error) {
	t.Helper()
	command := newUpdateCmd(recorder.dependencies())
	var output bytes.Buffer
	command.SetContext(ctx)
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(nil)
	err := command.Execute()
	return output.String(), err
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type failOnWrite struct {
	writes int
	failAt int
	err    error
}

func (w *failOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}
