// Package ufwx reads the ufw firewall state for TCP ports.
package ufwx

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// OutputFunc runs argv and returns its standard output.
type OutputFunc func(context.Context, []string) ([]byte, error)

// Report describes whether ufw is active and whether it admits TCP traffic
// on the inspected port.
type Report struct {
	Active      bool
	PortAllowed bool
}

// Inspect runs ufw status and reports the firewall state for one TCP port.
// Any error means the state is unknown; callers refuse to proceed rather
// than risk locking the user out.
func Inspect(ctx context.Context, output OutputFunc, port int) (Report, error) {
	if output == nil {
		return Report{}, fmt.Errorf("nil ufw command output runner")
	}
	if port < 1 || port > 65535 {
		return Report{}, fmt.Errorf("port must be between 1 and 65535: %d", port)
	}
	data, err := output(ctx, []string{"ufw", "status"})
	if err != nil {
		return Report{}, fmt.Errorf("reading ufw status: %w", err)
	}
	report, err := parseStatus(string(data), port)
	if err != nil {
		return Report{}, fmt.Errorf("parsing ufw status: %w", err)
	}
	return report, nil
}

func parseStatus(output string, port int) (Report, error) {
	report := Report{}
	statusSeen := false
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(trimmed, "Status:"); ok {
			switch strings.TrimSpace(value) {
			case "active":
				report.Active = true
			case "inactive":
				report.Active = false
			default:
				return Report{}, fmt.Errorf("unknown ufw status %q", strings.TrimSpace(value))
			}
			statusSeen = true
			continue
		}
		if report.Active && ruleAllowsPort(trimmed, port) {
			report.PortAllowed = true
		}
	}
	if !statusSeen {
		return Report{}, fmt.Errorf("no ufw status line found")
	}
	return report, nil
}

// ruleAllowsPort reports whether one ufw rule line admits TCP traffic on
// port. Application-profile rules (e.g. "Nginx Full ALLOW Anywhere") never
// match: their ports are invisible in ufw status output, so the caller asks
// the user for an explicit rule instead.
func ruleAllowsPort(line string, port int) bool {
	fields := make([]string, 0, 4)
	for _, field := range strings.Fields(line) {
		if field == "(v6)" {
			continue
		}
		fields = append(fields, field)
	}
	if len(fields) < 2 {
		return false
	}
	switch fields[1] {
	case "ALLOW", "LIMIT":
	default:
		return false
	}
	return portSpecCovers(fields[0], port)
}

func portSpecCovers(spec string, port int) bool {
	ports := spec
	if before, after, ok := strings.Cut(spec, "/"); ok {
		if after != "tcp" {
			return false
		}
		ports = before
	}
	for _, item := range strings.Split(ports, ",") {
		if from, to, ok := strings.Cut(item, ":"); ok {
			start, startErr := strconv.Atoi(from)
			end, endErr := strconv.Atoi(to)
			if startErr == nil && endErr == nil && port >= start && port <= end {
				return true
			}
			continue
		}
		if value, err := strconv.Atoi(item); err == nil && value == port {
			return true
		}
	}
	return false
}
