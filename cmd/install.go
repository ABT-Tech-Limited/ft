package cmd

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/installer"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
	"github.com/ABT-Tech-Limited/ft/internal/sysx"
	"github.com/ABT-Tech-Limited/ft/internal/userx"
	"github.com/spf13/cobra"
)

type installDependencies struct {
	detectOS        func() (osrelease.Info, error)
	goarch          string
	dockerGroupUser func() (string, bool, error)
	queryPackages   func(context.Context, ...string) (map[string]dpkgx.Status, error)
	run             func(context.Context, []string, io.Reader) error
}

func newInstallCmd(deps installDependencies) *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start server software",
		Long: `Install server software using supported, auditable package sources.

Currently available: Docker Engine from Docker's official apt repository,
and Nginx from Ubuntu's apt repository.`,
		Args: cobra.NoArgs,
	}

	installCmd.AddCommand(
		&cobra.Command{
			Use:   "docker",
			Short: "Install Docker Engine, Buildx and Compose",
			Long: `Install Docker Engine, Buildx and Compose from Docker's official repository.

After Docker is ready, ft adds the verified local non-root invoking user to
the docker group, which grants root-equivalent access. Sign out and sign back in
for the new membership to take effect. Direct root invocations do not change
Docker group membership.`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				info, err := deps.detectOS()
				if err != nil {
					return err
				}
				plan, err := installer.Docker(info, deps.goarch)
				if err != nil {
					return err
				}
				username, shouldAdd, err := deps.dockerGroupUser()
				if err != nil {
					return fmt.Errorf("resolving Docker group user failed: %w", err)
				}

				var finalSteps []installer.Step
				if shouldAdd {
					groupStep, err := installer.DockerGroupStep(username)
					if err != nil {
						return fmt.Errorf("building Docker group membership step failed: %w", err)
					}
					finalSteps = append(finalSteps, groupStep)
				}
				if err := runInstallPlan(cmd, "Docker", plan, deps, finalSteps...); err != nil {
					return err
				}
				if !shouldAdd {
					fmt.Fprintln(cmd.OutOrStdout(), "No non-root invoking user detected; skipped Docker group membership.")
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "User %q now belongs to the docker group, which grants root-equivalent access.\n", username)
				fmt.Fprintln(cmd.OutOrStdout(), "Sign out and sign back in before running Docker without sudo.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "nginx",
			Short: "Install and start Nginx",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if _, err := deps.detectOS(); err != nil {
					return err
				}
				return runInstallPlan(cmd, "Nginx", installer.Nginx(), deps)
			},
		},
	)

	return installCmd
}

func runInstallPlan(
	cmd *cobra.Command,
	product string,
	plan installer.Plan,
	deps installDependencies,
	finalSteps ...installer.Step,
) error {
	packages := make([]string, 0, len(plan.RequiredPackages)+len(plan.ConflictingPackages))
	packages = append(packages, plan.RequiredPackages...)
	packages = append(packages, plan.ConflictingPackages...)

	fmt.Fprintf(cmd.OutOrStdout(), "\n==> Checking %s package status\n", product)
	statuses, err := deps.queryPackages(cmd.Context(), packages...)
	if err != nil {
		return fmt.Errorf("checking %s package status failed: %w", product, err)
	}

	conflicts := make([]string, 0, len(plan.ConflictingPackages))
	for _, pkg := range plan.ConflictingPackages {
		if status := statuses[pkg]; status == dpkgx.Installed || status == dpkgx.Broken {
			conflicts = append(conflicts, pkg)
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("conflicting %s packages detected: %s; remove them and retry", product, strings.Join(conflicts, ", "))
	}

	alreadyInstalled := len(plan.RequiredPackages) > 0
	for _, pkg := range plan.RequiredPackages {
		if statuses[pkg] != dpkgx.Installed {
			alreadyInstalled = false
			break
		}
	}

	successMessage := product + " installed successfully."
	if alreadyInstalled {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is already installed; skipping package setup.\n", product)
		successMessage = product + " is ready."
	}
	steps := append(plan.Steps(alreadyInstalled), finalSteps...)
	return runInstallSteps(cmd, steps, deps.run, successMessage)
}

func runInstallSteps(
	cmd *cobra.Command,
	steps []installer.Step,
	run func(context.Context, []string, io.Reader) error,
	successMessage string,
) error {
	for i, step := range steps {
		fmt.Fprintf(cmd.OutOrStdout(), "\n==> [%d/%d] %s\n", i+1, len(steps), step.Title)
		var input io.Reader
		if step.Input != "" {
			input = strings.NewReader(step.Input)
		}
		if err := run(cmd.Context(), step.Argv, input); err != nil {
			return fmt.Errorf("%s failed: %w", lowerFirst(step.Title), err)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", successMessage)
	return nil
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func init() {
	rootCmd.AddCommand(newInstallCmd(installDependencies{
		detectOS:        detectOS,
		goarch:          runtime.GOARCH,
		dockerGroupUser: userx.Invoking,
		queryPackages:   dpkgx.Query,
		run:             sysx.RunInputContext,
	}))
}
