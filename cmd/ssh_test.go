package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/installer"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
	"github.com/ABT-Tech-Limited/ft/internal/sshx"
)

type sshRecorder struct {
	statuses      map[string]dpkgx.Status
	detectErr     error
	detected      int
	sshPorts      [][]int // consumed per detectSSHPorts call; last entry repeats
	sshPortErrs   []error // parallel to sshPorts; nil entries mean success
	sshPortCalls  int
	queries       [][]string
	queryErr      error
	ufwOutput     string
	ufwErr        error
	socketErr     error // result of systemctl is-enabled ssh.socket
	outputCalls   [][]string
	fileExists    bool
	fileExistsErr error
	existsCalls   []string
	commands      []recordedInstallCommand
	runErrorAt    int
	runErr        error

	// Recorded contexts, parallel to the call slices above, so tests can
	// assert the command context propagates into every dependency.
	sshPortContexts []context.Context
	queryContexts   []context.Context
	outputContexts  []context.Context
	runContexts     []context.Context
}

func newSSHRecorder() *sshRecorder {
	return &sshRecorder{
		sshPorts:   [][]int{{22}},
		runErrorAt: -1,
		socketErr:  errors.New("disabled"),
	}
}

func (r *sshRecorder) dependencies() sshDependencies {
	return sshDependencies{
		detectOS: func() (osrelease.Info, error) {
			r.detected++
			if r.detectErr != nil {
				return osrelease.Info{}, r.detectErr
			}
			return osrelease.Info{ID: "ubuntu", VersionID: "24.04"}, nil
		},
		detectSSHPorts: func(ctx context.Context) ([]int, error) {
			r.sshPortContexts = append(r.sshPortContexts, ctx)
			index := r.sshPortCalls
			r.sshPortCalls++
			if index >= len(r.sshPorts) {
				index = len(r.sshPorts) - 1
			}
			var err error
			if index < len(r.sshPortErrs) {
				err = r.sshPortErrs[index]
			}
			return append([]int(nil), r.sshPorts[index]...), err
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
		fileExists: func(path string) (bool, error) {
			r.existsCalls = append(r.existsCalls, path)
			return r.fileExists, r.fileExistsErr
		},
		output: func(ctx context.Context, argv []string) ([]byte, error) {
			r.outputContexts = append(r.outputContexts, ctx)
			r.outputCalls = append(r.outputCalls, append([]string(nil), argv...))
			if len(argv) > 0 && argv[0] == "ufw" {
				return []byte(r.ufwOutput), r.ufwErr
			}
			return nil, r.socketErr
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

func executeSSH(t *testing.T, recorder *sshRecorder, args ...string) (string, error) {
	t.Helper()
	command := newSSHCmd(recorder.dependencies())
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}

func TestSSHPortShowSinglePort(t *testing.T) {
	recorder := newSSHRecorder()
	output, err := executeSSH(t, recorder, "port")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorder.detected != 1 {
		t.Errorf("detectOS called %d times, want 1", recorder.detected)
	}
	if recorder.sshPortCalls != 1 {
		t.Errorf("detectSSHPorts called %d times, want 1", recorder.sshPortCalls)
	}
	if !strings.Contains(output, "Effective SSH port(s): 22") {
		t.Errorf("output %q does not contain effective port line", output)
	}
	if len(recorder.commands) != 0 {
		t.Errorf("show path ran commands %v, want none", recorder.commands)
	}
}

func TestSSHPortShowMultiplePorts(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.sshPorts = [][]int{{22, 10222}}
	output, err := executeSSH(t, recorder, "port")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Effective SSH port(s): 22, 10222") {
		t.Errorf("output %q does not contain multi-port line", output)
	}
}

func TestSSHPortShowFailsWhenDetectionFails(t *testing.T) {
	wantErr := errors.New("sshd -T failed")
	recorder := newSSHRecorder()
	recorder.sshPortErrs = []error{wantErr}
	_, err := executeSSH(t, recorder, "port")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
}

func TestSSHPortStopsWhenOSIsUnsupported(t *testing.T) {
	wantErr := errors.New("unsupported test OS")
	recorder := newSSHRecorder()
	recorder.detectErr = wantErr
	_, err := executeSSH(t, recorder, "port")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if recorder.sshPortCalls != 0 || len(recorder.commands) != 0 {
		t.Errorf("unsupported OS caused detections=%d commands=%v", recorder.sshPortCalls, recorder.commands)
	}
}

func TestSSHPortRejectsExtraArguments(t *testing.T) {
	recorder := newSSHRecorder()
	_, err := executeSSH(t, recorder, "port", "10222", "extra")
	if err == nil {
		t.Fatal("Execute() error = nil, want argument error")
	}
	if recorder.detected != 0 || len(recorder.commands) != 0 {
		t.Errorf("extra args caused detectOS=%d commands=%v", recorder.detected, recorder.commands)
	}
}

func TestSSHPortChangeHappyPathWithoutPreviousConfig(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.sshPorts = [][]int{{22}, {10222}} // before, verification
	output, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorder.detected != 1 {
		t.Errorf("detectOS called %d times, want 1", recorder.detected)
	}
	if recorder.sshPortCalls != 2 {
		t.Errorf("detectSSHPorts called %d times, want 2 (before + verify)", recorder.sshPortCalls)
	}
	wantQueries := [][]string{{"ufw", "fail2ban"}}
	if !reflect.DeepEqual(recorder.queries, wantQueries) {
		t.Errorf("queries = %v, want %v", recorder.queries, wantQueries)
	}
	// ufw absent + fail2ban absent → no ufw inspection, no jail sync.
	for _, call := range recorder.outputCalls {
		if call[0] == "ufw" {
			t.Errorf("ufw inspected despite package absent: %v", recorder.outputCalls)
		}
	}
	steps, err := sshx.PortSteps(10222)
	if err != nil {
		t.Fatalf("sshx.PortSteps() error = %v", err)
	}
	want := recordedCommands(steps)
	want = append(want, recordedCommands([]installer.Step{sshx.SyntaxCheckStep()})...)
	want = append(want, recordedCommands(sshx.RestartSteps(false))...)
	if !reflect.DeepEqual(recorder.commands, want) {
		t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
	}
	for _, wantText := range []string{
		"SSH port is set to 10222.",
		"keep this session open",
		"ssh -p 10222",
		"Old-port cleanup example: sudo ufw delete allow 22/tcp",
		"Fail2Ban is not installed; skipped SSH jail sync.",
	} {
		if !strings.Contains(output, wantText) {
			t.Errorf("output %q does not contain %q", output, wantText)
		}
	}
}

func TestSSHPortChangeBacksUpAndCleansUpExistingConfig(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.fileExists = true
	recorder.sshPorts = [][]int{{22}, {10222}}
	_, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := []string{sshx.PortConfigPath}; !reflect.DeepEqual(recorder.existsCalls, want) {
		t.Errorf("fileExists calls = %v, want %v", recorder.existsCalls, want)
	}
	steps, err := sshx.PortSteps(10222)
	if err != nil {
		t.Fatalf("sshx.PortSteps() error = %v", err)
	}
	want := recordedCommands([]installer.Step{sshx.BackupStep()})
	want = append(want, recordedCommands(steps)...)
	want = append(want, recordedCommands([]installer.Step{sshx.SyntaxCheckStep()})...)
	want = append(want, recordedCommands([]installer.Step{sshx.RemoveBackupStep()})...)
	want = append(want, recordedCommands(sshx.RestartSteps(false))...)
	if !reflect.DeepEqual(recorder.commands, want) {
		t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
	}
}

func TestSSHPortChangeIdempotentSkipsSSHDButSyncsFail2ban(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.statuses = map[string]dpkgx.Status{"fail2ban": dpkgx.Installed}
	recorder.sshPorts = [][]int{{10222}}
	output, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorder.sshPortCalls != 1 {
		t.Errorf("detectSSHPorts called %d times, want 1 (no verification pass)", recorder.sshPortCalls)
	}
	if !strings.Contains(output, "SSH already listens only on port 10222; skipping sshd configuration.") {
		t.Errorf("output %q does not contain idempotence message", output)
	}
	if want := fail2banPortCommands("10222"); !reflect.DeepEqual(recorder.commands, want) {
		t.Errorf("runner calls mismatch\ngot:  %#v\nwant: %#v", recorder.commands, want)
	}
}

func TestSSHPortChangeMultiplePortsIsNotIdempotent(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.sshPorts = [][]int{{22, 10222}, {10222}}
	_, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorder.sshPortCalls != 2 {
		t.Errorf("detectSSHPorts called %d times, want 2 (full flow)", recorder.sshPortCalls)
	}
	if len(recorder.commands) == 0 {
		t.Error("multi-port state skipped sshd configuration, want full flow")
	}
}

func TestSSHPortChangeRefusesWhenUfwBlocksPort(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.statuses = map[string]dpkgx.Status{"ufw": dpkgx.Installed}
	recorder.ufwOutput = "Status: active\n\nTo   Action   From\n--   ------   ----\n22/tcp   ALLOW   Anywhere\n"
	_, err := executeSSH(t, recorder, "port", "10222")
	if err == nil {
		t.Fatal("Execute() error = nil, want ufw refusal")
	}
	for _, want := range []string{"ufw is active", "sudo ufw allow 10222/tcp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Execute() error = %q, want %q", err, want)
		}
	}
	if len(recorder.commands) != 0 {
		t.Errorf("ufw refusal ran commands %v, want none", recorder.commands)
	}
}

func TestSSHPortChangeProceedsWhenUfwAllowsPort(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.statuses = map[string]dpkgx.Status{"ufw": dpkgx.Installed}
	recorder.ufwOutput = "Status: active\n\nTo   Action   From\n--   ------   ----\n10222/tcp   ALLOW   Anywhere\n"
	recorder.sshPorts = [][]int{{22}, {10222}}
	_, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recorder.commands) == 0 {
		t.Error("allowed port ran no commands, want full flow")
	}
}

func TestSSHPortChangeRefusesWhenUfwStateUnknown(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.statuses = map[string]dpkgx.Status{"ufw": dpkgx.Installed}
	recorder.ufwErr = errors.New("ufw status failed")
	_, err := executeSSH(t, recorder, "port", "10222")
	if err == nil || !strings.Contains(err.Error(), "checking ufw state failed") {
		t.Fatalf("Execute() error = %v, want ufw state error", err)
	}
	if len(recorder.commands) != 0 {
		t.Errorf("unknown ufw state ran commands %v, want none", recorder.commands)
	}
}

func TestSSHPortChangeInactiveUfwProceeds(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.statuses = map[string]dpkgx.Status{"ufw": dpkgx.Installed}
	recorder.ufwOutput = "Status: inactive\n"
	recorder.sshPorts = [][]int{{22}, {10222}}
	if _, err := executeSSH(t, recorder, "port", "10222"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSSHPortChangeRollsBackWhenSyntaxCheckFails(t *testing.T) {
	tests := []struct {
		name      string
		hadConfig bool
		finalStep installer.Step
	}{
		{name: "no previous config removes file", finalStep: sshx.RemoveConfigStep()},
		{name: "previous config restored", hadConfig: true, finalStep: sshx.RestoreStep()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantErr := errors.New("sshd -t failed")
			recorder := newSSHRecorder()
			recorder.fileExists = tt.hadConfig
			recorder.sshPorts = [][]int{{22}}
			syntaxIndex := 3 // PortSteps(3) then SyntaxCheckStep
			if tt.hadConfig {
				syntaxIndex = 4 // BackupStep first
			}
			recorder.runErrorAt = syntaxIndex
			recorder.runErr = wantErr
			_, err := executeSSH(t, recorder, "port", "10222")
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
			}
			last := recorder.commands[len(recorder.commands)-1]
			if !reflect.DeepEqual(last.argv, tt.finalStep.Argv) {
				t.Errorf("last command = %v, want rollback %v", last.argv, tt.finalStep.Argv)
			}
			for _, command := range recorder.commands {
				if command.argv[0] == "systemctl" {
					t.Errorf("validation failure restarted services: %v", recorder.commands)
				}
			}
		})
	}
}

func TestSSHPortChangeRollsBackWhenEffectivePortMismatch(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.sshPorts = [][]int{{22}, {22}} // verification still reports 22
	_, err := executeSSH(t, recorder, "port", "10222")
	if err == nil {
		t.Fatalf("Execute() error = nil, want mismatch error")
	}
	for _, want := range []string{"port(s) 22", "Port values accumulate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Execute() error = %q, want %q", err, want)
		}
	}
	last := recorder.commands[len(recorder.commands)-1]
	if !reflect.DeepEqual(last.argv, sshx.RemoveConfigStep().Argv) {
		t.Errorf("last command = %v, want config removal", last.argv)
	}
	for _, command := range recorder.commands {
		if command.argv[0] == "systemctl" {
			t.Errorf("mismatch restarted services: %v", recorder.commands)
		}
	}
}

func TestSSHPortChangeSocketActivatedRestart(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.socketErr = nil // is-enabled ssh.socket succeeds → socket mode
	recorder.sshPorts = [][]int{{22}, {10222}}
	_, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var systemctl [][]string
	for _, command := range recorder.commands {
		if command.argv[0] == "systemctl" {
			systemctl = append(systemctl, command.argv)
		}
	}
	want := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "restart", "ssh.socket"},
		{"systemctl", "restart", "ssh.service"},
	}
	if !reflect.DeepEqual(systemctl, want) {
		t.Errorf("systemctl calls = %v, want %v", systemctl, want)
	}
}

func TestSSHPortChangeSyncsInstalledFail2ban(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.statuses = map[string]dpkgx.Status{"fail2ban": dpkgx.Installed}
	recorder.sshPorts = [][]int{{22}, {10222}}
	output, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "Syncing Fail2Ban SSH jail") {
		t.Errorf("output %q does not mention fail2ban sync", output)
	}
	jail := fail2banPortCommands("10222")
	tail := recorder.commands[len(recorder.commands)-len(jail):]
	if !reflect.DeepEqual(tail, jail) {
		t.Errorf("fail2ban sync commands mismatch\ngot:  %#v\nwant: %#v", tail, jail)
	}
}

func TestSSHPortChangeInvalidArgumentRejectedEarly(t *testing.T) {
	for _, value := range []string{"abc", "0", "-5", "65536"} {
		t.Run(value, func(t *testing.T) {
			recorder := newSSHRecorder()
			_, err := executeSSH(t, recorder, "port", value)
			if err == nil {
				t.Fatal("Execute() error = nil, want parse error")
			}
			if recorder.detected != 0 || len(recorder.commands) != 0 {
				t.Errorf("invalid port caused detectOS=%d commands=%v", recorder.detected, recorder.commands)
			}
		})
	}
}

func TestSSHPortChangeBrokenFail2banWarnsAndSkips(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.statuses = map[string]dpkgx.Status{"fail2ban": dpkgx.Broken}
	recorder.sshPorts = [][]int{{22}, {10222}}
	output, err := executeSSH(t, recorder, "port", "10222")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "broken package state; skipped SSH jail sync") {
		t.Errorf("output %q does not contain broken fail2ban warning", output)
	}
	for _, command := range recorder.commands {
		if command.argv[0] == "/usr/bin/fail2ban-client" || strings.Contains(command.input, "[sshd]") {
			t.Errorf("broken fail2ban still synced: %v", recorder.commands)
		}
	}
}

func TestSSHPortChangeStopsWhenQueryFails(t *testing.T) {
	wantErr := errors.New("dpkg query failed")
	recorder := newSSHRecorder()
	recorder.queryErr = wantErr
	_, err := executeSSH(t, recorder, "port", "10222")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if len(recorder.commands) != 0 {
		t.Errorf("query failure ran commands %v, want none", recorder.commands)
	}
}

func TestSSHPortChangeStopsWhenFileCheckFails(t *testing.T) {
	wantErr := errors.New("stat failed")
	recorder := newSSHRecorder()
	recorder.fileExistsErr = wantErr
	recorder.sshPorts = [][]int{{22}}
	_, err := executeSSH(t, recorder, "port", "10222")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(_, wantErr)", err)
	}
	if len(recorder.commands) != 0 {
		t.Errorf("stat failure ran commands %v, want none", recorder.commands)
	}
}

func TestSSHPortChangeRestartFailureExplainsRecovery(t *testing.T) {
	recorder := newSSHRecorder()
	recorder.sshPorts = [][]int{{22}, {10222}}
	recorder.runErrorAt = 4 // PortSteps(3) + SyntaxCheckStep(1), then daemon-reload fails
	recorder.runErr = errors.New("daemon-reload failed")
	_, err := executeSSH(t, recorder, "port", "10222")
	if err == nil {
		t.Fatal("Execute() error = nil, want restart failure")
	}
	for _, want := range []string{"journalctl -u ssh", "session stays alive", sshx.PortConfigPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Execute() error = %q, want %q", err, want)
		}
	}
}

func TestSSHPortChangePassesCommandContext(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	recorder := newSSHRecorder()
	// Exercise every dependency: ufw active (output + inspect), full sshd
	// flow (run + detectSSHPorts before/verify), and fail2ban sync (run).
	recorder.statuses = map[string]dpkgx.Status{"ufw": dpkgx.Installed, "fail2ban": dpkgx.Installed}
	recorder.ufwOutput = "Status: active\n\nTo   Action   From\n--   ------   ----\n10222/tcp   ALLOW   Anywhere\n"
	recorder.sshPorts = [][]int{{22}, {10222}}
	command := newSSHCmd(recorder.dependencies())
	command.SetContext(ctx)
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"port", "10222"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	assertContexts := func(name string, got []context.Context) {
		if len(got) == 0 {
			t.Errorf("%s recorded no contexts, want the command context propagated", name)
		}
		for i, c := range got {
			if c != ctx {
				t.Errorf("%s context[%d] = %v, want injected command context", name, i, c)
			}
		}
	}
	assertContexts("detectSSHPorts", recorder.sshPortContexts)
	assertContexts("queryPackages", recorder.queryContexts)
	assertContexts("output", recorder.outputContexts)
	assertContexts("run", recorder.runContexts)
}

func TestSSHPortHelpDisclosesSafetyModel(t *testing.T) {
	recorder := newSSHRecorder()
	output, err := executeSSH(t, recorder, "port", "--help")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		sshx.PortConfigPath,
		"never modified",
		"sshd -t",
		"restored and SSH is not restarted",
		"ft does not change",
		"firewall rules",
		"Keep your current session open",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output %q does not contain %q", output, want)
		}
	}
	if recorder.detected != 0 || len(recorder.commands) != 0 {
		t.Errorf("help caused detectOS=%d commands=%v", recorder.detected, recorder.commands)
	}
}
