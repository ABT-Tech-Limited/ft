package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ABT-Tech-Limited/ft/internal/aptx"
	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
)

func TestNginxExamplesNeedNoSystemDependencies(t *testing.T) {
	for _, kind := range []string{"", "go", "static"} {
		t.Run(kind, func(t *testing.T) {
			args := []string{"example"}
			if kind != "" {
				args = append(args, kind)
			}
			output, err := executeNginx(t, nginxDependencies{}, args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, "server {") {
				t.Fatalf("missing server configuration: %s", output)
			}
			if kind != "static" && !strings.Contains(output, "include /etc/nginx/snippets/ft-go-proxy.conf;") {
				t.Errorf("missing Go proxy snippet: %s", output)
			}
			if kind != "go" {
				for _, want := range []string{"expires 1h;", "expires -1;", "=404"} {
					if !strings.Contains(output, want) {
						t.Errorf("missing static cache policy %q: %s", want, output)
					}
				}
			}
		})
	}
}

func TestNginxArgumentErrorsNeedNoSystemDependencies(t *testing.T) {
	for _, args := range [][]string{
		{"example", "unknown"}, {"example", "go", "extra"},
		{"configure", "extra"}, {"cloudflare", "sync", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := executeNginx(t, nginxDependencies{}, args...); err == nil {
				t.Fatal("expected argument error")
			}
		})
	}
}

func TestNginxHelpNeedsNoSystemDependencies(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"example", "--help"}, {"configure", "--help"}, {"cloudflare", "sync", "--help"}} {
		if _, err := executeNginx(t, nginxDependencies{}, args...); err != nil {
			t.Fatal(err)
		}
	}
}

type nginxRecorder struct {
	uid           int
	statuses      map[string]dpkgx.Status
	detectErr     error
	executableErr error
	queryErr      error
	actionErr     error
	runErr        error
	limit         uint64
	limitErr      error
	changed       bool
	configured    int
	synced        int
	queries       [][]string
	commands      [][]string
	context       context.Context
	executable    string
}

func newNginxRecorder() *nginxRecorder {
	return &nginxRecorder{
		statuses: map[string]dpkgx.Status{"nginx": dpkgx.Installed, "cron": dpkgx.Installed},
		limit:    65535,
		changed:  true,
	}
}

func (r *nginxRecorder) dependencies() nginxDependencies {
	return nginxDependencies{
		detectOS:   func() (osrelease.Info, error) { return osrelease.Info{ID: "ubuntu", VersionID: "24.04"}, r.detectErr },
		euid:       func() int { return r.uid },
		executable: func() (string, error) { return "/usr/local/bin/ft", r.executableErr },
		queryPackages: func(_ context.Context, packages ...string) (map[string]dpkgx.Status, error) {
			r.queries = append(r.queries, append([]string(nil), packages...))
			return r.statuses, r.queryErr
		},
		fileLimit: func(context.Context) (uint64, error) { return r.limit, r.limitErr },
		run: func(_ context.Context, argv []string, _ io.Reader) error {
			r.commands = append(r.commands, append([]string(nil), argv...))
			return r.runErr
		},
		configure: func(ctx context.Context, executable string) (bool, error) {
			r.context, r.executable = ctx, executable
			r.configured++
			return r.changed, r.actionErr
		},
		sync: func(ctx context.Context) (bool, error) {
			r.context = ctx
			r.synced++
			return r.changed, r.actionErr
		},
	}
}

func TestNginxConfigureEnsuresCronWithoutReinstallingNginx(t *testing.T) {
	for _, state := range []dpkgx.Status{dpkgx.Absent, dpkgx.Broken, dpkgx.Installed} {
		t.Run(map[dpkgx.Status]string{dpkgx.Absent: "absent", dpkgx.Broken: "broken", dpkgx.Installed: "installed"}[state], func(t *testing.T) {
			r := newNginxRecorder()
			r.statuses["cron"] = state
			output, err := executeNginx(t, r.dependencies(), "configure")
			if err != nil {
				t.Fatal(err)
			}
			if r.configured != 1 || r.synced != 0 || r.executable != "/usr/local/bin/ft" {
				t.Fatalf("configure=%d sync=%d executable=%q", r.configured, r.synced, r.executable)
			}
			var want [][]string
			if state != dpkgx.Installed {
				want = [][]string{aptx.Update(), aptx.Install("cron")}
			}
			if !reflect.DeepEqual(r.commands, want) {
				t.Errorf("commands=%v want=%v", r.commands, want)
			}
			if !strings.Contains(output, "every 10 minutes") {
				t.Errorf("missing cron cadence: %s", output)
			}
		})
	}
}

func TestNginxConfigureFailureStopsBeforeSuccess(t *testing.T) {
	wantErr := errors.New("test failure")
	for _, phase := range []string{"OS", "executable", "query", "nginx absent", "nginx broken", "cron install", "configuration"} {
		t.Run(phase, func(t *testing.T) {
			r := newNginxRecorder()
			switch phase {
			case "OS":
				r.detectErr = wantErr
			case "executable":
				r.executableErr = wantErr
			case "query":
				r.queryErr = wantErr
			case "nginx absent":
				r.statuses["nginx"] = dpkgx.Absent
			case "nginx broken":
				r.statuses["nginx"] = dpkgx.Broken
			case "cron install":
				r.statuses["cron"], r.runErr = dpkgx.Absent, wantErr
			case "configuration":
				r.actionErr = wantErr
			}
			output, err := executeNginx(t, r.dependencies(), "configure")
			if err == nil {
				t.Fatal("expected error")
			}
			if phase != "nginx absent" && phase != "nginx broken" && !errors.Is(err, wantErr) {
				t.Errorf("error=%v want=%v", err, wantErr)
			}
			if phase != "configuration" && r.configured != 0 {
				t.Error("configuration ran after prerequisite failure")
			}
			if strings.Contains(output, "configured successfully") {
				t.Errorf("false success: %s", output)
			}
		})
	}
}

func TestNginxNonRootDelegatesWithoutDoingPrivilegedWork(t *testing.T) {
	for _, args := range [][]string{{"configure"}, {"cloudflare", "sync"}} {
		r := newNginxRecorder()
		r.uid = 1000
		if _, err := executeNginx(t, r.dependencies(), args...); err != nil {
			t.Fatal(err)
		}
		want := [][]string{append([]string{"/usr/local/bin/ft", "nginx"}, args...)}
		if !reflect.DeepEqual(r.commands, want) {
			t.Errorf("commands=%v want=%v", r.commands, want)
		}
		if len(r.queries) != 0 || r.configured != 0 || r.synced != 0 {
			t.Fatal("privileged work ran without elevation")
		}
	}
}

func TestNginxSyncDoesNotInstallPackagesOrReconfigureDefaults(t *testing.T) {
	for _, changed := range []bool{false, true} {
		r := newNginxRecorder()
		r.changed = changed
		output, err := executeNginx(t, r.dependencies(), "cloudflare", "sync")
		if err != nil {
			t.Fatal(err)
		}
		if r.synced != 1 || r.configured != 0 || len(r.queries) != 0 || len(r.commands) != 0 {
			t.Fatalf("unexpected sync side effects: %#v", r)
		}
		if !changed && !strings.Contains(output, "unchanged") {
			t.Errorf("missing unchanged message: %s", output)
		}
	}
}

func TestNginxActionsPassContextAndPropagateErrors(t *testing.T) {
	type contextKey struct{}
	for _, args := range [][]string{{"configure"}, {"cloudflare", "sync"}} {
		r := newNginxRecorder()
		r.actionErr = errors.New("action failed")
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "command context"))
		defer cancel()
		command := newNginxCmd(r.dependencies())
		command.SetContext(ctx)
		command.SetArgs(args)
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		if err := command.Execute(); !errors.Is(err, r.actionErr) {
			t.Errorf("error=%v", err)
		}
		if r.context == nil || r.context.Value(contextKey{}) != "command context" {
			t.Error("context not propagated")
		}
	}
}

func TestNginxSyncHasBoundedContext(t *testing.T) {
	r := newNginxRecorder()
	started := time.Now()
	if _, err := executeNginx(t, r.dependencies(), "cloudflare", "sync"); err != nil {
		t.Fatal(err)
	}
	deadline, ok := r.context.Deadline()
	if !ok || deadline.Before(started) || deadline.After(started.Add(5*time.Minute+time.Second)) {
		t.Errorf("sync deadline = %v (%v), want bounded run shorter than cron interval", deadline, ok)
	}
}

func TestNginxConfigureWarnsAboutLowFileLimit(t *testing.T) {
	r := newNginxRecorder()
	r.limit = 1024
	output, err := executeNginx(t, r.dependencies(), "configure")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "1024") || !strings.Contains(output, "2048") {
		t.Errorf("missing limit warning: %s", output)
	}
}

func executeNginx(t *testing.T, deps nginxDependencies, args ...string) (string, error) {
	t.Helper()
	command := newNginxCmd(deps)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}
