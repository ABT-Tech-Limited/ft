package cmd

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/cloudflarex"
	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
	"github.com/ABT-Tech-Limited/ft/internal/sysx"
	"github.com/ABT-Tech-Limited/ft/internal/ufwx"
	"github.com/spf13/cobra"
)

const (
	managedRuleComment  = "ft-cloudflare"
	cloudflarePortsSpec = "80,443"
	afterRulesPath      = "/etc/ufw/after.rules"
	ufwDockerMarker     = "BEGIN UFW AND DOCKER"
	// maxRuleDeletions bounds the delete-requery loop; real rule sets are far
	// smaller, so hitting it means ufw is not actually removing rules.
	maxRuleDeletions = 500
)

var cloudflareTCPPorts = []int{80, 443}

type ufwDependencies struct {
	detectOS      func() (osrelease.Info, error)
	queryPackages func(context.Context, ...string) (map[string]dpkgx.Status, error)
	output        func(context.Context, []string) ([]byte, error)
	run           func(context.Context, []string, io.Reader) error
	fetch         cloudflarex.FetchFunc
}

func newUFWCmd(deps ufwDependencies) *cobra.Command {
	ufwCmd := &cobra.Command{
		Use:   "ufw",
		Short: "Manage the ufw firewall",
		Args:  cobra.NoArgs,
	}
	ufwCmd.AddCommand(newUFWOnlyCloudflareCmd(deps))
	return ufwCmd
}

func newUFWOnlyCloudflareCmd(deps ufwDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "only-cloudflare",
		Short: "Allow only Cloudflare IPs to reach TCP ports 80/443",
		Long: `Restrict TCP ports 80/443 to Cloudflare's published IP ranges.

ft fetches https://www.cloudflare.com/ips-v4 and ips-v6, validates the
lists, removes every other ufw rule that admits TCP traffic to ports 80/443
(including application profile rules such as "Nginx Full"), then adds one
allow rule per Cloudflare range tagged "` + managedRuleComment + `".
Re-running the command refreshes the ranges: tagged rules are rebuilt from
the freshly fetched lists.

Docker publishes ports through iptables directly, bypassing ufw's INPUT
rules, so plain ufw rules cannot protect Docker-published ports. When
Docker is installed, ft requires ufw-docker
(https://github.com/chaifeng/ufw-docker) and refuses to run without it
rather than create a false sense of security. When ufw-docker is set up,
ft also adds "ufw route allow" rules so Docker-published ports are
restricted to Cloudflare as well.

Requirements: ufw must be installed and active with a "deny (incoming)"
default policy. ft never runs "ufw enable" and never touches SSH or other
port rules. Rules admitting every port (e.g. an admin address allowed to
anything) are kept and reported for manual review.

Cloudflare updates its ranges over time; re-run this command to refresh.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUFWOnlyCloudflare(cmd, deps)
		},
	}
}

func runUFWOnlyCloudflare(cmd *cobra.Command, deps ufwDependencies) error {
	if _, err := deps.detectOS(); err != nil {
		return err
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "\n==> Checking system state")
	statuses, err := deps.queryPackages(ctx, "ufw", "docker-ce", "docker.io")
	if err != nil {
		return fmt.Errorf("checking package status failed: %w", err)
	}
	switch statuses["ufw"] {
	case dpkgx.Installed:
	case dpkgx.Broken:
		return fmt.Errorf("ufw is in a broken package state; repair it, then retry")
	default:
		return fmt.Errorf(
			"ufw is not installed; install it with \"sudo apt-get update && sudo apt-get install ufw\", " +
				"allow your SSH port, enable the firewall, then re-run this command",
		)
	}

	verboseOutput, err := deps.output(ctx, []string{"ufw", "status", "verbose"})
	if err != nil {
		return fmt.Errorf("reading ufw status failed: %w", err)
	}
	verbose, err := ufwx.ParseVerboseStatus(string(verboseOutput))
	if err != nil {
		return fmt.Errorf("parsing ufw status failed: %w", err)
	}
	if !verbose.Active {
		return fmt.Errorf(
			"ufw is inactive, so rules would not take effect; allow your SSH port first " +
				"(e.g. \"sudo ufw allow 22/tcp\"), run \"sudo ufw enable\" yourself, then re-run this command " +
				"(ft never enables the firewall for you)",
		)
	}
	if !verbose.DefaultIncomingDeny {
		return fmt.Errorf(
			"ufw's default incoming policy is not deny, so other addresses would still reach ports %s; "+
				"run \"sudo ufw default deny incoming\" (make sure your SSH port is allowed first), then re-run this command",
			cloudflarePortsSpec,
		)
	}
	fmt.Fprintln(out, "ufw is active with a default-deny incoming policy.")

	afterRules, afterRulesErr := deps.output(ctx, []string{"cat", afterRulesPath})
	ufwDockerReady := afterRulesErr == nil && strings.Contains(string(afterRules), ufwDockerMarker)
	dockerInstalled := statuses["docker-ce"] != dpkgx.Absent || statuses["docker.io"] != dpkgx.Absent
	if dockerInstalled && !ufwDockerReady {
		return fmt.Errorf(
			"Docker is installed but ufw-docker is not set up in %s.\n"+
				"Docker publishes ports through iptables directly, bypassing ufw, so without ufw-docker\n"+
				"ports %s stay open to everyone no matter what ufw rules say.\n"+
				"Install ufw-docker once (as root), then re-run this command:\n"+
				"  wget -O /usr/local/bin/ufw-docker https://github.com/chaifeng/ufw-docker/raw/master/ufw-docker\n"+
				"  chmod +x /usr/local/bin/ufw-docker\n"+
				"  ufw-docker install\n"+
				"  systemctl restart ufw",
			afterRulesPath, cloudflarePortsSpec,
		)
	}
	if ufwDockerReady {
		fmt.Fprintln(out, "ufw-docker detected; route rules for Docker-published ports will be managed too.")
	}

	fmt.Fprintln(out, "\n==> Fetching Cloudflare IP ranges")
	ranges, err := cloudflarex.Ranges(ctx, deps.fetch)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Fetched %d IPv4 + %d IPv6 Cloudflare ranges.\n", len(ranges.V4), len(ranges.V6))

	fmt.Fprintln(out, "\n==> Removing conflicting firewall rules")
	warnings, err := deleteConflictingRules(cmd, deps)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "\n==> Allowing %d Cloudflare ranges on ports %s\n", len(ranges.V4)+len(ranges.V6), cloudflarePortsSpec)
	for _, cidr := range ranges.All() {
		allow := []string{
			"ufw", "allow", "proto", "tcp",
			"from", cidr, "to", "any", "port", cloudflarePortsSpec,
			"comment", managedRuleComment,
		}
		if err := runFirewallCommand(ctx, deps, allow); err != nil {
			return err
		}
		if !ufwDockerReady {
			continue
		}
		route := append([]string{"ufw", "route"}, allow[1:]...)
		if err := runFirewallCommand(ctx, deps, route); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "\n==> Reloading ufw")
	if err := runFirewallCommand(ctx, deps, []string{"ufw", "reload"}); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nPorts %s now accept traffic from Cloudflare ranges only (%d IPv4 + %d IPv6).\n",
		cloudflarePortsSpec, len(ranges.V4), len(ranges.V6))
	if ufwDockerReady {
		fmt.Fprintln(out, "Docker-published ports are covered through ufw-docker route rules.")
	}
	for _, warning := range warnings {
		fmt.Fprintf(out, "Warning: %s\n", warning)
	}
	fmt.Fprintf(out, "Cloudflare updates its IP ranges over time; re-run \"ft ufw only-cloudflare\" to refresh.\n")
	fmt.Fprintln(out, "SSH and all other port rules were left untouched.")
	return nil
}

// deleteConflictingRules removes rules one at a time, re-reading the numbered
// list after every delete because ufw renumbers the remaining rules. It
// returns review warnings for the rules that were deliberately kept.
func deleteConflictingRules(cmd *cobra.Command, deps ufwDependencies) ([]string, error) {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	resolveApp := func(name string) ([]string, error) {
		infoOutput, err := deps.output(ctx, []string{"ufw", "app", "info", name})
		if err != nil {
			return nil, fmt.Errorf("reading profile info: %w", err)
		}
		return ufwx.AppInfoPorts(string(infoOutput))
	}
	deleted := 0
	for {
		statusOutput, err := deps.output(ctx, []string{"ufw", "status", "numbered"})
		if err != nil {
			return nil, fmt.Errorf("reading numbered ufw rules failed: %w", err)
		}
		rules, err := ufwx.ParseNumberedRules(string(statusOutput))
		if err != nil {
			return nil, fmt.Errorf("parsing numbered ufw rules failed: %w", err)
		}
		conflict, found, err := ufwx.NextConflict(rules, cloudflareTCPPorts, managedRuleComment, resolveApp)
		if err != nil {
			return nil, err
		}
		if !found {
			if deleted == 0 {
				fmt.Fprintln(out, "No conflicting rules found.")
			}
			return ufwx.RuleWarnings(rules, cloudflareTCPPorts), nil
		}
		if deleted >= maxRuleDeletions {
			return nil, fmt.Errorf(
				"rule deletion did not converge after %d attempts; inspect \"ufw status numbered\" manually",
				maxRuleDeletions,
			)
		}
		fmt.Fprintf(out, "Deleting rule %s\n", conflict.Raw)
		if err := runFirewallCommand(ctx, deps, []string{"ufw", "--force", "delete", strconv.Itoa(conflict.Number)}); err != nil {
			return nil, err
		}
		deleted++
	}
}

// runFirewallCommand wraps rule-changing failures with their fail-closed
// consequence: with a default-deny policy an aborted run can leave ports
// 80/443 closed to everyone, never open to everyone.
func runFirewallCommand(ctx context.Context, deps ufwDependencies, argv []string) error {
	if err := deps.run(ctx, argv, nil); err != nil {
		return fmt.Errorf(
			"%q failed: %w; firewall changes are incomplete and web traffic on ports %s may be blocked until this command succeeds — fix the cause and re-run",
			strings.Join(argv, " "), err, cloudflarePortsSpec,
		)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newUFWCmd(ufwDependencies{
		detectOS:      detectOS,
		queryPackages: dpkgx.Query,
		output:        sysx.OutputContext,
		run:           sysx.RunInputContext,
		fetch:         cloudflarex.Fetch,
	}))
}
