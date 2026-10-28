package codexx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "HTTPS with whitespace and trailing slashes", input: "  https://api.example.com/v1///  ", want: "https://api.example.com/v1"},
		{name: "HTTP loopback proxy", input: "http://127.0.0.1:8080/", want: "http://127.0.0.1:8080"},
		{name: "IPv6", input: "http://[::1]:8080/v1/", want: "http://[::1]:8080/v1"},
		{name: "remote HTTP", input: "http://192.168.1.3:8080/", wantErr: "HTTPS"},
		{name: "empty", input: "  ", wantErr: "empty"},
		{name: "relative", input: "api.example.com/v1", wantErr: "absolute"},
		{name: "unsupported scheme", input: "ftp://example.com", wantErr: "http or https"},
		{name: "missing host", input: "https:///v1", wantErr: "host"},
		{name: "userinfo", input: "https://user:pass@example.com", wantErr: "userinfo"},
		{name: "query", input: "https://example.com/v1?key=value", wantErr: "query"},
		{name: "fragment", input: "https://example.com/v1#frag", wantErr: "fragment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeHost(tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NormalizeHost(%q) = %q, %v; want error containing %q", tt.input, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeHost(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConfigPath(t *testing.T) {
	tests := []struct {
		name      string
		codexHome string
		home      string
		want      string
		wantErr   string
	}{
		{name: "CODEX_HOME wins", codexHome: "/srv/codex", home: "/home/user", want: "/srv/codex/config.toml"},
		{name: "default under HOME", home: "/home/user", want: "/home/user/.codex/config.toml"},
		{name: "no home", wantErr: "HOME"},
		{name: "trailing whitespace", codexHome: "/srv/codex ", wantErr: "CODEX_HOME"},
		{name: "leading whitespace", codexHome: " /srv/codex", wantErr: "CODEX_HOME"},
		{name: "whitespace is not unset", codexHome: " ", home: "/home/user", wantErr: "CODEX_HOME"},
		{name: "relative codex home", codexHome: "codex", wantErr: "CODEX_HOME"},
		{name: "parent traversal", codexHome: "/srv/link/../codex", wantErr: "CODEX_HOME"},
		{name: "home parent traversal", home: "/home/link/../user", wantErr: "HOME"},
		{name: "home whitespace", home: "/home/user ", wantErr: "HOME"},
		{name: "internal spaces", codexHome: "/srv/codex home", want: "/srv/codex home/config.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConfigPath(tt.codexHome, tt.home)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ConfigPath() = %q, %v; want error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ConfigPath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ConfigPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagedConfigMatches(t *testing.T) {
	const host = "https://gateway.example.com/v1"
	valid := `
model_provider = "custom"
model = "gpt-6-astra"
model_reasoning_effort = "ultra"
model_context_window = 1_000_000
model_auto_compact_token_limit = 800000

[model_providers.custom]
name = "Custom"
base_url = "https://gateway.example.com/v1"
wire_api = "responses"
requires_openai_auth = true
`
	tests := []struct {
		name    string
		input   string
		host    string
		want    bool
		wantErr error
	}{
		{name: "exact fields", input: valid, host: host, want: true},
		{name: "unmanaged fields allowed", input: valid + "\n[history]\npersistence = \"save-all\"\n", host: host, want: true},
		{name: "wrong model", input: strings.Replace(valid, "gpt-6-astra", "gpt-5.5", 1), host: host},
		{name: "wrong host", input: valid, host: "https://other.example.com/v1"},
		{name: "wrong integer type", input: strings.Replace(valid, "800000", `"800000"`, 1), host: host},
		{name: "syntax error", input: `model = "unterminated`, host: host, wantErr: ErrInvalidConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ManagedConfigMatches([]byte(tt.input), tt.host)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ManagedConfigMatches() = %v, %v; want %v", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ManagedConfigMatches() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ManagedConfigMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeConfigPreservesUnmanagedContentAndReplacesManagedDefinitions(t *testing.T) {
	existing := []byte(`# user comment stays
"model" = "old"
'model_provider' = "openai"
model_reasoning_effort = "high"
model_context_window = 200000
model_auto_compact_token_limit = 150000
model_providers.custom.name = "old dotted provider"

[profiles.work]
model = "profile-model"
custom = "profile setting"

[model_providers.custom]
name = "Old"
base_url = "https://old.example.com"
wire_api = "chat"
requires_openai_auth = false

# custom table trailing comment stays with following content
[model_providers.other]
name = "Other"
base_url = "https://other.example.com"

[history]
persistence = "save-all"
`)
	host := `https://gateway.example.com/v1/tenant"quoted`
	got, err := MergeConfig(existing, host)
	if err != nil {
		t.Fatalf("MergeConfig() error = %v", err)
	}

	for _, preserved := range []string{
		"# user comment stays",
		`[profiles.work]`,
		`model = "profile-model"`,
		`custom = "profile setting"`,
		`# custom table trailing comment stays with following content`,
		`[model_providers.other]`,
		`name = "Other"`,
		`[history]`,
		`persistence = "save-all"`,
	} {
		if !strings.Contains(string(got), preserved) {
			t.Errorf("merged config did not preserve %q:\n%s", preserved, got)
		}
	}
	for _, removed := range []string{`"model" = "old"`, `'model_provider' = "openai"`, `old dotted provider`, `base_url = "https://old.example.com"`} {
		if strings.Contains(string(got), removed) {
			t.Errorf("merged config retained managed content %q:\n%s", removed, got)
		}
	}
	wantHost, err := NormalizeHost(host)
	if err != nil {
		t.Fatalf("NormalizeHost() error = %v", err)
	}
	matches, err := ManagedConfigMatches(got, wantHost)
	if err != nil {
		t.Fatalf("merged config is invalid: %v\n%s", err, got)
	}
	if !matches {
		t.Errorf("merged config does not match managed settings:\n%s", got)
	}
}

func TestMergeConfigPreservesArrayOfTablesAfterCustomProvider(t *testing.T) {
	existing := []byte(`[model_providers.custom]
name = "Old"

[[hooks.SessionStart]]
command = "one"

[[hooks.SessionStart]]
command = "two"
`)
	got, err := MergeConfig(existing, "https://api.example.com/v1")
	if err != nil {
		t.Fatalf("MergeConfig() error = %v", err)
	}
	if count := strings.Count(string(got), `[[hooks.SessionStart]]`); count != 2 {
		t.Errorf("merged config contains array header %d times, want 2:\n%s", count, got)
	}
	for _, want := range []string{`command = "one"`, `command = "two"`} {
		if count := strings.Count(string(got), want); count != 1 {
			t.Errorf("merged config contains %q %d times, want 1:\n%s", want, count, got)
		}
	}
}

func TestMergeConfigHandlesEmptyAndInvalidInput(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := MergeConfig(nil, "http://localhost:8080/")
		if err != nil {
			t.Fatalf("MergeConfig() error = %v", err)
		}
		matches, err := ManagedConfigMatches(got, "http://localhost:8080")
		if err != nil || !matches {
			t.Fatalf("ManagedConfigMatches() = %v, %v\n%s", matches, err, got)
		}
	})

	t.Run("invalid TOML", func(t *testing.T) {
		const broken = `model = "broken`
		got, err := MergeConfig([]byte(broken), "https://api.example.com")
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("MergeConfig() = %q, %v; want ErrInvalidConfig", got, err)
		}
		if got != nil {
			t.Errorf("MergeConfig() output = %q, want nil", got)
		}
	})
}

func TestPrepareConfigRepairsInvalidInputAndReturnsOriginalForBackup(t *testing.T) {
	const broken = `model = "broken`
	merged, backup, err := PrepareConfig([]byte(broken), "https://api.example.com/v1")
	if err != nil {
		t.Fatalf("PrepareConfig() error = %v", err)
	}
	if string(backup) != broken {
		t.Errorf("backup = %q, want original %q", backup, broken)
	}
	matches, err := ManagedConfigMatches(merged, "https://api.example.com/v1")
	if err != nil || !matches {
		t.Fatalf("prepared config matches = %v, %v\n%s", matches, err, merged)
	}
}

func TestWritePreparedConfigBacksUpInvalidInputAndDetectsConcurrentChange(t *testing.T) {
	t.Run("backup invalid input", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		original := []byte(`model = "broken`)
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		prepared, backup, err := PrepareConfig(original, "https://api.example.com/v1")
		if err != nil {
			t.Fatal(err)
		}
		backupPath, err := WritePreparedConfig(context.Background(), path, original, prepared, backup)
		if err != nil {
			t.Fatalf("WritePreparedConfig() error = %v", err)
		}
		if backupPath == "" {
			t.Fatal("backup path is empty")
		}
		gotBackup, err := os.ReadFile(backupPath)
		if err != nil || !bytes.Equal(gotBackup, original) {
			t.Fatalf("backup = %q, %v; want original", gotBackup, err)
		}
		info, err := os.Stat(backupPath)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("backup mode = %v, %v; want 0600", info, err)
		}
	})

	t.Run("concurrent change", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		expected := []byte("custom = 1\n")
		if err := os.WriteFile(path, []byte("custom = 2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		prepared, _, err := PrepareConfig(expected, "https://api.example.com/v1")
		if err != nil {
			t.Fatal(err)
		}
		_, err = WritePreparedConfig(context.Background(), path, expected, prepared, nil)
		if !errors.Is(err, ErrConfigChanged) {
			t.Fatalf("WritePreparedConfig() error = %v, want ErrConfigChanged", err)
		}
	})
}

func TestReadAndWriteConfigUseSecureAtomicPermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "config.toml")
	const existing = "# preserve me\ncustom_setting = true\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(context.Background(), path, []byte(existing), "https://api.example.com/"); err != nil {
		t.Fatalf("WriteConfig() error = %v", err)
	}

	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat(config dir) error = %v", err)
	}
	if got, want := directoryInfo.Mode().Perm(), os.FileMode(0o700); got != want {
		t.Errorf("config dir mode = %o, want %o", got, want)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(config) error = %v", err)
	}
	if got, want := fileInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("config mode = %o, want %o", got, want)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir(config dir) error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Errorf("config dir entries = %v, want only config.toml", entries)
	}

	got, err := ReadConfig(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if !strings.Contains(string(got), existing) {
		t.Errorf("ReadConfig() did not preserve input:\n%s", got)
	}
	matches, err := ManagedConfigMatches(got, "https://api.example.com")
	if err != nil || !matches {
		t.Errorf("ManagedConfigMatches() = %v, %v\n%s", matches, err, got)
	}
}

func TestWriteConfigDoesNotWriteAPIKey(t *testing.T) {
	const secret = "sk-test-super-secret"
	path := filepath.Join(t.TempDir(), "config.toml")
	existing := []byte("# " + secret + " must not be supplied as config\n")
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteConfig(context.Background(), path, existing, "https://api.example.com")
	if err != nil {
		t.Fatalf("WriteConfig() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(got), secret) {
		t.Fatal("test fixture was unexpectedly altered; key leak assertion would be meaningless")
	}
	managed, err := MergeConfig(nil, "https://api.example.com")
	if err != nil {
		t.Fatalf("MergeConfig() error = %v", err)
	}
	if strings.Contains(string(managed), secret) || strings.Contains(string(managed), "api_key") {
		t.Fatalf("managed config leaked API key material: %s", managed)
	}
}
