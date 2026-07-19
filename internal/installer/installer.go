// Package installer builds deterministic command plans for software installation.
package installer

import (
	"fmt"
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
	ServiceSteps        []Step
}

// Steps returns the service-only path for a fully installed product, or the
// complete setup and service path otherwise.
func (p Plan) Steps(alreadyInstalled bool) []Step {
	if alreadyInstalled {
		return append([]Step(nil), p.ServiceSteps...)
	}
	steps := make([]Step, 0, len(p.SetupSteps)+len(p.ServiceSteps))
	steps = append(steps, p.SetupSteps...)
	return append(steps, p.ServiceSteps...)
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
		ServiceSteps: []Step{
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
		ServiceSteps: []Step{
			{Title: "Enabling and starting Nginx", Argv: []string{"systemctl", "enable", "--now", "nginx.service"}},
		},
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
