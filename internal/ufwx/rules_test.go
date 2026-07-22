package ufwx

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const verboseActiveDeny = `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
`

func TestParseVerboseStatus(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    VerboseStatus
		wantErr string
	}{
		{
			name:   "active with default deny incoming",
			output: verboseActiveDeny,
			want:   VerboseStatus{Active: true, DefaultIncomingDeny: true},
		},
		{
			name:   "active with default reject incoming",
			output: "Status: active\nDefault: reject (incoming), allow (outgoing), allow (routed)\n",
			want:   VerboseStatus{Active: true, DefaultIncomingDeny: true},
		},
		{
			name:   "active with default allow incoming",
			output: "Status: active\nDefault: allow (incoming), allow (outgoing), disabled (routed)\n",
			want:   VerboseStatus{Active: true, DefaultIncomingDeny: false},
		},
		{
			name:   "inactive",
			output: "Status: inactive\n",
			want:   VerboseStatus{},
		},
		{
			name:    "active without default line",
			output:  "Status: active\nLogging: on (low)\n",
			wantErr: "no default policy line",
		},
		{
			name:    "unknown incoming policy",
			output:  "Status: active\nDefault: maybe (incoming), allow (outgoing)\n",
			wantErr: "unknown default incoming policy",
		},
		{
			name:    "default line without incoming segment",
			output:  "Status: active\nDefault: allow (outgoing)\n",
			wantErr: "no default policy line",
		},
		{
			name:    "unknown status",
			output:  "Status: sideways\n",
			wantErr: "unknown ufw status",
		},
		{
			name:    "missing status line",
			output:  "Logging: on (low)\n",
			wantErr: "no ufw status line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVerboseStatus(tt.output)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseVerboseStatus() = %+v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseVerboseStatus() error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVerboseStatus() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseVerboseStatus() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func numberedStatus(rules ...string) string {
	header := "Status: active\n\n     To                         Action      From\n     --                         ------      ----\n"
	return header + strings.Join(rules, "\n") + "\n"
}

func TestParseNumberedRules(t *testing.T) {
	output := numberedStatus(
		"[ 1] 22/tcp                     ALLOW IN    Anywhere",
		"[ 2] 80,443/tcp                 ALLOW IN    103.21.244.0/22            # ft-cloudflare",
		"[ 3] Nginx Full                 ALLOW IN    Anywhere",
		"[ 4] Anywhere                   ALLOW IN    10.8.0.5",
		"[ 5] 443                        DENY IN     203.0.113.9",
		"[ 6] 80/tcp                     ALLOW FWD   Anywhere",
		"[ 7] 22/tcp (v6)                ALLOW IN    Anywhere (v6)",
		"[ 8] 3306/tcp on eth1           ALLOW IN    10.0.0.0/8",
		"[ 9] 25/tcp                     ALLOW OUT   Anywhere",
		"[10] 8080/tcp                   LIMIT IN    Anywhere",
	)
	want := []NumberedRule{
		{Number: 1, To: "22/tcp", Action: "ALLOW", Direction: "IN", From: "Anywhere"},
		{Number: 2, To: "80,443/tcp", Action: "ALLOW", Direction: "IN", From: "103.21.244.0/22", Comment: "ft-cloudflare"},
		{Number: 3, To: "Nginx Full", Action: "ALLOW", Direction: "IN", From: "Anywhere"},
		{Number: 4, To: "Anywhere", Action: "ALLOW", Direction: "IN", From: "10.8.0.5"},
		{Number: 5, To: "443", Action: "DENY", Direction: "IN", From: "203.0.113.9"},
		{Number: 6, To: "80/tcp", Action: "ALLOW", Direction: "FWD", From: "Anywhere"},
		{Number: 7, To: "22/tcp", Action: "ALLOW", Direction: "IN", From: "Anywhere"},
		{Number: 8, To: "3306/tcp on eth1", Action: "ALLOW", Direction: "IN", From: "10.0.0.0/8"},
		{Number: 9, To: "25/tcp", Action: "ALLOW", Direction: "OUT", From: "Anywhere"},
		{Number: 10, To: "8080/tcp", Action: "LIMIT", Direction: "IN", From: "Anywhere"},
	}
	got, err := ParseNumberedRules(output)
	if err != nil {
		t.Fatalf("ParseNumberedRules() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ParseNumberedRules() returned %d rules, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		raw := got[i].Raw
		if !strings.HasPrefix(raw, fmt.Sprintf("[%2d]", want[i].Number)) {
			t.Errorf("rule %d Raw = %q, want it to start with its number", i, raw)
		}
		got[i].Raw = ""
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("rule %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseNumberedRulesWithoutDirection(t *testing.T) {
	got, err := ParseNumberedRules(numberedStatus("[ 1] 80/tcp                     ALLOW       Anywhere"))
	if err != nil {
		t.Fatalf("ParseNumberedRules() error = %v", err)
	}
	want := NumberedRule{Number: 1, To: "80/tcp", Action: "ALLOW", Direction: "", From: "Anywhere"}
	if len(got) != 1 {
		t.Fatalf("ParseNumberedRules() returned %d rules, want 1", len(got))
	}
	got[0].Raw = ""
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("rule = %+v, want %+v", got[0], want)
	}
}

func TestParseNumberedRulesErrors(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr string
	}{
		{
			name:    "rule line without action",
			output:  numberedStatus("[ 1] something wicked"),
			wantErr: "no action",
		},
		{
			name:    "rule line with bad number",
			output:  numberedStatus("[xx] 80/tcp ALLOW IN Anywhere"),
			wantErr: "rule number",
		},
		{
			name:    "missing status line",
			output:  "     To  Action  From\n",
			wantErr: "no ufw status line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNumberedRules(tt.output)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseNumberedRules() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseNumberedRulesEmptyRuleSet(t *testing.T) {
	got, err := ParseNumberedRules("Status: active\n")
	if err != nil {
		t.Fatalf("ParseNumberedRules() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseNumberedRules() = %+v, want no rules", got)
	}
}

func rule(number int, to, action, direction, from, comment string) NumberedRule {
	return NumberedRule{
		Number: number, To: to, Action: action, Direction: direction,
		From: from, Comment: comment,
		Raw: fmt.Sprintf("[%2d] %s %s %s %s", number, to, action, direction, from),
	}
}

func noApps(name string) ([]string, error) {
	return nil, fmt.Errorf("unexpected app profile lookup %q", name)
}

func TestNextConflict(t *testing.T) {
	ports := []int{80, 443}
	tests := []struct {
		name       string
		rules      []NumberedRule
		resolveApp AppPortsFunc
		wantNumber int
		wantFound  bool
		wantErr    string
	}{
		{
			name:       "managed rule is rebuilt regardless of shape",
			rules:      []NumberedRule{rule(3, "80,443/tcp", "ALLOW", "IN", "103.21.244.0/22", "ft-cloudflare")},
			resolveApp: noApps,
			wantNumber: 3,
			wantFound:  true,
		},
		{
			name:       "blanket allow on covered port",
			rules:      []NumberedRule{rule(1, "80/tcp", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantNumber: 1,
			wantFound:  true,
		},
		{
			name:       "limit counts as an admit rule",
			rules:      []NumberedRule{rule(1, "443", "LIMIT", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantNumber: 1,
			wantFound:  true,
		},
		{
			name:       "route allow counts",
			rules:      []NumberedRule{rule(2, "80/tcp", "ALLOW", "FWD", "Anywhere", "")},
			resolveApp: noApps,
			wantNumber: 2,
			wantFound:  true,
		},
		{
			name:       "directionless allow counts as inbound",
			rules:      []NumberedRule{rule(1, "443/tcp", "ALLOW", "", "Anywhere", "")},
			resolveApp: noApps,
			wantNumber: 1,
			wantFound:  true,
		},
		{
			name:       "source-specific allow is still a conflict",
			rules:      []NumberedRule{rule(4, "443/tcp", "ALLOW", "IN", "203.0.113.9", "")},
			resolveApp: noApps,
			wantNumber: 4,
			wantFound:  true,
		},
		{
			name:       "port range covering 443",
			rules:      []NumberedRule{rule(1, "400:500/tcp", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantNumber: 1,
			wantFound:  true,
		},
		{
			name:       "destination address with covered port",
			rules:      []NumberedRule{rule(1, "203.0.113.7 443/tcp", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantNumber: 1,
			wantFound:  true,
		},
		{
			name:       "interface-scoped allow on covered port",
			rules:      []NumberedRule{rule(1, "80/tcp on eth0", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantNumber: 1,
			wantFound:  true,
		},
		{
			name:       "unrelated port is kept",
			rules:      []NumberedRule{rule(1, "8080/tcp", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantFound:  false,
		},
		{
			name:       "udp-only spec is kept",
			rules:      []NumberedRule{rule(1, "80,443/udp", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantFound:  false,
		},
		{
			name:       "deny rule is kept",
			rules:      []NumberedRule{rule(1, "443/tcp", "DENY", "IN", "Anywhere", "")},
			resolveApp: noApps,
			wantFound:  false,
		},
		{
			name:       "outbound allow is kept",
			rules:      []NumberedRule{rule(1, "80/tcp", "ALLOW", "OUT", "Anywhere", "")},
			resolveApp: noApps,
			wantFound:  false,
		},
		{
			name:       "all-port allow is kept",
			rules:      []NumberedRule{rule(1, "Anywhere", "ALLOW", "IN", "10.8.0.5", "")},
			resolveApp: noApps,
			wantFound:  false,
		},
		{
			name:  "app profile covering the ports",
			rules: []NumberedRule{rule(2, "Nginx Full", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: func(name string) ([]string, error) {
				if name != "Nginx Full" {
					return nil, fmt.Errorf("unexpected app profile %q", name)
				}
				return []string{"80,443/tcp"}, nil
			},
			wantNumber: 2,
			wantFound:  true,
		},
		{
			name:  "app profile not covering the ports",
			rules: []NumberedRule{rule(2, "OpenSSH", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: func(name string) ([]string, error) {
				return []string{"22/tcp"}, nil
			},
			wantFound: false,
		},
		{
			name:  "app profile resolution failure",
			rules: []NumberedRule{rule(2, "Mystery App", "ALLOW", "IN", "Anywhere", "")},
			resolveApp: func(name string) ([]string, error) {
				return nil, fmt.Errorf("profile not found")
			},
			wantErr: "Mystery App",
		},
		{
			name: "first conflicting rule wins",
			rules: []NumberedRule{
				rule(1, "22/tcp", "ALLOW", "IN", "Anywhere", ""),
				rule(2, "80/tcp", "ALLOW", "IN", "Anywhere", ""),
				rule(3, "443/tcp", "ALLOW", "IN", "Anywhere", ""),
			},
			resolveApp: noApps,
			wantNumber: 2,
			wantFound:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := NextConflict(tt.rules, ports, "ft-cloudflare", tt.resolveApp)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NextConflict() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NextConflict() error = %v", err)
			}
			if found != tt.wantFound {
				t.Fatalf("NextConflict() found = %v, want %v (rule %+v)", found, tt.wantFound, got)
			}
			if found && got.Number != tt.wantNumber {
				t.Errorf("NextConflict() rule number = %d, want %d", got.Number, tt.wantNumber)
			}
		})
	}
}

func TestAppInfoPorts(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    []string
		wantErr string
	}{
		{
			name: "single spec",
			output: `Profile: Nginx Full
Title: Web Server (Nginx, HTTP + HTTPS)
Description: Small, but very powerful and efficient web server

Ports:
  80,443/tcp
`,
			want: []string{"80,443/tcp"},
		},
		{
			name: "multiple specs",
			output: `Profile: Samba
Title: LanManager-like file and printer server

Ports:
  137,138/udp
  139,445/tcp
`,
			want: []string{"137,138/udp", "139,445/tcp"},
		},
		{
			name: "pipe-separated specs",
			output: `Profile: Custom
Title: Custom

Ports:
  80/tcp|8080/tcp
`,
			want: []string{"80/tcp", "8080/tcp"},
		},
		{
			name:    "missing ports section",
			output:  "Profile: Broken\nTitle: Broken\n",
			wantErr: "no Ports section",
		},
		{
			name:    "empty ports section",
			output:  "Profile: Broken\n\nPorts:\n",
			wantErr: "no Ports section",
		},
		{
			name:    "unparseable ports entry",
			output:  "Profile: Broken\n\nPorts:\n  what/even\n",
			wantErr: "unparseable port",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AppInfoPorts(tt.output)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("AppInfoPorts() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AppInfoPorts() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AppInfoPorts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRuleWarnings(t *testing.T) {
	ports := []int{80, 443}
	tests := []struct {
		name          string
		rules         []NumberedRule
		wantCount     int
		wantFragments []string
	}{
		{
			name:          "all-port allow from one address",
			rules:         []NumberedRule{rule(4, "Anywhere", "ALLOW", "IN", "10.8.0.5", "")},
			wantCount:     1,
			wantFragments: []string{"10.8.0.5", "every port"},
		},
		{
			name:          "deny covering a target port",
			rules:         []NumberedRule{rule(5, "443", "DENY", "IN", "203.0.113.9", "")},
			wantCount:     1,
			wantFragments: []string{"block Cloudflare"},
		},
		{
			name:          "reject all ports from anywhere",
			rules:         []NumberedRule{rule(1, "Anywhere", "REJECT", "IN", "Anywhere", "")},
			wantCount:     1,
			wantFragments: []string{"block Cloudflare"},
		},
		{
			name:          "deny on app profile",
			rules:         []NumberedRule{rule(1, "Nginx Full", "DENY", "IN", "Anywhere", "")},
			wantCount:     1,
			wantFragments: []string{"block Cloudflare"},
		},
		{
			name:      "deny on unrelated port",
			rules:     []NumberedRule{rule(1, "25/tcp", "DENY", "IN", "Anywhere", "")},
			wantCount: 0,
		},
		{
			name:      "outbound rules are ignored",
			rules:     []NumberedRule{rule(1, "Anywhere", "ALLOW", "OUT", "Anywhere", "")},
			wantCount: 0,
		},
		{
			name:      "port-specific allow produces no warning",
			rules:     []NumberedRule{rule(1, "8080/tcp", "ALLOW", "IN", "Anywhere", "")},
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RuleWarnings(tt.rules, ports)
			if len(got) != tt.wantCount {
				t.Fatalf("RuleWarnings() = %v, want %d warnings", got, tt.wantCount)
			}
			for _, fragment := range tt.wantFragments {
				matched := false
				for _, warning := range got {
					if strings.Contains(warning, fragment) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("RuleWarnings() = %v, want a warning containing %q", got, fragment)
				}
			}
		})
	}
}
