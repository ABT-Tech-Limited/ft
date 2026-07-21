// Package updater securely updates ft from its official GitHub Releases.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/mod/semver"
)

const checksumAssetName = "checksums.txt"

const maxReleaseResponseBytes int64 = 1 << 20

// Asset describes one downloadable GitHub Release asset.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is the stable GitHub Release metadata needed by the updater.
type Release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Client reads ft Release metadata from GitHub.
type Client struct {
	HTTPClient *http.Client
	LatestURL  string
}

// NormalizeVersion validates a release version and returns its canonical
// leading-v semantic version.
func NormalizeVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty version")
	}
	if value == "dev" {
		return "", fmt.Errorf("development build cannot self-update")
	}
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}

	core := strings.TrimPrefix(value, "v")
	core, _, _ = strings.Cut(core, "+")
	core, _, _ = strings.Cut(core, "-")
	if strings.Count(core, ".") != 2 || !semver.IsValid(value) {
		return "", fmt.Errorf("invalid semantic version %q", strings.TrimPrefix(value, "v"))
	}
	return semver.Canonical(value), nil
}

// CompareVersions compares the current release with the latest release.
func CompareVersions(current, latest string) (int, error) {
	currentVersion, err := NormalizeVersion(current)
	if err != nil {
		return 0, fmt.Errorf("invalid current version: %w", err)
	}
	latestVersion, err := NormalizeVersion(latest)
	if err != nil {
		return 0, fmt.Errorf("invalid latest version: %w", err)
	}
	return semver.Compare(currentVersion, latestVersion), nil
}

// AssetsFor returns the GoReleaser archive and checksum assets for a platform.
func (r Release) AssetsFor(goos, goarch string) (Asset, Asset, error) {
	if goos != "linux" || (goarch != "amd64" && goarch != "arm64") {
		return Asset{}, Asset{}, fmt.Errorf("unsupported update platform: %s/%s", goos, goarch)
	}
	if _, err := r.stableVersion(); err != nil {
		return Asset{}, Asset{}, err
	}
	assetVersion := strings.TrimPrefix(strings.TrimSpace(r.TagName), "v")
	archiveName := fmt.Sprintf("ft_%s_%s_%s.tar.gz", assetVersion, goos, goarch)

	var archive Asset
	var checksum Asset
	for _, asset := range r.Assets {
		switch asset.Name {
		case archiveName:
			if archive.Name != "" {
				return Asset{}, Asset{}, fmt.Errorf("duplicate release asset %q", archiveName)
			}
			archive = asset
		case checksumAssetName:
			if checksum.Name != "" {
				return Asset{}, Asset{}, fmt.Errorf("duplicate release asset %q", checksumAssetName)
			}
			checksum = asset
		}
	}
	if archive.Name == "" {
		return Asset{}, Asset{}, fmt.Errorf("release asset %q not found", archiveName)
	}
	if checksum.Name == "" {
		return Asset{}, Asset{}, fmt.Errorf("release asset %q not found", checksumAssetName)
	}
	return archive, checksum, nil
}

// Latest fetches and parses the latest stable Release metadata.
func (c Client) Latest(ctx context.Context) (Release, error) {
	if ctx == nil {
		return Release{}, fmt.Errorf("nil context")
	}
	if c.HTTPClient == nil {
		return Release{}, fmt.Errorf("nil HTTP client")
	}
	if strings.TrimSpace(c.LatestURL) == "" {
		return Release{}, fmt.Errorf("empty latest release URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.LatestURL, nil)
	if err != nil {
		return Release{}, fmt.Errorf("creating latest release request: %w", err)
	}
	if req.URL.Scheme != "https" {
		return Release{}, fmt.Errorf("latest release URL must use HTTPS")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ft-updater")

	resp, err := clientWithHTTPSRedirects(c.HTTPClient).Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("requesting latest release: %w", err)
	}
	if resp.Request == nil || resp.Request.URL.Scheme != "https" {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return Release{}, fmt.Errorf("latest release redirect must use HTTPS; closing response: %v", closeErr)
		}
		return Release{}, fmt.Errorf("latest release redirect must use HTTPS")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return Release{}, fmt.Errorf("requesting latest release: unexpected HTTP status %s; closing response: %v", resp.Status, closeErr)
		}
		return Release{}, fmt.Errorf("requesting latest release: unexpected HTTP status %s", resp.Status)
	}

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxReleaseResponseBytes+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return Release{}, fmt.Errorf("reading latest release response: %w", readErr)
	}
	if closeErr != nil {
		return Release{}, fmt.Errorf("closing latest release response: %w", closeErr)
	}
	if int64(len(data)) > maxReleaseResponseBytes {
		return Release{}, fmt.Errorf("latest release response exceeds %d bytes", maxReleaseResponseBytes)
	}

	var release Release
	if err := json.Unmarshal(data, &release); err != nil {
		return Release{}, fmt.Errorf("parsing latest release response: %w", err)
	}
	if _, err := release.stableVersion(); err != nil {
		return Release{}, err
	}
	return release, nil
}

func (r Release) stableVersion() (string, error) {
	if r.Draft || r.Prerelease {
		return "", fmt.Errorf("latest GitHub response is not a stable release")
	}
	version, err := NormalizeVersion(r.TagName)
	if err != nil {
		return "", fmt.Errorf("invalid release tag: %w", err)
	}
	if semver.Prerelease(version) != "" {
		return "", fmt.Errorf("latest GitHub response is not a stable release")
	}
	return version, nil
}
