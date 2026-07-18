package aptx

import (
	"slices"
	"testing"
)

func TestUpdate(t *testing.T) {
	want := []string{"apt-get", "update"}
	if got := Update(); !slices.Equal(got, want) {
		t.Errorf("Update() = %v, want %v", got, want)
	}
}

func TestUpgrade(t *testing.T) {
	want := []string{
		"apt-get", "upgrade", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}
	if got := Upgrade(); !slices.Equal(got, want) {
		t.Errorf("Upgrade() = %v, want %v", got, want)
	}
}

func TestInstall(t *testing.T) {
	want := []string{
		"apt-get", "install", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
		"curl", "net-tools",
	}
	if got := Install("curl", "net-tools"); !slices.Equal(got, want) {
		t.Errorf("Install() = %v, want %v", got, want)
	}
}

func TestWithSudo(t *testing.T) {
	argv := []string{"apt-get", "update"}
	want := []string{"sudo", "DEBIAN_FRONTEND=noninteractive", "apt-get", "update"}
	if got := WithSudo(argv); !slices.Equal(got, want) {
		t.Errorf("WithSudo() = %v, want %v", got, want)
	}
	// The input slice must not be mutated.
	if !slices.Equal(argv, []string{"apt-get", "update"}) {
		t.Errorf("WithSudo mutated its input: %v", argv)
	}
}

func TestDefaultPackagesIncludeUserRequested(t *testing.T) {
	for _, pkg := range []string{"curl", "net-tools"} {
		if !slices.Contains(DefaultPackages, pkg) {
			t.Errorf("DefaultPackages missing %q", pkg)
		}
	}
}
