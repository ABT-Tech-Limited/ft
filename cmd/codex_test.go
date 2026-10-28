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

func TestCodexConfigWritesCredentialsReloadsDaemonAndValidates(t *testing.T) {
	const secret = "sk-test-super-secret"
	recorder := &codexRecorder{config: []byte("# keep this\n[history]\npersistence = \"save-all\"\n")}
	input := " https://gateway.example.com/v1/// \n" + secret + "\nyes\n"
	output, err := executeCodexConfig(t, context.Background(), recorder, input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"gpt-6-astra", "https://gateway.example.com/v1", "# keep this", codexx.MaskAPIKey(secret), "shared Codex app-server", "配置正确，可以使用"} {
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
	want := []string{"write", "login", "reloadDaemon", "validate"}
	if !reflect.DeepEqual(recorder.events, want) {
		t.Errorf("events = %v, want %v", recorder.events, want)
	}
	if recorder.installCalls != 0 || recorder.detectCalls != 0 {
		t.Errorf("config command touched installation: install=%d detect=%d", recorder.installCalls, recorder.detectCalls)
	}
}

func TestCodexConfigCancellationWritesNothing(t *testing.T) {
	recorder := &codexRecorder{}
	output, err := executeCodexConfig(t, context.Background(), recorder, "not-a-url\nhttps://api.example.com/v1\nsk-secret-key\nno\n")
	if !errors.Is(err, ErrCodexSetupCancelled) {
		t.Fatalf("Execute() error = %v, want ErrCodexSetupCancelled", err)
	}
	if !strings.Contains(output, "absolute URL") {
		t.Errorf("output %q does not explain invalid host", output)
	}
	if len(recorder.events) != 0 {
		t.Errorf("cancelled setup changed state: %v", recorder.events)
	}
}

func TestCodexConfigRequiresInstalledCodex(t *testing.T) {
	recorder := &codexRecorder{resolveCurrentErr: codexx.ErrNotFound}
	_, err := executeCodexConfig(t, context.Background(), recorder, "https://api.example.com/v1\nsk-secret-key\nyes\n")
	if !errors.Is(err, codexx.ErrNotFound) {
		t.Fatalf("Execute() error = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "ft install codex") {
		t.Errorf("error %q does not point to ft install codex", err)
	}
	if recorder.readCalls != 0 || len(recorder.events) != 0 {
		t.Errorf("missing Codex caused side effects: reads=%d events=%v", recorder.readCalls, recorder.events)
	}
}

func TestCodexConfigStopsOnFailuresWithoutReadyMessage(t *testing.T) {
	const input = "https://api.example.com/v1\nsk-secret-key\nyes\n"
	tests := []struct {
		name       string
		recorder   *codexRecorder
		wantEvents []string
		wantInErr  string
	}{
		{name: "write", recorder: &codexRecorder{writeErr: errors.New("disk full")}, wantEvents: []string{"write"}, wantInErr: "disk full"},
		{name: "login", recorder: &codexRecorder{loginErr: errors.New("login failed")}, wantEvents: []string{"write", "login"}, wantInErr: "login failed"},
		{name: "daemon reload", recorder: &codexRecorder{reloadDaemonErr: errors.New("daemon reload failed")}, wantEvents: []string{"write", "login", "reloadDaemon"}, wantInErr: "ft codex reload"},
		{name: "validation", recorder: &codexRecorder{validateErrors: []error{errors.New("still bad")}}, wantEvents: []string{"write", "login", "reloadDaemon", "validate"}, wantInErr: "still bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := executeCodexConfig(t, context.Background(), tt.recorder, input)
			if err == nil {
				t.Fatal("Execute() error = nil")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantInErr)
			}
			if strings.Contains(output, "配置正确，可以使用") {
				t.Fatalf("failure output contains ready message: %q", output)
			}
			if !reflect.DeepEqual(tt.recorder.events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", tt.recorder.events, tt.wantEvents)
			}
		})
	}
}

func TestCodexConfigDoesNotClaimSuccessWhenUnmanagedRecoveryFails(t *testing.T) {
	recorder := &codexRecorder{reloadDaemonErr: codexx.ErrDaemonUnmanaged}
	output, err := executeCodexConfig(t, context.Background(), recorder, "https://api.example.com/v1\nsk-secret-key\nyes\n")
	if !errors.Is(err, codexx.ErrDaemonUnmanaged) {
		t.Fatalf("Execute() error = %v, want unmanaged recovery failure", err)
	}
	if recorder.validateCalls != 0 {
		t.Fatal("must not validate independently and hide a failed reload")
	}
	if !strings.Contains(err.Error(), "saved") || !strings.Contains(err.Error(), "codex reload") {
		t.Errorf("error must explain credentials are saved and reload can be retried: %v", err)
	}
	if strings.Contains(output, "配置正确，可以使用") || strings.Contains(output, "已保存并通过") {
		t.Fatalf("unmanaged recovery failure reports success: %q", output)
	}
}

func TestCodexConfigRefusesSudoAndHelpHasNoSideEffects(t *testing.T) {
	t.Run("sudo", func(t *testing.T) {
		recorder := &codexRecorder{}
		deps := recorder.dependencies()
		deps.isSudo = func() bool { return true }
		command := newCodexCmd(deps)
		command.SetArgs([]string{"config"})
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		err := command.Execute()
		if err == nil || !strings.Contains(err.Error(), "sudo") {
			t.Fatalf("Execute() error = %v, want sudo guidance", err)
		}
		if recorder.readCalls != 0 || len(recorder.events) != 0 {
			t.Fatal("sudo refusal caused side effects")
		}
	})

	t.Run("help", func(t *testing.T) {
		recorder := &codexRecorder{}
		command := newCodexCmd(recorder.dependencies())
		command.SetArgs([]string{"config", "--help"})
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		if err := command.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if recorder.readCalls != 0 || len(recorder.events) != 0 {
			t.Fatal("help caused side effects")
		}
	})
}

func TestCodexConfigPropagatesCommandContext(t *testing.T) {
	type key string
	ctx := context.WithValue(context.Background(), key("ctx"), "value")
	recorder := &codexRecorder{}
	if _, err := executeCodexConfig(t, ctx, recorder, "https://api.example.com/v1\nsk-secret-key\nyes\n"); err != nil {
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

func executeCodexConfig(t *testing.T, ctx context.Context, recorder *codexRecorder, input string) (string, error) {
	t.Helper()
	command := newCodexCmd(recorder.dependencies())
	command.SetArgs([]string{"config"})
	command.SetContext(ctx)
	var output bytes.Buffer
	command.SetIn(strings.NewReader(input))
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.Execute()
	return output.String(), err
}
