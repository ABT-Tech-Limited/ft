package updater

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr string
	}{
		{name: "GoReleaser version", value: "0.1.0", want: "v0.1.0"},
		{name: "tag version", value: "v1.2.3", want: "v1.2.3"},
		{name: "build metadata", value: "v1.2.3+build.7", want: "v1.2.3"},
		{name: "surrounding whitespace", value: "  2.0.1  ", want: "v2.0.1"},
		{name: "development build", value: "dev", wantErr: "development build"},
		{name: "empty", wantErr: "empty version"},
		{name: "missing patch", value: "1.2", wantErr: "semantic version"},
		{name: "invalid", value: "latest", wantErr: "semantic version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeVersion(tt.value)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NormalizeVersion(%q) = %q, %v; want error containing %q", tt.value, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeVersion(%q) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeVersion(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestReleaseAssetsForPreservesBuildMetadata(t *testing.T) {
	archive := Asset{
		Name: "ft_1.2.3+build.7_linux_amd64.tar.gz",
		URL:  "https://github.com/example/ft_1.2.3+build.7_linux_amd64.tar.gz",
	}
	checksum := Asset{Name: checksumAssetName, URL: "https://github.com/example/checksums.txt"}
	release := Release{
		TagName: "v1.2.3+build.7",
		Assets:  []Asset{archive, checksum},
	}

	gotArchive, gotChecksum, err := release.AssetsFor("linux", "amd64")
	if err != nil {
		t.Fatalf("AssetsFor() error = %v", err)
	}
	if !reflect.DeepEqual(gotArchive, archive) {
		t.Errorf("archive = %#v, want %#v", gotArchive, archive)
	}
	if !reflect.DeepEqual(gotChecksum, checksum) {
		t.Errorf("checksum = %#v, want %#v", gotChecksum, checksum)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    int
		wantErr string
	}{
		{name: "update available", current: "0.1.0", latest: "v0.2.0", want: -1},
		{name: "same version", current: "v1.2.3", latest: "1.2.3", want: 0},
		{name: "local version is newer", current: "2.0.0", latest: "v1.9.9", want: 1},
		{name: "invalid current", current: "dev", latest: "v1.0.0", wantErr: "current version"},
		{name: "invalid latest", current: "1.0.0", latest: "latest", wantErr: "latest version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareVersions(tt.current, tt.latest)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CompareVersions(%q, %q) = %d, %v; want error containing %q", tt.current, tt.latest, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q) error = %v", tt.current, tt.latest, err)
			}
			if got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestReleaseAssetsFor(t *testing.T) {
	checksum := Asset{Name: "checksums.txt", URL: "https://github.com/example/checksums.txt", Size: 188}
	amd64 := Asset{Name: "ft_0.2.0_linux_amd64.tar.gz", URL: "https://github.com/example/amd64", Size: 1200000}
	arm64 := Asset{Name: "ft_0.2.0_linux_arm64.tar.gz", URL: "https://github.com/example/arm64", Size: 1100000}
	release := Release{
		TagName: "v0.2.0",
		Assets:  []Asset{checksum, amd64, arm64},
	}

	tests := []struct {
		name        string
		goos        string
		goarch      string
		wantArchive Asset
		wantErr     string
	}{
		{name: "amd64", goos: "linux", goarch: "amd64", wantArchive: amd64},
		{name: "arm64", goos: "linux", goarch: "arm64", wantArchive: arm64},
		{name: "other operating system", goos: "darwin", goarch: "arm64", wantErr: "unsupported update platform"},
		{name: "other architecture", goos: "linux", goarch: "386", wantErr: "unsupported update platform"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive, gotChecksum, err := release.AssetsFor(tt.goos, tt.goarch)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("AssetsFor(%q, %q) error = %v, want error containing %q", tt.goos, tt.goarch, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AssetsFor(%q, %q) error = %v", tt.goos, tt.goarch, err)
			}
			if !reflect.DeepEqual(archive, tt.wantArchive) {
				t.Errorf("archive = %#v, want %#v", archive, tt.wantArchive)
			}
			if !reflect.DeepEqual(gotChecksum, checksum) {
				t.Errorf("checksum = %#v, want %#v", gotChecksum, checksum)
			}
		})
	}
}

func TestReleaseAssetsForRejectsInvalidRelease(t *testing.T) {
	archive := Asset{Name: "ft_0.2.0_linux_amd64.tar.gz", URL: "https://github.com/example/amd64"}
	checksum := Asset{Name: "checksums.txt", URL: "https://github.com/example/checksums.txt"}
	tests := []struct {
		name    string
		release Release
		wantErr string
	}{
		{
			name:    "draft",
			release: Release{TagName: "v0.2.0", Draft: true, Assets: []Asset{archive, checksum}},
			wantErr: "stable release",
		},
		{
			name:    "prerelease",
			release: Release{TagName: "v0.2.0-rc.1", Prerelease: true, Assets: []Asset{archive, checksum}},
			wantErr: "stable release",
		},
		{
			name:    "prerelease tag with false metadata",
			release: Release{TagName: "v0.2.0-rc.1", Assets: []Asset{archive, checksum}},
			wantErr: "stable release",
		},
		{
			name:    "invalid tag",
			release: Release{TagName: "latest", Assets: []Asset{archive, checksum}},
			wantErr: "invalid release tag",
		},
		{
			name:    "missing archive",
			release: Release{TagName: "v0.2.0", Assets: []Asset{checksum}},
			wantErr: "not found",
		},
		{
			name:    "missing checksum",
			release: Release{TagName: "v0.2.0", Assets: []Asset{archive}},
			wantErr: "not found",
		},
		{
			name:    "duplicate archive",
			release: Release{TagName: "v0.2.0", Assets: []Asset{archive, archive, checksum}},
			wantErr: "duplicate release asset",
		},
		{
			name:    "duplicate checksum",
			release: Release{TagName: "v0.2.0", Assets: []Asset{archive, checksum, checksum}},
			wantErr: "duplicate release asset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArchive, gotChecksum, err := tt.release.AssetsFor("linux", "amd64")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("AssetsFor() = %#v, %#v, %v; want error containing %q", gotArchive, gotChecksum, err, tt.wantErr)
			}
		})
	}
}

func TestClientLatest(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "ft-updater" {
			t.Errorf("User-Agent header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{
  "tag_name": "v0.2.0",
  "draft": false,
  "prerelease": false,
  "assets": [
    {
      "name": "checksums.txt",
      "browser_download_url": "https://github.com/ABT-Tech-Limited/ft/releases/download/v0.2.0/checksums.txt",
      "size": 188
    },
    {
      "name": "ft_0.2.0_linux_amd64.tar.gz",
      "browser_download_url": "https://github.com/ABT-Tech-Limited/ft/releases/download/v0.2.0/ft_0.2.0_linux_amd64.tar.gz",
      "size": 1200000
    }
  ]
}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer server.Close()

	want := Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{
				Name: "checksums.txt",
				URL:  "https://github.com/ABT-Tech-Limited/ft/releases/download/v0.2.0/checksums.txt",
				Size: 188,
			},
			{
				Name: "ft_0.2.0_linux_amd64.tar.gz",
				URL:  "https://github.com/ABT-Tech-Limited/ft/releases/download/v0.2.0/ft_0.2.0_linux_amd64.tar.gz",
				Size: 1200000,
			},
		},
	}
	client := Client{HTTPClient: server.Client(), LatestURL: server.URL}
	got, err := client.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Latest() = %#v, want %#v", got, want)
	}
}

func TestClientLatestRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "HTTP error", status: http.StatusBadGateway, body: `{"message":"bad gateway"}`, wantErr: "HTTP status 502"},
		{name: "malformed JSON", status: http.StatusOK, body: `{`, wantErr: "parsing latest release response"},
		{
			name:    "oversized JSON",
			status:  http.StatusOK,
			body:    strings.Repeat(" ", (1<<20)+1),
			wantErr: "response exceeds",
		},
		{
			name:    "draft",
			status:  http.StatusOK,
			body:    `{"tag_name":"v1.0.0","draft":true,"prerelease":false,"assets":[]}`,
			wantErr: "stable release",
		},
		{
			name:    "prerelease",
			status:  http.StatusOK,
			body:    `{"tag_name":"v1.0.0-rc.1","draft":false,"prerelease":true,"assets":[]}`,
			wantErr: "stable release",
		},
		{
			name:    "prerelease tag with false metadata",
			status:  http.StatusOK,
			body:    `{"tag_name":"v1.0.0-rc.1","draft":false,"prerelease":false,"assets":[]}`,
			wantErr: "stable release",
		},
		{
			name:    "invalid tag",
			status:  http.StatusOK,
			body:    `{"tag_name":"latest","draft":false,"prerelease":false,"assets":[]}`,
			wantErr: "invalid release tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				if _, err := w.Write([]byte(tt.body)); err != nil {
					t.Errorf("writing response: %v", err)
				}
			}))
			defer server.Close()

			client := Client{HTTPClient: server.Client(), LatestURL: server.URL}
			release, err := client.Latest(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Latest() = %#v, %v; want error containing %q", release, err, tt.wantErr)
			}
		})
	}
}

func TestClientLatestRejectsInsecureURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("insecure endpoint should not be requested")
	}))
	defer server.Close()

	client := Client{HTTPClient: server.Client(), LatestURL: server.URL}
	_, err := client.Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("Latest() error = %v, want HTTPS error", err)
	}
}

func TestClientLatestRejectsRedirectToHTTP(t *testing.T) {
	insecureRequests := 0
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		insecureRequests++
		if _, err := w.Write([]byte(`{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[]}`)); err != nil {
			t.Errorf("writing insecure response: %v", err)
		}
	}))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, insecure.URL, http.StatusFound)
	}))
	defer secure.Close()

	client := Client{HTTPClient: secure.Client(), LatestURL: secure.URL}
	_, err := client.Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("Latest() error = %v, want HTTPS redirect error", err)
	}
	if insecureRequests != 0 {
		t.Errorf("insecure requests = %d, want redirect rejected before request", insecureRequests)
	}
}

func TestClientLatestPreservesCustomRedirectPolicy(t *testing.T) {
	redirectErr := errors.New("redirect rejected by caller")
	targetRequests := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests++
	}))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	httpClient := redirect.Client()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return redirectErr
	}
	client := Client{HTTPClient: httpClient, LatestURL: redirect.URL}
	_, err := client.Latest(context.Background())
	if !errors.Is(err, redirectErr) {
		t.Fatalf("Latest() error = %v, want custom redirect error", err)
	}
	if targetRequests != 0 {
		t.Errorf("redirect target requests = %d, want 0", targetRequests)
	}
}

func TestClientLatestRetainsDefaultRedirectLimit(t *testing.T) {
	requests := 0
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, server.URL, http.StatusFound)
	}))
	defer server.Close()

	client := Client{HTTPClient: server.Client(), LatestURL: server.URL}
	_, err := client.Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("Latest() error = %v, want default redirect limit error", err)
	}
	if requests != 10 {
		t.Errorf("requests = %d, want 10", requests)
	}
}

func TestClientLatestValidatesDependenciesAndContext(t *testing.T) {
	tests := []struct {
		name    string
		client  Client
		ctx     context.Context
		wantErr string
	}{
		{
			name:    "nil HTTP client",
			client:  Client{LatestURL: "https://api.github.com/repos/ABT-Tech-Limited/ft/releases/latest"},
			ctx:     context.Background(),
			wantErr: "nil HTTP client",
		},
		{
			name:    "nil context",
			client:  Client{HTTPClient: http.DefaultClient, LatestURL: "https://api.github.com/repos/ABT-Tech-Limited/ft/releases/latest"},
			wantErr: "nil context",
		},
		{
			name:    "empty URL",
			client:  Client{HTTPClient: http.DefaultClient},
			ctx:     context.Background(),
			wantErr: "latest release URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.client.Latest(tt.ctx)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Latest() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestClientLatestPassesContextCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := Client{HTTPClient: server.Client(), LatestURL: server.URL}
	_, err := client.Latest(ctx)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Latest() error = %v, want context cancellation", err)
	}
}
