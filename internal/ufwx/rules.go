package ufwx

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// VerboseStatus reports the firewall state from "ufw status verbose".
type VerboseStatus struct {
	Active              bool
	DefaultIncomingDeny bool
}

// ParseVerboseStatus reads "ufw status verbose" output. The default incoming
// policy is only trusted when explicitly reported; an active firewall without
// a readable policy line is an error so callers refuse to act on unknown state.
func ParseVerboseStatus(output string) (VerboseStatus, error) {
	status := VerboseStatus{}
	statusSeen := false
	defaultSeen := false
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(trimmed, "Status:"); ok {
			switch strings.TrimSpace(value) {
			case "active":
				status.Active = true
			case "inactive":
				status.Active = false
			default:
				return VerboseStatus{}, fmt.Errorf("unknown ufw status %q", strings.TrimSpace(value))
			}
			statusSeen = true
			continue
		}
		value, ok := strings.CutPrefix(trimmed, "Default:")
		if !ok {
			continue
		}
		for segment := range strings.SplitSeq(value, ",") {
			segment = strings.TrimSpace(segment)
			if !strings.Contains(segment, "(incoming)") {
				continue
			}
			policy, _, _ := strings.Cut(segment, " ")
			switch policy {
			case "deny", "reject":
				status.DefaultIncomingDeny = true
			case "allow":
				status.DefaultIncomingDeny = false
			default:
				return VerboseStatus{}, fmt.Errorf("unknown default incoming policy %q", policy)
			}
			defaultSeen = true
		}
	}
	if !statusSeen {
		return VerboseStatus{}, fmt.Errorf("no ufw status line found")
	}
	if !status.Active {
		return VerboseStatus{}, nil
	}
	if !defaultSeen {
		return VerboseStatus{}, fmt.Errorf("no default policy line for incoming traffic found")
	}
	return status, nil
}

// NumberedRule is one rule line from "ufw status numbered". IPv6 markers are
// stripped from To and From; Raw preserves the original line for display.
type NumberedRule struct {
	Number    int
	To        string
	Action    string
	Direction string
	From      string
	Comment   string
	Raw       string
}

// ParseNumberedRules reads "ufw status numbered" output into rules.
func ParseNumberedRules(output string) ([]NumberedRule, error) {
	statusSeen := false
	rules := make([]NumberedRule, 0, 16)
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Status:") {
			statusSeen = true
			continue
		}
		if !strings.HasPrefix(trimmed, "[") {
			continue
		}
		rule, err := parseNumberedRule(trimmed)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if !statusSeen {
		return nil, fmt.Errorf("no ufw status line found")
	}
	return rules, nil
}

func parseNumberedRule(line string) (NumberedRule, error) {
	end := strings.Index(line, "]")
	if end < 0 {
		return NumberedRule{}, fmt.Errorf("malformed rule line %q", line)
	}
	number, err := strconv.Atoi(strings.TrimSpace(line[1:end]))
	if err != nil {
		return NumberedRule{}, fmt.Errorf("invalid rule number in %q: %w", line, err)
	}
	body, comment, _ := strings.Cut(line[end+1:], "#")

	fields := make([]string, 0, 8)
	for field := range strings.FieldsSeq(body) {
		if field == "(v6)" || field == "(out)" {
			continue
		}
		fields = append(fields, field)
	}
	actionIndex := -1
	for i, field := range fields {
		switch field {
		case "ALLOW", "DENY", "REJECT", "LIMIT":
			actionIndex = i
		}
		if actionIndex >= 0 {
			break
		}
	}
	if actionIndex < 0 {
		return NumberedRule{}, fmt.Errorf("rule %q has no action", line)
	}
	fromIndex := actionIndex + 1
	direction := ""
	if fromIndex < len(fields) {
		switch fields[fromIndex] {
		case "IN", "OUT", "FWD":
			direction = fields[fromIndex]
			fromIndex++
		}
	}
	return NumberedRule{
		Number:    number,
		To:        strings.Join(fields[:actionIndex], " "),
		Action:    fields[actionIndex],
		Direction: direction,
		From:      strings.Join(fields[fromIndex:], " "),
		Comment:   strings.TrimSpace(comment),
		Raw:       line,
	}, nil
}

// AppPortsFunc resolves a ufw application profile name to its port specs.
type AppPortsFunc func(name string) ([]string, error)

// NextConflict returns the first rule that must be removed before only the
// intended sources can reach the given TCP ports: every rule tagged with
// managedComment (rebuilt from scratch on each run), and every foreign
// ALLOW/LIMIT rule for inbound or routed traffic whose port target covers one
// of the ports. All-port rules (no port component) are never selected; they
// are surfaced through RuleWarnings instead. Unresolvable application
// profiles are errors: an unknown profile could silently keep a port open.
func NextConflict(rules []NumberedRule, ports []int, managedComment string, resolveApp AppPortsFunc) (NumberedRule, bool, error) {
	for _, rule := range rules {
		if managedComment != "" && rule.Comment == managedComment {
			return rule, true, nil
		}
		if rule.Action != "ALLOW" && rule.Action != "LIMIT" {
			continue
		}
		if rule.Direction == "OUT" {
			continue
		}
		kind, spec, profile := classifyTarget(rule.To)
		switch kind {
		case targetPortSpec:
			if specCoversAny(spec, ports) {
				return rule, true, nil
			}
		case targetAppProfile:
			if resolveApp == nil {
				return NumberedRule{}, false, fmt.Errorf("nil application profile resolver")
			}
			specs, err := resolveApp(profile)
			if err != nil {
				return NumberedRule{}, false, fmt.Errorf("resolving ufw application profile %q: %w", profile, err)
			}
			for _, appSpec := range specs {
				if specCoversAny(appSpec, ports) {
					return rule, true, nil
				}
			}
		}
	}
	return NumberedRule{}, false, nil
}

// RuleWarnings lists kept rules the user should review: all-port admits still
// reach the ports from non-Cloudflare sources, and deny/reject rules placed
// before the managed allows can block Cloudflare itself (ufw applies the
// first matching rule).
func RuleWarnings(rules []NumberedRule, ports []int) []string {
	var warnings []string
	for _, rule := range rules {
		if rule.Direction == "OUT" {
			continue
		}
		kind, spec, _ := classifyTarget(rule.To)
		switch rule.Action {
		case "ALLOW", "LIMIT":
			if kind == targetAllPorts {
				warnings = append(warnings, fmt.Sprintf(
					"kept rule %q: it admits every port from %s, including the Cloudflare-only ports",
					rule.Raw, rule.From,
				))
			}
		case "DENY", "REJECT":
			blocking := kind == targetAllPorts ||
				kind == targetAppProfile ||
				(kind == targetPortSpec && specCoversAny(spec, ports))
			if blocking {
				warnings = append(warnings, fmt.Sprintf(
					"kept rule %q: it may block Cloudflare traffic (ufw applies the first matching rule)",
					rule.Raw,
				))
			}
		}
	}
	return warnings
}

// AppInfoPorts extracts the port specs from "ufw app info <profile>" output.
func AppInfoPorts(output string) ([]string, error) {
	inPorts := false
	var specs []string
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inPorts {
			if trimmed == "Ports:" {
				inPorts = true
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		for spec := range strings.SplitSeq(trimmed, "|") {
			spec = strings.TrimSpace(spec)
			if spec == "" || !isPortSpec(spec) {
				return nil, fmt.Errorf("unparseable port entry %q in ufw app info output", trimmed)
			}
			specs = append(specs, spec)
		}
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no Ports section in ufw app info output")
	}
	return specs, nil
}

type targetKind int

const (
	// targetAllPorts is a To column without any port component ("Anywhere"
	// or a bare address): the rule spans every port.
	targetAllPorts targetKind = iota
	// targetPortSpec is a numeric port target such as "80,443/tcp".
	targetPortSpec
	// targetAppProfile is a named application profile such as "Nginx Full".
	targetAppProfile
)

// classifyTarget splits a To column into its port semantics. Interface
// scoping ("on eth0") never changes which ports a rule covers, so it is
// dropped before classification.
func classifyTarget(to string) (kind targetKind, spec string, profile string) {
	tokens := strings.Fields(to)
	if len(tokens) >= 2 && tokens[len(tokens)-2] == "on" {
		tokens = tokens[:len(tokens)-2]
	}
	if len(tokens) == 0 {
		return targetAllPorts, "", ""
	}
	if tokens[0] == "Anywhere" || isAddress(tokens[0]) {
		rest := tokens[1:]
		if len(rest) == 0 {
			return targetAllPorts, "", ""
		}
		if len(rest) == 1 && isPortSpec(rest[0]) {
			return targetPortSpec, rest[0], ""
		}
		return targetAppProfile, "", strings.Join(rest, " ")
	}
	if len(tokens) == 1 && isPortSpec(tokens[0]) {
		return targetPortSpec, tokens[0], ""
	}
	return targetAppProfile, "", strings.Join(tokens, " ")
}

func specCoversAny(spec string, ports []int) bool {
	for _, port := range ports {
		if portSpecCovers(spec, port) {
			return true
		}
	}
	return false
}

func isAddress(value string) bool {
	if _, err := netip.ParseAddr(value); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(value)
	return err == nil
}

// isPortSpec reports whether value has the shape of a ufw port target:
// comma-separated ports or from:to ranges, optionally followed by /protocol.
func isPortSpec(value string) bool {
	ports := value
	if before, after, ok := strings.Cut(value, "/"); ok {
		if after == "" {
			return false
		}
		for _, r := range after {
			if r < 'a' || r > 'z' {
				return false
			}
		}
		ports = before
	}
	if ports == "" {
		return false
	}
	for item := range strings.SplitSeq(ports, ",") {
		if from, to, ok := strings.Cut(item, ":"); ok {
			if !isPortNumber(from) || !isPortNumber(to) {
				return false
			}
			continue
		}
		if !isPortNumber(item) {
			return false
		}
	}
	return true
}

func isPortNumber(value string) bool {
	_, err := strconv.Atoi(value)
	return err == nil
}
