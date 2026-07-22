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

type ufwRecorder struct {
	statuses      map[string]dpkgx.Status
	detectErr     error
	queryErr      error
	queries       [][]string
	verboseOutput string
	verboseErr    error
	afterRules    string
	afterRulesErr error
	numbered      []string // consumed per status-numbered call; last entry repeats
	numberedCalls int
	appInfo       map[string]string
	appInfoErr    error
	outputCalls   [][]string
	commands      []recordedInstallCommand
	runErrorAt    int
	runErr        error
	v4            string
	v6            string
	v4Err         error
	v6Err         error
	fetchCalls    []string
}

func newUFWRecorder() *ufwRecorder {
	return &ufwRecorder{
		statuses:      map[string]dpkgx.Status{"ufw": dpkgx.Installed},
		verboseOutput: testVerboseActive,
		afterRulesErr: errors.New("cat: /etc/ufw/after.rules: No such file or directory"),
		numbered:      []string{testNumberedClean},
		v4:            testCloudflareV4,
		v6:            testCloudflareV6,
		runErrorAt:    -1,
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
				return []byte(r.afterRules), r.afterRulesErr
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
			current := len(r.commands)
			r.commands = append(r.commands, recordedInstallCommand{argv: append([]string(nil), argv...)})
			if current == r.runErrorAt {
				return r.runErr
			}
			return nil
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

func TestUFWOnlyCloudflareHappyPathWithoutDocker(t *testing.T) {
	recorder := newUFWRecorder()
	output, err := executeUFW(t, recorder, "only-cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}

	if want := [][]string{{"ufw", "docker-ce", "docker.io"}}; !reflect.DeepEqual(recorder.queries, want) {
		t.Errorf("package queries = %v, want %v", recorder.queries, want)
	}
	if want := []string{"https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"}; !reflect.DeepEqual(recorder.fetchCalls, want) {
		t.Errorf("fetch calls = %v, want %v", recorder.fetchCalls, want)
	}

	want := make([][]string, 0, len(testSortedRanges)+1)
	for _, cidr := range testSortedRanges {
		want = append(want, allowArgv(cidr))
	}
	want = append(want, []string{"ufw", "reload"})
	assertCommands(t, recorder.commands, want)

	if !strings.Contains(output, "5 IPv4 + 2 IPv6") {
		t.Errorf("output %q does not mention the range counts", output)
	}
	if !strings.Contains(output, "No conflicting rules found.") {
		t.Errorf("output %q does not mention the empty cleanup", output)
	}
	if !strings.Contains(output, "re-run") {
		t.Errorf("output %q does not remind about refreshing ranges", output)
	}
}

func TestUFWOnlyCloudflareAddsRouteRulesWithUFWDocker(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.afterRules = "# BEGIN UFW AND DOCKER\n-A DOCKER-USER -j RETURN\n# END UFW AND DOCKER\n"
	recorder.afterRulesErr = nil
	output, err := executeUFW(t, recorder, "only-cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, output)
	}

	want := make([][]string, 0, 2*len(testSortedRanges)+1)
	for _, cidr := range testSortedRanges {
		want = append(want, allowArgv(cidr), routeAllowArgv(cidr))
	}
	want = append(want, []string{"ufw", "reload"})
	assertCommands(t, recorder.commands, want)

	if !strings.Contains(output, "ufw-docker") {
		t.Errorf("output %q does not mention the route rule coverage", output)
	}
}

func TestUFWOnlyCloudflareRefusesDockerWithoutUFWDocker(t *testing.T) {
	for _, pkg := range []string{"docker-ce", "docker.io"} {
		t.Run(pkg, func(t *testing.T) {
			recorder := newUFWRecorder()
			recorder.statuses[pkg] = dpkgx.Installed
			_, err := executeUFW(t, recorder, "only-cloudflare")
			if err == nil || !strings.Contains(err.Error(), "ufw-docker") {
				t.Fatalf("Execute() error = %v, want ufw-docker guidance", err)
			}
			if len(recorder.commands) != 0 {
				t.Errorf("ran commands %v, want none", recorder.commands)
			}
			if len(recorder.fetchCalls) != 0 {
				t.Errorf("fetched %v before refusing, want no fetches", recorder.fetchCalls)
			}
		})
	}
}

func TestUFWOnlyCloudflareProceedsWithDockerAndUFWDocker(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.statuses["docker-ce"] = dpkgx.Installed
	recorder.afterRules = "# BEGIN UFW AND DOCKER\n"
	recorder.afterRulesErr = nil
	_, err := executeUFW(t, recorder, "only-cloudflare")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recorder.commands) != 2*len(testSortedRanges)+1 {
		t.Errorf("ran %d commands, want %d", len(recorder.commands), 2*len(testSortedRanges)+1)
	}
}

func TestUFWOnlyCloudflareGuards(t *testing.T) {
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
			_, err := executeUFW(t, recorder, "only-cloudflare")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Execute() error = %v, want it to contain %q", err, tt.wantErr)
			}
			if len(recorder.commands) != 0 {
				t.Errorf("ran commands %v, want none", recorder.commands)
			}
		})
	}
}

func TestUFWOnlyCloudflareFetchFailureLeavesRulesUnread(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.v6Err = errors.New("timeout")
	_, err := executeUFW(t, recorder, "only-cloudflare")
	if err == nil || !strings.Contains(err.Error(), "fetching Cloudflare IPv6 list") {
		t.Fatalf("Execute() error = %v, want fetch failure", err)
	}
	for _, call := range recorder.outputCalls {
		if strings.Join(call, " ") == "ufw status numbered" {
			t.Errorf("queried numbered rules despite fetch failure: %v", recorder.outputCalls)
		}
	}
}

func TestUFWOnlyCloudflareDeletesConflictingRules(t *testing.T) {
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
	output, err := executeUFW(t, recorder, "only-cloudflare")
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

func TestUFWOnlyCloudflareWarnsAboutKeptRules(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.numbered = []string{
		"Status: active\n" +
			"[ 1] Anywhere                   ALLOW IN    10.8.0.5\n" +
			"[ 2] 443                        DENY IN     203.0.113.9\n",
	}
	output, err := executeUFW(t, recorder, "only-cloudflare")
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

func TestUFWOnlyCloudflareAppProfileResolutionFailure(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.appInfoErr = errors.New("profile missing")
	recorder.numbered = []string{
		"Status: active\n[ 1] Mystery App                ALLOW IN    Anywhere\n",
	}
	_, err := executeUFW(t, recorder, "only-cloudflare")
	if err == nil || !strings.Contains(err.Error(), "Mystery App") {
		t.Fatalf("Execute() error = %v, want app profile failure", err)
	}
	if len(recorder.commands) != 0 {
		t.Errorf("ran commands %v, want none", recorder.commands)
	}
}

func TestUFWOnlyCloudflareRunFailureExplainsFailClosed(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.runErrorAt = 2
	recorder.runErr = errors.New("ufw exploded")
	_, err := executeUFW(t, recorder, "only-cloudflare")
	if err == nil || !strings.Contains(err.Error(), "may be blocked") {
		t.Fatalf("Execute() error = %v, want fail-closed explanation", err)
	}
}

func TestUFWOnlyCloudflareDeletionMustConverge(t *testing.T) {
	recorder := newUFWRecorder()
	recorder.numbered = []string{
		"Status: active\n[ 1] 80/tcp                     ALLOW IN    Anywhere\n",
	}
	_, err := executeUFW(t, recorder, "only-cloudflare")
	if err == nil || !strings.Contains(err.Error(), "converge") {
		t.Fatalf("Execute() error = %v, want convergence failure", err)
	}
}
