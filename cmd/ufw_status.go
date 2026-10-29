package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/ufwx"
	"github.com/spf13/cobra"
)

func newUFWStatusCmd(deps ufwDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the ufw firewall state and every rule",
		Long: `Show the current ufw state and every rule in detail.

The report covers the firewall state (active/inactive, default policies,
logging), whether ufw starts at boot, whether ufw-docker gates
Docker-published ports, whether the 10-minute Cloudflare refresh is
registered, and the full numbered rule list with a count of the
ft-managed Cloudflare rules.

This command only reads state; it never changes anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUFWStatus(cmd, deps)
		},
	}
}

func runUFWStatus(cmd *cobra.Command, deps ufwDependencies) error {
	if _, err := deps.detectOS(); err != nil {
		return err
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	statuses, err := deps.queryPackages(ctx, "ufw")
	if err != nil {
		return fmt.Errorf("checking package status failed: %w", err)
	}
	switch statuses["ufw"] {
	case dpkgx.Installed:
	case dpkgx.Broken:
		fmt.Fprintln(out, "ufw package state: broken; the report below may be incomplete until it is repaired.")
	default:
		fmt.Fprintln(out, "ufw is not installed; install it with \"sudo apt-get update && sudo apt-get install ufw\".")
		return nil
	}

	verbose, verboseRaw, err := readVerboseStatus(ctx, deps)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Firewall state")
	printVerboseHeader(out, verboseRaw)

	bootState := "not enabled"
	if enabled, err := deps.output(ctx, []string{"systemctl", "is-enabled", "ufw"}); err == nil &&
		strings.TrimSpace(string(enabled)) == "enabled" {
		bootState = "enabled"
	}
	fmt.Fprintf(out, "Startup at boot: %s (systemctl is-enabled ufw)\n", bootState)

	ufwDockerReady, err := hasUFWDockerMarker(ctx, deps)
	if err != nil {
		return err
	}
	if ufwDockerReady {
		fmt.Fprintln(out, "ufw-docker: set up (Docker-published ports are gated by ufw)")
	} else {
		fmt.Fprintln(out, "ufw-docker: not set up (Docker-published ports bypass ufw)")
	}

	cronState := "not registered (run \"ft ufw cloudflare\" to set it up)"
	if lines := managedCronLines(ctx, deps); lines != "" {
		if minute, hourly := parseCronSchedule(lines); minute >= 0 {
			if hourly {
				cronState = fmt.Sprintf("registered (hourly at minute %d, %s); re-run \"ft ufw cloudflare\" to migrate to every 10 minutes", minute, cronFilePath)
			} else {
				cronState = fmt.Sprintf("registered (every 10 minutes, offset %d, %s)", minute, cronFilePath)
			}
		} else {
			cronState = fmt.Sprintf("unrecognized schedule (%s); re-run \"ft ufw cloudflare\" to repair", cronFilePath)
		}
	}
	fmt.Fprintf(out, "Cloudflare refresh: %s\n", cronState)

	if !verbose.Active {
		fmt.Fprintln(out, "\nufw is inactive; allow your SSH port first, then enable it with \"sudo ufw enable\".")
		return nil
	}

	fmt.Fprintln(out, "\n==> Rules")
	statusOutput, err := deps.output(ctx, []string{"ufw", "status", "numbered"})
	if err != nil {
		return fmt.Errorf("reading numbered ufw rules failed: %w", err)
	}
	rules, err := ufwx.ParseNumberedRules(string(statusOutput))
	if err != nil {
		return fmt.Errorf("parsing numbered ufw rules failed: %w", err)
	}
	if len(rules) == 0 {
		fmt.Fprintln(out, "No rules defined.")
		return nil
	}
	inbound, routed := 0, 0
	for _, rule := range rules {
		fmt.Fprintln(out, rule.Raw)
		if rule.Comment != managedRuleComment {
			continue
		}
		switch rule.Direction {
		case "", "IN":
			inbound++
		case "FWD":
			routed++
		}
	}
	fmt.Fprintf(out, "\n%d rules total; ft-managed Cloudflare rules: %d inbound + %d routed\n", len(rules), inbound, routed)
	return nil
}

// printVerboseHeader prints the state lines of "ufw status verbose" (Status,
// Logging, Default, New profiles) and stops before the rule table, which the
// numbered listing renders in more detail.
func printVerboseHeader(out io.Writer, verboseOutput string) {
	for line := range strings.SplitSeq(verboseOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "To") && strings.Contains(trimmed, "Action") {
			break
		}
		fmt.Fprintln(out, trimmed)
	}
}
