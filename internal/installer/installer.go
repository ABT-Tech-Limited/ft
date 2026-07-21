// Package installer builds auditable command plans for software installation.
package installer

import (
	"fmt"
	"os"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/aptx"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
)

const (
	dockerKeyringPath     = "/etc/apt/keyrings/docker.asc"
	dockerKeyringTempPath = dockerKeyringPath + ".tmp"
	dockerSourcePath      = "/etc/apt/sources.list.d/docker.sources"
	dockerSourceTempPath  = dockerSourcePath + ".tmp"
	dockerRepository      = "https://download.docker.com/linux/ubuntu"
	fail2banConfigDir     = "/etc/fail2ban/jail.d"
	fail2banSSHConfigPath = fail2banConfigDir + "/zz-ft-sshd.conf"
)

var debianArchitectures = map[string]string{
	"amd64": "amd64",
	"arm64": "arm64",
}

// Step describes one privileged command in an installation plan.
type Step struct {
	Title string
	Argv  []string
	Input string
}

// Plan describes the packages and privileged steps for one product.
type Plan struct {
	RequiredPackages    []string
	ConflictingPackages []string
	SetupSteps          []Step
	EnsureSteps         []Step
}

// Steps returns only the always-run ensure path for a fully installed product,
// or the complete setup and ensure path otherwise.
func (p Plan) Steps(alreadyInstalled bool) []Step {
	if alreadyInstalled {
		return append([]Step(nil), p.EnsureSteps...)
	}
	steps := make([]Step, 0, len(p.SetupSteps)+len(p.EnsureSteps))
	steps = append(steps, p.SetupSteps...)
	return append(steps, p.EnsureSteps...)
}

// Docker returns the installation plan for Docker Engine from Docker's
// official Ubuntu apt repository.
func Docker(info osrelease.Info, goarch string) (Plan, error) {
	codename, err := info.Codename()
	if err != nil {
		return Plan{}, fmt.Errorf("unsupported platform for Docker: %w", err)
	}
	architecture, ok := debianArchitectures[goarch]
	if !ok {
		return Plan{}, fmt.Errorf("unsupported architecture for Docker: %s", goarch)
	}
	requiredPackages := []string{
		"docker-ce",
		"docker-ce-cli",
		"containerd.io",
		"docker-buildx-plugin",
		"docker-compose-plugin",
	}

	source := fmt.Sprintf(`Types: deb
URIs: %s
Suites: %s
Components: stable
Architectures: %s
Signed-By: %s
`, dockerRepository, codename, architecture, dockerKeyringPath)

	return Plan{
		RequiredPackages: requiredPackages,
		ConflictingPackages: []string{
			"docker.io",
			"docker-doc",
			"docker-compose",
			"docker-compose-v2",
			"podman-docker",
			"containerd",
			"runc",
		},
		SetupSteps: []Step{
			{Title: "Updating package index", Argv: aptx.Update()},
			{Title: "Installing Docker prerequisites", Argv: aptx.Install("ca-certificates", "curl")},
			{Title: "Creating Docker keyring", Argv: []string{"install", "-m", "0755", "-d", "/etc/apt/keyrings"}},
			{
				Title: "Downloading Docker signing key",
				Argv: []string{
					"curl", "--fail", "--show-error", "--silent", "--location",
					"--proto", "=https", "--tlsv1.2", "--remove-on-error",
					"--output", dockerKeyringTempPath,
					dockerRepository + "/gpg",
				},
			},
			{Title: "Securing Docker signing key", Argv: []string{"chmod", "0644", dockerKeyringTempPath}},
			{
				Title: "Activating Docker signing key",
				Argv:  []string{"mv", "-f", dockerKeyringTempPath, dockerKeyringPath},
			},
			{
				Title: "Configuring Docker repository",
				Argv: []string{
					"install", "-o", "root", "-g", "root", "-m", "0644",
					"/dev/stdin", dockerSourceTempPath,
				},
				Input: source,
			},
			{
				Title: "Activating Docker repository",
				Argv:  []string{"mv", "-f", dockerSourceTempPath, dockerSourcePath},
			},
			{Title: "Updating Docker package index", Argv: aptx.Update()},
			{Title: "Installing Docker Engine", Argv: aptx.Install(requiredPackages...)},
		},
		EnsureSteps: []Step{
			{Title: "Enabling and starting Docker", Argv: []string{"systemctl", "enable", "--now", "docker.service"}},
		},
	}, nil
}

// Nginx returns the installation plan for Ubuntu's nginx package.
func Nginx() Plan {
	packages := []string{"nginx"}
	return Plan{
		RequiredPackages: packages,
		SetupSteps: []Step{
			{Title: "Updating package index", Argv: aptx.Update()},
			{Title: "Installing Nginx", Argv: aptx.Install(packages...)},
		},
		EnsureSteps: []Step{
			{Title: "Enabling and starting Nginx", Argv: []string{"systemctl", "enable", "--now", "nginx.service"}},
		},
	}
}

// Fail2ban returns the installation plan for Ubuntu's Fail2Ban package and
// its default nftables-backed SSH jail.
func Fail2ban() Plan {
	return fail2banPlan("")
}

// Fail2banWithSSHPort returns a plan that atomically installs an ft-managed
// SSH jail port override and restarts Fail2Ban so a running service applies it.
func Fail2banWithSSHPort(sshPort int) (Plan, error) {
	return Fail2banWithSSHPorts([]int{sshPort})
}

// Fail2banWithSSHPorts returns a plan for every effective SSH listening port.
func Fail2banWithSSHPorts(sshPorts []int) (Plan, error) {
	if len(sshPorts) == 0 {
		return Plan{}, fmt.Errorf("at least one SSH port is required")
	}
	seen := make(map[int]struct{}, len(sshPorts))
	portValues := make([]string, 0, len(sshPorts))
	for _, sshPort := range sshPorts {
		if sshPort < 1 || sshPort > 65535 {
			return Plan{}, fmt.Errorf("SSH port must be between 1 and 65535: %d", sshPort)
		}
		if _, ok := seen[sshPort]; ok {
			continue
		}
		seen[sshPort] = struct{}{}
		portValues = append(portValues, fmt.Sprintf("%d", sshPort))
	}
	return fail2banPlan(strings.Join(portValues, ",")), nil
}

func fail2banPlan(sshPorts string) Plan {
	packages := []string{"fail2ban", "nftables"}
	ensureSteps := make([]Step, 0, 6)
	if sshPorts != "" {
		configTempPath := fmt.Sprintf("%s.%d.tmp", fail2banSSHConfigPath, os.Getpid())
		config := "# Managed by ft. Re-run ft install fail2ban to update.\n\n" +
			"[sshd]\n" +
			"enabled = true\n" +
			"port = " + sshPorts + "\n"
		ensureSteps = append(ensureSteps,
			Step{
				Title: "Creating Fail2Ban configuration directory",
				Argv:  []string{"install", "-m", "0755", "-d", fail2banConfigDir},
			},
			Step{
				Title: "Writing Fail2Ban SSH port configuration",
				Argv: []string{
					"install", "-o", "root", "-g", "root", "-m", "0644",
					"/dev/stdin", configTempPath,
				},
				Input: config,
			},
			Step{
				Title: "Activating Fail2Ban SSH port configuration",
				Argv:  []string{"mv", "-f", configTempPath, fail2banSSHConfigPath},
			},
		)
	}
	ensureSteps = append(ensureSteps, Step{
		Title: "Validating Fail2Ban configuration",
		Argv:  []string{"/usr/bin/fail2ban-client", "-t"},
	})
	if sshPorts == "" {
		ensureSteps = append(ensureSteps, Step{
			Title: "Enabling and starting Fail2Ban",
			Argv:  []string{"systemctl", "enable", "--now", "fail2ban.service"},
		})
	} else {
		ensureSteps = append(ensureSteps,
			Step{
				Title: "Enabling Fail2Ban at boot",
				Argv:  []string{"systemctl", "enable", "fail2ban.service"},
			},
			Step{
				Title: "Restarting Fail2Ban with SSH port configuration",
				Argv:  []string{"systemctl", "restart", "fail2ban.service"},
			},
		)
	}
	return Plan{
		RequiredPackages: packages,
		SetupSteps: []Step{
			{Title: "Updating package index", Argv: aptx.Update()},
			{Title: "Installing Fail2Ban and nftables", Argv: aptx.Install(packages...)},
		},
		EnsureSteps: ensureSteps,
	}
}

// DockerGroupStep returns the idempotent command that grants a user Docker's
// root-equivalent group access while preserving existing supplementary groups.
func DockerGroupStep(username string) (Step, error) {
	if strings.TrimSpace(username) == "" {
		return Step{}, fmt.Errorf("empty username for Docker group membership")
	}
	return Step{
		Title: "Ensuring " + username + " belongs to the Docker group",
		Argv: []string{
			"/usr/sbin/usermod",
			"--append",
			"--groups", "docker",
			"--",
			username,
		},
	}, nil
}
