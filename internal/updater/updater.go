package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const (
	maxChecksumAssetBytes int64 = 1 << 20
	maxArchiveAssetBytes  int64 = 100 << 20
	maxBinaryBytes        int64 = 100 << 20
)

// Options contains the runtime dependencies and local build state for Update.
type Options struct {
	CurrentVersion string
	GOOS           string
	GOARCH         string
	ExecutablePath string
	Client         Client
	PrivilegedRun  PrivilegedRunner
}

// Result describes the version decision and installed executable path.
type Result struct {
	CurrentVersion string
	LatestVersion  string
	Path           string
	Updated        bool
}

// Update checks the latest stable Release and installs it when it is newer.
func Update(ctx context.Context, options Options) (result Result, resultErr error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("nil context")
	}
	currentVersion, err := NormalizeVersion(options.CurrentVersion)
	if err != nil {
		return Result{}, fmt.Errorf("validating current version: %w", err)
	}
	if options.GOOS != "linux" || (options.GOARCH != "amd64" && options.GOARCH != "arm64") {
		return Result{}, fmt.Errorf("unsupported update platform: %s/%s", options.GOOS, options.GOARCH)
	}
	target, err := resolveExecutable(options.ExecutablePath)
	if err != nil {
		return Result{}, fmt.Errorf("resolving installed ft executable: %w", err)
	}
	release, err := options.Client.Latest(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("checking latest ft release: %w", err)
	}
	latestVersion, err := NormalizeVersion(release.TagName)
	if err != nil {
		return Result{}, fmt.Errorf("validating latest release version: %w", err)
	}
	comparison, err := CompareVersions(currentVersion, latestVersion)
	if err != nil {
		return Result{}, err
	}
	result = Result{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		Path:           target,
	}
	if comparison >= 0 {
		return result, nil
	}

	archiveAsset, checksumAsset, err := release.AssetsFor(options.GOOS, options.GOARCH)
	if err != nil {
		return Result{}, fmt.Errorf("selecting release assets: %w", err)
	}
	workDir, err := os.MkdirTemp("", "ft-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("creating update workspace: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(workDir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("removing update workspace: %w", err))
		}
	}()

	checksumPath, err := downloadAsset(ctx, options.Client.HTTPClient, checksumAsset, workDir, maxChecksumAssetBytes)
	if err != nil {
		return Result{}, fmt.Errorf("downloading release checksums: %w", err)
	}
	archivePath, err := downloadAsset(ctx, options.Client.HTTPClient, archiveAsset, workDir, maxArchiveAssetBytes)
	if err != nil {
		return Result{}, fmt.Errorf("downloading release archive: %w", err)
	}
	if err := verifyChecksum(archivePath, checksumPath); err != nil {
		return Result{}, fmt.Errorf("verifying release archive: %w", err)
	}
	binaryPath, err := extractBinary(archivePath, workDir, maxBinaryBytes)
	if err != nil {
		return Result{}, fmt.Errorf("extracting release archive: %w", err)
	}
	if err := installBinary(ctx, binaryPath, target, options.PrivilegedRun); err != nil {
		return Result{}, fmt.Errorf("replacing installed ft executable: %w", err)
	}
	result.Updated = true
	return result, nil
}
