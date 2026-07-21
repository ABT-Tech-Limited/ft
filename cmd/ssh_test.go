package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
)

type sshRecorder struct {
	statuses      map[string]dpkgx.Status
	detectErr     error
	detected      int
	sshPorts      [][]int // consumed per detectSSHPorts call; last entry repeats
	sshPortErrs   []error // parallel to sshPorts; nil entries mean success
	sshPortCalls  int
	queries       [][]string
	queryErr      error
	ufwOutput     string
	ufwErr        error
	socketErr     error // result of systemctl is-enabled ssh.socket
	outputCalls   [][]string
	fileExists    bool
	fileExistsErr error
	existsCalls   []string
	commands      []recordedInstallCommand
	runErrorAt    int
	runErr        error
}

func newSSHRecorder() *sshRecorder {
	return &sshRecorder{
		sshPorts:   [][]int{{22}},
		runErrorAt: -1,
		socketErr:  errors.New("disabled"),
	}
}

func (r *sshRecorder) dependencies() sshDependencies {
	return sshDependencies{
		detectOS: func() (osrelease.Info, error) {
			r.detected++
			if r.detectErr != nil {
				return osrelease.Info{}, r.detectErr
			}
			return osrelease.Info{ID: "ubuntu", VersionID: "24.04"}, nil
		},
		detectSSHPorts: func(ctx context.Context) ([]int, error) {
			index := r.sshPortCalls
			r.sshPortCalls++
			if index >= len(r.sshPorts) {
				index = len(r.sshPorts) - 1
			}
			var err error
			if index < len(r.sshPortErrs) {
				err = r.sshPortErrs[index]
			}
			return append([]int(nil), r.sshPorts[index]...), err
		},
		queryPackages: func(ctx context.Context, packages ...string) (map[string]dpkgx.Status, error) {
			r.queries = append(r.queries, append([]string(nil), packages...))
			if r.queryErr != nil {
				return nil, r.queryErr
			}
			result := make(map[string]dpkgx.Status, len(packages))
			for _, pkg := range packages {
				result[pkg] = r.statuses[pkg]
			}
			return result, nil
		},
		fileExists: func(path string) (bool, error) {
			r.existsCalls = append(r.existsCalls, path)
			return r.fileExists, r.fileExistsErr
		},
		output: func(ctx context.Context, argv []string) ([]byte, error) {
			r.outputCalls = append(r.outputCalls, append([]string(nil), argv...))
			if len(argv) > 0 && argv[0] == "ufw" {
				return []byte(r.ufwOutput), r.ufwErr
			}
			return nil, r.socketErr
		},
		run: func(ctx context.Context, argv []string, stdin io.Reader) error {
			var input []byte
			if stdin != nil {
				var err error
				input, err = io.ReadAll(stdin)
				if err != nil {
					return err
				}
			}
			current := len(r.commands)
			r.commands = append(r.commands, recordedInstallCommand{
				argv:  append([]string(nil), argv...),
				input: string(input),
			})
			if current == r.runErrorAt {
				return r.runErr
			}
			return nil
		},
	}
}

func executeSSH(t *testing.T, recorder *sshRecorder, args ...string) (string, error) {
	t.Helper()
	command := newSSHCmd(recorder.dependencies())
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}

func TestSSHPortShowSinglePort(t *testing.T) {
	recorder := newSSHRecorder()
	output, err := executeSSH(t, recorder, "port")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorder.detected != 1 {
		t.Errorf("detectOS called %d times, want 1", recorder.detected)
	}
	if recorder.sshPortCalls != 1 {
		t.Errorf("detectSSHPorts called %d times, want 1", recorder.sshPortCalls)
	}
	if !strings.Contains(output, "Effective SSH port(s): 22") {
		t.Errorf("output %q does not contain effective port line", output)
	}
	if len(recorder.commands) != 0 {
		t.Errorf("show path ran commands %v, want none", recorder.commands)
	}
}

func TestSSHPortShowMultiplePorts(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.sshPorts = [][]int{{22, 10222}}
	output, err := executeSSH(t, recorder, "port")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Effective SSH port(s): 22, 10222") {
		t.Errorf("output %q does not contain multi-port line", output)
	}
}

func TestSSHPortShowFailsWhenDetectionFails(t *testing.T) {
	wantErr := errors.New("sshd -T failed")
	recorder := newSSHRecorder()
	recorder.sshPortErrs = []error{wantErr}
	_, err := executeSSH(t, recorder, "port")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
}

func TestSSHPortStopsWhenOSIsUnsupported(t *testing.T) {
	wantErr := errors.New("unsupported test OS")
	recorder := newSSHRecorder()
	recorder.detectErr = wantErr
	_, err := executeSSH(t, recorder, "port")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if recorder.sshPortCalls != 0 || len(recorder.commands) != 0 {
		t.Errorf("unsupported OS caused detections=%d commands=%v", recorder.sshPortCalls, recorder.commands)
	}
}

func TestSSHPortRejectsExtraArguments(t *testing.T) {
	recorder := newSSHRecorder()
	_, err := executeSSH(t, recorder, "port", "10222", "extra")
	if err == nil {
		t.Fatal("Execute() error = nil, want argument error")
	}
	if recorder.detected != 0 || len(recorder.commands) != 0 {
		t.Errorf("extra args caused detectOS=%d commands=%v", recorder.detected, recorder.commands)
	}
}
