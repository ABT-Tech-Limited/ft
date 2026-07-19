// Package dpkgx queries Debian package installation state.
package dpkgx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const statusFormat = "${Package}\t${db:Status-Want}\t${db:Status-Eflag}\t${db:Status-Status}\n"

// Status describes whether a package is absent, fully installed, or in a
// partial/broken dpkg state.
type Status uint8

const (
	// Absent means the package is missing or only configuration files remain.
	Absent Status = iota
	// Installed means dpkg reports an error-free installed state.
	Installed
	// Broken means dpkg knows the package but it is not fully configured.
	Broken
)

type queryRunner func(context.Context, []string) ([]byte, int, error)

// Query returns the dpkg state of every requested package. Missing packages
// are returned as Absent rather than errors.
func Query(ctx context.Context, packages ...string) (map[string]Status, error) {
	return query(ctx, runDpkgQuery, packages...)
}

func query(ctx context.Context, runner queryRunner, packages ...string) (map[string]Status, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	if runner == nil {
		return nil, fmt.Errorf("nil dpkg-query runner")
	}

	statuses := make(map[string]Status, len(packages))
	requested := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		if strings.TrimSpace(pkg) == "" {
			return nil, fmt.Errorf("empty package name")
		}
		if _, exists := requested[pkg]; exists {
			return nil, fmt.Errorf("duplicate package name %q", pkg)
		}
		requested[pkg] = struct{}{}
		statuses[pkg] = Absent
	}
	if len(packages) == 0 {
		return statuses, nil
	}

	argv := append([]string{
		"dpkg-query",
		"--admindir=/var/lib/dpkg",
		"--show",
		"--showformat=" + statusFormat,
	}, packages...)
	output, exitCode, runErr := runner(ctx, argv)
	if exitCode != 0 && exitCode != 1 {
		if runErr != nil {
			return nil, fmt.Errorf("dpkg-query failed: %w", runErr)
		}
		return nil, fmt.Errorf("dpkg-query failed with exit code %d", exitCode)
	}
	if exitCode == 0 && runErr != nil {
		return nil, fmt.Errorf("dpkg-query failed: %w", runErr)
	}

	seen := make(map[string]struct{}, len(packages))
	for _, line := range strings.Split(string(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("malformed dpkg-query record %q", line)
		}
		pkg, want, eflag, current := fields[0], fields[1], fields[2], fields[3]
		if _, ok := requested[pkg]; !ok {
			return nil, fmt.Errorf("unexpected package %q in dpkg-query output", pkg)
		}
		if _, duplicate := seen[pkg]; duplicate {
			return nil, fmt.Errorf("duplicate dpkg-query record for package %q", pkg)
		}
		seen[pkg] = struct{}{}

		status, err := parseStatus(want, eflag, current)
		if err != nil {
			return nil, fmt.Errorf("parse package %q status: %w", pkg, err)
		}
		statuses[pkg] = status
	}

	if exitCode == 0 {
		for _, pkg := range packages {
			if _, ok := seen[pkg]; !ok {
				return nil, fmt.Errorf("missing status for package %q", pkg)
			}
		}
	}
	return statuses, nil
}

func parseStatus(want, eflag, current string) (Status, error) {
	switch want {
	case "unknown", "install", "hold", "deinstall", "purge":
	default:
		return Absent, fmt.Errorf("unknown dpkg desired state %q", want)
	}

	switch eflag {
	case "ok":
	case "reinstreq":
		return Broken, nil
	default:
		return Absent, fmt.Errorf("unknown dpkg error flag %q", eflag)
	}

	switch current {
	case "not-installed", "config-files":
		return Absent, nil
	case "installed":
		return Installed, nil
	case "half-installed", "unpacked", "half-configured", "triggers-awaited", "triggers-pending":
		return Broken, nil
	default:
		return Absent, fmt.Errorf("unknown dpkg package status %q", current)
	}
}

func runDpkgQuery(ctx context.Context, argv []string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = hostDpkgEnvironment()
	output, err := cmd.Output()
	if err == nil {
		return output, 0, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return output, -1, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if diagnostic := strings.TrimSpace(string(exitErr.Stderr)); diagnostic != "" {
			err = fmt.Errorf("%s: %w", diagnostic, err)
		}
		return output, exitErr.ExitCode(), err
	}
	return output, -1, err
}

func hostDpkgEnvironment() []string {
	environment := os.Environ()
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name == "DPKG_ROOT" || name == "DPKG_ADMINDIR" {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
