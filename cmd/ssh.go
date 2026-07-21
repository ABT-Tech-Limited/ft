package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/installer"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
	"github.com/ABT-Tech-Limited/ft/internal/sshx"
	"github.com/ABT-Tech-Limited/ft/internal/sysx"
	"github.com/ABT-Tech-Limited/ft/internal/ufwx"
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
	if _, err := deps.detectOS(); err != nil {
		return err
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	currentPorts, err := deps.detectSSHPorts(ctx)
	if err != nil {
		return fmt.Errorf("detecting effective SSH ports failed: %w", err)
	}
	alreadyActive := len(currentPorts) == 1 && currentPorts[0] == port

	fmt.Fprintln(out, "\n==> Checking firewall and package status")
	statuses, err := deps.queryPackages(ctx, "ufw", "fail2ban")
	if err != nil {
		return fmt.Errorf("checking package status failed: %w", err)
	}

	if alreadyActive {
		fmt.Fprintf(out, "SSH already listens only on port %d; skipping sshd configuration.\n", port)
	} else {
		if statuses["ufw"] != dpkgx.Absent {
			report, err := ufwx.Inspect(ctx, deps.output, port)
			if err != nil {
				return fmt.Errorf(
					"checking ufw state failed: %w; refusing to change the SSH port while the firewall state is unknown",
					err,
				)
			}
			if report.Active && !report.PortAllowed {
				return fmt.Errorf(
					"ufw is active but TCP port %d is not allowed; run \"sudo ufw allow %d/tcp\" first, then retry",
					port, port,
				)
			}
		}
		if err := applySSHPortChange(cmd, deps, port); err != nil {
			return err
		}
	}

	// Print the safety reminder BEFORE syncing fail2ban: sshd has already
	// restarted onto the new port by now, so a fail2ban sync failure must not
	// swallow the "keep this session open" guidance. The text is correct on
	// the idempotent path too, so ordering it unconditionally is fine.
	fmt.Fprintf(out, "\nSSH port is set to %d.\n", port)
	fmt.Fprintln(out, "IMPORTANT: keep this session open. From a new terminal, verify that")
	fmt.Fprintf(out, "\"ssh -p %d user@host\" works before you disconnect.\n", port)
	// Old ufw rules for the previous port stay in place; ft never edits the
	// firewall. Print a concrete delete example for each replaced port.
	for _, old := range currentPorts {
		if old == port {
			continue
		}
		fmt.Fprintf(out, "Old-port cleanup example: sudo ufw delete allow %d/tcp\n", old)
	}

	if err := syncFail2banSSHPort(cmd, deps, statuses["fail2ban"], port); err != nil {
		return err
	}
	return nil
}

func joinPorts(ports []int) string {
	values := make([]string, len(ports))
	for i, port := range ports {
		values[i] = strconv.Itoa(port)
	}
	return strings.Join(values, ", ")
}

// applySSHPortChange installs the drop-in, validates it, and restarts SSH.
// Validation failures roll the file back and never restart: the running
// sshd keeps serving its old configuration, so the system stays reachable.
func applySSHPortChange(cmd *cobra.Command, deps sshDependencies, port int) error {
	hadPrevious, err := deps.fileExists(sshx.PortConfigPath)
	if err != nil {
		return fmt.Errorf("checking existing SSH port configuration failed: %w", err)
	}
	steps, err := sshx.PortSteps(port)
	if err != nil {
		return err
	}
	if hadPrevious {
		steps = append([]installer.Step{sshx.BackupStep()}, steps...)
	}
	if err := runSSHSteps(cmd, deps, steps); err != nil {
		return err
	}

	if err := verifySSHConfiguration(cmd, deps, port); err != nil {
		rollbackSSHPortChange(cmd, deps, hadPrevious)
		return err
	}
	if hadPrevious {
		if err := runSSHSteps(cmd, deps, []installer.Step{sshx.RemoveBackupStep()}); err != nil {
			return err
		}
	}
	return restartSSH(cmd, deps)
}

func verifySSHConfiguration(cmd *cobra.Command, deps sshDependencies, port int) error {
	if err := runSSHSteps(cmd, deps, []installer.Step{sshx.SyntaxCheckStep()}); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\n==> Verifying effective SSH configuration")
	ports, err := deps.detectSSHPorts(cmd.Context())
	if err != nil {
		return fmt.Errorf("re-reading effective SSH ports failed: %w", err)
	}
	if len(ports) != 1 || ports[0] != port {
		return fmt.Errorf(
			"sshd reports port(s) %s instead of %d alone; Port values accumulate across sshd_config and its drop-ins — remove the other Port directives, then retry (ft manages only %s)",
			joinPorts(ports), port, sshx.PortConfigPath,
		)
	}
	return nil
}

// rollbackSSHPortChange restores the pre-change drop-in state. Failures are
// reported on stdout rather than returned: the caller's validation error is
// the actionable one, and the running sshd is untouched either way.
func rollbackSSHPortChange(cmd *cobra.Command, deps sshDependencies, hadPrevious bool) {
	step := sshx.RemoveConfigStep()
	if hadPrevious {
		step = sshx.RestoreStep()
	}
	if err := runSSHSteps(cmd, deps, []installer.Step{step}); err != nil {
		fmt.Fprintf(
			cmd.OutOrStdout(),
			"Rollback failed: %v\nManually inspect %s before restarting SSH.\n",
			err, sshx.PortConfigPath,
		)
	}
}

func restartSSH(cmd *cobra.Command, deps sshDependencies) error {
	_, socketErr := deps.output(cmd.Context(), []string{"systemctl", "is-enabled", "ssh.socket"})
	for _, step := range sshx.RestartSteps(socketErr == nil) {
		if err := runSSHSteps(cmd, deps, []installer.Step{step}); err != nil {
			return fmt.Errorf(
				"%w\nSSH may be down, but your current session stays alive. Inspect \"journalctl -u ssh\"; to roll back, remove %s and restart ssh",
				err, sshx.PortConfigPath,
			)
		}
	}
	return nil
}

func syncFail2banSSHPort(cmd *cobra.Command, deps sshDependencies, status dpkgx.Status, port int) error {
	out := cmd.OutOrStdout()
	switch status {
	case dpkgx.Installed:
	case dpkgx.Broken:
		fmt.Fprintln(out, "Warning: fail2ban is in a broken package state; skipped SSH jail sync. Repair it, then re-run \"ft install fail2ban\".")
		return nil
	default:
		fmt.Fprintln(out, "Fail2Ban is not installed; skipped SSH jail sync.")
		return nil
	}
	plan, err := installer.Fail2banWithSSHPort(port)
	if err != nil {
		return fmt.Errorf("building Fail2Ban SSH port configuration failed: %w", err)
	}
	fmt.Fprintln(out, "\n==> Syncing Fail2Ban SSH jail")
	return runSSHSteps(cmd, deps, plan.EnsureSteps)
}

func runSSHSteps(cmd *cobra.Command, deps sshDependencies, steps []installer.Step) error {
	for _, step := range steps {
		fmt.Fprintf(cmd.OutOrStdout(), "\n==> %s\n", step.Title)
		var input io.Reader
		if step.Input != "" {
			input = strings.NewReader(step.Input)
		}
		if err := deps.run(cmd.Context(), step.Argv, input); err != nil {
			return fmt.Errorf("%s failed: %w", lowerFirst(step.Title), err)
		}
	}
	return nil
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
