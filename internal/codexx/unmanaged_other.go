//go:build !linux

package codexx

import (
	"context"
	"fmt"
	"io"
)

func checkReloadOrigin(_ context.Context, _ string) error { return nil }

func terminateSocketServer(_ context.Context, socket string, _ io.Writer) error {
	return fmt.Errorf("automatic unmanaged app-server recovery is supported only on Linux; stop the client or service owning %q, then run 'ft codex reload'", socket)
}
