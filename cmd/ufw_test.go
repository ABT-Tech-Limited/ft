package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/osrelease"
)

const (
	testCloudflareV4 = "173.245.48.0/20\n103.21.244.0/22\n141.101.64.0/18\n108.162.192.0/18\n190.93.240.0/20\n"
	testCloudflareV6 = "2606:4700::/32\n2400:cb00::/32\n"

	testVerboseActive = "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"

	testNumberedClean = "Status: active\n\n     To                         Action      From\n     --                         ------      ----\n[ 1] 22/tcp                     ALLOW IN    Anywhere\n"

	testUFWDockerMarker = "# BEGIN UFW AND DOCKER\n-A DOCKER-USER -j RETURN\n# END UFW AND DOCKER\n"

	testExecutable = "/usr/local/bin/ft"
)

var testSortedRanges = []string{
	"103.21.244.0/22",
	"108.162.192.0/18",
	"141.101.64.0/18",
	"173.245.48.0/20",
	"190.93.240.0/20",
	"2400:cb00::/32",
	"2606:4700::/32",
}

// testNumberedInSync lists exactly the managed rules matching testSortedRanges
// on the INPUT chain, plus an unrelated SSH rule.
const testNumberedInSync = "Status: active\n" +
	"[ 1] 22/tcp                     ALLOW IN    Anywhere\n" +
	"[ 2] 80,443/tcp                 ALLOW IN    103.21.244.0/22            # ft-cloudflare\n" +
	"[ 3] 80,443/tcp                 ALLOW IN    108.162.192.0/18           # ft-cloudflare\n" +
	"[ 4] 80,443/tcp                 ALLOW IN    141.101.64.0/18            # ft-cloudflare\n" +
	"[ 5] 80,443/tcp                 ALLOW IN    173.245.48.0/20            # ft-cloudflare\n" +
	"[ 6] 80,443/tcp                 ALLOW IN    190.93.240.0/20            # ft-cloudflare\n" +
	"[ 7] 80,443/tcp (v6)            ALLOW IN    2400:cb00::/32             # ft-cloudflare\n" +
	"[ 8] 80,443/tcp (v6)            ALLOW IN    2606:4700::/32             # ft-cloudflare\n"

type afterRulesResponse struct {
	content string
	err     error
}

type ufwRecorder struct {
	statuses        map[string]dpkgx.Status
	detectErr       error
	queryErr        error
	queries         [][]string
	verboseOutput   string
	verboseErr      error
	afterRules      []afterRulesResponse // consumed per cat call; last entry repeats
	afterRulesCalls int
	numbered        []string // consumed per status-numbered call; last entry repeats
	numberedCalls   int
	appInfo         map[string]string
	appInfoErr      error
	sha256Output    string
	sha256Err       error
	outputCalls     [][]string
	commands        []recordedInstallCommand
	runErrorAt      int
	runErr          error
	executablePath  string
	executableErr   error
	v4              string
	v6              string
	v4Err           error
	v6Err           error
	fetchCalls      []string
}

func newUFWRecorder() *ufwRecorder {
	return &ufwRecorder{
		statuses: map[string]dpkgx.Status{
			"ufw":  dpkgx.Installed,
			"cron": dpkgx.Installed,
		},
		verboseOutput: testVerboseActive,
		afterRules: []afterRulesResponse{
			{err: errors.New("cat: /etc/ufw/after.rules: No such file or directory")},
		},
		numbered:       []string{testNumberedClean},
		sha256Output:   "643e56b080567c567b4aa28650196849b2a2da5dd0473fd3e5216b0886035ab0  /usr/local/bin/ufw-docker.tmp\n",
		executablePath: testExecutable,
		v4:             testCloudflareV4,
		v6:             testCloudflareV6,
		runErrorAt:     -1,
	}
}

func (r *ufwRecorder) dependencies() ufwDependencies {
	return ufwDependencies{
		detectOS: func() (osrelease.Info, error) {
			if r.detectErr != nil {
				return osrelease.Info{}, r.detectErr
			}
			return osrelease.Info{ID: "ubuntu", VersionID: "24.04"}, nil
		},
		queryPackages: func(ctx context.Context, packages ...string) (map[string]dpkgx.Status, error) {
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
		output: func(ctx context.Context, argv []string) ([]byte, error) {
			r.outputCalls = append(r.outputCalls, append([]string(nil), argv...))
			key := strings.Join(argv, " ")
			switch {
			case key == "ufw status verbose":
				return []byte(r.verboseOutput), r.verboseErr
			case key == "ufw status numbered":
				index := r.numberedCalls
				r.numberedCalls++
				if index >= len(r.numbered) {
					index = len(r.numbered) - 1
				}
				return []byte(r.numbered[index]), nil
			case key == "cat /etc/ufw/after.rules":
				index := r.afterRulesCalls
				r.afterRulesCalls++
				if index >= len(r.afterRules) {
					index = len(r.afterRules) - 1
				}
				response := r.afterRules[index]
				return []byte(response.content), response.err
			case key == "sha256sum /usr/local/bin/ufw-docker.tmp":
				return []byte(r.sha256Output), r.sha256Err
			case len(argv) == 4 && key == "ufw app info "+argv[3]:
				if r.appInfoErr != nil {
					return nil, r.appInfoErr
				}
				return []byte(r.appInfo[argv[3]]), nil
			default:
				return nil, fmt.Errorf("unexpected output command %q", key)
			}
		},
		run: func(ctx context.Context, argv []string, stdin io.Reader) error {
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
		executable: func() (string, error) {
			return r.executablePath, r.executableErr
		},
		fetch: func(ctx context.Context, url string) ([]byte, error) {
			r.fetchCalls = append(r.fetchCalls, url)
			if strings.HasSuffix(url, "ips-v4") {
				return []byte(r.v4), r.v4Err
			}
			return []byte(r.v6), r.v6Err
		},
	}
}

func executeUFW(t *testing.T, recorder *ufwRecorder, args ...string) (string, error) {
	t.Helper()
	command := newUFWCmd(recorder.dependencies())
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}

func allowArgv(cidr string) []string {
	return []string{"ufw", "allow", "proto", "tcp", "from", cidr, "to", "any", "port", "80,443", "comment", "ft-cloudflare"}
}

func routeAllowArgv(cidr string) []string {
	return []string{"ufw", "route", "allow", "proto", "tcp", "from", cidr, "to", "any", "port", "80,443", "comment", "ft-cloudflare"}
}

func cronArgvs() [][]string {
	return [][]string{
		{"install", "-o", "root", "-g", "root", "-m", "0644", "/dev/stdin", "/etc/cron.d/ft-ufw-cloudflare.tmp"},
		{"mv", "-f", "/etc/cron.d/ft-ufw-cloudflare.tmp", "/etc/cron.d/ft-ufw-cloudflare"},
	}
}

func ufwDockerInstallArgvs() [][]string {
	return [][]string{
		{
			"curl", "--fail", "--show-error", "--silent", "--location",
			"--proto", "=https", "--tlsv1.2", "--remove-on-error",
			"--output", "/usr/local/bin/ufw-docker.tmp",
			"https://raw.githubusercontent.com/chaifeng/ufw-docker/020a8699f95592561f254d8d4ad1bb40d401dfc7/ufw-docker",
		},
		{"chmod", "0755", "/usr/local/bin/ufw-docker.tmp"},
		{"mv", "-f", "/usr/local/bin/ufw-docker.tmp", "/usr/local/bin/ufw-docker"},
		{"/usr/local/bin/ufw-docker", "install"},
		{"systemctl", "restart", "ufw"},
	}
}

func assertCommands(t *testing.T, got []recordedInstallCommand, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ran %d commands, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i].argv, want[i]) {
			t.Errorf("command %d = %v, want %v", i, got[i].argv, want[i])
		}
	}
}

func TestUFWCloudflareHappyPathWithoutDocker(t *testing.T) {
	recorder := newUFWRecorder()
	output, err := executeUFW(t, recorder, "cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}

	if want := [][]string{{"ufw", "docker-ce", "docker.io", "cron"}}; !reflect.DeepEqual(recorder.queries, want) {
		t.Errorf("package queries = %v, want %v", recorder.queries, want)
	}
	if want := []string{"https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"}; !reflect.DeepEqual(recorder.fetchCalls, want) {
		t.Errorf("fetch calls = %v, want %v", recorder.fetchCalls, want)
	}

	want := make([][]string, 0, len(testSortedRanges)+3)
	for _, cidr := range testSortedRanges {
		want = append(want, allowArgv(cidr))
	}
	want = append(want, []string{"ufw", "reload"})
	want = append(want, cronArgvs()...)
	assertCommands(t, recorder.commands, want)

	cronContent := recorder.commands[len(recorder.commands)-2].input
	if !strings.Contains(cronContent, `17 * * * * root "/usr/local/bin/ft" ufw cloudflare >> /var/log/ft-ufw-cloudflare.log 2>&1`) {
		t.Errorf("cron file content %q does not contain the hourly entry", cronContent)
	}
	if !strings.Contains(cronContent, "Managed by ft") {
		t.Errorf("cron file content %q lacks the managed-by marker", cronContent)
	}

	if !strings.Contains(output, "5 IPv4 + 2 IPv6") {
		t.Errorf("output %q does not mention the range counts", output)
	}
	if !strings.Contains(output, "No conflicting rules found.") {
		t.Errorf("output %q does not mention the empty cleanup", output)
	}
	if !strings.Contains(output, "every hour") {
		t.Errorf("output %q does not mention the hourly refresh", output)
	}
}

func TestUFWCloudflareAddsRouteRulesWithUFWDocker(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.afterRules = []afterRulesResponse{{content: testUFWDockerMarker}}
	output, err := executeUFW(t, recorder, "cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}

	want := make([][]string, 0, 2*len(testSortedRanges)+3)
	for _, cidr := range testSortedRanges {
		want = append(want, allowArgv(cidr), routeAllowArgv(cidr))
	}
	want = append(want, []string{"ufw", "reload"})
	want = append(want, cronArgvs()...)
	assertCommands(t, recorder.commands, want)

	if !strings.Contains(output, "ufw-docker") {
		t.Errorf("output %q does not mention the route rule coverage", output)
	}
}

func TestUFWCloudflareInstallsUFWDockerWhenDockerPresent(t *testing.T) {
	for _, pkg := range []string{"docker-ce", "docker.io"} {
		t.Run(pkg, func(t *testing.T) {
			recorder := newUFWRecorder()
			recorder.statuses[pkg] = dpkgx.Installed
			recorder.afterRules = []afterRulesResponse{
				{content: "# regular ufw after rules\n"},
				{content: testUFWDockerMarker},
			}
			output, err := executeUFW(t, recorder, "cloudflare")
			if err != nil {
				t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
			}

			want := ufwDockerInstallArgvs()
			for _, cidr := range testSortedRanges {
				want = append(want, allowArgv(cidr), routeAllowArgv(cidr))
			}
			want = append(want, []string{"ufw", "reload"})
			want = append(want, cronArgvs()...)
			assertCommands(t, recorder.commands, want)

			checksumChecked := false
			for _, call := range recorder.outputCalls {
				if strings.Join(call, " ") == "sha256sum /usr/local/bin/ufw-docker.tmp" {
					checksumChecked = true
				}
			}
			if !checksumChecked {
				t.Error("downloaded ufw-docker script was not checksum-verified")
			}
			if recorder.afterRulesCalls != 2 {
				t.Errorf("read after.rules %d times, want 2 (detect + verify)", recorder.afterRulesCalls)
			}
		})
	}
}

func TestUFWCloudflareRejectsUFWDockerChecksumMismatch(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.statuses["docker-ce"] = dpkgx.Installed
	recorder.afterRules = []afterRulesResponse{{content: "# no marker\n"}}
	recorder.sha256Output = "deadbeef  /usr/local/bin/ufw-docker.tmp\n"
	_, err := executeUFW(t, recorder, "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Execute() error = %v, want checksum mismatch", err)
	}
	want := [][]string{
		ufwDockerInstallArgvs()[0],
		{"rm", "-f", "/usr/local/bin/ufw-docker.tmp"},
	}
	assertCommands(t, recorder.commands, want)
}

func TestUFWCloudflareFailsWhenUFWDockerInstallLeavesNoMarker(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.statuses["docker-ce"] = dpkgx.Installed
	recorder.afterRules = []afterRulesResponse{{content: "# no marker\n"}}
	_, err := executeUFW(t, recorder, "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "did not add") {
		t.Fatalf("Execute() error = %v, want missing-marker failure", err)
	}
	assertCommands(t, recorder.commands, ufwDockerInstallArgvs())
}

func TestUFWCloudflareSkipsRebuildWhenRulesInSync(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.numbered = []string{testNumberedInSync}
	output, err := executeUFW(t, recorder, "cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}
	assertCommands(t, recorder.commands, cronArgvs())
	if !strings.Contains(output, "already match") {
		t.Errorf("output %q does not mention the in-sync fast path", output)
	}
	if recorder.numberedCalls != 1 {
		t.Errorf("queried numbered rules %d times, want 1", recorder.numberedCalls)
	}
}

func TestUFWCloudflareRebuildsWhenRouteRulesMissing(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.afterRules = []afterRulesResponse{{content: testUFWDockerMarker}}
	recorder.numbered = []string{testNumberedInSync, testNumberedClean}
	_, err := executeUFW(t, recorder, "cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recorder.commands) == 0 || !reflect.DeepEqual(recorder.commands[0].argv, []string{"ufw", "--force", "delete", "2"}) {
		t.Fatalf("first command = %+v, want deletion of the stale managed rule", recorder.commands)
	}
}

func TestUFWCloudflareSkipsCronWhenCronAbsent(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.statuses["cron"] = dpkgx.Absent
	output, err := executeUFW(t, recorder, "cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}
	want := make([][]string, 0, len(testSortedRanges)+1)
	for _, cidr := range testSortedRanges {
		want = append(want, allowArgv(cidr))
	}
	want = append(want, []string{"ufw", "reload"})
	assertCommands(t, recorder.commands, want)
	if !strings.Contains(output, "not registered") {
		t.Errorf("output %q does not warn about the missing cron package", output)
	}
}

func TestUFWCloudflareGuards(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ufwRecorder)
		wantErr string
	}{
		{
			name:    "os detection failure",
			mutate:  func(r *ufwRecorder) { r.detectErr = errors.New("unsupported system") },
			wantErr: "unsupported system",
		},
		{
			name:    "executable resolution failure",
			mutate:  func(r *ufwRecorder) { r.executableErr = errors.New("proc unavailable") },
			wantErr: "executable",
		},
		{
			name:    "package query failure",
			mutate:  func(r *ufwRecorder) { r.queryErr = errors.New("dpkg exploded") },
			wantErr: "dpkg exploded",
		},
		{
			name:    "ufw not installed",
			mutate:  func(r *ufwRecorder) { r.statuses["ufw"] = dpkgx.Absent },
			wantErr: "ufw is not installed",
		},
		{
			name:    "ufw broken",
			mutate:  func(r *ufwRecorder) { r.statuses["ufw"] = dpkgx.Broken },
			wantErr: "broken",
		},
		{
			name:    "ufw status unreadable",
			mutate:  func(r *ufwRecorder) { r.verboseErr = errors.New("permission denied") },
			wantErr: "reading ufw status",
		},
		{
			name:    "ufw inactive",
			mutate:  func(r *ufwRecorder) { r.verboseOutput = "Status: inactive\n" },
			wantErr: "inactive",
		},
		{
			name: "default incoming allow",
			mutate: func(r *ufwRecorder) {
				r.verboseOutput = "Status: active\nDefault: allow (incoming), allow (outgoing), disabled (routed)\n"
			},
			wantErr: "default deny incoming",
		},
		{
			name:    "cloudflare fetch failure",
			mutate:  func(r *ufwRecorder) { r.v4Err = errors.New("connection reset") },
			wantErr: "fetching Cloudflare IPv4 list",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newUFWRecorder()
			tt.mutate(recorder)
			_, err := executeUFW(t, recorder, "cloudflare")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Execute() error = %v, want it to contain %q", err, tt.wantErr)
			}
			if len(recorder.commands) != 0 {
				t.Errorf("ran commands %v, want none", recorder.commands)
			}
		})
	}
}

func TestUFWCloudflareFetchFailureLeavesRulesUnread(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.v6Err = errors.New("timeout")
	_, err := executeUFW(t, recorder, "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "fetching Cloudflare IPv6 list") {
		t.Fatalf("Execute() error = %v, want fetch failure", err)
	}
	for _, call := range recorder.outputCalls {
		if strings.Join(call, " ") == "ufw status numbered" {
			t.Errorf("queried numbered rules despite fetch failure: %v", recorder.outputCalls)
		}
	}
}

func TestUFWCloudflareDeletesConflictingRules(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.appInfo = map[string]string{"Nginx Full": "Profile: Nginx Full\n\nPorts:\n  80,443/tcp\n"}
	recorder.numbered = []string{
		"Status: active\n" +
			"[ 1] 22/tcp                     ALLOW IN    Anywhere\n" +
			"[ 2] 80/tcp                     ALLOW IN    Anywhere\n" +
			"[ 3] Nginx Full                 ALLOW IN    Anywhere\n" +
			"[ 4] 80,443/tcp                 ALLOW IN    198.51.100.9               # ft-cloudflare\n",
		"Status: active\n" +
			"[ 1] 22/tcp                     ALLOW IN    Anywhere\n" +
			"[ 2] Nginx Full                 ALLOW IN    Anywhere\n" +
			"[ 3] 80,443/tcp                 ALLOW IN    198.51.100.9               # ft-cloudflare\n",
		"Status: active\n" +
			"[ 1] 22/tcp                     ALLOW IN    Anywhere\n" +
			"[ 2] 80,443/tcp                 ALLOW IN    198.51.100.9               # ft-cloudflare\n",
		testNumberedClean,
	}
	output, err := executeUFW(t, recorder, "cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}

	deletes := [][]string{}
	for _, command := range recorder.commands {
		if len(command.argv) == 4 && command.argv[1] == "--force" {
			deletes = append(deletes, command.argv)
		}
	}
	want := [][]string{
		{"ufw", "--force", "delete", "2"},
		{"ufw", "--force", "delete", "2"},
		{"ufw", "--force", "delete", "2"},
	}
	if !reflect.DeepEqual(deletes, want) {
		t.Errorf("delete commands = %v, want %v", deletes, want)
	}
	if !strings.Contains(output, "Nginx Full") {
		t.Errorf("output %q does not show the deleted profile rule", output)
	}
	if recorder.numberedCalls != 4 {
		t.Errorf("queried numbered rules %d times, want 4", recorder.numberedCalls)
	}
}

func TestUFWCloudflareWarnsAboutKeptRules(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.numbered = []string{
		"Status: active\n" +
			"[ 1] Anywhere                   ALLOW IN    10.8.0.5\n" +
			"[ 2] 443                        DENY IN     203.0.113.9\n",
	}
	output, err := executeUFW(t, recorder, "cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "10.8.0.5") || !strings.Contains(output, "every port") {
		t.Errorf("output %q does not warn about the all-port rule", output)
	}
	if !strings.Contains(output, "may block Cloudflare") {
		t.Errorf("output %q does not warn about the deny rule", output)
	}
	for _, command := range recorder.commands {
		if len(command.argv) > 1 && command.argv[1] == "--force" {
			t.Errorf("deleted a rule %v, want none deleted", command.argv)
		}
	}
}

func TestUFWCloudflareAppProfileResolutionFailure(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.appInfoErr = errors.New("profile missing")
	recorder.numbered = []string{
		"Status: active\n[ 1] Mystery App                ALLOW IN    Anywhere\n",
	}
	_, err := executeUFW(t, recorder, "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "Mystery App") {
		t.Fatalf("Execute() error = %v, want app profile failure", err)
	}
	if len(recorder.commands) != 0 {
		t.Errorf("ran commands %v, want none", recorder.commands)
	}
}

func TestUFWCloudflareRunFailureExplainsFailClosed(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.runErrorAt = 2
	recorder.runErr = errors.New("ufw exploded")
	_, err := executeUFW(t, recorder, "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "may be blocked") {
		t.Fatalf("Execute() error = %v, want fail-closed explanation", err)
	}
}

func TestUFWCloudflareCronRegistrationFailure(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.runErrorAt = len(testSortedRanges) + 1 // first cron step, right after the reload
	recorder.runErr = errors.New("read-only filesystem")
	_, err := executeUFW(t, recorder, "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "registering the hourly refresh") {
		t.Fatalf("Execute() error = %v, want cron registration failure", err)
	}
	if strings.Contains(err.Error(), "may be blocked") {
		t.Errorf("cron failure %v must not claim firewall inconsistency", err)
	}
}

func TestUFWCloudflareDeletionMustConverge(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.numbered = []string{
		"Status: active\n[ 1] 80/tcp                     ALLOW IN    Anywhere\n",
	}
	_, err := executeUFW(t, recorder, "cloudflare")
	if err == nil || !strings.Contains(err.Error(), "converge") {
		t.Fatalf("Execute() error = %v, want convergence failure", err)
	}
}
