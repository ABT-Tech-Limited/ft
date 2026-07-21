package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallDirectReplacesBinary(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-ft")
	target := filepath.Join(dir, "ft")
	if err := os.WriteFile(source, []byte("new binary"), 0o700); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing target: %v", err)
	}

	if err := installDirect(source, target); err != nil {
		t.Fatalf("installDirect() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading updated target: %v", err)
	}
	if got, want := string(data), "new binary"; got != want {
		t.Errorf("updated target = %q, want %q", got, want)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat updated target: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("updated target mode = %o, want 755", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading target directory: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("target directory entries = %v, want only source and target", entries)
	}
}

func TestInstallDirectPreservesTargetOnInvalidSource(t *testing.T) {
	tests := []struct {
		name       string
		makeSource func(t *testing.T, dir string) string
		wantErr    string
	}{
		{
			name: "missing source",
			makeSource: func(t *testing.T, dir string) string {
				t.Helper()
				return filepath.Join(dir, "missing")
			},
			wantErr: "opening verified ft binary",
		},
		{
			name: "source is directory",
			makeSource: func(t *testing.T, dir string) string {
				t.Helper()
				path := filepath.Join(dir, "source-directory")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("creating source directory: %v", err)
				}
				return path
			},
			wantErr: "not a regular file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "ft")
			if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
				t.Fatalf("writing target: %v", err)
			}
			source := tt.makeSource(t, dir)

			err := installDirect(source, target)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("installDirect() error = %v, want error containing %q", err, tt.wantErr)
			}
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("reading preserved target: %v", err)
			}
			if got, want := string(data), "old binary"; got != want {
				t.Errorf("preserved target = %q, want %q", got, want)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("reading target directory: %v", err)
			}
			for _, entry := range entries {
				if strings.Contains(entry.Name(), ".update-") {
					t.Errorf("left temporary update file %q", entry.Name())
				}
			}
		})
	}
}

func TestInstallDirectPreservesTargetOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-ft")
	if err := os.WriteFile(source, []byte("new binary"), 0o700); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	target := filepath.Join(dir, "ft")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("creating target directory: %v", err)
	}
	marker := filepath.Join(target, "old-binary")
	if err := os.WriteFile(marker, []byte("old binary"), 0o600); err != nil {
		t.Fatalf("writing target marker: %v", err)
	}

	err := installDirect(source, target)
	if err == nil || !strings.Contains(err.Error(), "activating updated ft binary") {
		t.Fatalf("installDirect() error = %v, want rename failure", err)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatalf("reading preserved target marker: %v", readErr)
	}
	if got, want := string(data), "old binary"; got != want {
		t.Errorf("preserved target marker = %q, want %q", got, want)
	}
	entries, readDirErr := os.ReadDir(dir)
	if readDirErr != nil {
		t.Fatalf("reading target parent directory: %v", readDirErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".update-") {
			t.Errorf("left temporary update file %q", entry.Name())
		}
	}
}

func TestResolveExecutableFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "ft-real")
	if err := os.WriteFile(realPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("writing real executable: %v", err)
	}
	linkPath := filepath.Join(dir, "ft")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("creating executable symlink: %v", err)
	}

	got, err := resolveExecutable(linkPath)
	if err != nil {
		t.Fatalf("resolveExecutable() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		t.Fatalf("resolving expected executable path: %v", err)
	}
	if got != want {
		t.Errorf("resolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableRejectsInvalidPath(t *testing.T) {
	tests := []struct {
		name    string
		path    func(t *testing.T) string
		wantErr string
	}{
		{name: "empty", path: func(t *testing.T) string { return "" }, wantErr: "empty executable path"},
		{
			name:    "missing",
			path:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") },
			wantErr: "resolving executable symlinks",
		},
		{
			name:    "directory",
			path:    func(t *testing.T) string { return t.TempDir() },
			wantErr: "not a regular file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := resolveExecutable(tt.path(t))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolveExecutable() = %q, %v; want error containing %q", path, err, tt.wantErr)
			}
		})
	}
}

func TestInstallPrivilegedStagesAndActivatesBinary(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-ft")
	target := filepath.Join(dir, "ft")
	if err := os.WriteFile(source, []byte("new binary"), 0o700); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing target: %v", err)
	}

	var contexts []context.Context
	var commands [][]string
	run := func(callContext context.Context, argv []string, stdin io.Reader) error {
		contexts = append(contexts, callContext)
		commands = append(commands, append([]string(nil), argv...))
		if stdin != nil {
			t.Errorf("stdin is non-nil for %v", argv)
		}
		switch filepath.Base(argv[0]) {
		case "install":
			data, err := os.ReadFile(argv[len(argv)-2])
			if err != nil {
				return err
			}
			return os.WriteFile(argv[len(argv)-1], data, 0o755)
		case "mv":
			return os.Rename(argv[len(argv)-2], argv[len(argv)-1])
		default:
			t.Fatalf("unexpected command %v", argv)
			return nil
		}
	}

	if err := installPrivileged(ctx, source, target, run); err != nil {
		t.Fatalf("installPrivileged() error = %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v, want install and mv", commands)
	}
	tempTarget := commands[0][len(commands[0])-1]
	if got, want := filepath.Dir(tempTarget), filepath.Dir(target); got != want {
		t.Errorf("temporary target directory = %q, want %q", got, want)
	}
	wantInstall := []string{"/usr/bin/install", "-o", "root", "-g", "root", "-m", "0755", "--", source, tempTarget}
	if !reflect.DeepEqual(commands[0], wantInstall) {
		t.Errorf("install command = %v, want %v", commands[0], wantInstall)
	}
	wantMove := []string{"/usr/bin/mv", "-f", "--", tempTarget, target}
	if !reflect.DeepEqual(commands[1], wantMove) {
		t.Errorf("move command = %v, want %v", commands[1], wantMove)
	}
	for i, got := range contexts {
		if got != ctx {
			t.Errorf("command %d context = %v, want original context", i, got)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading updated target: %v", err)
	}
	if got, want := string(data), "new binary"; got != want {
		t.Errorf("updated target = %q, want %q", got, want)
	}
}

func TestInstallPrivilegedCleansTemporaryFileOnFailure(t *testing.T) {
	tests := []struct {
		name         string
		failedAction string
		wantErr      string
		wantCalls    int
	}{
		{name: "install fails", failedAction: "install", wantErr: "staging privileged ft update", wantCalls: 2},
		{name: "move fails", failedAction: "mv", wantErr: "activating privileged ft update", wantCalls: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "verified-ft")
			target := filepath.Join(dir, "ft")
			if err := os.WriteFile(source, []byte("new binary"), 0o700); err != nil {
				t.Fatalf("writing source: %v", err)
			}
			if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
				t.Fatalf("writing target: %v", err)
			}
			wantFailure := errors.New(tt.failedAction + " failed")
			var commands [][]string
			run := func(ctx context.Context, argv []string, stdin io.Reader) error {
				commands = append(commands, append([]string(nil), argv...))
				switch filepath.Base(argv[0]) {
				case "install":
					data, err := os.ReadFile(argv[len(argv)-2])
					if err != nil {
						return err
					}
					if err := os.WriteFile(argv[len(argv)-1], data, 0o755); err != nil {
						return err
					}
					if tt.failedAction == "install" {
						return wantFailure
					}
				case "mv":
					if tt.failedAction == "mv" {
						return wantFailure
					}
				case "rm":
					return os.Remove(argv[len(argv)-1])
				default:
					t.Fatalf("unexpected command %v", argv)
				}
				return nil
			}

			err := installPrivileged(context.Background(), source, target, run)
			if !errors.Is(err, wantFailure) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("installPrivileged() error = %v, want wrapped %v containing %q", err, wantFailure, tt.wantErr)
			}
			if len(commands) != tt.wantCalls {
				t.Fatalf("commands = %v, want %d calls", commands, tt.wantCalls)
			}
			if got := commands[len(commands)-1][0]; got != "/usr/bin/rm" {
				t.Errorf("last command = %q, want /usr/bin/rm", got)
			}
			tempTarget := commands[0][len(commands[0])-1]
			if _, err := os.Stat(tempTarget); !os.IsNotExist(err) {
				t.Errorf("temporary target stat error = %v, want not exist", err)
			}
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("reading preserved target: %v", err)
			}
			if got, want := string(data), "old binary"; got != want {
				t.Errorf("preserved target = %q, want %q", got, want)
			}
		})
	}
}

func TestInstallPrivilegedCleansTemporaryFileAfterCancellation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-ft")
	target := filepath.Join(dir, "ft")
	if err := os.WriteFile(source, []byte("new binary"), 0o700); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing target: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var tempTarget string
	var cleanupContextErr error
	run := func(callContext context.Context, argv []string, stdin io.Reader) error {
		switch filepath.Base(argv[0]) {
		case "install":
			tempTarget = argv[len(argv)-1]
			data, err := os.ReadFile(argv[len(argv)-2])
			if err != nil {
				return err
			}
			if err := os.WriteFile(tempTarget, data, 0o755); err != nil {
				return err
			}
			cancel()
			return nil
		case "mv":
			return callContext.Err()
		case "rm":
			cleanupContextErr = callContext.Err()
			if cleanupContextErr != nil {
				return cleanupContextErr
			}
			return os.Remove(argv[len(argv)-1])
		default:
			return fmt.Errorf("unexpected command %v", argv)
		}
	}

	err := installPrivileged(ctx, source, target, run)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("installPrivileged() error = %v, want context cancellation", err)
	}
	if cleanupContextErr != nil {
		t.Errorf("cleanup context error = %v, want usable cleanup context", cleanupContextErr)
	}
	if _, err := os.Stat(tempTarget); !os.IsNotExist(err) {
		t.Errorf("temporary target stat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading preserved target: %v", err)
	}
	if got, want := string(data), "old binary"; got != want {
		t.Errorf("preserved target = %q, want %q", got, want)
	}
}

func TestInstallPrivilegedValidatesBeforeRunningCommands(t *testing.T) {
	validRun := func(context.Context, []string, io.Reader) error { return nil }
	tests := []struct {
		name    string
		ctx     context.Context
		source  string
		target  string
		run     PrivilegedRunner
		wantErr string
	}{
		{name: "nil context", source: "/tmp/source", target: "/usr/local/bin/ft", run: validRun, wantErr: "nil context"},
		{name: "nil runner", ctx: context.Background(), source: "/tmp/source", target: "/usr/local/bin/ft", wantErr: "nil privileged runner"},
		{name: "empty source", ctx: context.Background(), target: "/usr/local/bin/ft", run: validRun, wantErr: "empty verified binary path"},
		{name: "empty target", ctx: context.Background(), source: "/tmp/source", run: validRun, wantErr: "empty target executable path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			run := tt.run
			if run != nil {
				run = func(ctx context.Context, argv []string, stdin io.Reader) error {
					calls++
					return tt.run(ctx, argv, stdin)
				}
			}
			err := installPrivileged(tt.ctx, tt.source, tt.target, run)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("installPrivileged() error = %v, want error containing %q", err, tt.wantErr)
			}
			if calls != 0 {
				t.Errorf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestInstallBinaryUsesDirectReplacementWhenWritable(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-ft")
	target := filepath.Join(dir, "ft")
	if err := os.WriteFile(source, []byte("new binary"), 0o700); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing target: %v", err)
	}
	privilegedCalls := 0
	run := func(context.Context, []string, io.Reader) error {
		privilegedCalls++
		return fmt.Errorf("privileged runner must not be called")
	}

	if err := installBinary(context.Background(), source, target, run); err != nil {
		t.Fatalf("installBinary() error = %v", err)
	}
	if privilegedCalls != 0 {
		t.Errorf("privileged runner calls = %d, want 0", privilegedCalls)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading updated target: %v", err)
	}
	if got, want := string(data), "new binary"; got != want {
		t.Errorf("updated target = %q, want %q", got, want)
	}
}

func TestInstallBinaryDoesNotElevateNonPermissionFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ft")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing target: %v", err)
	}
	privilegedCalls := 0
	run := func(context.Context, []string, io.Reader) error {
		privilegedCalls++
		return nil
	}

	err := installBinary(context.Background(), filepath.Join(dir, "missing"), target, run)
	if err == nil || !strings.Contains(err.Error(), "installing ft update directly") {
		t.Fatalf("installBinary() error = %v, want direct installation error", err)
	}
	if privilegedCalls != 0 {
		t.Errorf("privileged runner calls = %d, want 0", privilegedCalls)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("reading preserved target: %v", readErr)
	}
	if got, want := string(data), "old binary"; got != want {
		t.Errorf("preserved target = %q, want %q", got, want)
	}
}

func TestInstallBinaryFallsBackToPrivilegedReplacementOnPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission fallback requires a non-root test process")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-ft")
	if err := os.WriteFile(source, []byte("new binary"), 0o700); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	protectedDir := filepath.Join(dir, "protected")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatalf("creating protected directory: %v", err)
	}
	target := filepath.Join(protectedDir, "ft")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing target: %v", err)
	}
	if err := os.Chmod(protectedDir, 0o500); err != nil {
		t.Fatalf("protecting target directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(protectedDir, 0o700); err != nil {
			t.Errorf("restoring target directory permissions: %v", err)
		}
	})

	var commands [][]string
	run := func(ctx context.Context, argv []string, stdin io.Reader) error {
		commands = append(commands, append([]string(nil), argv...))
		switch filepath.Base(argv[0]) {
		case "install":
			if err := os.Chmod(protectedDir, 0o700); err != nil {
				return err
			}
			data, err := os.ReadFile(argv[len(argv)-2])
			if err != nil {
				return err
			}
			return os.WriteFile(argv[len(argv)-1], data, 0o755)
		case "mv":
			return os.Rename(argv[len(argv)-2], argv[len(argv)-1])
		default:
			return fmt.Errorf("unexpected command %v", argv)
		}
	}

	if err := installBinary(context.Background(), source, target, run); err != nil {
		t.Fatalf("installBinary() error = %v", err)
	}
	if len(commands) != 2 || commands[0][0] != "/usr/bin/install" || commands[1][0] != "/usr/bin/mv" {
		t.Errorf("privileged commands = %v, want install then mv", commands)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading updated target: %v", err)
	}
	if got, want := string(data), "new binary"; got != want {
		t.Errorf("updated target = %q, want %q", got, want)
	}
}
