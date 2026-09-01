// Package codexx installs and configures the OpenAI Codex CLI without
// requiring privileged command execution.
package codexx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrNotFound reports that no usable Codex executable could be found.
var ErrNotFound = errors.New("Codex is not installed")

// Command describes one non-privileged process invocation.
type Command struct {
	Path   string
	Args   []string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Env    []string
}

// Runner executes non-privileged commands.
type Runner interface {
	Run(context.Context, Command) error
}

// ExecRunner executes commands as the current user with os/exec.
type ExecRunner struct{}

// Run executes command and waits for it to finish or for ctx to be cancelled.
func (ExecRunner) Run(ctx context.Context, command Command) error {
	if ctx == nil {
		return fmt.Errorf("nil command context")
	}
	if strings.TrimSpace(command.Path) == "" {
		return fmt.Errorf("empty command path")
	}

	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdin = command.Stdin
	cmd.Stdout = command.Stdout
	cmd.Stderr = command.Stderr
	cmd.Env = append(os.Environ(), command.Env...)
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("run %q: %w", command.Path, ctxErr)
		}
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("run %q: %w: %w", command.Path, exec.ErrNotFound, err)
		}
		return fmt.Errorf("run %q: %w", command.Path, err)
	}
	return nil
}

var codexVersionPattern = regexp.MustCompile(`^codex-cli\s+([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)\s*$`)

// DetectVersion runs codex --version and returns the parsed CLI version.
func DetectVersion(ctx context.Context, runner Runner, executable string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("nil version context")
	}
	if runner == nil {
		return "", fmt.Errorf("nil command runner")
	}
	if strings.TrimSpace(executable) == "" {
		return "", fmt.Errorf("empty Codex executable")
	}

	var output strings.Builder
	err := runner.Run(ctx, Command{Path: executable, Args: []string{"--version"}, Stdout: &output})
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return "", fmt.Errorf("detect Codex version: %w", err)
	}
	match := codexVersionPattern.FindStringSubmatch(output.String())
	if len(match) != 2 {
		return "", fmt.Errorf("parse Codex version from %q", strings.TrimSpace(output.String()))
	}
	return match[1], nil
}

// ResolveExecutable finds codex in pathEnv, then in $HOME/.local/bin.
func ResolveExecutable(pathEnv, home string) (string, error) {
	for _, directory := range filepath.SplitList(pathEnv) {
		if directory == "" {
			directory = "."
		}
		candidate := filepath.Join(directory, "codex")
		if executableFile(candidate) {
			return candidate, nil
		}
	}
	if strings.TrimSpace(home) != "" {
		candidate := filepath.Join(home, ".local", "bin", "codex")
		if executableFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w", ErrNotFound)
}

// ResolveInstalledExecutable finds the binary managed by the official
// installer before considering an older command earlier on PATH.
func ResolveInstalledExecutable(pathEnv, home, installDir string) (string, error) {
	if strings.TrimSpace(installDir) != "" {
		candidate := filepath.Join(installDir, "codex")
		if executableFile(candidate) {
			return candidate, nil
		}
	}
	if strings.TrimSpace(home) != "" {
		candidate := filepath.Join(home, ".local", "bin", "codex")
		if executableFile(candidate) {
			return candidate, nil
		}
	}
	return ResolveExecutable(pathEnv, home)
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
