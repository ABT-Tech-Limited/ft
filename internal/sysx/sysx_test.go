package sysx

import (
	"context"
	"strings"
	"testing"
)

func TestWithSudo(t *testing.T) {
	argv := []string{"systemctl", "enable", "--now", "nginx.service"}
	want := []string{"sudo", "DEBIAN_FRONTEND=noninteractive", "systemctl", "enable", "--now", "nginx.service"}

	got := WithSudo(argv)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("WithSudo() = %v, want %v", got, want)
	}
	if strings.Join(argv, "\x00") != strings.Join([]string{"systemctl", "enable", "--now", "nginx.service"}, "\x00") {
		t.Errorf("WithSudo() mutated its input: %v", argv)
	}
}

func TestRunInputContextRejectsEmptyCommand(t *testing.T) {
	err := RunInputContext(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("RunInputContext() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "empty command") {
		t.Errorf("RunInputContext() error = %q, want it to contain %q", err, "empty command")
	}
}

func TestOutputContextRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		argv    []string
		wantErr string
	}{
		{name: "empty command", ctx: context.Background(), wantErr: "empty command"},
		{name: "nil context", argv: []string{"true"}, wantErr: "nil command context"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := OutputContext(tt.ctx, tt.argv)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("OutputContext() output=%q error=%v, want error containing %q", output, err, tt.wantErr)
			}
			if output != nil {
				t.Errorf("OutputContext() output = %q, want nil", output)
			}
		})
	}
}
