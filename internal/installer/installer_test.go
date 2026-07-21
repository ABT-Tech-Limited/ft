package installer

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
)

func TestDocker(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		goarch   string
		codename string
		debArch  string
	}{
		{
			name:     "Ubuntu 24.04 on amd64",
			version:  "24.04",
			goarch:   "amd64",
			codename: "noble",
			debArch:  "amd64",
		},
		{
			name:     "Ubuntu 26.04 on arm64",
			version:  "26.04",
			goarch:   "arm64",
			codename: "resolute",
			debArch:  "arm64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Docker(osrelease.Info{ID: "ubuntu", VersionID: tt.version}, tt.goarch)
			if err != nil {
				t.Fatalf("Docker() error = %v", err)
			}

			source := "Types: deb\n" +
				"URIs: https://download.docker.com/linux/ubuntu\n" +
				"Suites: " + tt.codename + "\n" +
				"Components: stable\n" +
				"Architectures: " + tt.debArch + "\n" +
				"Signed-By: /etc/apt/keyrings/docker.asc\n"
			wantSetup := []Step{
				{Title: "Updating package index", Argv: []string{"apt-get", "update"}},
				{
					Title: "Installing Docker prerequisites",
					Argv: []string{
						"apt-get", "install", "-y",
						"-o", "Dpkg::Options::=--force-confdef",
						"-o", "Dpkg::Options::=--force-confold",
						"ca-certificates", "curl",
					},
				},
				{Title: "Creating Docker keyring", Argv: []string{"install", "-m", "0755", "-d", "/etc/apt/keyrings"}},
				{
					Title: "Downloading Docker signing key",
					Argv: []string{
						"curl", "--fail", "--show-error", "--silent", "--location",
						"--proto", "=https", "--tlsv1.2", "--remove-on-error",
						"--output", "/etc/apt/keyrings/docker.asc.tmp",
						"https://download.docker.com/linux/ubuntu/gpg",
					},
				},
				{Title: "Securing Docker signing key", Argv: []string{"chmod", "0644", "/etc/apt/keyrings/docker.asc.tmp"}},
				{
					Title: "Activating Docker signing key",
					Argv:  []string{"mv", "-f", "/etc/apt/keyrings/docker.asc.tmp", "/etc/apt/keyrings/docker.asc"},
				},
				{
					Title: "Configuring Docker repository",
					Argv: []string{
						"install", "-o", "root", "-g", "root", "-m", "0644",
						"/dev/stdin", "/etc/apt/sources.list.d/docker.sources.tmp",
					},
					Input: source,
				},
				{
					Title: "Activating Docker repository",
					Argv: []string{
						"mv", "-f", "/etc/apt/sources.list.d/docker.sources.tmp", "/etc/apt/sources.list.d/docker.sources",
					},
				},
				{Title: "Updating Docker package index", Argv: []string{"apt-get", "update"}},
				{
					Title: "Installing Docker Engine",
					Argv: []string{
						"apt-get", "install", "-y",
						"-o", "Dpkg::Options::=--force-confdef",
						"-o", "Dpkg::Options::=--force-confold",
						"docker-ce", "docker-ce-cli", "containerd.io",
						"docker-buildx-plugin", "docker-compose-plugin",
					},
				},
			}
			wantEnsure := []Step{
				{Title: "Enabling and starting Docker", Argv: []string{"systemctl", "enable", "--now", "docker.service"}},
			}
			want := Plan{
				RequiredPackages: []string{
					"docker-ce",
					"docker-ce-cli",
					"containerd.io",
					"docker-buildx-plugin",
					"docker-compose-plugin",
				},
				ConflictingPackages: []string{
					"docker.io",
					"docker-doc",
					"docker-compose",
					"docker-compose-v2",
					"podman-docker",
					"containerd",
					"runc",
				},
				SetupSteps:  wantSetup,
				EnsureSteps: wantEnsure,
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("Docker() plan mismatch\ngot:  %#v\nwant: %#v", got, want)
			}
			assertPlanSteps(t, got, wantSetup, wantEnsure)
		})
	}
}

func TestDockerRejectsUnsupportedPlatform(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		version string
		goarch  string
		wantErr string
	}{
		{name: "Ubuntu version", id: "ubuntu", version: "22.04", goarch: "amd64", wantErr: "unsupported system"},
		{name: "distribution", id: "debian", version: "24.04", goarch: "amd64", wantErr: "unsupported system"},
		{name: "Go architecture", id: "ubuntu", version: "24.04", goarch: "386", wantErr: "unsupported architecture"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Docker(osrelease.Info{ID: tt.id, VersionID: tt.version}, tt.goarch)
			if err == nil {
				t.Fatalf("Docker() error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Docker() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if !reflect.DeepEqual(plan, Plan{}) {
				t.Errorf("Docker() plan = %#v, want zero value", plan)
			}
		})
	}
}

func TestNginx(t *testing.T) {
	wantSetup := []Step{
		{Title: "Updating package index", Argv: []string{"apt-get", "update"}},
		{
			Title: "Installing Nginx",
			Argv: []string{
				"apt-get", "install", "-y",
				"-o", "Dpkg::Options::=--force-confdef",
				"-o", "Dpkg::Options::=--force-confold",
				"nginx",
			},
		},
	}
	wantEnsure := []Step{
		{Title: "Enabling and starting Nginx", Argv: []string{"systemctl", "enable", "--now", "nginx.service"}},
	}
	want := Plan{
		RequiredPackages: []string{"nginx"},
		SetupSteps:       wantSetup,
		EnsureSteps:      wantEnsure,
	}

	if got := Nginx(); !reflect.DeepEqual(got, want) {
		t.Errorf("Nginx() plan mismatch\ngot:  %#v\nwant: %#v", got, want)
	} else {
		assertPlanSteps(t, got, wantSetup, wantEnsure)
	}
}

func TestFail2ban(t *testing.T) {
	wantSetup := []Step{
		{Title: "Updating package index", Argv: []string{"apt-get", "update"}},
		{
			Title: "Installing Fail2Ban and nftables",
			Argv: []string{
				"apt-get", "install", "-y",
				"-o", "Dpkg::Options::=--force-confdef",
				"-o", "Dpkg::Options::=--force-confold",
				"fail2ban", "nftables",
			},
		},
	}
	wantEnsure := []Step{
		{
			Title: "Validating Fail2Ban configuration",
			Argv:  []string{"/usr/bin/fail2ban-client", "-t"},
		},
		{
			Title: "Enabling and starting Fail2Ban",
			Argv:  []string{"systemctl", "enable", "--now", "fail2ban.service"},
		},
	}
	want := Plan{
		RequiredPackages: []string{"fail2ban", "nftables"},
		SetupSteps:       wantSetup,
		EnsureSteps:      wantEnsure,
	}

	if got := Fail2ban(); !reflect.DeepEqual(got, want) {
		t.Errorf("Fail2ban() plan mismatch\ngot:  %#v\nwant: %#v", got, want)
	} else {
		assertPlanSteps(t, got, wantSetup, wantEnsure)
	}
}

func TestFail2banWithSSHPorts(t *testing.T) {
	base := Fail2ban()
	tempPath := fmt.Sprintf("/etc/fail2ban/jail.d/zz-ft-sshd.conf.%d.tmp", os.Getpid())
	got, err := Fail2banWithSSHPorts([]int{22, 10222, 22})
	if err != nil {
		t.Fatalf("Fail2banWithSSHPorts() error = %v", err)
	}
	wantEnsure := []Step{
		{
			Title: "Creating Fail2Ban configuration directory",
			Argv:  []string{"install", "-m", "0755", "-d", "/etc/fail2ban/jail.d"},
		},
		{
			Title: "Writing Fail2Ban SSH port configuration",
			Argv: []string{
				"install", "-o", "root", "-g", "root", "-m", "0644",
				"/dev/stdin", tempPath,
			},
			Input: "# Managed by ft. Re-run ft install fail2ban to update.\n\n[sshd]\nenabled = true\nport = 22,10222\n",
		},
		{
			Title: "Activating Fail2Ban SSH port configuration",
			Argv: []string{
				"mv", "-f",
				tempPath,
				"/etc/fail2ban/jail.d/zz-ft-sshd.conf",
			},
		},
		{
			Title: "Validating Fail2Ban configuration",
			Argv:  []string{"/usr/bin/fail2ban-client", "-t"},
		},
		{
			Title: "Enabling Fail2Ban at boot",
			Argv:  []string{"systemctl", "enable", "fail2ban.service"},
		},
		{
			Title: "Restarting Fail2Ban with SSH port configuration",
			Argv:  []string{"systemctl", "restart", "fail2ban.service"},
		},
	}
	want := Plan{
		RequiredPackages: base.RequiredPackages,
		SetupSteps:       base.SetupSteps,
		EnsureSteps:      wantEnsure,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fail2banWithSSHPorts() plan mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
	assertPlanSteps(t, got, base.SetupSteps, wantEnsure)
}

func TestFail2banWithSSHPortsRejectsInvalidPorts(t *testing.T) {
	tests := []struct {
		name  string
		ports []int
		want  string
	}{
		{name: "empty", want: "at least one SSH port"},
		{name: "negative", ports: []int{-1}, want: "between 1 and 65535"},
		{name: "zero", ports: []int{0}, want: "between 1 and 65535"},
		{name: "too large", ports: []int{65536}, want: "between 1 and 65535"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Fail2banWithSSHPorts(tt.ports)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Fail2banWithSSHPorts() plan=%#v error=%v, want error containing %q", plan, err, tt.want)
			}
			if !reflect.DeepEqual(plan, Plan{}) {
				t.Errorf("Fail2banWithSSHPorts() plan = %#v, want zero value", plan)
			}
		})
	}
}

func TestDockerGroupStep(t *testing.T) {
	tests := []struct {
		name     string
		username string
	}{
		{name: "regular username", username: "ubuntu"},
		{name: "option-like username remains an argument", username: "-operator"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DockerGroupStep(tt.username)
			if err != nil {
				t.Fatalf("DockerGroupStep() error = %v", err)
			}
			want := Step{
				Title: "Ensuring " + tt.username + " belongs to the Docker group",
				Argv: []string{
					"/usr/sbin/usermod",
					"--append",
					"--groups", "docker",
					"--",
					tt.username,
				},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("DockerGroupStep() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestDockerGroupStepRejectsEmptyUsername(t *testing.T) {
	for _, username := range []string{"", "   "} {
		t.Run(username, func(t *testing.T) {
			got, err := DockerGroupStep(username)
			if err == nil {
				t.Fatal("DockerGroupStep() error = nil, want error")
			}
			if !strings.Contains(err.Error(), "empty username") {
				t.Errorf("DockerGroupStep() error = %q, want empty username", err)
			}
			if !reflect.DeepEqual(got, Step{}) {
				t.Errorf("DockerGroupStep() = %#v, want zero Step", got)
			}
		})
	}
}

func assertPlanSteps(t *testing.T, plan Plan, wantSetup, wantEnsure []Step) {
	t.Helper()
	wantFull := append(append([]Step(nil), wantSetup...), wantEnsure...)
	if got := plan.Steps(false); !reflect.DeepEqual(got, wantFull) {
		t.Errorf("Steps(false) = %#v, want %#v", got, wantFull)
	}
	if got := plan.Steps(true); !reflect.DeepEqual(got, wantEnsure) {
		t.Errorf("Steps(true) = %#v, want %#v", got, wantEnsure)
	}
}
