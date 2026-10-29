package nginxx

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ABT-Tech-Limited/ft/internal/cloudflarex"
)

var managerV4 = strings.Join([]string{
	netip.PrefixFrom(netip.AddrFrom4([4]byte{173, 245, 48, 0}), 20).String(),
	netip.PrefixFrom(netip.AddrFrom4([4]byte{103, 21, 244, 0}), 22).String(),
	netip.PrefixFrom(netip.AddrFrom4([4]byte{103, 22, 200, 0}), 22).String(),
	netip.PrefixFrom(netip.AddrFrom4([4]byte{103, 31, 4, 0}), 22).String(),
}, "\n")

var managerV6 = strings.Join([]string{
	netip.PrefixFrom(netip.AddrFrom16([16]byte{0x24, 0x00, 0xcb, 0x00}), 32).String(),
	netip.PrefixFrom(netip.AddrFrom16([16]byte{0x26, 0x06, 0x47, 0x00}), 32).String(),
}, "\n")

type managerHarness struct {
	m        Manager
	calls    [][]string
	v4       string
	fetchErr error
	run      func(context.Context, []string) error
}

func newManagerHarness(t *testing.T) *managerHarness {
	t.Helper()
	h := &managerHarness{v4: managerV4}
	h.m = Manager{
		Root:       t.TempDir(),
		Executable: "/usr/local/bin/ft",
		Fetch: func(_ context.Context, url string) ([]byte, error) {
			if h.fetchErr != nil {
				return nil, h.fetchErr
			}
			if url == cloudflarex.V4URL {
				return []byte(h.v4), nil
			}
			if url == cloudflarex.V6URL {
				return []byte(managerV6), nil
			}
			return nil, errors.New("unexpected URL")
		},
		Run: func(ctx context.Context, argv []string) error {
			h.calls = append(h.calls, append([]string(nil), argv...))
			if h.run != nil {
				return h.run(ctx, argv)
			}
			return nil
		},
	}
	content, err := os.ReadFile("testdata/ubuntu-24.04.conf")
	if err != nil {
		t.Fatal(err)
	}
	h.write(t, MainPath, content)
	h.write(t, h.m.Executable, []byte("test executable\n"))
	if err := os.Chmod(h.path(h.m.Executable), 0755); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *managerHarness) path(path string) string {
	return filepath.Join(h.m.Root, strings.TrimPrefix(path, "/"))
}

func (h *managerHarness) write(t *testing.T, path string, content []byte) {
	t.Helper()
	path = h.path(path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
}

func (h *managerHarness) read(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(h.path(path))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestManagerConfigureAndRepeat(t *testing.T) {
	h := newManagerHarness(t)
	original := h.read(t, MainPath)
	h.write(t, "/etc/nginx/sites-enabled/example", []byte("existing site must stay untouched\n"))
	changed, err := h.m.Configure(context.Background())
	if err != nil || !changed {
		t.Fatalf("Configure() = %v, %v", changed, err)
	}
	wantMain, err := OptimizeMain(original)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{MainPath: wantMain, HTTPPath: HTTPConfig(), ProxyPath: ProxySnippet()} {
		if got := h.read(t, path); !bytes.Equal(got, want) {
			t.Errorf("%s content differs", path)
		}
	}
	if got := string(h.read(t, "/etc/nginx/sites-enabled/example")); got != "existing site must stay untouched\n" {
		t.Errorf("site changed: %q", got)
	}
	cron := string(h.read(t, CronPath))
	for _, want := range []string{"*/10 * * * * root", "'/usr/local/bin/ft' nginx cloudflare sync", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"} {
		if !strings.Contains(cron, want) {
			t.Errorf("cron missing %q: %s", want, cron)
		}
	}
	wantCalls := [][]string{{"nginx", "-t"}, {"systemctl", "reload", "nginx.service"}, {"systemctl", "enable", "--now", "cron.service"}}
	if !reflect.DeepEqual(h.calls, wantCalls) {
		t.Errorf("commands = %#v, want %#v", h.calls, wantCalls)
	}
	backups, err := filepath.Glob(h.path(ManagedDir) + "/backup-*/nginx.conf")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup files = %v, error %v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup differs or failed: %v", err)
	}
	info, err := os.Stat(backups[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup permissions = %v, error %v", info, err)
	}
	h.calls = nil
	changed, err = h.m.Configure(context.Background())
	if err != nil || changed {
		t.Fatalf("repeated Configure() = %v, %v", changed, err)
	}
	for _, call := range h.calls {
		if reflect.DeepEqual(call, []string{"systemctl", "reload", "nginx.service"}) {
			t.Error("idempotent configure reloaded nginx")
		}
	}
}

func TestManagerConfigureFailurePreservesOriginals(t *testing.T) {
	for _, stage := range []string{"fetch", "validate", "reload"} {
		t.Run(stage, func(t *testing.T) {
			h := newManagerHarness(t)
			original := h.read(t, MainPath)
			wantErr := errors.New("injected " + stage + " failure")
			if stage == "fetch" {
				h.fetchErr = wantErr
			} else {
				failed := false
				h.run = func(_ context.Context, argv []string) error {
					if !failed && ((stage == "validate" && argv[0] == "nginx") || (stage == "reload" && argv[0] == "systemctl" && argv[1] == "reload")) {
						failed = true
						return wantErr
					}
					return nil
				}
			}
			if _, err := h.m.Configure(context.Background()); !errors.Is(err, wantErr) {
				t.Fatalf("Configure() error = %v, want %v", err, wantErr)
			}
			if !bytes.Equal(h.read(t, MainPath), original) {
				t.Error("original main config was not restored")
			}
			for _, path := range []string{HTTPPath, ProxyPath, ActivePath, CronPath} {
				if _, err := os.Lstat(h.path(path)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("failed initial configuration left %s: %v", path, err)
				}
			}
			if stage != "reload" {
				for _, argv := range h.calls {
					if argv[0] == "systemctl" {
						t.Errorf("premature service command: %v", argv)
					}
				}
			}
		})
	}
}

func TestManagerSyncIsReadOnlyWhenUnchanged(t *testing.T) {
	h := newManagerHarness(t)
	if _, err := h.m.Configure(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(h.path(ActivePath))
	if err != nil {
		t.Fatal(err)
	}
	h.calls = nil
	changed, err := h.m.Sync(context.Background())
	if err != nil || changed || len(h.calls) != 0 {
		t.Fatalf("Sync() = %v, %v; commands=%v", changed, err, h.calls)
	}
	after, err := os.Stat(h.path(ActivePath))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("unchanged sync rewrote manifest: %v", err)
	}
}

func TestManagerSyncUpdatesOnlyCloudflareAndRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "validation failure"}[fail], func(t *testing.T) {
			h := newManagerHarness(t)
			if _, err := h.m.Configure(context.Background()); err != nil {
				t.Fatal(err)
			}
			old := h.read(t, ActivePath)
			unchanged := map[string][]byte{}
			for _, path := range []string{MainPath, HTTPPath, ProxyPath, CronPath} {
				unchanged[path] = h.read(t, path)
			}
			h.v4 = managerV4 + "\n" + netip.PrefixFrom(netip.AddrFrom4([4]byte{108, 162, 192, 0}), 18).String()
			h.calls = nil
			wantErr := errors.New("nginx config invalid")
			if fail {
				h.run = func(context.Context, []string) error { return wantErr }
			}
			changed, err := h.m.Sync(context.Background())
			if fail {
				if !errors.Is(err, wantErr) || changed || !bytes.Equal(old, h.read(t, ActivePath)) {
					t.Fatalf("failed sync = %v, %v or old manifest lost", changed, err)
				}
			} else if err != nil || !changed || bytes.Equal(old, h.read(t, ActivePath)) {
				t.Fatalf("changed sync = %v, %v or manifest unchanged", changed, err)
			}
			for path, content := range unchanged {
				if !bytes.Equal(content, h.read(t, path)) {
					t.Errorf("Sync modified %s", path)
				}
			}
			for _, argv := range h.calls {
				if strings.Contains(strings.Join(argv, " "), "cron") {
					t.Errorf("Sync operated cron: %v", argv)
				}
			}
		})
	}
}

func TestManagerRejectsUnsafeOrUninitializedState(t *testing.T) {
	tests := []struct {
		name string
		edit func(*testing.T, *managerHarness)
		sync bool
	}{
		{name: "custom main", edit: func(t *testing.T, h *managerHarness) { h.write(t, MainPath, []byte("events {}\nhttp {}\n")) }},
		{name: "foreign HTTP file", edit: func(t *testing.T, h *managerHarness) { h.write(t, HTTPPath, []byte("# user-owned\n")) }},
		{name: "foreign cron", edit: func(t *testing.T, h *managerHarness) { h.write(t, CronPath, []byte("# user-owned\n")) }},
		{name: "forged cron marker", edit: func(t *testing.T, h *managerHarness) {
			h.write(t, CronPath, []byte(ManagedHeader+"\n* * * * * root /bin/true\n"))
		}},
		{name: "writable main", edit: func(t *testing.T, h *managerHarness) {
			if err := os.Chmod(h.path(MainPath), 0666); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "writable managed directory", edit: func(t *testing.T, h *managerHarness) {
			if err := os.MkdirAll(h.path(ManagedDir), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(h.path(ManagedDir), 0777); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unsafe executable", edit: func(_ *testing.T, h *managerHarness) { h.m.Executable = "/tmp/ft%command" }},
		{name: "relative executable", edit: func(_ *testing.T, h *managerHarness) { h.m.Executable = "ft" }},
		{name: "missing executable", edit: func(t *testing.T, h *managerHarness) {
			if err := os.Remove(h.path(h.m.Executable)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "nonexecutable binary", edit: func(t *testing.T, h *managerHarness) {
			if err := os.Chmod(h.path(h.m.Executable), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "writable binary", edit: func(t *testing.T, h *managerHarness) {
			if err := os.Chmod(h.path(h.m.Executable), 0777); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "writable binary parent", edit: func(t *testing.T, h *managerHarness) {
			if err := os.Chmod(filepath.Dir(h.path(h.m.Executable)), 0777); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink binary", edit: func(t *testing.T, h *managerHarness) {
			h.write(t, "/another-ft", []byte("untrusted binary"))
			if err := os.Remove(h.path(h.m.Executable)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(h.path("/another-ft"), h.path(h.m.Executable)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "uninitialized sync", sync: true},
		{name: "symlink HTTP file", edit: func(t *testing.T, h *managerHarness) {
			h.write(t, "/outside", []byte("outside\n"))
			if err := os.MkdirAll(filepath.Dir(h.path(HTTPPath)), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(h.path("/outside"), h.path(HTTPPath)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink managed directory", edit: func(t *testing.T, h *managerHarness) {
			if err := os.MkdirAll(h.path("/outside-dir"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(h.path("/outside-dir"), h.path(ManagedDir)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newManagerHarness(t)
			if tt.edit != nil {
				tt.edit(t, h)
			}
			original := h.read(t, MainPath)
			var err error
			if tt.sync {
				_, err = h.m.Sync(context.Background())
			} else {
				_, err = h.m.Configure(context.Background())
			}
			if err == nil {
				t.Fatal("expected refusal")
			}
			if len(h.calls) != 0 {
				t.Errorf("refusal ran commands: %v", h.calls)
			}
			if !bytes.Equal(original, h.read(t, MainPath)) {
				t.Error("refusal modified main config")
			}
		})
	}
}

func TestManagerCronFailureDoesNotUndoValidatedConfiguration(t *testing.T) {
	h := newManagerHarness(t)
	wantErr := errors.New("cron cannot start")
	h.run = func(_ context.Context, argv []string) error {
		if argv[len(argv)-1] == "cron.service" {
			return wantErr
		}
		return nil
	}
	changed, err := h.m.Configure(context.Background())
	if !changed || !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "configuration") {
		t.Fatalf("Configure() = %v, %v", changed, err)
	}
	if _, err := os.Stat(h.path(CronPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cron task registered before daemon ready: %v", err)
	}
	if !bytes.Equal(h.read(t, HTTPPath), HTTPConfig()) {
		t.Error("validated configuration lost")
	}
}

func TestManagerLockWaitRespectsCancellation(t *testing.T) {
	h := newManagerHarness(t)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	fetch := h.m.Fetch
	h.m.Fetch = func(ctx context.Context, url string) ([]byte, error) {
		if url == cloudflarex.V4URL {
			close(started)
			<-release
		}
		return fetch(ctx, url)
	}
	go func() { _, err := h.m.Configure(context.Background()); finished <- err }()
	<-started
	other := h.m
	other.Fetch = fetch
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := other.Configure(ctx)
	close(release)
	firstErr := <-finished
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("blocked Configure error = %v", err)
	}
	if firstErr != nil {
		t.Fatalf("first Configure failed: %v", firstErr)
	}
}

func TestManagerPreservesConcurrentEditsInsteadOfOverwriting(t *testing.T) {
	for _, duringValidation := range []bool{false, true} {
		t.Run(map[bool]string{false: "during fetch", true: "during validation"}[duringValidation], func(t *testing.T) {
			h := newManagerHarness(t)
			originalMain := h.read(t, MainPath)
			userConfig := []byte("# changed by the administrator\n")
			if duringValidation {
				h.run = func(_ context.Context, argv []string) error {
					if argv[0] == "nginx" {
						h.write(t, HTTPPath, userConfig)
					}
					return nil
				}
			} else {
				fetch := h.m.Fetch
				h.m.Fetch = func(ctx context.Context, url string) ([]byte, error) {
					if url == cloudflarex.V4URL {
						h.write(t, HTTPPath, userConfig)
					}
					return fetch(ctx, url)
				}
			}
			_, err := h.m.Configure(context.Background())
			if err == nil || !strings.Contains(err.Error(), "concurrent") {
				t.Fatalf("Configure() error = %v, want concurrent change refusal", err)
			}
			if !bytes.Equal(h.read(t, HTTPPath), userConfig) {
				t.Error("administrator edit overwritten")
			}
			if !bytes.Equal(h.read(t, MainPath), originalMain) {
				t.Error("unrelated main config not restored")
			}
			for _, argv := range h.calls {
				if argv[0] == "systemctl" {
					t.Errorf("concurrent edit caused service command %v", argv)
				}
			}
		})
	}
}

func TestManagerReloadCancellationUsesLiveRecoveryContext(t *testing.T) {
	h := newManagerHarness(t)
	original := h.read(t, MainPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := false
	recoveryCommands := 0
	h.run = func(runCtx context.Context, argv []string) error {
		if failed {
			recoveryCommands++
			if err := runCtx.Err(); err != nil {
				t.Errorf("recovery used canceled context: %v", err)
			}
			return nil
		}
		if argv[0] == "systemctl" && argv[1] == "reload" {
			failed = true
			cancel()
			return context.Canceled
		}
		return nil
	}
	if _, err := h.m.Configure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Configure() error = %v", err)
	}
	if recoveryCommands != 2 || !bytes.Equal(original, h.read(t, MainPath)) {
		t.Fatalf("recovery commands = %d or main config lost", recoveryCommands)
	}
}

func TestManagerSyncDownloadFailurePreservesActiveFiles(t *testing.T) {
	h := newManagerHarness(t)
	if _, err := h.m.Configure(context.Background()); err != nil {
		t.Fatal(err)
	}
	active := h.read(t, ActivePath)
	h.calls = nil
	wantErr := errors.New("network unavailable")
	h.fetchErr = wantErr
	changed, err := h.m.Sync(context.Background())
	if changed || !errors.Is(err, wantErr) || len(h.calls) != 0 || !bytes.Equal(active, h.read(t, ActivePath)) {
		t.Fatalf("Sync() = %v, %v; commands=%v", changed, err, h.calls)
	}
}

func TestManagerCronQuotesExecutableAndRepairsMissingTask(t *testing.T) {
	h := newManagerHarness(t)
	h.m.Executable = "/opt/ft tools/it's ft"
	h.write(t, h.m.Executable, []byte("test executable\n"))
	if err := os.Chmod(h.path(h.m.Executable), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Configure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := string(h.read(t, CronPath)); !strings.Contains(got, "'/opt/ft tools/it'\\''s ft'") {
		t.Fatalf("unsafe cron quoting: %s", got)
	}
	if err := os.Remove(h.path(CronPath)); err != nil {
		t.Fatal(err)
	}
	h.calls = nil
	changed, err := h.m.Configure(context.Background())
	if err != nil || !changed {
		t.Fatalf("repair Configure() = %v, %v", changed, err)
	}
	for _, argv := range h.calls {
		if len(argv) > 1 && argv[0] == "systemctl" && argv[1] == "reload" {
			t.Errorf("cron repair reloaded Nginx: %v", argv)
		}
	}
}

func TestManagerAllowsLargeTrustedBinary(t *testing.T) {
	h := newManagerHarness(t)
	if err := os.Truncate(h.path(h.m.Executable), 8<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Configure(context.Background()); err != nil {
		t.Fatalf("large executable refused: %v", err)
	}
}

func TestManagerProductionRequiresRootOwnership(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires a genuinely non-root-owned test file")
	}
	h := newManagerHarness(t)
	info, err := os.Stat(h.path(h.m.Executable))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", "/"} {
		if err := (Manager{Root: root}).trustedPermissions(info, h.m.Executable); err == nil || !strings.Contains(err.Error(), "root") {
			t.Fatalf("production root %q accepted user-owned executable metadata: %v", root, err)
		}
	}
}
