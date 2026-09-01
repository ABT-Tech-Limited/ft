package codexx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	// InstallerURL is the official OpenAI Codex installer script.
	InstallerURL = "https://raw.githubusercontent.com/openai/codex/main/scripts/install/install.sh"
	// InstallerMaxBytes bounds the installer kept in memory before execution.
	InstallerMaxBytes int64 = 2 << 20
)

// Install securely downloads the complete official installer and executes it
// as the current user through /bin/sh.
func Install(ctx context.Context, client *http.Client, runner Runner, output ...io.Writer) error {
	if ctx == nil {
		return fmt.Errorf("nil context for Codex installation")
	}
	if client == nil {
		return fmt.Errorf("nil HTTP client")
	}
	if runner == nil {
		return fmt.Errorf("nil runner")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, InstallerURL, nil)
	if err != nil {
		return fmt.Errorf("build Codex installer request: %w", err)
	}
	secureClient := githubOnlyClient(client)
	response, err := secureClient.Do(request)
	if err != nil {
		return fmt.Errorf("download Codex installer: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download Codex installer: HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > InstallerMaxBytes {
		return fmt.Errorf("Codex installer exceeds %d bytes", InstallerMaxBytes)
	}
	script, err := io.ReadAll(io.LimitReader(response.Body, InstallerMaxBytes+1))
	if err != nil {
		return fmt.Errorf("read Codex installer: %w", err)
	}
	if int64(len(script)) > InstallerMaxBytes {
		return fmt.Errorf("Codex installer exceeds %d bytes", InstallerMaxBytes)
	}
	if len(script) == 0 {
		return fmt.Errorf("downloaded Codex installer is empty")
	}

	var stdout, stderr io.Writer
	if len(output) > 0 {
		stdout = output[0]
	}
	if len(output) > 1 {
		stderr = output[1]
	}
	if err := runner.Run(ctx, Command{
		Path:   "/bin/sh",
		Stdin:  bytes.NewReader(script),
		Stdout: stdout,
		Stderr: stderr,
		Env:    []string{"CODEX_NON_INTERACTIVE=1"},
	}); err != nil {
		return fmt.Errorf("run Codex installer: %w", err)
	}
	return nil
}

func githubOnlyClient(client *http.Client) *http.Client {
	secureClient := *client
	originalCheckRedirect := client.CheckRedirect
	secureClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL == nil || request.URL.Scheme != "https" {
			return fmt.Errorf("redirect URL must use HTTPS")
		}
		if !allowedInstallerHost(request.URL) {
			return fmt.Errorf("redirect URL must remain on GitHub")
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(request, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &secureClient
}

func allowedInstallerHost(value *url.URL) bool {
	return value != nil && (value.Hostname() == "raw.githubusercontent.com" || value.Hostname() == "github.com")
}
