package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	privilegedInstallPath = "/usr/bin/install"
	privilegedMovePath    = "/usr/bin/mv"
	privilegedRemovePath  = "/usr/bin/rm"
)

// PrivilegedRunner executes a command through the application's elevation boundary.
type PrivilegedRunner func(context.Context, []string, io.Reader) error

func installDirect(source, target string) (resultErr error) {
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("opening verified ft binary: %w", err)
	}
	defer func() {
		if sourceFile != nil {
			resultErr = errors.Join(resultErr, sourceFile.Close())
		}
	}()
	info, err := sourceFile.Stat()
	if err != nil {
		return fmt.Errorf("inspecting verified ft binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("verified ft binary is not a regular file")
	}

	tempFile, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".update-*")
	if err != nil {
		return fmt.Errorf("creating target update file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if tempFile != nil {
			resultErr = errors.Join(resultErr, tempFile.Close())
		}
		if resultErr != nil {
			if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("removing target update file %q: %w", tempPath, err))
			}
		}
	}()

	if _, err := io.Copy(tempFile, sourceFile); err != nil {
		return fmt.Errorf("copying verified ft binary: %w", err)
	}
	if err := sourceFile.Close(); err != nil {
		return fmt.Errorf("closing verified ft binary: %w", err)
	}
	sourceFile = nil
	if err := tempFile.Chmod(0o755); err != nil {
		return fmt.Errorf("setting updated ft permissions: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("syncing updated ft binary: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("closing updated ft binary: %w", err)
	}
	tempFile = nil
	if err := os.Rename(tempPath, target); err != nil {
		return fmt.Errorf("activating updated ft binary: %w", err)
	}
	return nil
}

func resolveExecutable(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("empty executable path")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolving executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving executable symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspecting resolved executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("resolved executable is not a regular file")
	}
	return resolved, nil
}

func installPrivileged(ctx context.Context, source, target string, run PrivilegedRunner) error {
	if ctx == nil {
		return fmt.Errorf("nil context")
	}
	if run == nil {
		return fmt.Errorf("nil privileged runner")
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("empty verified binary path")
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("empty target executable path")
	}
	tempName := fmt.Sprintf(".%s.update-%s", filepath.Base(target), filepath.Base(filepath.Dir(source)))
	tempTarget := filepath.Join(filepath.Dir(target), tempName)
	cleanup := func(cause error) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := run(cleanupCtx, []string{privilegedRemovePath, "-f", "--", tempTarget}, nil); err != nil {
			return errors.Join(cause, fmt.Errorf("cleaning privileged ft update: %w", err))
		}
		return cause
	}
	installCommand := []string{
		privilegedInstallPath, "-o", "root", "-g", "root", "-m", "0755", "--",
		source, tempTarget,
	}
	if err := run(ctx, installCommand, nil); err != nil {
		return cleanup(fmt.Errorf("staging privileged ft update: %w", err))
	}
	if err := run(ctx, []string{privilegedMovePath, "-f", "--", tempTarget, target}, nil); err != nil {
		return cleanup(fmt.Errorf("activating privileged ft update: %w", err))
	}
	return nil
}

func installBinary(ctx context.Context, source, target string, run PrivilegedRunner) error {
	directErr := installDirect(source, target)
	if directErr == nil {
		return nil
	}
	if !errors.Is(directErr, os.ErrPermission) {
		return fmt.Errorf("installing ft update directly: %w", directErr)
	}
	if err := installPrivileged(ctx, source, target, run); err != nil {
		return fmt.Errorf("installing ft update with elevated privileges: %w", err)
	}
	return nil
}
