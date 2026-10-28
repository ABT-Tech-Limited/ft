package codexx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	Model             = "gpt-6-astra"
	ModelProvider     = "custom"
	ReasoningEffort   = "ultra"
	ModelContext      = int64(1_000_000)
	AutoCompactTokens = int64(800_000)
)

// ErrInvalidConfig identifies malformed or structurally unsafe Codex TOML.
var ErrInvalidConfig = errors.New("invalid Codex configuration")

// ErrConfigChanged reports that config.toml changed after it was previewed.
var ErrConfigChanged = errors.New("Codex configuration changed during setup")

// NormalizeHost validates and normalizes a Responses API base URL.
func NormalizeHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("API host is empty")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse API host: %w", err)
	}
	if !parsed.IsAbs() {
		return "", fmt.Errorf("API host must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("API host must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("API host URL has no host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("API host must not contain userinfo")
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("API host must not contain a query")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("API host must not contain a fragment")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("remote API hosts must use HTTPS; HTTP is allowed only for loopback")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ConfigPath resolves the user-level Codex config path.
func ConfigPath(codexHome, home string) (string, error) {
	base, name := codexHome, "CODEX_HOME"
	if base == "" {
		base, name = home, "HOME"
	}
	if base == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	// Codex inherits the raw environment. Trimming or cleaning an ambiguous
	// path here could make config writes and process recovery target another home.
	if base != strings.TrimSpace(base) || !filepath.IsAbs(base) {
		return "", fmt.Errorf("%s must be an absolute path without leading or trailing whitespace", name)
	}
	for _, component := range strings.Split(filepath.ToSlash(base), "/") {
		if component == ".." {
			return "", fmt.Errorf("%s must not contain '..'; use its resolved absolute path", name)
		}
	}
	if codexHome == "" {
		base = filepath.Join(base, ".codex")
	}
	return filepath.Join(base, "config.toml"), nil
}

type parsedConfig struct {
	ModelProvider string `toml:"model_provider"`
	Model         string `toml:"model"`
	Reasoning     string `toml:"model_reasoning_effort"`
	ContextWindow any    `toml:"model_context_window"`
	CompactLimit  any    `toml:"model_auto_compact_token_limit"`
	Providers     map[string]struct {
		Name               string `toml:"name"`
		BaseURL            string `toml:"base_url"`
		WireAPI            string `toml:"wire_api"`
		RequiresOpenAIAuth bool   `toml:"requires_openai_auth"`
	} `toml:"model_providers"`
}

func parseConfig(data []byte) (parsedConfig, error) {
	var result parsedConfig
	if len(bytes.TrimSpace(data)) == 0 {
		return result, nil
	}
	if err := toml.Unmarshal(data, &result); err != nil {
		return parsedConfig{}, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	return result, nil
}

// ManagedConfigMatches reports whether all ft-managed settings match host.
func ManagedConfigMatches(data []byte, host string) (bool, error) {
	config, err := parseConfig(data)
	if err != nil {
		return false, err
	}
	provider, ok := config.Providers[ModelProvider]
	return ok &&
		config.ModelProvider == ModelProvider &&
		config.Model == Model &&
		config.Reasoning == ReasoningEffort &&
		integerEquals(config.ContextWindow, ModelContext) &&
		integerEquals(config.CompactLimit, AutoCompactTokens) &&
		provider.Name == "Custom" &&
		provider.BaseURL == host &&
		provider.WireAPI == "responses" &&
		provider.RequiresOpenAIAuth, nil
}

func integerEquals(value any, want int64) bool {
	switch typed := value.(type) {
	case int64:
		return typed == want
	case int:
		return int64(typed) == want
	default:
		return false
	}
}

// MergeConfig replaces ft-managed settings while preserving unrelated text.
func MergeConfig(existing []byte, host string) ([]byte, error) {
	normalizedHost, err := NormalizeHost(host)
	if err != nil {
		return nil, err
	}
	if _, err := parseConfig(existing); err != nil && !isManagedDefinitionConflict(err) {
		return nil, err
	}

	lines := strings.Split(string(existing), "\n")
	kept := make([]string, 0, len(lines))
	section := ""
	var customComments []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if table, ok := tableName(trimmed); ok {
			if section == "model_providers.custom" && table != section {
				kept = append(kept, customComments...)
				customComments = nil
			}
			section = table
			if table == "model_providers.custom" {
				continue
			}
		}
		if section == "model_providers.custom" {
			if _, ok := tableName(trimmed); !ok {
				if strings.HasPrefix(trimmed, "#") {
					customComments = append(customComments, line)
				}
				continue
			}
		}
		if section == "" && isManagedAssignment(trimmed) {
			continue
		}
		kept = append(kept, line)
	}

	base := strings.TrimSpace(strings.Join(kept, "\n"))
	var output strings.Builder
	output.WriteString(managedTOML(normalizedHost))
	if base != "" {
		output.WriteString("\n\n")
		output.WriteString(base)
		output.WriteByte('\n')
	}
	merged := []byte(output.String())
	if _, err := parseConfig(merged); err != nil {
		return nil, fmt.Errorf("merge Codex config: %w", err)
	}
	return merged, nil
}

// PrepareConfig builds the exact config shown for confirmation. If the
// existing file is malformed, it returns a clean managed config and the
// original bytes that must be backed up before committing the repair.
func PrepareConfig(existing []byte, host string) (prepared, backup []byte, err error) {
	prepared, err = MergeConfig(existing, host)
	if err == nil {
		return prepared, nil, nil
	}
	if !errors.Is(err, ErrInvalidConfig) {
		return nil, nil, err
	}
	prepared, cleanErr := MergeConfig(nil, host)
	if cleanErr != nil {
		return nil, nil, cleanErr
	}
	return prepared, append([]byte(nil), existing...), nil
}

// Codex config written by older tools can contain both a dotted custom-provider
// key and the equivalent table. That is invalid TOML as a whole, but both
// definitions are owned by this repair operation and are removed below. No
// other syntax error is tolerated.
func isManagedDefinitionConflict(err error) bool {
	message := err.Error()
	return strings.Contains(message, "already exists as defined by a dotted key") &&
		strings.Contains(message, "custom")
}

func tableName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") {
		return "", false
	}
	arrayTable := strings.HasPrefix(line, "[[")
	start := 1
	closing := "]"
	if arrayTable {
		start = 2
		closing = "]]"
	}
	end := strings.Index(line[start:], closing)
	if end < 0 {
		return "", false
	}
	name := strings.TrimSpace(line[start : start+end])
	return unquoteDotted(name), true
}

func unquoteDotted(value string) string {
	parts := strings.Split(value, ".")
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if unquoted, err := strconv.Unquote(part); err == nil {
			parts[index] = unquoted
		} else if len(part) >= 2 && part[0] == '\'' && part[len(part)-1] == '\'' {
			parts[index] = part[1 : len(part)-1]
		} else {
			parts[index] = part
		}
	}
	return strings.Join(parts, ".")
}

func isManagedAssignment(line string) bool {
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	equals := strings.IndexByte(line, '=')
	if equals < 0 {
		return false
	}
	key := unquoteDotted(strings.TrimSpace(line[:equals]))
	switch key {
	case "model_provider", "model", "model_reasoning_effort", "model_context_window", "model_auto_compact_token_limit":
		return true
	default:
		return strings.HasPrefix(key, "model_providers.custom.")
	}
}

func managedTOML(host string) string {
	return fmt.Sprintf(`model_provider = %s
model = %s
model_reasoning_effort = %s
model_context_window = %d
model_auto_compact_token_limit = %d

[model_providers.custom]
name = "Custom"
base_url = %s
wire_api = "responses"
requires_openai_auth = true`,
		strconv.Quote(ModelProvider), strconv.Quote(Model), strconv.Quote(ReasoningEffort),
		ModelContext, AutoCompactTokens, strconv.Quote(host))
}

// ReadConfig reads a user-level config. A missing file is an empty config.
func ReadConfig(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil config read context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Codex config: %w", err)
	}
	return data, nil
}

// WriteConfig prepares and atomically writes a user-level Codex config.
func WriteConfig(ctx context.Context, path string, existing []byte, host string) error {
	prepared, backup, err := PrepareConfig(existing, host)
	if err != nil {
		return err
	}
	_, err = WritePreparedConfig(ctx, path, existing, prepared, backup)
	return err
}

// WritePreparedConfig commits the exact bytes previously shown to the user.
// It refuses to overwrite a file that changed after the preview. Malformed
// input is copied to a private backup before the clean replacement is made.
func WritePreparedConfig(ctx context.Context, path string, expected, prepared, backup []byte) (backupPath string, resultErr error) {
	if ctx == nil {
		return "", fmt.Errorf("nil config write context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := parseConfig(prepared); err != nil {
		return "", fmt.Errorf("prepared Codex config: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create Codex config directory: %w", err)
	}
	current, err := readConfigFile(path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, expected) {
		return "", ErrConfigChanged
	}
	if backup != nil {
		backupFile, err := os.CreateTemp(directory, filepath.Base(path)+".invalid-*.bak")
		if err != nil {
			return "", fmt.Errorf("create invalid Codex config backup: %w", err)
		}
		backupPath = backupFile.Name()
		backupOK := false
		defer func() {
			_ = backupFile.Close()
			if !backupOK {
				_ = os.Remove(backupPath)
				backupPath = ""
			}
		}()
		if err := backupFile.Chmod(0o600); err != nil {
			return "", fmt.Errorf("secure invalid Codex config backup: %w", err)
		}
		if _, err := backupFile.Write(backup); err != nil {
			return "", fmt.Errorf("write invalid Codex config backup: %w", err)
		}
		if err := backupFile.Sync(); err != nil {
			return "", fmt.Errorf("sync invalid Codex config backup: %w", err)
		}
		if err := backupFile.Close(); err != nil {
			return "", fmt.Errorf("close invalid Codex config backup: %w", err)
		}
		backupOK = true
	}
	temporary, err := os.CreateTemp(directory, ".config.toml.*")
	if err != nil {
		return backupPath, fmt.Errorf("create temporary Codex config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if resultErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return backupPath, fmt.Errorf("secure temporary Codex config: %w", err)
	}
	if _, err := temporary.Write(prepared); err != nil {
		return backupPath, fmt.Errorf("write temporary Codex config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return backupPath, fmt.Errorf("sync temporary Codex config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return backupPath, fmt.Errorf("close temporary Codex config: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return backupPath, err
	}
	current, err = readConfigFile(path)
	if err != nil {
		return backupPath, err
	}
	if !bytes.Equal(current, expected) {
		return backupPath, ErrConfigChanged
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return backupPath, fmt.Errorf("activate Codex config: %w", err)
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return backupPath, fmt.Errorf("open Codex config directory after commit: %w", err)
	}
	defer directoryFile.Close()
	if err := directoryFile.Sync(); err != nil {
		return backupPath, fmt.Errorf("Codex config was replaced but syncing its directory failed: %w", err)
	}
	return backupPath, nil
}

func readConfigFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read current Codex config: %w", err)
	}
	return data, nil
}

// MaskAPIKey returns a safe preview of a credential.
func MaskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}
