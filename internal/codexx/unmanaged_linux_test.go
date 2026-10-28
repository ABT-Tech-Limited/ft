//go:build linux

package codexx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The copied test binary acts as a direct server, without starting real Codex
// or reading its configuration. Its argv and executable name match production.
func TestMain(m *testing.M) {
	if socket := os.Getenv("FT_TEST_CODEX_SOCKET"); socket != "" {
		if os.Getenv("FT_TEST_CODEX_IGNORE_TERM") == "1" {
			signal.Ignore(syscall.SIGTERM)
		}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer listener.Close()
		fmt.Println("ready")
		for {
			conn, err := listener.Accept()
			if err != nil {
				os.Exit(2)
			}
			conn.Close()
		}
	}
	os.Exit(m.Run())
}

func startSocketFixture(t *testing.T, name string, args []string, ignoreTERM bool) (string, *exec.Cmd) {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(dir, name)
	if err := os.WriteFile(copy, data, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "control.sock")
	command := exec.Command(copy, args...)
	command.Env = append(os.Environ(), "FT_TEST_CODEX_SOCKET="+socket)
	if ignoreTERM {
		command.Env = append(command.Env, "FT_TEST_CODEX_IGNORE_TERM=1")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-done })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("fixture did not start: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fixture startup timeout")
	}
	return socket, command
}

func assertSocketAlive(t *testing.T, socket string) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatalf("unrelated/refused server was stopped: %v", err)
	}
	conn.Close()
}

func TestUnmanagedLinuxStopsOnlySocketOwnerThroughSymlink(t *testing.T) {
	args := []string{"-c", "features.code_mode_host=true", "app-server", "--listen", "unix://"}
	socket, _ := startSocketFixture(t, "codex", args, false)
	other, _ := startSocketFixture(t, "codex", args, false)
	home := t.TempDir()
	control := filepath.Join(home, "app-server-control")
	if err := os.Mkdir(control, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(control, "app-server-control.sock")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	var diagnostics bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := recoverUnmanagedAppServer(ctx, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), "SIGTERM") {
		t.Fatalf("missing stop diagnostic: %s", diagnostics.String())
	}
	if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
		conn.Close()
		t.Fatal("target still listening")
	}
	assertSocketAlive(t, other)
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("recovery removed socket symlink: %v", err)
	}
}

func TestUnmanagedLinuxRefusesOtherProcesses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		binary string
		args   []string
	}{
		{"wrong executable", "worker", []string{"app-server", "--listen", "unix://"}},
		{"proxy", "codex", []string{"app-server", "proxy"}},
		{"managed", "codex", []string{"app-server", "--listen", "unix://", "--managed-daemon"}},
		{"exec prompt", "codex", []string{"exec", "app-server", "--listen", "unix://"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			socket, _ := startSocketFixture(t, tt.binary, tt.args, false)
			if err := terminateSocketServer(context.Background(), socket, nil); err == nil {
				t.Fatal("expected refusal")
			}
			assertSocketAlive(t, socket)
		})
	}
}

func TestUnmanagedLinuxTimeoutDoesNotForceKill(t *testing.T) {
	socket, _ := startSocketFixture(t, "codex", []string{"app-server", "--listen", "unix://"}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := terminateSocketServer(ctx, socket, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want deadline", err)
	}
	assertSocketAlive(t, socket)
}

func TestUnmanagedLinuxCancellationDoesNotSignal(t *testing.T) {
	socket, _ := startSocketFixture(t, "codex", []string{"app-server", "--listen", "unix://"}, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := terminateSocketServer(ctx, socket, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	assertSocketAlive(t, socket)
}

func TestUnmanagedLinuxRejectsSelf(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "self.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := terminateSocketServer(context.Background(), socket, nil); err == nil {
		t.Fatal("must not signal self")
	}
}

func TestReloadLinuxRefusesOwnServerBeforeDaemonStop(t *testing.T) {
	home := t.TempDir()
	control := filepath.Join(home, "app-server-control")
	if err := os.Mkdir(control, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(control, "app-server-control.sock")
	physical := filepath.Join(home, "s")
	listener, err := net.Listen("unix", physical)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Symlink(physical, socket); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	runner := &recordingRunner{run: func(_ context.Context, command Command) error {
		switch command.Args[len(command.Args)-1] {
		case "stop":
			io.WriteString(command.Stdout, `{"status":"stopped"}`)
		case "start":
			io.WriteString(command.Stdout, `{"status":"started","backend":"pid"}`)
		case "version":
			io.WriteString(command.Stdout, `{"status":"running","backend":"pid","appServerVersion":"0.165.0"}`)
		}
		return nil
	}}
	if err := ReloadDaemon(context.Background(), runner, "codex", nil, nil); err == nil {
		t.Fatal("must refuse before asking Codex to stop this command's own server")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("self-owned server invoked commands: %v", runner.commands)
	}
}

func TestUnmanagedLinuxIdentityChecks(t *testing.T) {
	args := []string{"codex", "-c", "features.code_mode_host=true", "app-server", "--listen", "unix://"}
	if err := validateDirectServer(42, 1000, 1000, "/opt/bin/codex", args); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectServer(42, 1001, 1000, "/opt/bin/codex", args); err == nil {
		t.Fatal("must reject another UID")
	}
	if err := validateDirectServer(1, 1000, 1000, "/opt/bin/codex", args); err == nil {
		t.Fatal("must reject PID 1")
	}
	if err := validateDirectServer(42, 1000, 1000, "/bin/sh", args); err == nil {
		t.Fatal("must reject spoofed argv")
	}
	ancestor, err := processIsAncestor(os.Getpid())
	if err != nil || !ancestor {
		t.Fatalf("self ancestry=%v, %v", ancestor, err)
	}
	if os.Getppid() > 1 {
		ancestor, err = processIsAncestor(os.Getppid())
		if err != nil || !ancestor {
			t.Fatalf("parent ancestry=%v, %v", ancestor, err)
		}
	}
}

func TestUnmanagedLinuxAmbiguousHomeCannotStopAnotherServer(t *testing.T) {
	args := []string{"app-server", "--listen", "unix://"}
	socketA, _ := startSocketFixture(t, "codex", args, false)
	socketB, _ := startSocketFixture(t, "codex", args, false)
	base := t.TempDir()
	for _, entry := range []struct{ home, socket string }{{filepath.Join(base, "home"), socketA}, {filepath.Join(base, "home "), socketB}} {
		control := filepath.Join(entry.home, "app-server-control")
		if err := os.MkdirAll(control, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(entry.socket, filepath.Join(control, "app-server-control.sock")); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODEX_HOME", filepath.Join(base, "home "))
	if err := recoverUnmanagedAppServer(context.Background(), nil); err == nil {
		t.Fatal("ambiguous home must be rejected")
	}
	assertSocketAlive(t, socketA)
	assertSocketAlive(t, socketB)
	runner := &recordingRunner{}
	if err := ReloadDaemon(context.Background(), runner, "codex", nil, nil); err == nil {
		t.Fatal("reload must reject ambiguous home before commands")
	}
	if len(runner.commands) != 0 {
		t.Fatal("ambiguous home invoked daemon commands")
	}
}
