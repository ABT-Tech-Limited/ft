package cmd

import (
	"context"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"os"
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

	// ufw-docker is pinned to one commit and checksum-verified: the script
	// runs as root, so ft never executes whatever master happens to be.
	ufwDockerCommit   = "020a8699f95592561f254d8d4ad1bb40d401dfc7"
	ufwDockerSHA256   = "643e56b080567c567b4aa28650196849b2a2da5dd0473fd3e5216b0886035ab0"
	ufwDockerURL      = "https://raw.githubusercontent.com/chaifeng/ufw-docker/" + ufwDockerCommit + "/ufw-docker"
	ufwDockerBinPath  = "/usr/local/bin/ufw-docker"
	ufwDockerTempPath = ufwDockerBinPath + ".tmp"

	// Cron temp files carry a dot so cron ignores them until the rename.
	cronFilePath = "/etc/cron.d/ft-ufw-cloudflare"
	cronTempPath = cronFilePath + ".tmp"
	cronLogPath  = "/var/log/ft-ufw-cloudflare.log"

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
	executable    func() (string, error)
	// cronMinute returns the minute (1-59) for a fresh hourly cron entry.
	cronMinute func() int
	fetch      cloudflarex.FetchFunc
}

func newUFWCmd(deps ufwDependencies) *cobra.Command {
	ufwCmd := &cobra.Command{
		Use:   "ufw",
		Short: "Manage the ufw firewall",
		Args:  cobra.NoArgs,
	}
	ufwCmd.AddCommand(newUFWCloudflareCmd(deps))
	ufwCmd.AddCommand(newUFWStatusCmd(deps))
	ufwCmd.AddCommand(newUFWReloadCmd(deps))
	return ufwCmd
}

// ensureUFWReady turns a missing or broken ufw package into an actionable
// refusal shared by every rule-touching subcommand.
func ensureUFWReady(statuses map[string]dpkgx.Status) error {
	switch statuses["ufw"] {
	case dpkgx.Installed:
		return nil
	case dpkgx.Broken:
		return fmt.Errorf("ufw is in a broken package state; repair it, then retry")
	default:
		return fmt.Errorf(
			"ufw is not installed; install it with \"sudo apt-get update && sudo apt-get install ufw\", " +
				"allow your SSH port, enable the firewall, then re-run this command",
		)
	}
}

// readVerboseStatus reads and parses "ufw status verbose", returning the raw
// output as well for callers that display it.
func readVerboseStatus(ctx context.Context, deps ufwDependencies) (ufwx.VerboseStatus, string, error) {
	verboseOutput, err := deps.output(ctx, []string{"ufw", "status", "verbose"})
	if err != nil {
		return ufwx.VerboseStatus{}, "", fmt.Errorf("reading ufw status failed: %w", err)
	}
	verbose, err := ufwx.ParseVerboseStatus(string(verboseOutput))
	if err != nil {
		return ufwx.VerboseStatus{}, "", fmt.Errorf("parsing ufw status failed: %w", err)
	}
	return verbose, string(verboseOutput), nil
}

func newUFWCloudflareCmd(deps ufwDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "cloudflare",
		Short: "Allow only Cloudflare IPs to reach TCP ports 80/443",
		Long: `Restrict TCP ports 80/443 to Cloudflare's published IP ranges.

ft fetches https://www.cloudflare.com/ips-v4 and ips-v6, validates the
lists, removes every other ufw rule that admits TCP traffic to ports
80/443 (including application profile rules such as "Nginx Full"), then
adds one allow rule per Cloudflare range tagged "` + managedRuleComment + `".
When the existing rules already match the fetched list, nothing changes.

The command registers itself in ` + cronFilePath + ` to run
every hour, so the allowed ranges follow Cloudflare's updates (output is
appended to ` + cronLogPath + `). Remove that file to stop
the automatic refresh.

Docker publishes ports through iptables directly, bypassing ufw's INPUT
rules, so plain ufw rules cannot protect Docker-published ports. When
Docker is installed, ft downloads a commit-pinned, checksum-verified copy
of ufw-docker (https://github.com/chaifeng/ufw-docker) to
` + ufwDockerBinPath + `, runs "ufw-docker install" and restarts ufw,
then also manages "ufw route allow" rules so Docker-published ports
80/443 are restricted to Cloudflare. Note that ufw-docker default-denies
external access to every other Docker-published port; re-allow those
with "ufw-docker allow <container> <port>" or "ufw route allow".

Requirements: ufw must be installed and active with a "deny (incoming)"
default policy. ft never runs "ufw enable" and never touches SSH or
other port rules. Rules admitting every port (e.g. an admin address
allowed to anything) are kept and reported for manual review.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUFWCloudflare(cmd, deps)
		},
	}
}

func runUFWCloudflare(cmd *cobra.Command, deps ufwDependencies) error {
	if _, err := deps.detectOS(); err != nil {
		return err
	}
	executablePath, err := deps.executable()
	if err != nil {
		return fmt.Errorf("resolving the ft executable path for cron failed: %w", err)
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "\n==> Checking system state")
	statuses, err := deps.queryPackages(ctx, "ufw", "docker-ce", "docker.io", "cron")
	if err != nil {
		return fmt.Errorf("checking package status failed: %w", err)
	}
	if err := ensureUFWReady(statuses); err != nil {
		return err
	}

	verbose, _, err := readVerboseStatus(ctx, deps)
	if err != nil {
		return err
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

	ufwDockerReady, err := hasUFWDockerMarker(ctx, deps)
	if err != nil {
		return err
	}
	dockerInstalled := statuses["docker-ce"] != dpkgx.Absent || statuses["docker.io"] != dpkgx.Absent

	// The Cloudflare lists are fetched before any mutation so a fetch or
	// validation failure leaves the system fully untouched.
	fmt.Fprintln(out, "\n==> Fetching Cloudflare IP ranges")
	ranges, err := cloudflarex.Ranges(ctx, deps.fetch)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Fetched %d IPv4 + %d IPv6 Cloudflare ranges.\n", len(ranges.V4), len(ranges.V6))

	if dockerInstalled && !ufwDockerReady {
		if err := installUFWDocker(cmd, deps); err != nil {
			return err
		}
		ufwDockerReady = true
	}

	fmt.Fprintln(out, "\n==> Checking existing firewall rules")
	statusOutput, err := deps.output(ctx, []string{"ufw", "status", "numbered"})
	if err != nil {
		return fmt.Errorf("reading numbered ufw rules failed: %w", err)
	}
	rules, err := ufwx.ParseNumberedRules(string(statusOutput))
	if err != nil {
		return fmt.Errorf("parsing numbered ufw rules failed: %w", err)
	}
	resolveApp := func(name string) ([]string, error) {
		infoOutput, err := deps.output(ctx, []string{"ufw", "app", "info", name})
		if err != nil {
			return nil, fmt.Errorf("reading profile info: %w", err)
		}
		return ufwx.AppInfoPorts(string(infoOutput))
	}
	foreign := make([]ufwx.NumberedRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Comment != managedRuleComment {
			foreign = append(foreign, rule)
		}
	}
	_, conflictFound, err := ufwx.NextConflict(foreign, cloudflareTCPPorts, "", resolveApp)
	if err != nil {
		return err
	}

	var warnings []string
	if !conflictFound && managedRulesInSync(rules, ranges, ufwDockerReady) {
		// The common hourly-cron case: skipping the rebuild avoids both the
		// rule churn and the brief window in which Cloudflare is blocked.
		fmt.Fprintln(out, "Managed rules already match the fetched ranges; no firewall changes needed.")
		warnings = ufwx.RuleWarnings(rules, cloudflareTCPPorts)
	} else {
		warnings, err = rebuildFirewallRules(cmd, deps, rules, resolveApp, ranges, ufwDockerReady)
		if err != nil {
			return err
		}
	}

	cronRegistered, err := registerCronRefresh(cmd, deps, statuses["cron"], executablePath)
	if err != nil {
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
	if cronRegistered {
		fmt.Fprintln(out, "The allowed ranges refresh automatically every hour via cron; rules are rewritten only when Cloudflare's list changes.")
	}
	fmt.Fprintln(out, "SSH and all other port rules were left untouched.")
	return nil
}

// hasUFWDockerMarker probes after.rules with grep -s: quiet when the file is
// missing (a plain "not set up"), non-zero exit when the marker is absent.
func hasUFWDockerMarker(ctx context.Context, deps ufwDependencies) (bool, error) {
	_, err := deps.output(ctx, []string{"grep", "-Fqs", "--", ufwDockerMarker, afterRulesPath})
	return err == nil, nil
}

// managedCronLines returns the lines of the managed cron file that mention
// this command, or "" when the file is missing or has no such line.
func managedCronLines(ctx context.Context, deps ufwDependencies) string {
	lines, err := deps.output(ctx, []string{"grep", "-Fhs", "--", "ufw cloudflare", cronFilePath})
	if err != nil {
		return ""
	}
	return string(lines)
}

// installUFWDocker downloads the pinned ufw-docker script, verifies its
// checksum, installs it and lets it patch ufw. Docker traffic cannot be
// restricted through ufw without it, so a failure aborts the whole command
// before any Cloudflare rule is touched.
func installUFWDocker(cmd *cobra.Command, deps ufwDependencies) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n==> Installing ufw-docker (pinned commit %.12s) to protect Docker-published ports\n", ufwDockerCommit)

	download := []string{
		"curl", "--fail", "--show-error", "--silent", "--location",
		"--proto", "=https", "--tlsv1.2", "--remove-on-error",
		"--output", ufwDockerTempPath,
		ufwDockerURL,
	}
	if err := deps.run(ctx, download, nil); err != nil {
		return fmt.Errorf(
			"downloading ufw-docker failed: %w; install it manually (https://github.com/chaifeng/ufw-docker), then re-run this command",
			err,
		)
	}
	sumOutput, err := deps.output(ctx, []string{"sha256sum", ufwDockerTempPath})
	if err != nil {
		return fmt.Errorf("verifying the ufw-docker download failed: %w", err)
	}
	checksum, _, _ := strings.Cut(strings.TrimSpace(string(sumOutput)), " ")
	if !strings.EqualFold(checksum, ufwDockerSHA256) {
		if rmErr := deps.run(ctx, []string{"rm", "-f", ufwDockerTempPath}, nil); rmErr != nil {
			fmt.Fprintf(out, "Note: removing %s failed: %v\n", ufwDockerTempPath, rmErr)
		}
		return fmt.Errorf(
			"ufw-docker download checksum mismatch: got %s, want %s; refusing to install an unverified script",
			checksum, ufwDockerSHA256,
		)
	}
	steps := [][]string{
		{"chmod", "0755", ufwDockerTempPath},
		{"mv", "-f", ufwDockerTempPath, ufwDockerBinPath},
		{ufwDockerBinPath, "install"},
		// ufw-docker's route rules sometimes need a full ufw restart, not
		// just a reload, to take effect.
		{"systemctl", "restart", "ufw"},
	}
	for _, argv := range steps {
		if err := deps.run(ctx, argv, nil); err != nil {
			return fmt.Errorf("installing ufw-docker: %q failed: %w", strings.Join(argv, " "), err)
		}
	}
	ready, err := hasUFWDockerMarker(ctx, deps)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf(
			"ufw-docker install did not add its rules to %s; inspect the file manually, then re-run this command",
			afterRulesPath,
		)
	}
	fmt.Fprintln(out, "ufw-docker installed; Docker-published ports now default-deny external traffic.")
	return nil
}

// managedRulesInSync reports whether the ft-managed rules are exactly one
// INPUT allow per fetched range — plus one route allow per range when
// ufw-docker is set up — with no strays, duplicates or tampered shapes.
func managedRulesInSync(rules []ufwx.NumberedRule, ranges cloudflarex.RangeSet, wantRoute bool) bool {
	wantIn := make(map[string]int, len(ranges.V4)+len(ranges.V6))
	wantFwd := make(map[string]int)
	for _, cidr := range ranges.All() {
		wantIn[cidr]++
		if wantRoute {
			wantFwd[cidr]++
		}
	}
	gotIn := make(map[string]int)
	gotFwd := make(map[string]int)
	for _, rule := range rules {
		if rule.Comment != managedRuleComment {
			continue
		}
		if rule.Action != "ALLOW" || rule.To != cloudflarePortsSpec+"/tcp" {
			return false
		}
		switch rule.Direction {
		case "", "IN":
			gotIn[rule.From]++
		case "FWD":
			gotFwd[rule.From]++
		default:
			return false
		}
	}
	return maps.Equal(wantIn, gotIn) && maps.Equal(wantFwd, gotFwd)
}

// rebuildFirewallRules deletes conflicting rules, adds the managed allow set
// and reloads ufw. It returns review warnings for deliberately kept rules.
func rebuildFirewallRules(
	cmd *cobra.Command,
	deps ufwDependencies,
	rules []ufwx.NumberedRule,
	resolveApp ufwx.AppPortsFunc,
	ranges cloudflarex.RangeSet,
	ufwDockerReady bool,
) ([]string, error) {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "\n==> Removing conflicting firewall rules")
	deleted := 0
	for {
		conflict, found, err := ufwx.NextConflict(rules, cloudflareTCPPorts, managedRuleComment, resolveApp)
		if err != nil {
			return nil, err
		}
		if !found {
			if deleted == 0 {
				fmt.Fprintln(out, "No conflicting rules found.")
			}
			break
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

		// ufw renumbers the remaining rules after every delete.
		statusOutput, err := deps.output(ctx, []string{"ufw", "status", "numbered"})
		if err != nil {
			return nil, fmt.Errorf("reading numbered ufw rules failed: %w", err)
		}
		rules, err = ufwx.ParseNumberedRules(string(statusOutput))
		if err != nil {
			return nil, fmt.Errorf("parsing numbered ufw rules failed: %w", err)
		}
	}
	warnings := ufwx.RuleWarnings(rules, cloudflareTCPPorts)

	fmt.Fprintf(out, "\n==> Allowing %d Cloudflare ranges on ports %s\n", len(ranges.V4)+len(ranges.V6), cloudflarePortsSpec)
	for _, cidr := range ranges.All() {
		allow := []string{
			"ufw", "allow", "proto", "tcp",
			"from", cidr, "to", "any", "port", cloudflarePortsSpec,
			"comment", managedRuleComment,
		}
		if err := runFirewallCommand(ctx, deps, allow); err != nil {
			return nil, err
		}
		if !ufwDockerReady {
			continue
		}
		route := append([]string{"ufw", "route"}, allow[1:]...)
		if err := runFirewallCommand(ctx, deps, route); err != nil {
			return nil, err
		}
	}

	fmt.Fprintln(out, "\n==> Reloading ufw")
	if err := runFirewallCommand(ctx, deps, []string{"ufw", "reload"}); err != nil {
		return nil, err
	}
	return warnings, nil
}

// registerCronRefresh writes the hourly cron entry and reports whether it was
// registered. A missing cron package downgrades to a warning: the firewall
// work already succeeded and must not be reported as failed.
func registerCronRefresh(cmd *cobra.Command, deps ufwDependencies, cronStatus dpkgx.Status, executablePath string) (bool, error) {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	if cronStatus != dpkgx.Installed {
		fmt.Fprintln(out, "Warning: the cron package is missing or broken, so the hourly Cloudflare refresh was not registered.")
		fmt.Fprintln(out, "Install it (\"sudo apt-get update && sudo apt-get install cron\") and re-run this command,")
		fmt.Fprintln(out, "or re-run \"ft ufw cloudflare\" manually whenever Cloudflare updates its ranges.")
		return false, nil
	}

	fmt.Fprintln(out, "\n==> Registering hourly Cloudflare refresh in cron")
	// A random minute (1-59, never the crowded top of the hour) spreads
	// servers across the hour instead of hammering Cloudflare's endpoints
	// together. Once registered the minute is reused, so the hourly self-run
	// never reshuffles its own schedule.
	minute := parseCronMinute(managedCronLines(ctx, deps))
	if minute < 0 {
		minute = deps.cronMinute()
	}
	content := fmt.Sprintf(
		"# Managed by ft. Re-run \"ft ufw cloudflare\" to update, remove this file to stop.\n"+
			"# Refreshes the Cloudflare allow rules for TCP ports %s every hour.\n"+
			"%d * * * * root %q ufw cloudflare >> %s 2>&1\n",
		cloudflarePortsSpec, minute, executablePath, cronLogPath,
	)
	steps := []struct {
		argv  []string
		input string
	}{
		{
			argv:  []string{"install", "-o", "root", "-g", "root", "-m", "0644", "/dev/stdin", cronTempPath},
			input: content,
		},
		{
			argv: []string{"mv", "-f", cronTempPath, cronFilePath},
		},
	}
	for _, step := range steps {
		var input io.Reader
		if step.input != "" {
			input = strings.NewReader(step.input)
		}
		if err := deps.run(ctx, step.argv, input); err != nil {
			return false, fmt.Errorf(
				"registering the hourly refresh failed: %w; the firewall rules are already applied — fix the cause and re-run, or add the cron entry manually",
				err,
			)
		}
	}
	fmt.Fprintf(out, "Hourly refresh registered (minute %d) at %s (logs: %s).\n", minute, cronFilePath, cronLogPath)
	return true, nil
}

// parseCronMinute extracts the minute of the managed hourly entry from cron
// file content, or -1 when no valid entry exists.
func parseCronMinute(content string) int {
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.Contains(trimmed, "ufw cloudflare") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 6 || fields[1] != "*" {
			continue
		}
		minute, err := strconv.Atoi(fields[0])
		if err != nil || minute < 0 || minute > 59 {
			continue
		}
		return minute
	}
	return -1
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
		executable:    os.Executable,
		cronMinute:    func() int { return rand.IntN(59) + 1 },
		fetch:         cloudflarex.Fetch,
	}))
}
