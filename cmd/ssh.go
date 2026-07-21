package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
	"github.com/ABT-Tech-Limited/ft/internal/sshx"
	"github.com/ABT-Tech-Limited/ft/internal/sysx"
	"github.com/spf13/cobra"
)

type sshDependencies struct {
	detectOS       func() (osrelease.Info, error)
	detectSSHPorts func(context.Context) ([]int, error)
	queryPackages  func(context.Context, ...string) (map[string]dpkgx.Status, error)
	fileExists     func(string) (bool, error)
	output         func(context.Context, []string) ([]byte, error)
	run            func(context.Context, []string, io.Reader) error
}

func newSSHCmd(deps sshDependencies) *cobra.Command {
	sshCmd := &cobra.Command{
		Use:   "ssh",
		Short: "Manage the OpenSSH server",
		Args:  cobra.NoArgs,
	}
	sshCmd.AddCommand(newSSHPortCmd(deps))
	return sshCmd
}

func newSSHPortCmd(deps sshDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "port [port]",
		Short: "Show or change the SSH listening port",
		Long: `Show the effective SSH listening port, or change it to a new value.

Without an argument, ft reads every effective port from /usr/sbin/sshd -T.

With a port argument, ft writes ` + sshx.PortConfigPath + ` (an ft-managed
drop-in; the main sshd_config is never modified), validates the result with
sshd -t and sshd -T, then reloads systemd and restarts SSH. If validation
fails, the previous configuration is restored and SSH is not restarted.

When ufw is active, the new port must be allowed first; ft does not change
firewall rules. When Fail2Ban is installed, the ft-managed SSH jail is
updated to watch the new port.

Keep your current session open and verify the new port from a second
terminal before disconnecting.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runSSHPortShow(cmd, deps)
			}
			port, err := parseSSHPortArgument(args[0])
			if err != nil {
				return err
			}
			return runSSHPortChange(cmd, deps, port)
		},
	}
}

func parseSSHPortArgument(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid SSH port %q: %w", value, err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("SSH port must be between 1 and 65535: %d", port)
	}
	return port, nil
}

func runSSHPortShow(cmd *cobra.Command, deps sshDependencies) error {
	if _, err := deps.detectOS(); err != nil {
		return err
	}
	ports, err := deps.detectSSHPorts(cmd.Context())
	if err != nil {
		return fmt.Errorf("detecting effective SSH ports failed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Effective SSH port(s): %s\n", joinPorts(ports))
	return nil
}

func runSSHPortChange(cmd *cobra.Command, deps sshDependencies, port int) error {
	return fmt.Errorf("changing the SSH port is not implemented yet")
}

func joinPorts(ports []int) string {
	values := make([]string, len(ports))
	for i, port := range ports {
		values[i] = strconv.Itoa(port)
	}
	return strings.Join(values, ", ")
}

func init() {
	rootCmd.AddCommand(newSSHCmd(sshDependencies{
		detectOS: detectOS,
		detectSSHPorts: func(ctx context.Context) ([]int, error) {
			return sshx.EffectivePorts(ctx, sysx.OutputContext)
		},
		queryPackages: dpkgx.Query,
		fileExists: func(path string) (bool, error) {
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		},
		output: sysx.OutputContext,
		run:    sysx.RunInputContext,
	}))
}
