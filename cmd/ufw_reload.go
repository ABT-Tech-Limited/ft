package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newUFWReloadCmd(deps ufwDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload ufw rules and restart the ufw service",
		Long: `Reload the ufw rule set and restart the ufw service.

"ufw reload" re-reads every rule file; the "systemctl restart ufw"
afterwards re-runs the full framework initialization, which also
reapplies the before/after rule chains (e.g. ufw-docker's DOCKER-USER
rules). The firewall state is verified afterwards and the command fails
if ufw is no longer active.

Requires ufw to be installed and active; ft never runs "ufw enable".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUFWReload(cmd, deps)
		},
	}
}

func runUFWReload(cmd *cobra.Command, deps ufwDependencies) error {
	if _, err := deps.detectOS(); err != nil {
		return err
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	statuses, err := deps.queryPackages(ctx, "ufw")
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
			"ufw is inactive, so there is nothing to reload; allow your SSH port first, " +
				"then run \"sudo ufw enable\" yourself (ft never enables the firewall for you)",
		)
	}

	steps := [][]string{
		{"ufw", "reload"},
		{"systemctl", "restart", "ufw"},
	}
	titles := []string{"Reloading ufw rules", "Restarting the ufw service"}
	for i, argv := range steps {
		fmt.Fprintf(out, "\n==> %s\n", titles[i])
		if err := deps.run(ctx, argv, nil); err != nil {
			return fmt.Errorf("%q failed: %w", strings.Join(argv, " "), err)
		}
	}

	fmt.Fprintln(out, "\n==> Verifying firewall state")
	verbose, _, err = readVerboseStatus(ctx, deps)
	if err != nil {
		return err
	}
	if !verbose.Active {
		return fmt.Errorf(
			"ufw is no longer active after the restart; investigate immediately with \"ufw status\" and " +
				"\"journalctl -u ufw\" (existing SSH sessions stay alive)",
		)
	}
	fmt.Fprintln(out, "ufw rules reloaded and service restarted; the firewall is active.")
	return nil
}
