package codexx

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type recordedCommand struct {
	command Command
	stdin   string
}

type recordingRunner struct {
	commands []recordedCommand
	run      func(context.Context, Command) error
}

func (r *recordingRunner) Run(ctx context.Context, command Command) error {
	input := ""
	if command.Stdin != nil {
		data, err := io.ReadAll(command.Stdin)
		if err != nil {
			return fmt.Errorf("read recorded stdin: %w", err)
		}
		input = string(data)
	}
	r.commands = append(r.commands, recordedCommand{command: command, stdin: input})
	if r.run != nil {
		command.Stdin = strings.NewReader(input)
		return r.run(ctx, command)
	}
	return nil
}

func TestExecRunnerUsesUnprivilegedCommandIOAndEnvironment(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := (ExecRunner{}).Run(context.Background(), Command{
		Path:   "/bin/sh",
		Args:   []string{"-c", `read value; printf "%s:%s" "$FT_CODEXX_TEST" "$value"; printf warning >&2`},
		Stdin:  strings.NewReader("input\n"),
		Stdout: &stdout,
		Stderr: &stderr,
		Env:    []string{"FT_CODEXX_TEST=environment"},
	})
	if err != nil {
		t.Fatalf("ExecRunner.Run() error = %v", err)
	}
	if got, want := stdout.String(), "environment:input"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "warning"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestExecRunnerPreservesContextAndNotFoundErrors(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := (ExecRunner{}).Run(ctx, Command{Path: "/bin/sh", Args: []string{"-c", "exit 0"}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ExecRunner.Run() error = %v, want context.Canceled", err)
		}
	})

	t.Run("missing executable", func(t *testing.T) {
		err := (ExecRunner{}).Run(context.Background(), Command{Path: filepath.Join(t.TempDir(), "missing")})
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("ExecRunner.Run() error = %v, want exec.ErrNotFound", err)
		}
	})
}

func TestDetectVersion(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		runErr     error
		want       string
		wantErr    string
		wantIs     error
		executable string
	}{
		{name: "current Codex output", output: "codex-cli 0.151.0\n", want: "0.151.0", executable: "/opt/bin/codex"},
		{name: "pre-release", output: "codex-cli 1.2.3-beta.1\n", want: "1.2.3-beta.1", executable: "codex"},
		{name: "unexpected output", output: "Codex version unknown", wantErr: "parse Codex version", executable: "codex"},
		{name: "not found", runErr: exec.ErrNotFound, wantErr: "not installed", wantIs: ErrNotFound, executable: "codex"},
		{name: "command failure", runErr: errors.New("exit status 1"), wantErr: "detect Codex version", executable: "codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingRunner{run: func(_ context.Context, command Command) error {
				if command.Stdout != nil {
					_, _ = io.WriteString(command.Stdout, tt.output)
				}
				return tt.runErr
			}}
			got, err := DetectVersion(context.Background(), runner, tt.executable)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DetectVersion() = %q, %v; want error containing %q", got, err, tt.wantErr)
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Errorf("DetectVersion() error = %v, want errors.Is(_, %v)", err, tt.wantIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectVersion() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("DetectVersion() = %q, want %q", got, tt.want)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("commands = %d, want 1", len(runner.commands))
			}
			command := runner.commands[0].command
			if command.Path != tt.executable || strings.Join(command.Args, " ") != "--version" {
				t.Errorf("command = %q %q, want %q --version", command.Path, command.Args, tt.executable)
			}
		})
	}
}

func TestResolveExecutableUsesPATHThenLocalFallback(t *testing.T) {
	home := t.TempDir()
	pathDir := t.TempDir()
	writeExecutable(t, filepath.Join(pathDir, "codex"))
	writeExecutable(t, filepath.Join(home, ".local", "bin", "codex"))

	tests := []struct {
		name    string
		pathEnv string
		home    string
		want    string
		wantErr error
	}{
		{name: "PATH wins", pathEnv: pathDir, home: home, want: filepath.Join(pathDir, "codex")},
		{name: "local fallback", pathEnv: t.TempDir(), home: home, want: filepath.Join(home, ".local", "bin", "codex")},
		{name: "not found", pathEnv: t.TempDir(), home: t.TempDir(), wantErr: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveExecutable(tt.pathEnv, tt.home)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveExecutable() = %q, %v; want %v", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveExecutable() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveExecutable() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveInstalledExecutablePrefersInstallerLocation(t *testing.T) {
	home := t.TempDir()
	pathDir := t.TempDir()
	customDir := t.TempDir()
	writeExecutable(t, filepath.Join(pathDir, "codex"))
	writeExecutable(t, filepath.Join(home, ".local", "bin", "codex"))
	writeExecutable(t, filepath.Join(customDir, "codex"))

	tests := []struct {
		name       string
		installDir string
		want       string
	}{
		{name: "custom install dir wins", installDir: customDir, want: filepath.Join(customDir, "codex")},
		{name: "default standalone wins over old PATH", want: filepath.Join(home, ".local", "bin", "codex")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveInstalledExecutable(pathDir, home, tt.installDir)
			if err != nil {
				t.Fatalf("ResolveInstalledExecutable() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveInstalledExecutable() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstallDownloadsOfficialScriptBeforeRunning(t *testing.T) {
	const script = "#!/bin/sh\nprintf installed\n"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/openai/codex/main/scripts/install/install.sh"; got != want {
			t.Errorf("request path = %q, want %q", got, want)
		}
		if got, want := r.Host, "raw.githubusercontent.com"; got != want {
			t.Errorf("request host = %q, want %q", got, want)
		}
		_, _ = io.WriteString(w, script)
	}))
	defer server.Close()

	runner := &recordingRunner{}
	err := Install(context.Background(), githubTestClient(t, server), runner)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("runner commands = %d, want 1", len(runner.commands))
	}
	recorded := runner.commands[0]
	if recorded.command.Path != "/bin/sh" || len(recorded.command.Args) != 0 {
		t.Errorf("installer command = %q %q, want /bin/sh with no args", recorded.command.Path, recorded.command.Args)
	}
	if recorded.stdin != script {
		t.Errorf("installer stdin = %q, want complete script %q", recorded.stdin, script)
	}
	if !containsEnv(recorded.command.Env, "CODEX_NON_INTERACTIVE=1") {
		t.Errorf("installer env = %q, want CODEX_NON_INTERACTIVE=1", recorded.command.Env)
	}
}

func TestInstallRejectsUnsafeOrInvalidDownloadsWithoutRunning(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		maxBody    bool
		wantErr    string
		wantTarget string
	}{
		{name: "non-2xx", handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusBadGateway) }, wantErr: "HTTP status 502"},
		{name: "empty body", handler: func(w http.ResponseWriter, _ *http.Request) {}, wantErr: "empty"},
		{name: "oversized body", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.CopyN(w, zeroReader{}, InstallerMaxBytes+1) }, wantErr: "exceeds"},
		{name: "redirect downgrade", handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://github.com/openai/codex/install.sh", http.StatusFound)
		}, wantErr: "HTTPS"},
		{name: "redirect leaves GitHub", handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.com/install.sh", http.StatusFound)
		}, wantErr: "GitHub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(tt.handler)
			defer server.Close()
			runner := &recordingRunner{}
			err := Install(context.Background(), githubTestClient(t, server), runner)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Install() error = %v, want error containing %q", err, tt.wantErr)
			}
			if len(runner.commands) != 0 {
				t.Errorf("runner commands = %d, want 0", len(runner.commands))
			}
		})
	}
}

func TestInstallAllowsOnlySecureRawToGitHubRedirect(t *testing.T) {
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.Host {
		case "raw.githubusercontent.com":
			http.Redirect(w, r, "https://github.com/openai/codex/raw/main/scripts/install/install.sh", http.StatusFound)
		case "github.com":
			_, _ = io.WriteString(w, "echo redirected")
		default:
			t.Errorf("unexpected host %q", r.Host)
		}
	}))
	defer server.Close()
	runner := &recordingRunner{}
	if err := Install(context.Background(), githubTestClient(t, server), runner); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if requests != 2 || len(runner.commands) != 1 {
		t.Errorf("requests = %d, commands = %d; want 2 and 1", requests, len(runner.commands))
	}
}

func TestInstallValidatesDependencies(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		client  *http.Client
		runner  Runner
		wantErr string
	}{
		{name: "nil context", client: http.DefaultClient, runner: &recordingRunner{}, wantErr: "nil context"},
		{name: "nil client", ctx: context.Background(), runner: &recordingRunner{}, wantErr: "nil HTTP client"},
		{name: "nil runner", ctx: context.Background(), client: http.DefaultClient, wantErr: "nil runner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Install(tt.ctx, tt.client, tt.runner)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Install() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func githubTestClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	serverAddress := server.Listener.Addr().String()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, serverAddress)
	}
	return &http.Client{Transport: transport}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func containsEnv(environment []string, want string) bool {
	for _, value := range environment {
		if value == want {
			return true
		}
	}
	return false
}
