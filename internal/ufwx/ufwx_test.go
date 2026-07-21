package ufwx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const activeHeader = "Status: active\n\nTo                         Action      From\n--                         ------      ----\n"

func activeStatus(rules ...string) string {
	return activeHeader + strings.Join(rules, "\n") + "\n"
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		port    int
		want    Report
		wantErr string
	}{
		{name: "inactive", output: "Status: inactive\n", port: 10222, want: Report{}},
		{
			name:   "active without rules",
			output: "Status: active\n",
			port:   10222,
			want:   Report{Active: true},
		},
		{
			name:   "allow tcp rule",
			output: activeStatus("10222/tcp                  ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true, PortAllowed: true},
		},
		{
			name:   "limit tcp rule",
			output: activeStatus("10222/tcp                  LIMIT       Anywhere"),
			port:   10222,
			want:   Report{Active: true, PortAllowed: true},
		},
		{
			name:   "deny rule does not allow",
			output: activeStatus("10222/tcp                  DENY        Anywhere"),
			port:   10222,
			want:   Report{Active: true},
		},
		{
			name:   "reject rule does not allow",
			output: activeStatus("10222/tcp                  REJECT      Anywhere"),
			port:   10222,
			want:   Report{Active: true},
		},
		{
			name:   "udp rule does not allow",
			output: activeStatus("10222/udp                  ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true},
		},
		{
			name:   "bare port covers tcp",
			output: activeStatus("10222                      ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true, PortAllowed: true},
		},
		{
			name:   "ipv6 only rule counts",
			output: activeStatus("10222/tcp (v6)             ALLOW (v6)  Anywhere (v6)"),
			port:   10222,
			want:   Report{Active: true, PortAllowed: true},
		},
		{
			name:   "comma list matches",
			output: activeStatus("80,10222/tcp               ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true, PortAllowed: true},
		},
		{
			name:   "range matches",
			output: activeStatus("10000:10300/tcp            ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true, PortAllowed: true},
		},
		{
			name:   "range excludes port outside",
			output: activeStatus("10000:10100/tcp            ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true},
		},
		{
			name:   "other port does not match",
			output: activeStatus("22/tcp                     ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true},
		},
		{
			name:   "application profile never matches",
			output: activeStatus("Nginx Full                 ALLOW       Anywhere"),
			port:   10222,
			want:   Report{Active: true},
		},
		{
			name:   "specific source still allows",
			output: activeStatus("10222/tcp                  ALLOW       192.168.1.0/24"),
			port:   10222,
			want:   Report{Active: true, PortAllowed: true},
		},
		{name: "missing status line", output: "garbage\n", port: 10222, wantErr: "no ufw status line"},
		{name: "unknown status value", output: "Status: mystery\n", port: 10222, wantErr: "unknown ufw status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStatus(tt.output, tt.port)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseStatus() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStatus() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseStatus() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInspect(t *testing.T) {
	t.Run("runs ufw status and parses", func(t *testing.T) {
		var recorded [][]string
		output := func(_ context.Context, argv []string) ([]byte, error) {
			recorded = append(recorded, argv)
			return []byte(activeStatus("10222/tcp                  ALLOW       Anywhere")), nil
		}
		report, err := Inspect(context.Background(), output, 10222)
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}
		if want := (Report{Active: true, PortAllowed: true}); report != want {
			t.Errorf("Inspect() = %+v, want %+v", report, want)
		}
		if len(recorded) != 1 || fmt.Sprintf("%v", recorded[0]) != "[ufw status]" {
			t.Errorf("Inspect() ran %v, want [[ufw status]]", recorded)
		}
	})
	t.Run("nil runner", func(t *testing.T) {
		if _, err := Inspect(context.Background(), nil, 10222); err == nil {
			t.Fatal("Inspect() error = nil, want nil runner error")
		}
	})
	t.Run("invalid port", func(t *testing.T) {
		output := func(context.Context, []string) ([]byte, error) { return nil, nil }
		for _, port := range []int{0, -1, 65536} {
			if _, err := Inspect(context.Background(), output, port); err == nil {
				t.Errorf("Inspect(port=%d) error = nil, want range error", port)
			}
		}
	})
	t.Run("command failure", func(t *testing.T) {
		wantErr := errors.New("ufw exploded")
		output := func(context.Context, []string) ([]byte, error) { return nil, wantErr }
		if _, err := Inspect(context.Background(), output, 10222); !errors.Is(err, wantErr) {
			t.Fatalf("Inspect() error = %v, want errors.Is(_, wantErr)", err)
		}
	})
}
