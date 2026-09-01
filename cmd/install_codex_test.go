package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/codexx"
)

type codexInstallRecorder struct {
	versionBefore  string
	versionAfter   string
	detectCalls    int
	installCalls   int
	readCalls      int
	writeCalls     int
	loginCalls     int
	validateCalls  int
	validateErrors []error
	config         []byte
	writtenHost    string
	loginKey       string
	contexts       []context.Context
	installErr     error
	writeErr       error
	loginErr       error
}

func (r *codexInstallRecorder) dependencies() codexInstallDependencies {
	return codexInstallDependencies{
		isSudo:           func() bool { return false },
		resolveCurrent:   func() (string, error) { return "/usr/bin/codex", nil },
		resolveInstalled: func() (string, error) { return "/home/test/.local/bin/codex", nil },
		detectVersion: func(ctx context.Context, _ string) (string, error) {
			r.contexts = append(r.contexts, ctx)
			r.detectCalls++
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
			r.loginKey = key
			return r.loginErr
		},
		validate: func(ctx context.Context, _ string, _, _ io.Writer) error {
			r.contexts = append(r.contexts, ctx)
			r.validateCalls++
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
	recorder := &codexInstallRecorder{versionAfter: "0.151.0"}
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
	if recorder.writeCalls != 0 || recorder.loginCalls != 0 {
		t.Errorf("valid setup wrote config/login: write=%d login=%d", recorder.writeCalls, recorder.loginCalls)
	}
}

func TestInstallCodexUpdatesExistingVersionAndRepairsConfiguration(t *testing.T) {
	const secret = "sk-test-super-secret"
	recorder := &codexInstallRecorder{
		versionBefore:  "0.150.0",
		versionAfter:   "0.151.0",
		validateErrors: []error{errors.New("unauthorized")},
	}
	input := " https://gateway.example.com/v1/// \n" + secret + "\nyes\n"
	output, err := executeInstallCodex(t, context.Background(), recorder, input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"0.150.0", "0.151.0", "gpt-5.6-sol", "ultra", "1000000", "800000", "https://gateway.example.com/v1", codexx.MaskAPIKey(secret), "配置正确，可以使用"} {
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
	if recorder.writeCalls != 1 || recorder.loginCalls != 1 || recorder.validateCalls != 2 {
		t.Errorf("calls write=%d login=%d validate=%d, want 1,1,2", recorder.writeCalls, recorder.loginCalls, recorder.validateCalls)
	}
}

func TestInstallCodexPreviewShowsCompleteFinalConfiguration(t *testing.T) {
	const secret = "sk-test-super-secret"
	recorder := &codexInstallRecorder{
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
	recorder := &codexInstallRecorder{validateErrors: []error{errors.New("bad config")}}
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
	tests := []struct {
		name     string
		recorder *codexInstallRecorder
		input    string
		want     error
	}{
		{name: "installer", recorder: &codexInstallRecorder{installErr: errors.New("download failed")}, want: nil},
		{name: "login", recorder: &codexInstallRecorder{validateErrors: []error{errors.New("bad")}, loginErr: errors.New("login failed")}, input: "https://api.example.com/v1\nsk-secret-key\nyes\n", want: nil},
		{name: "post validation", recorder: &codexInstallRecorder{validateErrors: []error{errors.New("bad"), errors.New("still bad")}}, input: "https://api.example.com/v1\nsk-secret-key\nyes\n", want: nil},
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
		})
	}
}

func TestInstallCodexRefusesSudoAndHelpHasNoSideEffects(t *testing.T) {
	t.Run("sudo", func(t *testing.T) {
		recorder := &codexInstallRecorder{}
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
		recorder := &codexInstallRecorder{}
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
	recorder := &codexInstallRecorder{}
	if _, err := executeInstallCodex(t, ctx, recorder, ""); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for index, got := range recorder.contexts {
		if got != ctx {
			t.Errorf("context[%d] = %v, want command context", index, got)
		}
	}
}

func executeInstallCodex(t *testing.T, ctx context.Context, recorder *codexInstallRecorder, input string) (string, error) {
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
