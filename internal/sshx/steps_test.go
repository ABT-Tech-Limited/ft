package sshx

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/installer"
)

func TestPortSteps(t *testing.T) {
	tempPath := fmt.Sprintf("/etc/ssh/sshd_config.d/00-ft-port.conf.%d.tmp", os.Getpid())
	steps, err := PortSteps(10222)
	if err != nil {
		t.Fatalf("PortSteps() error = %v", err)
	}
	want := []installer.Step{
		{
			Title: "Creating SSH configuration directory",
			Argv:  []string{"install", "-m", "0755", "-d", "/etc/ssh/sshd_config.d"},
		},
		{
			Title: "Writing SSH port configuration",
			Argv: []string{
				"install", "-o", "root", "-g", "root", "-m", "0644",
				"/dev/stdin", tempPath,
			},
			Input: "# Managed by ft. Re-run ft ssh port to update.\nPort 10222\n",
		},
		{
			Title: "Activating SSH port configuration",
			Argv:  []string{"mv", "-f", tempPath, "/etc/ssh/sshd_config.d/00-ft-port.conf"},
		},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("PortSteps() mismatch\ngot:  %#v\nwant: %#v", steps, want)
	}
}

func TestPortStepsRejectsInvalidPorts(t *testing.T) {
	for _, port := range []int{0, -1, 65536} {
		if _, err := PortSteps(port); err == nil || !strings.Contains(err.Error(), "between 1 and 65535") {
			t.Errorf("PortSteps(%d) error = %v, want range error", port, err)
		}
	}
}

func TestFixedSteps(t *testing.T) {
	backupPath := fmt.Sprintf("/etc/ssh/sshd_config.d/00-ft-port.conf.%d.bak", os.Getpid())
	tests := []struct {
		name string
		got  installer.Step
		want installer.Step
	}{
		{
			name: "syntax check",
			got:  SyntaxCheckStep(),
			want: installer.Step{Title: "Validating SSH configuration syntax", Argv: []string{"/usr/sbin/sshd", "-t"}},
		},
		{
			name: "backup",
			got:  BackupStep(),
			want: installer.Step{
				Title: "Backing up current SSH port configuration",
				Argv:  []string{"cp", "-p", "/etc/ssh/sshd_config.d/00-ft-port.conf", backupPath},
			},
		},
		{
			name: "restore",
			got:  RestoreStep(),
			want: installer.Step{
				Title: "Restoring previous SSH port configuration",
				Argv:  []string{"mv", "-f", backupPath, "/etc/ssh/sshd_config.d/00-ft-port.conf"},
			},
		},
		{
			name: "remove config",
			got:  RemoveConfigStep(),
			want: installer.Step{
				Title: "Removing SSH port configuration",
				Argv:  []string{"rm", "-f", "/etc/ssh/sshd_config.d/00-ft-port.conf"},
			},
		},
		{
			name: "remove backup",
			got:  RemoveBackupStep(),
			want: installer.Step{
				Title: "Removing SSH port configuration backup",
				Argv:  []string{"rm", "-f", backupPath},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("step mismatch\ngot:  %#v\nwant: %#v", tt.got, tt.want)
			}
		})
	}
}

func TestRestartSteps(t *testing.T) {
	reload := installer.Step{Title: "Reloading systemd configuration", Argv: []string{"systemctl", "daemon-reload"}}
	restartSocket := installer.Step{Title: "Restarting SSH socket", Argv: []string{"systemctl", "restart", "ssh.socket"}}
	restartService := installer.Step{Title: "Restarting SSH service", Argv: []string{"systemctl", "restart", "ssh.service"}}

	if got, want := RestartSteps(true), []installer.Step{reload, restartSocket, restartService}; !reflect.DeepEqual(got, want) {
		t.Errorf("RestartSteps(true) mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
	if got, want := RestartSteps(false), []installer.Step{reload, restartService}; !reflect.DeepEqual(got, want) {
		t.Errorf("RestartSteps(false) mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}
