package sshx

import (
	"fmt"
	"os"

	"github.com/ABT-Tech-Limited/ft/internal/installer"
)

const sshdConfigDir = "/etc/ssh/sshd_config.d"

// PortConfigPath is the ft-managed sshd port drop-in. The 00- prefix is
// load-bearing: sshd honours the first Port value it sees (first-match
// wins), so this file must sort before 50-cloud-init.conf and friends —
// the opposite direction from fail2ban's last-wins zz- prefix in jail.d.
const PortConfigPath = sshdConfigDir + "/00-ft-port.conf"

func portConfigTempPath() string {
	return fmt.Sprintf("%s.%d.tmp", PortConfigPath, os.Getpid())
}

func portConfigBackupPath() string {
	return fmt.Sprintf("%s.%d.bak", PortConfigPath, os.Getpid())
}

// PortSteps returns the privileged steps that atomically install the
// ft-managed sshd port drop-in.
func PortSteps(port int) ([]installer.Step, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("SSH port must be between 1 and 65535: %d", port)
	}
	config := fmt.Sprintf("# Managed by ft. Re-run ft ssh port to update.\nPort %d\n", port)
	tempPath := portConfigTempPath()
	return []installer.Step{
		{
			Title: "Creating SSH configuration directory",
			Argv:  []string{"install", "-m", "0755", "-d", sshdConfigDir},
		},
		{
			Title: "Writing SSH port configuration",
			Argv: []string{
				"install", "-o", "root", "-g", "root", "-m", "0644",
				"/dev/stdin", tempPath,
			},
			Input: config,
		},
		{
			Title: "Activating SSH port configuration",
			Argv:  []string{"mv", "-f", tempPath, PortConfigPath},
		},
	}, nil
}

// SyntaxCheckStep validates the complete sshd configuration on disk.
func SyntaxCheckStep() installer.Step {
	return installer.Step{
		Title: "Validating SSH configuration syntax",
		Argv:  []string{"/usr/sbin/sshd", "-t"},
	}
}

// BackupStep preserves the current drop-in before it is overwritten.
func BackupStep() installer.Step {
	return installer.Step{
		Title: "Backing up current SSH port configuration",
		Argv:  []string{"cp", "-p", PortConfigPath, portConfigBackupPath()},
	}
}

// RestoreStep reinstates the drop-in preserved by BackupStep.
func RestoreStep() installer.Step {
	return installer.Step{
		Title: "Restoring previous SSH port configuration",
		Argv:  []string{"mv", "-f", portConfigBackupPath(), PortConfigPath},
	}
}

// RemoveConfigStep deletes the drop-in when no previous version existed.
func RemoveConfigStep() installer.Step {
	return installer.Step{
		Title: "Removing SSH port configuration",
		Argv:  []string{"rm", "-f", PortConfigPath},
	}
}

// RemoveBackupStep discards the backup after a successful change.
func RemoveBackupStep() installer.Step {
	return installer.Step{
		Title: "Removing SSH port configuration backup",
		Argv:  []string{"rm", "-f", portConfigBackupPath()},
	}
}

// RestartSteps returns the reload and restart sequence for the OpenSSH
// server. socketActivated selects Ubuntu's socket-activation path, where
// ssh.socket owns the listening port and must be restarted before
// ssh.service picks up the new file descriptors.
func RestartSteps(socketActivated bool) []installer.Step {
	steps := []installer.Step{
		{Title: "Reloading systemd configuration", Argv: []string{"systemctl", "daemon-reload"}},
	}
	if socketActivated {
		steps = append(steps, installer.Step{
			Title: "Restarting SSH socket",
			Argv:  []string{"systemctl", "restart", "ssh.socket"},
		})
	}
	return append(steps, installer.Step{
		Title: "Restarting SSH service",
		Argv:  []string{"systemctl", "restart", "ssh.service"},
	})
}
