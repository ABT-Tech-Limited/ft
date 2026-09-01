package codexx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Login authenticates Codex by passing the API key through standard input.
func Login(ctx context.Context, runner Runner, executable, apiKey string, stdout, stderr io.Writer) error {
	if ctx == nil {
		return fmt.Errorf("nil login context")
	}
	if runner == nil {
		return fmt.Errorf("nil command runner")
	}
	if strings.TrimSpace(executable) == "" {
		return fmt.Errorf("empty Codex executable")
	}

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("empty API key")
	}

	err := runner.Run(ctx, Command{
		Path:   executable,
		Args:   []string{"login", "--with-api-key"},
		Stdin:  strings.NewReader(apiKey + "\n"),
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return loginError{cause: err}
	}
	return nil
}

// loginError preserves the command failure for errors.Is without rendering a
// possibly credential-bearing error returned by an injected Runner.
type loginError struct {
	cause error
}

func (loginError) Error() string {
	return "log in to Codex: command failed"
}

func (err loginError) Is(target error) bool {
	return errors.Is(err.cause, target)
}

// Validate runs a minimal request using the configured Codex provider.
func Validate(ctx context.Context, runner Runner, executable string, stdout, stderr io.Writer) error {
	if ctx == nil {
		return fmt.Errorf("nil validation context")
	}
	if runner == nil {
		return fmt.Errorf("nil command runner")
	}
	if strings.TrimSpace(executable) == "" {
		return fmt.Errorf("empty Codex executable")
	}
	workDir, err := os.MkdirTemp("", "ft-codex-validate-*")
	if err != nil {
		return fmt.Errorf("create isolated Codex validation directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	err = runner.Run(ctx, Command{
		Path: executable,
		Dir:  workDir,
		Args: []string{
			"--strict-config",
			"exec",
			"--skip-git-repo-check",
			"--ephemeral",
			"--sandbox", "read-only",
			"--color", "never",
			"-",
		},
		Stdin:  strings.NewReader("Reply exactly OK. Do not use tools.\n"),
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return fmt.Errorf("validate Codex configuration: %w", err)
	}
	return nil
}
