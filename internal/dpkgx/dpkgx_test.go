package dpkgx

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestQueryPackageStates(t *testing.T) {
	tests := []struct {
		name     string
		packages []string
		output   string
		exitCode int
		runErr   error
		want     map[string]Status
	}{
		{
			name:     "installed including held package",
			packages: []string{"docker-ce", "nginx"},
			output: "docker-ce\thold\tok\tinstalled\n" +
				"nginx\tinstall\tok\tinstalled\n",
			want: map[string]Status{"docker-ce": Installed, "nginx": Installed},
		},
		{
			name:     "exit one allows missing packages and partial output",
			packages: []string{"docker-ce", "docker-ce-cli"},
			output:   "docker-ce\tinstall\tok\tinstalled\n",
			exitCode: 1,
			runErr:   errors.New("exit status 1"),
			want:     map[string]Status{"docker-ce": Installed, "docker-ce-cli": Absent},
		},
		{
			name:     "exit one with no output means all absent",
			packages: []string{"nginx"},
			exitCode: 1,
			runErr:   errors.New("exit status 1"),
			want:     map[string]Status{"nginx": Absent},
		},
		{
			name:     "inactive package states are absent",
			packages: []string{"nginx", "docker.io"},
			output: "nginx\tdeinstall\tok\tconfig-files\n" +
				"docker.io\tpurge\tok\tnot-installed\n",
			want: map[string]Status{"nginx": Absent, "docker.io": Absent},
		},
		{
			name:     "partial and reinstreq package states are broken",
			packages: []string{"docker-ce", "containerd.io"},
			output: "docker-ce\tinstall\tok\tunpacked\n" +
				"containerd.io\tinstall\treinstreq\tinstalled\n",
			want: map[string]Status{"docker-ce": Broken, "containerd.io": Broken},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotArgv []string
			runner := func(_ context.Context, argv []string) ([]byte, int, error) {
				gotArgv = append([]string(nil), argv...)
				return []byte(tt.output), tt.exitCode, tt.runErr
			}

			got, err := query(context.Background(), runner, tt.packages...)
			if err != nil {
				t.Fatalf("query() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("query() = %#v, want %#v", got, tt.want)
			}
			wantArgv := append([]string{
				"dpkg-query",
				"--admindir=/var/lib/dpkg",
				"--show",
				"--showformat=${Package}\t${db:Status-Want}\t${db:Status-Eflag}\t${db:Status-Status}\n",
			}, tt.packages...)
			if !reflect.DeepEqual(gotArgv, wantArgv) {
				t.Errorf("runner argv = %v, want %v", gotArgv, wantArgv)
			}
		})
	}
}

func TestQueryRejectsInvalidResults(t *testing.T) {
	wantRunErr := errors.New("dpkg failed")
	tests := []struct {
		name     string
		packages []string
		output   string
		exitCode int
		runErr   error
		wantErr  string
		wantIs   error
	}{
		{
			name:     "exit zero missing package record",
			packages: []string{"docker-ce", "docker-ce-cli"},
			output:   "docker-ce\tinstall\tok\tinstalled\n",
			wantErr:  "missing status",
		},
		{
			name:     "malformed record",
			packages: []string{"nginx"},
			output:   "nginx\tinstalled\n",
			wantErr:  "malformed",
		},
		{
			name:     "duplicate record",
			packages: []string{"nginx"},
			output: "nginx\tinstall\tok\tinstalled\n" +
				"nginx\tinstall\tok\tinstalled\n",
			wantErr: "duplicate",
		},
		{
			name:     "unexpected package",
			packages: []string{"nginx"},
			output:   "apache2\tinstall\tok\tinstalled\n",
			wantErr:  "unexpected package",
		},
		{
			name:     "unknown error flag",
			packages: []string{"nginx"},
			output:   "nginx\tinstall\tunknown\tinstalled\n",
			wantErr:  "unknown dpkg error flag",
		},
		{
			name:     "unknown current status",
			packages: []string{"nginx"},
			output:   "nginx\tinstall\tok\tmystery\n",
			wantErr:  "unknown dpkg package status",
		},
		{
			name:     "dpkg exit two",
			packages: []string{"nginx"},
			exitCode: 2,
			runErr:   wantRunErr,
			wantErr:  "dpkg-query failed",
			wantIs:   wantRunErr,
		},
		{
			name:     "dpkg startup failure",
			packages: []string{"nginx"},
			exitCode: -1,
			runErr:   wantRunErr,
			wantErr:  "dpkg-query failed",
			wantIs:   wantRunErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(context.Context, []string) ([]byte, int, error) {
				return []byte(tt.output), tt.exitCode, tt.runErr
			}
			_, err := query(context.Background(), runner, tt.packages...)
			if err == nil {
				t.Fatalf("query() error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("query() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("query() error = %v, want errors.Is(_, %v)", err, tt.wantIs)
			}
		})
	}
}

func TestQueryRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		packages []string
		wantErr  string
	}{
		{name: "nil context", packages: []string{"nginx"}, wantErr: "nil context"},
		{name: "empty package name", ctx: context.Background(), packages: []string{""}, wantErr: "empty package"},
		{name: "duplicate package name", ctx: context.Background(), packages: []string{"nginx", "nginx"}, wantErr: "duplicate package"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(context.Context, []string) ([]byte, int, error) {
				t.Fatal("runner must not be called")
				return nil, 0, nil
			}
			_, err := query(tt.ctx, runner, tt.packages...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("query() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestQueryEmptyPackageList(t *testing.T) {
	runner := func(context.Context, []string) ([]byte, int, error) {
		t.Fatal("runner must not be called")
		return nil, 0, nil
	}
	got, err := query(context.Background(), runner)
	if err != nil {
		t.Fatalf("query() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("query() = %#v, want empty map", got)
	}
}

func TestQueryPreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := func(ctx context.Context, _ []string) ([]byte, int, error) {
		return nil, -1, ctx.Err()
	}

	_, err := query(ctx, runner, "nginx")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("query() error = %v, want context.Canceled", err)
	}
}

func TestRunDpkgQuerySanitizesDpkgEnvironment(t *testing.T) {
	t.Setenv("DPKG_ROOT", "/tmp/not-the-host-root")
	t.Setenv("DPKG_ADMINDIR", "/tmp/not-the-host-dpkg")
	argv := []string{
		"sh", "-c",
		`if [ -n "$DPKG_ROOT$DPKG_ADMINDIR" ]; then echo "dpkg environment leaked" >&2; exit 2; fi; printf "host database"`,
	}

	output, exitCode, err := runDpkgQuery(context.Background(), argv)
	if err != nil {
		t.Fatalf("runDpkgQuery() error = %v", err)
	}
	if exitCode != 0 {
		t.Errorf("runDpkgQuery() exit code = %d, want 0", exitCode)
	}
	if got := string(output); got != "host database" {
		t.Errorf("runDpkgQuery() output = %q, want %q", got, "host database")
	}
}

func TestRunDpkgQueryIncludesFatalStderr(t *testing.T) {
	argv := []string{"sh", "-c", `echo "dpkg database is corrupt" >&2; exit 2`}

	_, exitCode, err := runDpkgQuery(context.Background(), argv)
	if err == nil {
		t.Fatal("runDpkgQuery() error = nil, want exit error")
	}
	if exitCode != 2 {
		t.Errorf("runDpkgQuery() exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(err.Error(), "dpkg database is corrupt") {
		t.Errorf("runDpkgQuery() error = %q, want stderr diagnostic", err)
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Errorf("runDpkgQuery() error = %v, want wrapped exit code 2", err)
	}
}
