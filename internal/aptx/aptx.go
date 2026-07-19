// Package aptx builds and runs apt-get command lines for ft.
//
// Command construction is kept as pure functions so it can be unit
// tested on any platform; only Run touches the underlying system.
package aptx

import (
	"context"
	"io"

	"github.com/ABT-Tech-Limited/ft/internal/sysx"
)

// DefaultPackages is the tool set installed by `ft init`.
var DefaultPackages = []string{
	"curl",
	"wget",
	"git",
	"vim",
	"unzip",
	"zip",
	"tar",
	"net-tools",
	"htop",
	"ca-certificates",
	"gnupg",
	"lsb-release",
	"software-properties-common",
}

// dpkgOpts keeps dpkg from prompting about conffile changes during
// unattended upgrades: prefer the default answer, keep the old file.
var dpkgOpts = []string{
	"-o", "Dpkg::Options::=--force-confdef",
	"-o", "Dpkg::Options::=--force-confold",
}

// Update returns the argv for refreshing the package index.
func Update() []string {
	return []string{"apt-get", "update"}
}

// Upgrade returns the argv for a non-interactive full upgrade.
func Upgrade() []string {
	return append([]string{"apt-get", "upgrade", "-y"}, dpkgOpts...)
}

// Install returns the argv for installing the given packages.
func Install(pkgs ...string) []string {
	argv := append([]string{"apt-get", "install", "-y"}, dpkgOpts...)
	return append(argv, pkgs...)
}

// WithSudo prefixes argv with sudo, forwarding DEBIAN_FRONTEND across
// the sudo boundary. The input slice is not modified.
func WithSudo(argv []string) []string {
	return sysx.WithSudo(argv)
}

// Run executes argv with output streamed to the terminal. When not
// running as root the command is wrapped with sudo (stdin stays
// attached so sudo can prompt for a password).
func Run(argv []string) error {
	return sysx.Run(argv)
}

// RunContext executes argv with output streamed to the terminal.
func RunContext(ctx context.Context, argv []string) error {
	return sysx.RunContext(ctx, argv)
}

// RunInputContext executes argv with custom stdin and terminal output.
func RunInputContext(ctx context.Context, argv []string, stdin io.Reader) error {
	return sysx.RunInputContext(ctx, argv, stdin)
}
