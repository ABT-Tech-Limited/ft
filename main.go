// ft is a CLI that wraps common ops commands for Linux servers.
package main

import (
	"errors"
	"os"
	"os/exec"

	"github.com/ABT-Tech-Limited/ft/cmd"
)

// Set by GoReleaser via -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	if err := cmd.Execute(); err != nil {
		// Propagate the underlying command's exit code when available.
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}
