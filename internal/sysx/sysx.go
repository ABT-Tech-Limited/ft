// Package sysx runs privileged system commands with terminal I/O attached.
package sysx

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// WithSudo prefixes argv with sudo and preserves the non-interactive apt
// environment across the privilege boundary. The input slice is not modified.
func WithSudo(argv []string) []string {
	return append([]string{"sudo", "DEBIAN_FRONTEND=noninteractive"}, argv...)
}

// Run executes argv with a background context and terminal I/O attached.
func Run(argv []string) error {
	return RunContext(context.Background(), argv)
}

// RunContext executes argv with terminal I/O attached.
func RunContext(ctx context.Context, argv []string) error {
	return RunInputContext(ctx, argv, os.Stdin)
}

// RunInputContext executes argv with the provided stdin and terminal output.
// Non-root callers are elevated through sudo.
func RunInputContext(ctx context.Context, argv []string, stdin io.Reader) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	if ctx == nil {
		return fmt.Errorf("nil command context")
	}
	if stdin == nil {
		stdin = os.Stdin
	}

	if os.Geteuid() != 0 {
		argv = WithSudo(argv)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", argv[0], err)
	}
	return nil
}
