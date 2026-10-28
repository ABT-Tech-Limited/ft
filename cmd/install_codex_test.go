package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/codexx"
)

type codexRecorder struct {
	versionBefore     string
	versionAfter      string
	detectCalls       int
	installCalls      int
	readCalls         int
	writeCalls        int
	loginCalls        int
	reloadDaemonCalls int
	updateDaemonCalls int
	validateCalls     int
	validateErrors    []error
	config            []byte
	writtenHost       string
	loginKey          string
	events            []string
	contexts          []context.Context
	resolveCurrentErr error
	installErr        error
	writeErr          error
	loginErr          error
	reloadDaemonErr   error
	updateDaemonErr   error
	detectErr         error
	daemonUpdate      codexx.DaemonUpdate
}

func (r *codexRecorder) dependencies() codexDependencies {
	return codexDependencies{
		isSudo: func() bool { return false },
		resolveCurrent: func() (string, error) {
			if r.resolveCurrentErr != nil {
				return "", r.resolveCurrentErr
			}
			return "/usr/bin/codex", nil
		},
		resolveInstalled: func() (string, error) { return "/home/test/.local/bin/codex", nil },
		detectVersion: func(ctx context.Context, _ string) (string, error) {
			r.contexts = append(r.contexts, ctx)
			r.detectCalls++
			if r.detectErr != nil {
				return "", r.detectErr
			}
			if r.detectCalls == 1 {
				if r.versionBefore == "" {
					return "", codexx.ErrNotFound
				}
				return r.versionBefore, nil
			}
			if r.versionAfter == "" {
				return "0.151.0", nil
			}
			return r.versionAfter, nil
		},
		install: func(ctx context.Context, _, _ io.Writer) error {
			r.contexts = append(r.contexts, ctx)
			r.installCalls++
			r.events = append(r.events, "install")
			return r.installErr
		},
		configPath:    func() (string, error) { return "/home/test/.codex/config.toml", nil },
		prepareConfig: codexx.PrepareConfig,
		readConfig: func(ctx context.Context, _ string) ([]byte, error) {
			r.contexts = append(r.contexts, ctx)
			r.readCalls++
			return append([]byte(nil), r.config...), nil
		},
		writeConfig: func(ctx context.Context, _ string, _ []byte, prepared []byte, _ []byte) (string, error) {
			r.contexts = append(r.contexts, ctx)
			r.writeCalls++
			r.events = append(r.events, "write")
			for _, line := range strings.Split(string(prepared), "\n") {
				if strings.HasPrefix(line, "base_url = ") {
					r.writtenHost = strings.Trim(strings.TrimPrefix(line, "base_url = "), `"`)
				}
			}
			return "", r.writeErr
		},
		login: func(ctx context.Context, _ string, key string, _, _ io.Writer) error {
			r.contexts = append(r.contexts, ctx)
			r.loginCalls++
			r.events = append(r.events, "login")
			r.loginKey = key
			return r.loginErr
		},
		reloadDaemon: func(ctx context.Context, _ string, _, _ io.Writer) error {
			r.contexts = append(r.contexts, ctx)
			r.reloadDaemonCalls++
			r.events = append(r.events, "reloadDaemon")
			return r.reloadDaemonErr
		},
		updateDaemon: func(ctx context.Context, _ string, _ io.Writer) (codexx.DaemonUpdate, error) {
			r.contexts = append(r.contexts, ctx)
			r.updateDaemonCalls++
			r.events = append(r.events, "updateDaemon")
			if r.updateDaemonErr != nil {
				return codexx.DaemonUpdate{}, r.updateDaemonErr
			}
			if r.daemonUpdate.Status == "" {
				return codexx.DaemonUpdate{Status: codexx.DaemonNoUpdate, InstalledVersion: "0.151.0"}, nil
			}
			return r.daemonUpdate, nil
		},
		validate: func(ctx context.Context, _ string, _, _ io.Writer) error {
			r.contexts = append(r.contexts, ctx)
			r.validateCalls++
			r.events = append(r.events, "validate")
			if len(r.validateErrors) >= r.validateCalls {
				return r.validateErrors[r.validateCalls-1]
			}
			return nil
		},
		readSecret: func(input io.Reader, output io.Writer) (string, error) {
			return readPromptLine(input, output)
		},
	}
}

func TestInstallCodexInstallsLatestAndAcceptsValidConfiguration(t *testing.T) {
	recorder := &codexRecorder{versionAfter: "0.151.0"}
	output, err := executeInstallCodex(t, context.Background(), recorder, "")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"Codex CLI is not installed", "Codex CLI 0.151.0", "配置正确，可以使用"} {
		if !strings.Contains(output, want) {
			t.Errorf("output %q does not contain %q", output, want)
		}
	}
	if recorder.installCalls != 1 || recorder.detectCalls != 2 || recorder.validateCalls != 1 {
		t.Errorf("calls install=%d detect=%d validate=%d, want 1,2,1", recorder.installCalls, recorder.detectCalls, recorder.validateCalls)
	}
	if recorder.writeCalls != 0 || recorder.loginCalls != 0 || recorder.reloadDaemonCalls != 0 {
		t.Errorf("valid setup changed state: write=%d login=%d reloadDaemon=%d", recorder.writeCalls, recorder.loginCalls, recorder.reloadDaemonCalls)
	}
	want := []string{"install", "updateDaemon", "validate"}
	if !reflect.DeepEqual(recorder.events, want) {
		t.Errorf("events = %v, want %v", recorder.events, want)
	}
	if recorder.updateDaemonCalls != 1 {
		t.Errorf("updateDaemon calls = %d, want 1", recorder.updateDaemonCalls)
	}
}

func TestInstallCodexUpdatesExistingVersionAndRepairsConfiguration(t *testing.T) {
	const secret = "sk-test-super-secret"
	recorder := &codexRecorder{
		versionBefore:  "0.150.0",
		versionAfter:   "0.151.0",
		validateErrors: []error{errors.New("unauthorized")},
	}
	input := " https://gateway.example.com/v1/// \n" + secret + "\nyes\n"
	output, err := executeInstallCodex(t, context.Background(), recorder, input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"0.150.0", "0.151.0", "gpt-6-astra", "ultra", "1000000", "800000", "https://gateway.example.com/v1", codexx.MaskAPIKey(secret), "shared Codex app-server", "配置正确，可以使用"} {
		if !strings.Contains(output, want) {
			t.Errorf("output %q does not contain %q", output, want)
		}
	}
	if strings.Contains(output, secret) {
		t.Fatalf("output leaked API key: %q", output)
	}
	if recorder.writtenHost != "https://gateway.example.com/v1" || recorder.loginKey != secret {
		t.Errorf("written host/key = %q/%q", recorder.writtenHost, recorder.loginKey)
	}
	want := []string{"install", "updateDaemon", "validate", "write", "login", "reloadDaemon", "validate"}
	if !reflect.DeepEqual(recorder.events, want) {
		t.Errorf("events = %v, want %v", recorder.events, want)
	}
}

func TestInstallCodexPreviewShowsCompleteFinalConfiguration(t *testing.T) {
	const secret = "sk-test-super-secret"
	recorder := &codexRecorder{
		config:         []byte("# keep this\n[history]\npersistence = \"save-all\"\n"),
		validateErrors: []error{errors.New("bad config")},
	}
	output, err := executeInstallCodex(t, context.Background(), recorder, "https://api.example.com/v1\n"+secret+"\nno\n")
	if !errors.Is(err, ErrCodexSetupCancelled) {
		t.Fatalf("Execute() error = %v, want cancellation", err)
	}
	for _, want := range []string{`name = "Custom"`, "# keep this", `[history]`, `persistence = "save-all"`} {
		if !strings.Contains(output, want) {
			t.Errorf("preview %q does not contain %q", output, want)
		}
	}
	if recorder.writeCalls != 0 {
		t.Fatal("cancelled preview wrote config")
	}
}

func TestInstallCodexRejectsInvalidHostThenAllowsCancellation(t *testing.T) {
	recorder := &codexRecorder{validateErrors: []error{errors.New("bad config")}}
	output, err := executeInstallCodex(t, context.Background(), recorder, "not-a-url\nhttps://api.example.com/v1\nsk-secret-key\nno\n")
	if !errors.Is(err, ErrCodexSetupCancelled) {
		t.Fatalf("Execute() error = %v, want ErrCodexSetupCancelled", err)
	}
	if !strings.Contains(output, "absolute URL") {
		t.Errorf("output %q does not explain invalid host", output)
	}
	if recorder.writeCalls != 0 || recorder.loginCalls != 0 {
		t.Errorf("cancelled setup wrote state: write=%d login=%d", recorder.writeCalls, recorder.loginCalls)
	}
}

func TestInstallCodexStopsOnFailuresWithoutReadyMessage(t *testing.T) {
	const repairInput = "https://api.example.com/v1\nsk-secret-key\nyes\n"
	tests := []struct {
		name          string
		recorder      *codexRecorder
		input         string
		wantValidates int
	}{
		{name: "installer", recorder: &codexRecorder{installErr: errors.New("download failed")}},
		{name: "daemon update", recorder: &codexRecorder{updateDaemonErr: errors.New("daemon update failed")}},
		{name: "login", recorder: &codexRecorder{validateErrors: []error{errors.New("bad")}, loginErr: errors.New("login failed")}, input: repairInput, wantValidates: 1},
		{name: "daemon reload", recorder: &codexRecorder{validateErrors: []error{errors.New("bad")}, reloadDaemonErr: errors.New("daemon reload failed")}, input: repairInput, wantValidates: 1},
		{name: "post validation", recorder: &codexRecorder{validateErrors: []error{errors.New("bad"), errors.New("still bad")}}, input: repairInput, wantValidates: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := executeInstallCodex(t, context.Background(), tt.recorder, tt.input)
			if err == nil {
				t.Fatal("Execute() error = nil")
			}
			if strings.Contains(output, "配置正确，可以使用") {
				t.Fatalf("failure output contains ready message: %q", output)
			}
			if tt.recorder.validateCalls != tt.wantValidates {
				t.Errorf("validate calls = %d, want %d", tt.recorder.validateCalls, tt.wantValidates)
			}
		})
	}
}

func TestInstallCodexExplainsDaemonReloadFailure(t *testing.T) {
	recorder := &codexRecorder{validateErrors: []error{errors.New("bad")}, reloadDaemonErr: errors.New("daemon reload failed")}
	_, err := executeInstallCodex(t, context.Background(), recorder, "https://api.example.com/v1\nsk-secret-key\nyes\n")
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	for _, want := range []string{"daemon reload failed", "ft codex reload"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestInstallCodexExplainsDaemonUpdateFailure(t *testing.T) {
	recorder := &codexRecorder{updateDaemonErr: errors.New("offline")}
	output, err := executeInstallCodex(t, context.Background(), recorder, "")
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	for _, want := range []string{"offline", "codex app-server daemon update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if !strings.Contains(output, "0.151.0 is installed") {
		t.Errorf("output %q does not report the installed CLI version", output)
	}
}

func TestInstallCodexRefusesSudoAndHelpHasNoSideEffects(t *testing.T) {
	t.Run("sudo", func(t *testing.T) {
		recorder := &codexRecorder{}
		deps := recorder.dependencies()
		deps.isSudo = func() bool { return true }
		command := newInstallCodexCmd(deps)
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		err := command.Execute()
		if err == nil || !strings.Contains(err.Error(), "sudo") {
			t.Fatalf("Execute() error = %v, want sudo guidance", err)
		}
		if recorder.installCalls != 0 || recorder.detectCalls != 0 {
			t.Fatal("sudo refusal caused side effects")
		}
	})

	t.Run("help", func(t *testing.T) {
		recorder := &codexRecorder{}
		command := newInstallCodexCmd(recorder.dependencies())
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		command.SetArgs([]string{"--help"})
		if err := command.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if recorder.installCalls != 0 || recorder.detectCalls != 0 {
			t.Fatal("help caused side effects")
		}
	})
}

func TestInstallCodexPropagatesCommandContext(t *testing.T) {
	type key string
	ctx := context.WithValue(context.Background(), key("ctx"), "value")
	recorder := &codexRecorder{}
	if _, err := executeInstallCodex(t, ctx, recorder, ""); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for index, got := range recorder.contexts {
		if got != ctx {
			t.Errorf("context[%d] = %v, want command context", index, got)
		}
	}
}

func executeInstallCodex(t *testing.T, ctx context.Context, recorder *codexRecorder, input string) (string, error) {
	t.Helper()
	command := newInstallCodexCmd(recorder.dependencies())
	command.SetContext(ctx)
	var output bytes.Buffer
	command.SetIn(strings.NewReader(input))
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.Execute()
	return output.String(), err
}
