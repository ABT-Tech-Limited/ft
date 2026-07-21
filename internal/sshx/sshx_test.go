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
	var gotContext context.Context
	var gotArgv []string

	ports, err := EffectivePorts(ctx, func(callContext context.Context, argv []string) ([]byte, error) {
		gotContext = callContext
		gotArgv = append([]string(nil), argv...)
		return []byte("port 10222\n"), nil
	})
	if err != nil {
		t.Fatalf("EffectivePorts() error = %v", err)
	}
	if !reflect.DeepEqual(ports, []int{10222}) {
		t.Errorf("EffectivePorts() = %v, want [10222]", ports)
	}
	if gotContext != ctx {
		t.Errorf("EffectivePorts() context = %v, want original context", gotContext)
	}
	if want := []string{"/usr/sbin/sshd", "-T"}; !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("EffectivePorts() argv = %v, want %v", gotArgv, want)
	}
}

func TestEffectivePortsWrapsCommandFailure(t *testing.T) {
	wantErr := errors.New("sshd test failed")
	ports, err := EffectivePorts(context.Background(), func(context.Context, []string) ([]byte, error) {
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
