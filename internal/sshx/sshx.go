// Package sshx reads effective OpenSSH server settings.
package sshx

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// OutputFunc runs argv and returns its standard output.
type OutputFunc func(context.Context, []string) ([]byte, error)

// EffectivePorts asks OpenSSH to expand its server configuration and returns
// every configured listening port in declaration order.
func EffectivePorts(ctx context.Context, output OutputFunc) ([]int, error) {
	if output == nil {
		return nil, fmt.Errorf("nil SSH command output runner")
	}
	data, err := output(ctx, []string{"/usr/sbin/sshd", "-T"})
	if err != nil {
		return nil, fmt.Errorf("reading effective SSH configuration: %w", err)
	}
	ports, err := parseEffectivePorts(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing effective SSH configuration: %w", err)
	}
	return ports, nil
}

func parseEffectivePorts(output string) ([]int, error) {
	var configuredPorts []int
	var listenPorts []int
	hasUnqualifiedListenAddress := false
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "port":
			if len(fields) != 2 {
				return nil, fmt.Errorf("invalid SSH port output: %q", strings.TrimSpace(line))
			}
			port, err := parsePort(fields[1])
			if err != nil {
				return nil, err
			}
			configuredPorts = appendUniquePort(configuredPorts, port)
		case "listenaddress":
			if len(fields) < 2 {
				return nil, fmt.Errorf("invalid SSH listen address output: %q", strings.TrimSpace(line))
			}
			port, qualified, err := parseListenAddressPort(fields[1])
			if err != nil {
				return nil, err
			}
			if qualified {
				listenPorts = appendUniquePort(listenPorts, port)
			} else {
				hasUnqualifiedListenAddress = true
			}
		}
	}
	ports := listenPorts
	if len(listenPorts) == 0 || hasUnqualifiedListenAddress {
		for _, port := range configuredPorts {
			ports = appendUniquePort(ports, port)
		}
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("no effective SSH port reported by sshd")
	}
	return ports, nil
}

func parseListenAddressPort(address string) (int, bool, error) {
	_, value, err := net.SplitHostPort(address)
	if err != nil {
		unbracketed := strings.TrimPrefix(strings.TrimSuffix(address, "]"), "[")
		if !strings.Contains(address, ":") || net.ParseIP(unbracketed) != nil {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("invalid SSH listen address %q: %w", address, err)
	}
	port, err := parsePort(value)
	if err != nil {
		return 0, false, err
	}
	return port, true, nil
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid SSH port %q: %w", value, err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("SSH port must be between 1 and 65535: %d", port)
	}
	return port, nil
}

func appendUniquePort(ports []int, port int) []int {
	for _, existing := range ports {
		if existing == port {
			return ports
		}
	}
	return append(ports, port)
}
