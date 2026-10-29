package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/installer"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
)

type recordedInstallCommand struct {
	argv  []string
	input string
}

type installRecorder struct {
	statuses             map[string]dpkgx.Status
	detectErr            error
	queryErr             error
	goarch               string
	dockerGroupUsername  string
	dockerGroupShouldAdd bool
	dockerGroupErr       error
	sshPorts             []int
	sshPortErr           error
	detected             int
	dockerGroupCalls     int
	sshPortCalls         int
	queries              [][]string
	commands             []recordedInstallCommand
	queryContexts        []context.Context
	runContexts          []context.Context
	sshPortContexts      []context.Context
	runErrorAt           int
	runErr               error
}

func newInstallRecorder(statuses map[string]dpkgx.Status) *installRecorder {
	return &installRecorder{
		statuses:   statuses,
		goarch:     "amd64",
		sshPorts:   []int{22},
		runErrorAt: -1,
	}
}

func (r *installRecorder) dependencies() installDependencies {
	return installDependencies{
		executable: func() (string, error) { return "/usr/local/bin/ft", nil },
		detectOS: func() (osrelease.Info, error) {
			r.detected++
			if r.detectErr != nil {
				return osrelease.Info{}, r.detectErr
			}
			return osrelease.Info{ID: "ubuntu", VersionID: "24.04"}, nil
		},
		goarch: r.goarch,
		detectSSHPorts: func(ctx context.Context) ([]int, error) {
			r.sshPortCalls++
			r.sshPortContexts = append(r.sshPortContexts, ctx)
			return append([]int(nil), r.sshPorts...), r.sshPortErr
		},
		dockerGroupUser: func() (string, bool, error) {
			r.dockerGroupCalls++
			return r.dockerGroupUsername, r.dockerGroupShouldAdd, r.dockerGroupErr
		},
		queryPackages: func(ctx context.Context, packages ...string) (map[string]dpkgx.Status, error) {
			r.queryContexts = append(r.queryContexts, ctx)
			r.queries = append(r.queries, append([]string(nil), packages...))
			if r.queryErr != nil {
				return nil, r.queryErr
			}
			result := make(map[string]dpkgx.Status, len(packages))
			for _, pkg := range packages {
				result[pkg] = r.statuses[pkg]
			}
			return result, nil
		},
		run: func(ctx context.Context, argv []string, stdin io.Reader) error {
			r.runContexts = append(r.runContexts, ctx)
			var input []byte
			if stdin != nil {
				var err error
				input, err = io.ReadAll(stdin)
				if err != nil {
					return err
				}
			}
			current := len(r.commands)
			r.commands = append(r.commands, recordedInstallCommand{
				argv:  append([]string(nil), argv...),
				input: string(input),
			})
			if current == r.runErrorAt {
				return r.runErr
			}
			return nil
		},
	}
}

func TestInstallAbsentPackagesRunExpectedSteps(t *testing.T) {
	tests := []struct {
		name    string
		command string
		product string
		plan    installer.Plan
	}{
		{name: "Docker", command: "docker", product: "Docker", plan: mustDockerPlan(t)},
		{name: "Nginx", command: "nginx", product: "Nginx", plan: installer.Nginx()},
		{name: "Fail2Ban", command: "fail2ban", product: "Fail2Ban", plan: mustFail2banPlan(t, 22)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newInstallRecorder(nil)
			wantSteps := tt.plan.Steps(false)
			if tt.command == "nginx" {
				wantSteps = append(wantSteps, nginxConfigureTestStep())
			}
			if tt.command == "docker" {
				recorder.dockerGroupUsername = "ubuntu"
				recorder.dockerGroupShouldAdd = true
				wantSteps = append(wantSteps, mustDockerGroupStep(t, "ubuntu"))
			}
			output, err := executeInstall(t, recorder, tt.command)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if recorder.detected != 1 {
				t.Errorf("detectOS called %d times, want 1", recorder.detected)
			}
			wantQueries := [][]string{allPlanPackages(tt.plan)}
			if !reflect.DeepEqual(recorder.queries, wantQueries) {
				t.Errorf("package queries = %v, want %v", recorder.queries, wantQueries)
			}
			wantCommands := recordedCommands(wantSteps)
			if !reflect.DeepEqual(recorder.commands, wantCommands) {
				t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, wantCommands)
			}
			for _, step := range wantSteps {
				if !strings.Contains(output, step.Title) {
					t.Errorf("output %q does not contain step title %q", output, step.Title)
				}
			}
			if want := tt.product + " installed successfully."; !strings.Contains(output, want) {
				t.Errorf("output %q does not contain %q", output, want)
			}
			assertDockerGroupOutcome(t, recorder, output, tt.command)
		})
	}
}

func TestInstallAlreadyInstalledSkipsPackageSetup(t *testing.T) {
	tests := []struct {
		name    string
		command string
		product string
		plan    installer.Plan
	}{
		{name: "Docker", command: "docker", product: "Docker", plan: mustDockerPlan(t)},
		{name: "Nginx", command: "nginx", product: "Nginx", plan: installer.Nginx()},
		{name: "Fail2Ban", command: "fail2ban", product: "Fail2Ban", plan: mustFail2banPlan(t, 22)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newInstallRecorder(installedStatuses(tt.plan.RequiredPackages))
			wantSteps := tt.plan.Steps(true)
			if tt.command == "nginx" {
				wantSteps = append(wantSteps, nginxConfigureTestStep())
			}
			if tt.command == "docker" {
				recorder.dockerGroupUsername = "ubuntu"
				recorder.dockerGroupShouldAdd = true
				wantSteps = append(wantSteps, mustDockerGroupStep(t, "ubuntu"))
			}
			output, err := executeInstall(t, recorder, tt.command)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			wantCommands := recordedCommands(wantSteps)
			if !reflect.DeepEqual(recorder.commands, wantCommands) {
				t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, wantCommands)
			}
			if want := tt.product + " is already installed; skipping package setup."; !strings.Contains(output, want) {
				t.Errorf("output %q does not contain %q", output, want)
			}
			if want := tt.product + " is ready."; !strings.Contains(output, want) {
				t.Errorf("output %q does not contain %q", output, want)
			}
			if strings.Contains(output, "installed successfully") {
				t.Errorf("already-installed output contains fresh-install message: %q", output)
			}
			assertDockerGroupOutcome(t, recorder, output, tt.command)
		})
	}
}

func TestInstallDockerDirectRootSkipsGroupMembership(t *testing.T) {
	plan := mustDockerPlan(t)
	tests := []struct {
		name             string
		statuses         map[string]dpkgx.Status
		alreadyInstalled bool
	}{
		{name: "fresh install"},
		{name: "already installed", statuses: installedStatuses(plan.RequiredPackages), alreadyInstalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newInstallRecorder(tt.statuses)
			output, err := executeInstall(t, recorder, "docker")
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if recorder.dockerGroupCalls != 1 {
				t.Errorf("Docker group resolver called %d times, want 1", recorder.dockerGroupCalls)
			}
			if want := recordedCommands(plan.Steps(tt.alreadyInstalled)); !reflect.DeepEqual(recorder.commands, want) {
				t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
			}
			if want := "No non-root invoking user detected; skipped Docker group membership."; !strings.Contains(output, want) {
				t.Errorf("output %q does not contain %q", output, want)
			}
			if strings.Contains(output, "root-equivalent") || strings.Contains(output, "Sign out and sign back in") {
				t.Errorf("direct-root output contains group access guidance: %q", output)
			}
		})
	}
}

func TestInstallDockerStopsWhenGroupUserResolutionFails(t *testing.T) {
	wantErr := errors.New("identity lookup failed")
	recorder := newInstallRecorder(nil)
	recorder.dockerGroupErr = wantErr

	output, err := executeInstall(t, recorder, "docker")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if !strings.Contains(err.Error(), "resolving Docker group user") {
		t.Errorf("Execute() error = %q, want Docker group user context", err)
	}
	if recorder.dockerGroupCalls != 1 {
		t.Errorf("Docker group resolver called %d times, want 1", recorder.dockerGroupCalls)
	}
	if len(recorder.queries) != 0 || len(recorder.commands) != 0 {
		t.Errorf("identity failure caused queries %v or commands %v", recorder.queries, recorder.commands)
	}
	if strings.Contains(output, "is ready") || strings.Contains(output, "installed successfully") {
		t.Errorf("identity failure output contains success message: %q", output)
	}
}

func TestInstallDockerPartialInstallationRunsFullPlan(t *testing.T) {
	plan := mustDockerPlan(t)
	allInstalled := installedStatuses(plan.RequiredPackages)
	tests := []struct {
		name     string
		statuses map[string]dpkgx.Status
	}{
		{
			name:     "only first package installed",
			statuses: map[string]dpkgx.Status{plan.RequiredPackages[0]: dpkgx.Installed},
		},
		{
			name:     "only last package missing",
			statuses: withStatus(allInstalled, plan.RequiredPackages[len(plan.RequiredPackages)-1], dpkgx.Absent),
		},
		{
			name:     "required package broken",
			statuses: withStatus(allInstalled, plan.RequiredPackages[0], dpkgx.Broken),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newInstallRecorder(tt.statuses)
			if _, err := executeInstall(t, recorder, "docker"); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			want := recordedCommands(plan.Steps(false))
			if !reflect.DeepEqual(recorder.commands, want) {
				t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
			}
		})
	}
}

func TestInstallFail2banWithoutNftablesRunsFullPlan(t *testing.T) {
	plan := mustFail2banPlan(t, 22)
	recorder := newInstallRecorder(map[string]dpkgx.Status{"fail2ban": dpkgx.Installed})

	if _, err := executeInstall(t, recorder, "fail2ban"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := recordedCommands(plan.Steps(false))
	if !reflect.DeepEqual(recorder.commands, want) {
		t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
	}
}

func TestInstallFail2banConfiguresCustomSSHPort(t *testing.T) {
	plan := installer.Fail2ban()
	recorder := newInstallRecorder(installedStatuses(plan.RequiredPackages))

	output, err := executeInstall(t, recorder, "fail2ban", "--ssh-port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := fail2banPortCommands("10222")
	if !reflect.DeepEqual(recorder.commands, want) {
		t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
	}
	if recorder.sshPortCalls != 0 {
		t.Errorf("explicit SSH port caused %d automatic detections, want 0", recorder.sshPortCalls)
	}
	if !strings.Contains(output, "Fail2Ban is ready.") {
		t.Errorf("output %q does not contain readiness message", output)
	}
}

func TestInstallFail2banAutomaticallyConfiguresEffectiveSSHPorts(t *testing.T) {
	tests := []struct {
		name       string
		ports      []int
		wantConfig string
		wantOutput string
	}{
		{name: "custom port", ports: []int{10222}, wantConfig: "10222", wantOutput: "10222"},
		{name: "migration ports", ports: []int{22, 10222}, wantConfig: "22,10222", wantOutput: "22, 10222"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := installer.Fail2ban()
			recorder := newInstallRecorder(installedStatuses(plan.RequiredPackages))
			recorder.sshPorts = tt.ports

			output, err := executeInstall(t, recorder, "fail2ban")
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if want := fail2banPortCommands(tt.wantConfig); !reflect.DeepEqual(recorder.commands, want) {
				t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
			}
			if recorder.sshPortCalls != 1 {
				t.Errorf("automatic SSH port detections = %d, want 1", recorder.sshPortCalls)
			}
			if want := "Detected effective SSH port(s): " + tt.wantOutput; !strings.Contains(output, want) {
				t.Errorf("output %q does not contain %q", output, want)
			}
		})
	}
}

func TestInstallFail2banStopsWhenSSHPortDetectionFails(t *testing.T) {
	wantErr := errors.New("sshd effective config failed")
	recorder := newInstallRecorder(nil)
	recorder.sshPortErr = wantErr

	output, err := executeInstall(t, recorder, "fail2ban")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	for _, want := range []string{"detecting effective SSH ports failed", "--ssh-port"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Execute() error = %q, want %q", err, want)
		}
	}
	if recorder.detected != 1 || recorder.sshPortCalls != 1 || len(recorder.queries) != 0 || len(recorder.commands) != 0 {
		t.Errorf(
			"detection failure caused detectOS=%d, detectSSH=%d, queries=%v, commands=%v",
			recorder.detected,
			recorder.sshPortCalls,
			recorder.queries,
			recorder.commands,
		)
	}
	if strings.Contains(output, "is ready") || strings.Contains(output, "installed successfully") {
		t.Errorf("detection failure output contains success message: %q", output)
	}
}

func TestInstallFail2banRejectsInvalidSSHPortBeforeInspection(t *testing.T) {
	for _, port := range []string{"-1", "0", "65536"} {
		t.Run(port, func(t *testing.T) {
			recorder := newInstallRecorder(nil)
			output, err := executeInstall(t, recorder, "fail2ban", "--ssh-port", port)
			if err == nil || !strings.Contains(err.Error(), "SSH port must be between 1 and 65535") {
				t.Fatalf("Execute() error = %v, want SSH port range error", err)
			}
			if recorder.detected != 0 || recorder.sshPortCalls != 0 || len(recorder.queries) != 0 || len(recorder.commands) != 0 {
				t.Errorf(
					"invalid port caused detectOS=%d, detectSSH=%d, queries=%v, commands=%v",
					recorder.detected,
					recorder.sshPortCalls,
					recorder.queries,
					recorder.commands,
				)
			}
			if strings.Contains(output, "is ready") || strings.Contains(output, "installed successfully") {
				t.Errorf("invalid port output contains success message: %q", output)
			}
		})
	}
}

func fail2banPortCommands(portList string) []recordedInstallCommand {
	tempPath := fmt.Sprintf("/etc/fail2ban/jail.d/zz-ft-sshd.conf.%d.tmp", os.Getpid())
	return []recordedInstallCommand{
		{argv: []string{"install", "-m", "0755", "-d", "/etc/fail2ban/jail.d"}},
		{
			argv: []string{
				"install", "-o", "root", "-g", "root", "-m", "0644",
				"/dev/stdin", tempPath,
			},
			input: "# Managed by ft. Re-run ft install fail2ban to update.\n\n[sshd]\nenabled = true\nport = " + portList + "\n",
		},
		{argv: []string{"mv", "-f", tempPath, "/etc/fail2ban/jail.d/zz-ft-sshd.conf"}},
		{argv: []string{"/usr/bin/fail2ban-client", "-t"}},
		{argv: []string{"systemctl", "enable", "fail2ban.service"}},
		{argv: []string{"systemctl", "restart", "fail2ban.service"}},
	}
}

func TestInstallDockerRejectsConflictsBeforeMutation(t *testing.T) {
	plan := mustDockerPlan(t)
	tests := make([]struct {
		name      string
		conflicts map[string]dpkgx.Status
		wantNames []string
	}, 0, len(plan.ConflictingPackages)+1)
	for _, pkg := range plan.ConflictingPackages {
		tests = append(tests, struct {
			name      string
			conflicts map[string]dpkgx.Status
			wantNames []string
		}{
			name:      pkg,
			conflicts: map[string]dpkgx.Status{pkg: dpkgx.Installed},
			wantNames: []string{pkg},
		})
	}
	tests = append(tests, struct {
		name      string
		conflicts map[string]dpkgx.Status
		wantNames []string
	}{
		name: "multiple conflicts keep plan order",
		conflicts: map[string]dpkgx.Status{
			plan.ConflictingPackages[0]:                               dpkgx.Installed,
			plan.ConflictingPackages[len(plan.ConflictingPackages)-1]: dpkgx.Broken,
		},
		wantNames: []string{plan.ConflictingPackages[0], plan.ConflictingPackages[len(plan.ConflictingPackages)-1]},
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statuses := installedStatuses(plan.RequiredPackages)
			for pkg, status := range tt.conflicts {
				statuses[pkg] = status
			}
			recorder := newInstallRecorder(statuses)
			recorder.dockerGroupUsername = "ubuntu"
			recorder.dockerGroupShouldAdd = true
			output, err := executeInstall(t, recorder, "docker")
			if err == nil {
				t.Fatal("Execute() error = nil, want conflict error")
			}
			if !strings.Contains(err.Error(), "conflicting Docker packages detected") || !strings.Contains(err.Error(), "remove them and retry") {
				t.Errorf("Execute() error = %q, want conflict guidance", err)
			}
			lastIndex := -1
			for _, pkg := range tt.wantNames {
				index := strings.Index(err.Error(), pkg)
				if index < 0 {
					t.Errorf("Execute() error = %q, want package %q", err, pkg)
				}
				if index < lastIndex {
					t.Errorf("Execute() error = %q, conflicts not in plan order", err)
				}
				lastIndex = index
			}
			if len(recorder.commands) != 0 {
				t.Errorf("runner called %d times before conflict error, want 0", len(recorder.commands))
			}
			if strings.Contains(output, "is ready") || strings.Contains(output, "installed successfully") {
				t.Errorf("conflict output contains success message: %q", output)
			}
			if strings.Contains(output, "root-equivalent") || strings.Contains(output, "Sign out and sign back in") {
				t.Errorf("conflict output contains group access guidance: %q", output)
			}
		})
	}
}

func TestInstallStopsWhenPackageQueryFails(t *testing.T) {
	wantErr := errors.New("dpkg query failed")
	tests := []struct {
		command string
		product string
	}{
		{command: "docker", product: "Docker"},
		{command: "nginx", product: "Nginx"},
		{command: "fail2ban", product: "Fail2Ban"},
	}

	for _, tt := range tests {
		t.Run(tt.product, func(t *testing.T) {
			recorder := newInstallRecorder(nil)
			if tt.command == "docker" {
				recorder.dockerGroupUsername = "ubuntu"
				recorder.dockerGroupShouldAdd = true
			}
			recorder.queryErr = wantErr
			output, err := executeInstall(t, recorder, tt.command)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			if want := "checking " + tt.product + " package status failed"; !strings.Contains(err.Error(), want) {
				t.Errorf("Execute() error = %q, want %q", err, want)
			}
			if len(recorder.commands) != 0 {
				t.Errorf("runner called %d times after query failure, want 0", len(recorder.commands))
			}
			if strings.Contains(output, "is ready") || strings.Contains(output, "installed successfully") {
				t.Errorf("query failure output contains success message: %q", output)
			}
			if strings.Contains(output, "root-equivalent") || strings.Contains(output, "Sign out and sign back in") {
				t.Errorf("query failure output contains group access guidance: %q", output)
			}
		})
	}
}

func TestAlreadyInstalledEnsureFailure(t *testing.T) {
	wantErr := errors.New("ensure step failed")
	tests := []struct {
		command string
		product string
		plan    installer.Plan
	}{
		{command: "docker", product: "Docker", plan: mustDockerPlan(t)},
		{command: "nginx", product: "Nginx", plan: installer.Nginx()},
		{command: "fail2ban", product: "Fail2Ban", plan: mustFail2banPlan(t, 22)},
	}

	for _, tt := range tests {
		t.Run(tt.product, func(t *testing.T) {
			recorder := newInstallRecorder(installedStatuses(tt.plan.RequiredPackages))
			if tt.command == "docker" {
				recorder.dockerGroupUsername = "ubuntu"
				recorder.dockerGroupShouldAdd = true
			}
			recorder.runErrorAt = 0
			recorder.runErr = wantErr
			output, err := executeInstall(t, recorder, tt.command)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			if len(recorder.commands) != 1 {
				t.Errorf("runner called %d times, want 1", len(recorder.commands))
			}
			if !strings.Contains(err.Error(), lowerFirst(tt.plan.EnsureSteps[0].Title)) {
				t.Errorf("Execute() error = %q, want ensure step context", err)
			}
			if strings.Contains(output, "is ready") {
				t.Errorf("ensure failure output contains ready message: %q", output)
			}
			if tt.command == "docker" {
				if recorder.dockerGroupCalls != 1 {
					t.Errorf("Docker group resolver called %d times, want 1", recorder.dockerGroupCalls)
				}
				if strings.Contains(output, "root-equivalent") || strings.Contains(output, "Sign out and sign back in") {
					t.Errorf("ensure failure output contains group access guidance: %q", output)
				}
			}
		})
	}
}

func TestInstallFail2banStopsAtFailedEnsureStep(t *testing.T) {
	plan := mustFail2banPlan(t, 22)
	for failAt, failedStep := range plan.EnsureSteps {
		t.Run(failedStep.Title, func(t *testing.T) {
			wantErr := errors.New("Fail2Ban ensure step failed")
			recorder := newInstallRecorder(installedStatuses(plan.RequiredPackages))
			recorder.runErrorAt = failAt
			recorder.runErr = wantErr

			output, err := executeInstall(t, recorder, "fail2ban")
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			if len(recorder.commands) != failAt+1 {
				t.Errorf("runner called %d times, want %d", len(recorder.commands), failAt+1)
			}
			if !strings.Contains(err.Error(), lowerFirst(failedStep.Title)) {
				t.Errorf("Execute() error = %q, want step context", err)
			}
			if strings.Contains(output, "Fail2Ban is ready.") {
				t.Errorf("failed ensure step output contains ready message: %q", output)
			}
		})
	}
}

func TestInstallDockerGroupUpdateFailureHasNoSuccessGuidance(t *testing.T) {
	plan := mustDockerPlan(t)
	wantErr := errors.New("usermod failed")
	groupStep := mustDockerGroupStep(t, "ubuntu")
	tests := []struct {
		name             string
		statuses         map[string]dpkgx.Status
		alreadyInstalled bool
		unwantedSuccess  string
	}{
		{name: "fresh install", unwantedSuccess: "Docker installed successfully."},
		{
			name:             "already installed",
			statuses:         installedStatuses(plan.RequiredPackages),
			alreadyInstalled: true,
			unwantedSuccess:  "Docker is ready.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := plan.Steps(tt.alreadyInstalled)
			recorder := newInstallRecorder(tt.statuses)
			recorder.dockerGroupUsername = "ubuntu"
			recorder.dockerGroupShouldAdd = true
			recorder.runErrorAt = len(steps)
			recorder.runErr = wantErr

			output, err := executeInstall(t, recorder, "docker")
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			wantCommands := recordedCommands(append(steps, groupStep))
			if !reflect.DeepEqual(recorder.commands, wantCommands) {
				t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, wantCommands)
			}
			if !strings.Contains(err.Error(), lowerFirst(groupStep.Title)) {
				t.Errorf("Execute() error = %q, want group step context", err)
			}
			for _, unwanted := range []string{tt.unwantedSuccess, "root-equivalent", "Sign out and sign back in"} {
				if strings.Contains(output, unwanted) {
					t.Errorf("group update failure output %q contains %q", output, unwanted)
				}
			}
		})
	}
}

func TestInstallDockerFailureBeforeGroupStepStopsAuthorization(t *testing.T) {
	plan := mustDockerPlan(t)
	steps := plan.Steps(false)
	groupStep := mustDockerGroupStep(t, "ubuntu")

	for failAt, failedStep := range steps {
		t.Run(failedStep.Title, func(t *testing.T) {
			wantErr := errors.New("runner failed")
			recorder := newInstallRecorder(nil)
			recorder.dockerGroupUsername = "ubuntu"
			recorder.dockerGroupShouldAdd = true
			recorder.runErrorAt = failAt
			recorder.runErr = wantErr

			output, err := executeInstall(t, recorder, "docker")
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			if len(recorder.commands) != failAt+1 {
				t.Errorf("runner called %d times, want %d", len(recorder.commands), failAt+1)
			}
			if strings.Contains(output, groupStep.Title) {
				t.Errorf("failure before authorization output contains group step: %q", output)
			}
			for _, unwanted := range []string{"Docker installed successfully.", "root-equivalent", "Sign out and sign back in"} {
				if strings.Contains(output, unwanted) {
					t.Errorf("failure before authorization output %q contains %q", output, unwanted)
				}
			}
		})
	}
}

func TestInstallNonDockerCommandsNeverResolveDockerGroupUser(t *testing.T) {
	tests := []struct {
		command string
		product string
		plan    installer.Plan
	}{
		{command: "nginx", product: "Nginx", plan: installer.Nginx()},
		{command: "fail2ban", product: "Fail2Ban", plan: installer.Fail2ban()},
	}

	for _, tt := range tests {
		t.Run(tt.product, func(t *testing.T) {
			recorder := newInstallRecorder(installedStatuses(tt.plan.RequiredPackages))
			recorder.dockerGroupErr = errors.New("Docker group resolver must not be called")

			output, err := executeInstall(t, recorder, tt.command)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if recorder.dockerGroupCalls != 0 {
				t.Errorf("Docker group resolver called %d times, want 0", recorder.dockerGroupCalls)
			}
			if want := tt.product + " is ready."; !strings.Contains(output, want) {
				t.Errorf("output %q does not contain %q", output, want)
			}
		})
	}
}

func TestInstallDockerHelpDisclosesGroupPrivilege(t *testing.T) {
	recorder := newInstallRecorder(nil)
	output, err := executeInstall(t, recorder, "docker", "--help")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"local non-root invoking user",
		"root-equivalent access",
		"Sign out and sign back in",
		"Direct root invocations do not change",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output %q does not contain %q", output, want)
		}
	}
	if recorder.detected != 0 || recorder.sshPortCalls != 0 || recorder.dockerGroupCalls != 0 || len(recorder.queries) != 0 || len(recorder.commands) != 0 {
		t.Errorf(
			"help caused detectOS=%d, detectSSH=%d, resolver=%d, queries=%v, commands=%v",
			recorder.detected,
			recorder.sshPortCalls,
			recorder.dockerGroupCalls,
			recorder.queries,
			recorder.commands,
		)
	}
}

func TestInstallFail2banHelpDisclosesDefaultPolicy(t *testing.T) {
	recorder := newInstallRecorder(nil)
	output, err := executeInstall(t, recorder, "fail2ban", "--help")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Ubuntu's default SSH jail",
		"nftables",
		"/usr/sbin/sshd -T",
		"effective SSH port",
		"managed",
		"--ssh-port",
		"does not change sshd",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output %q does not contain %q", output, want)
		}
	}
	if recorder.detected != 0 || recorder.sshPortCalls != 0 || recorder.dockerGroupCalls != 0 || len(recorder.queries) != 0 || len(recorder.commands) != 0 {
		t.Errorf(
			"help caused detectOS=%d, detectSSH=%d, resolver=%d, queries=%v, commands=%v",
			recorder.detected,
			recorder.sshPortCalls,
			recorder.dockerGroupCalls,
			recorder.queries,
			recorder.commands,
		)
	}
}

func TestInstallStopsWhenOSIsUnsupported(t *testing.T) {
	wantErr := errors.New("unsupported test OS")
	recorder := newInstallRecorder(nil)
	recorder.detectErr = wantErr
	_, err := executeInstall(t, recorder, "nginx")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if len(recorder.queries) != 0 || len(recorder.commands) != 0 {
		t.Errorf("unsupported OS caused queries %v or commands %v", recorder.queries, recorder.commands)
	}
}

func TestInstallStopsAtFailedStep(t *testing.T) {
	plan := installer.Nginx()
	steps := plan.Steps(false)
	for failAt := range steps {
		t.Run(steps[failAt].Title, func(t *testing.T) {
			wantErr := errors.New("runner failed")
			recorder := newInstallRecorder(nil)
			recorder.runErrorAt = failAt
			recorder.runErr = wantErr
			output, err := executeInstall(t, recorder, "nginx")
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			if len(recorder.commands) != failAt+1 {
				t.Errorf("runner called %d times, want %d", len(recorder.commands), failAt+1)
			}
			if !strings.Contains(err.Error(), lowerFirst(steps[failAt].Title)) {
				t.Errorf("Execute() error = %q, want step context %q", err, lowerFirst(steps[failAt].Title))
			}
			if strings.Contains(output, "installed successfully") {
				t.Errorf("failure output contains success message: %q", output)
			}
		})
	}
}

func TestInstallRejectsArguments(t *testing.T) {
	for _, subcommand := range []string{"docker", "nginx", "ncdu", "fail2ban"} {
		t.Run(subcommand, func(t *testing.T) {
			recorder := newInstallRecorder(nil)
			_, err := executeInstall(t, recorder, subcommand, "unexpected")
			if err == nil {
				t.Fatal("Execute() error = nil, want argument error")
			}
			if len(recorder.queries) != 0 || len(recorder.commands) != 0 {
				t.Errorf("invalid args caused queries %v or commands %v", recorder.queries, recorder.commands)
			}
		})
	}
}

func TestInstallDockerRejectsUnsupportedArchitectureBeforeQuery(t *testing.T) {
	recorder := newInstallRecorder(nil)
	recorder.goarch = "386"
	_, err := executeInstall(t, recorder, "docker")
	if err == nil || !strings.Contains(err.Error(), "unsupported architecture") {
		t.Fatalf("Execute() error = %v, want unsupported architecture error", err)
	}
	if len(recorder.queries) != 0 || len(recorder.commands) != 0 {
		t.Errorf("unsupported architecture caused queries %v or commands %v", recorder.queries, recorder.commands)
	}
	if recorder.dockerGroupCalls != 0 {
		t.Errorf("unsupported architecture called Docker group resolver %d times, want 0", recorder.dockerGroupCalls)
	}
}

func TestInstallPassesCommandContext(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	recorder := newInstallRecorder(installedStatuses(installer.Nginx().RequiredPackages))
	command := newInstallCmd(recorder.dependencies())
	command.SetContext(ctx)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"nginx"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recorder.queryContexts) != 1 || recorder.queryContexts[0] != ctx {
		t.Errorf("query contexts = %v, want command context", recorder.queryContexts)
	}
	if len(recorder.runContexts) != 2 || recorder.runContexts[0] != ctx || recorder.runContexts[1] != ctx {
		t.Errorf("run contexts = %v, want command context", recorder.runContexts)
	}
}

func TestInstallFail2banPassesCommandContextToSSHDetection(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	plan := mustFail2banPlan(t, 22)
	recorder := newInstallRecorder(installedStatuses(plan.RequiredPackages))
	command := newInstallCmd(recorder.dependencies())
	command.SetContext(ctx)
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"fail2ban"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recorder.sshPortContexts) != 1 || recorder.sshPortContexts[0] != ctx {
		t.Errorf("SSH port detection contexts = %v, want command context", recorder.sshPortContexts)
	}
}

func executeInstall(t *testing.T, recorder *installRecorder, args ...string) (string, error) {
	t.Helper()
	command := newInstallCmd(recorder.dependencies())
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}

func nginxConfigureTestStep() installer.Step {
	return installer.Step{
		Title: "Configuring Nginx defaults and Cloudflare refresh",
		Argv:  []string{"/usr/local/bin/ft", "nginx", "configure"},
	}
}

func mustDockerPlan(t *testing.T) installer.Plan {
	t.Helper()
	plan, err := installer.Docker(osrelease.Info{ID: "ubuntu", VersionID: "24.04"}, "amd64")
	if err != nil {
		t.Fatalf("installer.Docker() error = %v", err)
	}
	return plan
}

func mustFail2banPlan(t *testing.T, sshPorts ...int) installer.Plan {
	t.Helper()
	plan, err := installer.Fail2banWithSSHPorts(sshPorts)
	if err != nil {
		t.Fatalf("installer.Fail2banWithSSHPorts() error = %v", err)
	}
	return plan
}

func mustDockerGroupStep(t *testing.T, username string) installer.Step {
	t.Helper()
	step, err := installer.DockerGroupStep(username)
	if err != nil {
		t.Fatalf("installer.DockerGroupStep() error = %v", err)
	}
	return step
}

func assertDockerGroupOutcome(t *testing.T, recorder *installRecorder, output, command string) {
	t.Helper()
	if command == "docker" {
		if recorder.dockerGroupCalls != 1 {
			t.Errorf("Docker group resolver called %d times, want 1", recorder.dockerGroupCalls)
		}
		for _, want := range []string{
			`User "ubuntu" now belongs to the docker group, which grants root-equivalent access.`,
			"Sign out and sign back in before running Docker without sudo.",
		} {
			if !strings.Contains(output, want) {
				t.Errorf("output %q does not contain %q", output, want)
			}
		}
		return
	}

	if recorder.dockerGroupCalls != 0 {
		t.Errorf("Docker group resolver called %d times for %s, want 0", recorder.dockerGroupCalls, command)
	}
	if strings.Contains(output, "root-equivalent") || strings.Contains(output, "Sign out and sign back in") {
		t.Errorf("%s output contains Docker group guidance: %q", command, output)
	}
}

func allPlanPackages(plan installer.Plan) []string {
	packages := append([]string(nil), plan.RequiredPackages...)
	return append(packages, plan.ConflictingPackages...)
}

func installedStatuses(packages []string) map[string]dpkgx.Status {
	statuses := make(map[string]dpkgx.Status, len(packages))
	for _, pkg := range packages {
		statuses[pkg] = dpkgx.Installed
	}
	return statuses
}

func withStatus(base map[string]dpkgx.Status, pkg string, status dpkgx.Status) map[string]dpkgx.Status {
	result := make(map[string]dpkgx.Status, len(base))
	for name, value := range base {
		result[name] = value
	}
	result[pkg] = status
	return result
}

func recordedCommands(steps []installer.Step) []recordedInstallCommand {
	commands := make([]recordedInstallCommand, 0, len(steps))
	for _, step := range steps {
		commands = append(commands, recordedInstallCommand{
			argv:  append([]string(nil), step.Argv...),
			input: step.Input,
		})
	}
	return commands
}
