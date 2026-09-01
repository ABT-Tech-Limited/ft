package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ABT-Tech-Limited/ft/internal/codexx"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ErrCodexSetupCancelled reports that the user rejected the proposed config.
var ErrCodexSetupCancelled = errors.New("Codex setup cancelled")

type codexInstallDependencies struct {
	isSudo           func() bool
	resolveCurrent   func() (string, error)
	resolveInstalled func() (string, error)
	detectVersion    func(context.Context, string) (string, error)
	install          func(context.Context, io.Writer, io.Writer) error
	configPath       func() (string, error)
	prepareConfig    func([]byte, string) ([]byte, []byte, error)
	readConfig       func(context.Context, string) ([]byte, error)
	writeConfig      func(context.Context, string, []byte, []byte, []byte) (string, error)
	login            func(context.Context, string, string, io.Writer, io.Writer) error
	validate         func(context.Context, string, io.Writer, io.Writer) error
	readSecret       func(io.Reader, io.Writer) (string, error)
}

func newInstallCodexCmd(deps codexInstallDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "codex",
		Short: "Install, configure and verify OpenAI Codex CLI",
		Long: `Install or update Codex with OpenAI's official standalone installer.

After installation, ft verifies the active configuration with a minimal model
request. If repair is needed, it asks for a Responses API host and API key,
shows a masked preview, preserves unrelated config.toml settings, stores the
credential through "codex login", and validates the result. Run without sudo.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstallCodex(cmd, deps)
		},
	}
}

func runInstallCodex(cmd *cobra.Command, deps codexInstallDependencies) error {
	if deps.isSudo() {
		return fmt.Errorf("do not run this command with sudo; run \"ft install codex\" as the user who will use Codex")
	}
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	executable, resolveErr := deps.resolveCurrent()
	before := ""
	if resolveErr == nil {
		version, err := deps.detectVersion(cmd.Context(), executable)
		if err != nil && !errors.Is(err, codexx.ErrNotFound) {
			return fmt.Errorf("checking installed Codex version: %w", err)
		}
		before = version
	}
	if before == "" {
		fmt.Fprintln(out, "Codex CLI is not installed; installing the latest release...")
	} else {
		fmt.Fprintf(out, "Detected Codex CLI %s; checking for the latest release...\n", before)
	}
	if err := deps.install(cmd.Context(), out, errOut); err != nil {
		return fmt.Errorf("installing or updating Codex CLI: %w", err)
	}

	executable, err := deps.resolveInstalled()
	if err != nil {
		return fmt.Errorf("locating Codex after installation: %w", err)
	}
	after, err := deps.detectVersion(cmd.Context(), executable)
	if err != nil {
		return fmt.Errorf("checking installed Codex version after installation: %w", err)
	}
	if before != "" && before != after {
		fmt.Fprintf(out, "Updated Codex CLI from %s to %s.\n", before, after)
	} else {
		fmt.Fprintf(out, "Codex CLI %s is installed.\n", after)
	}

	configPath, err := deps.configPath()
	if err != nil {
		return fmt.Errorf("resolving Codex configuration path: %w", err)
	}
	existing, err := deps.readConfig(cmd.Context(), configPath)
	if err != nil {
		return fmt.Errorf("reading Codex configuration: %w", err)
	}

	fmt.Fprintln(out, "Validating the current Codex configuration...")
	if err := deps.validate(cmd.Context(), executable, out, errOut); err == nil {
		fmt.Fprintln(out, "Codex 配置正确，可以使用。")
		return nil
	} else {
		fmt.Fprintf(out, "Current Codex configuration could not be validated: %v\n", err)
	}

	input := cmd.InOrStdin()
	host, err := promptHost(input, out)
	if err != nil {
		return err
	}
	key, err := promptSecret(input, out, deps.readSecret)
	if err != nil {
		return err
	}
	prepared, backup, err := deps.prepareConfig(existing, host)
	if err != nil {
		return fmt.Errorf("preparing Codex configuration: %w", err)
	}
	printCodexConfigPreview(out, prepared, key, backup != nil)
	confirmed, err := promptConfirmation(input, out)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrCodexSetupCancelled
	}

	backupPath, err := deps.writeConfig(cmd.Context(), configPath, existing, prepared, backup)
	if err != nil {
		return fmt.Errorf("writing Codex configuration: %w", err)
	}
	if backupPath != "" {
		fmt.Fprintf(out, "原损坏配置已备份到 %s\n", backupPath)
	}
	if err := deps.login(cmd.Context(), executable, key, out, errOut); err != nil {
		return fmt.Errorf("saving Codex API key: %w", err)
	}
	fmt.Fprintln(out, "Validating the new Codex configuration...")
	if err := deps.validate(cmd.Context(), executable, out, errOut); err != nil {
		return fmt.Errorf("Codex configuration was saved but validation failed: %w; review the API host and key, then run \"ft install codex\" again", err)
	}
	fmt.Fprintln(out, "Codex 配置正确，可以使用。")
	return nil
}

func promptHost(input io.Reader, output io.Writer) (string, error) {
	for {
		fmt.Fprint(output, "API 请求地址 HOST: ")
		line, err := readLine(input)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading API host: %w", err)
		}
		host, validationErr := codexx.NormalizeHost(line)
		if validationErr == nil {
			return host, nil
		}
		fmt.Fprintf(output, "HOST 无效: %v\n", validationErr)
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading API host: %w", io.EOF)
		}
	}
}

func promptSecret(input io.Reader, output io.Writer, readSecret func(io.Reader, io.Writer) (string, error)) (string, error) {
	for {
		fmt.Fprint(output, "API Key: ")
		value, err := readSecret(input, output)
		if err != nil {
			return "", fmt.Errorf("reading API key: %w", err)
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value, nil
		}
		fmt.Fprintln(output, "API Key 不能为空。")
	}
}

func readPromptLine(input io.Reader, _ io.Writer) (string, error) {
	return readLine(input)
}

func readSecretFromTerminal(input io.Reader, output io.Writer) (string, error) {
	file, ok := input.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return readPromptLine(input, output)
	}
	secret, err := term.ReadPassword(int(file.Fd()))
	fmt.Fprintln(output)
	if err != nil {
		return "", err
	}
	return string(secret), nil
}

func promptConfirmation(input io.Reader, output io.Writer) (bool, error) {
	fmt.Fprint(output, "确认写入以上配置？[y/N]: ")
	line, err := readLine(input)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func readLine(input io.Reader) (string, error) {
	var line strings.Builder
	buffer := []byte{0}
	for {
		count, err := input.Read(buffer)
		if count > 0 {
			if buffer[0] == '\n' {
				return line.String(), nil
			}
			line.WriteByte(buffer[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && line.Len() > 0 {
				return line.String(), nil
			}
			return line.String(), err
		}
		if count == 0 {
			return line.String(), io.ErrNoProgress
		}
	}
}

func printCodexConfigPreview(output io.Writer, prepared []byte, key string, willBackup bool) {
	fmt.Fprintln(output, "\n请确认将写入的完整 config.toml：")
	fmt.Fprintln(output, "```toml")
	fmt.Fprint(output, string(prepared))
	if len(prepared) == 0 || prepared[len(prepared)-1] != '\n' {
		fmt.Fprintln(output)
	}
	fmt.Fprintln(output, "```")
	fmt.Fprintf(output, "API Key（由 Codex 安全保存）= %s\n", codexx.MaskAPIKey(key))
	if willBackup {
		fmt.Fprintln(output, "现有 config.toml 语法损坏；确认后会先保存 0600 权限备份，再写入以上干净配置。")
	}
}

func productionCodexInstallDependencies() codexInstallDependencies {
	runner := codexx.ExecRunner{}
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	return codexInstallDependencies{
		isSudo: func() bool { return os.Getenv("SUDO_USER") != "" || os.Getenv("SUDO_UID") != "" },
		resolveCurrent: func() (string, error) {
			return codexx.ResolveExecutable(os.Getenv("PATH"), os.Getenv("HOME"))
		},
		resolveInstalled: func() (string, error) {
			return codexx.ResolveInstalledExecutable(os.Getenv("PATH"), os.Getenv("HOME"), os.Getenv("CODEX_INSTALL_DIR"))
		},
		detectVersion: func(ctx context.Context, executable string) (string, error) {
			return codexx.DetectVersion(ctx, runner, executable)
		},
		install: func(ctx context.Context, stdout, stderr io.Writer) error {
			return codexx.Install(ctx, httpClient, runner, stdout, stderr)
		},
		configPath: func() (string, error) {
			return codexx.ConfigPath(os.Getenv("CODEX_HOME"), os.Getenv("HOME"))
		},
		prepareConfig: codexx.PrepareConfig,
		readConfig:    codexx.ReadConfig,
		writeConfig:   codexx.WritePreparedConfig,
		login: func(ctx context.Context, executable, key string, stdout, stderr io.Writer) error {
			return codexx.Login(ctx, runner, executable, key, stdout, stderr)
		},
		validate: func(ctx context.Context, executable string, stdout, stderr io.Writer) error {
			return codexx.Validate(ctx, runner, executable, stdout, stderr)
		},
		readSecret: readSecretFromTerminal,
	}
}
