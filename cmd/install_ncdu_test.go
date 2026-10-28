package cmd

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
)

func TestInstallNcduPackageStates(t *testing.T) {
	installCommands := []recordedInstallCommand{
		{argv: []string{"apt-get", "update"}},
		{argv: []string{
			"apt-get", "install", "-y",
			"-o", "Dpkg::Options::=--force-confdef",
			"-o", "Dpkg::Options::=--force-confold",
			"ncdu",
		}},
	}
	tests := []struct {
		name         string
		status       dpkgx.Status
		wantCommands []recordedInstallCommand
		wantMessage  string
	}{
		{name: "absent", status: dpkgx.Absent, wantCommands: installCommands, wantMessage: "ncdu installed successfully."},
		{name: "broken", status: dpkgx.Broken, wantCommands: installCommands, wantMessage: "ncdu installed successfully."},
		{name: "installed", status: dpkgx.Installed, wantMessage: "ncdu is ready."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newInstallRecorder(map[string]dpkgx.Status{"ncdu": tt.status})
			output, err := executeInstall(t, recorder, "ncdu")
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if recorder.detected != 1 {
				t.Errorf("detectOS called %d times, want 1", recorder.detected)
			}
			if want := [][]string{{"ncdu"}}; !reflect.DeepEqual(recorder.queries, want) {
				t.Errorf("package queries = %v, want %v", recorder.queries, want)
			}
			if !reflect.DeepEqual(recorder.commands, tt.wantCommands) {
				t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, tt.wantCommands)
			}
			if !strings.Contains(output, tt.wantMessage) {
				t.Errorf("output %q does not contain %q", output, tt.wantMessage)
			}
			if tt.status == dpkgx.Installed {
				if !strings.Contains(output, "ncdu is already installed; skipping package setup.") {
					t.Errorf("already-installed output lacks skip message: %q", output)
				}
				if strings.Contains(output, "installed successfully") {
					t.Errorf("already-installed output contains fresh-install message: %q", output)
				}
			}
			if recorder.sshPortCalls != 0 || recorder.dockerGroupCalls != 0 {
				t.Errorf("ncdu caused SSH detection=%d or Docker group resolution=%d", recorder.sshPortCalls, recorder.dockerGroupCalls)
			}
		})
	}
}

func TestInstallNcduStopsOnFailure(t *testing.T) {
	wantErr := errors.New("test failure")
	tests := []struct {
		name         string
		configure    func(*installRecorder)
		wantQueries  int
		wantCommands int
		wantContext  string
	}{
		{
			name:      "unsupported OS",
			configure: func(r *installRecorder) { r.detectErr = wantErr },
		},
		{
			name:        "package query",
			configure:   func(r *installRecorder) { r.queryErr = wantErr },
			wantQueries: 1,
			wantContext: "checking ncdu package status failed",
		},
		{
			name:         "apt update",
			configure:    func(r *installRecorder) { r.runErrorAt, r.runErr = 0, wantErr },
			wantQueries:  1,
			wantCommands: 1,
			wantContext:  "updating package index failed",
		},
		{
			name:         "apt install",
			configure:    func(r *installRecorder) { r.runErrorAt, r.runErr = 1, wantErr },
			wantQueries:  1,
			wantCommands: 2,
			wantContext:  "installing ncdu failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newInstallRecorder(nil)
			tt.configure(recorder)
			output, err := executeInstall(t, recorder, "ncdu")
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			if !strings.Contains(err.Error(), tt.wantContext) {
				t.Errorf("Execute() error = %q, want %q", err, tt.wantContext)
			}
			if len(recorder.queries) != tt.wantQueries || len(recorder.commands) != tt.wantCommands {
				t.Errorf("queries=%d commands=%d, want queries=%d commands=%d", len(recorder.queries), len(recorder.commands), tt.wantQueries, tt.wantCommands)
			}
			if strings.Contains(output, "is ready") || strings.Contains(output, "installed successfully") {
				t.Errorf("failure output contains success message: %q", output)
			}
		})
	}
}
