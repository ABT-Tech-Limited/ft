package sshx

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEffectivePortsRunsOpenSSHEffectiveConfig(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	var gotContexts []context.Context
	var gotArgvs [][]string

	ports, err := EffectivePorts(ctx, func(callContext context.Context, argv []string) ([]byte, error) {
		gotContexts = append(gotContexts, callContext)
		gotArgvs = append(gotArgvs, append([]string(nil), argv...))
		return []byte("port 10222\n"), nil
	})
	if err != nil {
		t.Fatalf("EffectivePorts() error = %v", err)
	}
	if !reflect.DeepEqual(ports, []int{10222}) {
		t.Errorf("EffectivePorts() = %v, want [10222]", ports)
	}
	want := [][]string{
		{"install", "-m", "0755", "-d", "/run/sshd"},
		{"/usr/sbin/sshd", "-T"},
	}
	if !reflect.DeepEqual(gotArgvs, want) {
		t.Errorf("EffectivePorts() commands = %v, want %v", gotArgvs, want)
	}
	for i, gotContext := range gotContexts {
		if gotContext != ctx {
			t.Errorf("EffectivePorts() command %d context = %v, want original context", i, gotContext)
		}
	}
}

func TestEffectivePortsWrapsPrivsepDirectoryFailure(t *testing.T) {
	wantErr := errors.New("install failed")
	var calls [][]string
	ports, err := EffectivePorts(context.Background(), func(_ context.Context, argv []string) ([]byte, error) {
		calls = append(calls, append([]string(nil), argv...))
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("EffectivePorts() error = %v, want errors.Is(_, wantErr)", err)
	}
	if !strings.Contains(err.Error(), "privilege separation directory /run/sshd") {
		t.Errorf("EffectivePorts() error = %q, want privilege separation directory context", err)
	}
	if len(calls) != 1 {
		t.Errorf("EffectivePorts() ran commands %v, want only the directory command", calls)
	}
	if ports != nil {
		t.Errorf("EffectivePorts() ports = %v, want nil", ports)
	}
}

func TestEffectivePortsWrapsCommandFailure(t *testing.T) {
	wantErr := errors.New("sshd test failed")
	ports, err := EffectivePorts(context.Background(), func(_ context.Context, argv []string) ([]byte, error) {
		if argv[0] != "/usr/sbin/sshd" {
			return nil, nil
		}
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("EffectivePorts() error = %v, want errors.Is(_, wantErr)", err)
	}
	if !strings.Contains(err.Error(), "reading effective SSH configuration") {
		t.Errorf("EffectivePorts() error = %q, want SSH configuration context", err)
	}
	if ports != nil {
		t.Errorf("EffectivePorts() ports = %v, want nil", ports)
	}
}

func TestParseEffectivePorts(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []int
	}{
		{
			name:   "custom port",
			output: "port 10222\naddressfamily any\nlistenaddress 0.0.0.0:10222\n",
			want:   []int{10222},
		},
		{
			name:   "IPv4 ListenAddress overrides Port",
			output: "port 22\nlistenaddress 0.0.0.0:10222\n",
			want:   []int{10222},
		},
		{
			name:   "IPv6 ListenAddress overrides Port",
			output: "port 22\nlistenaddress [::]:10222\n",
			want:   []int{10222},
		},
		{
			name:   "migration with duplicate ports",
			output: "port 22\nport 10222\nlistenaddress 0.0.0.0:22\nlistenaddress [::]:22\nlistenaddress 0.0.0.0:10222\nlistenaddress [::]:10222\n",
			want:   []int{22, 10222},
		},
		{
			name:   "unqualified ListenAddress falls back to Port",
			output: "port 2222\nlistenaddress 0.0.0.0\n",
			want:   []int{2222},
		},
		{
			name:   "whitespace",
			output: "  port\t2222  \r\n",
			want:   []int{2222},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEffectivePorts(tt.output)
			if err != nil {
				t.Fatalf("parseEffectivePorts() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseEffectivePorts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseEffectivePortsRejectsInvalidOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "missing port", output: "addressfamily any\n", want: "no effective SSH port"},
		{name: "non-numeric port", output: "port ssh\n", want: "invalid SSH port"},
		{name: "zero port", output: "port 0\n", want: "between 1 and 65535"},
		{name: "out-of-range port", output: "port 65536\n", want: "between 1 and 65535"},
		{name: "invalid ListenAddress port", output: "port 22\nlistenaddress 0.0.0.0:ssh\n", want: "invalid SSH port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ports, err := parseEffectivePorts(tt.output)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseEffectivePorts() ports=%v error=%v, want error containing %q", ports, err, tt.want)
			}
			if ports != nil {
				t.Errorf("parseEffectivePorts() ports = %v, want nil", ports)
			}
		})
	}
}
