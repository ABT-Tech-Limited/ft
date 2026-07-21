package updater

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadAsset(t *testing.T) {
	data := []byte("verified release archive")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("Accept-Encoding = %q, want identity", got)
		}
		if got := r.Header.Get("User-Agent"); got != "ft-updater" {
			t.Errorf("User-Agent = %q, want ft-updater", got)
		}
		if _, err := w.Write(data); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	asset := Asset{Name: "ft_0.2.0_linux_amd64.tar.gz", URL: server.URL, Size: int64(len(data))}
	path, err := downloadAsset(context.Background(), server.Client(), asset, dir, 1<<20)
	if err != nil {
		t.Fatalf("downloadAsset() error = %v", err)
	}
	if want := filepath.Join(dir, asset.Name); path != want {
		t.Errorf("downloadAsset() path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading downloaded asset: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("downloaded data = %q, want %q", got, data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat downloaded asset: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Errorf("downloaded mode = %o, want 600", gotMode)
	}
}

func TestDownloadAssetRejectsNilDependencies(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		client  *http.Client
		wantErr string
	}{
		{name: "nil context", client: http.DefaultClient, wantErr: "nil context"},
		{name: "nil HTTP client", ctx: context.Background(), wantErr: "nil HTTP client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := downloadAsset(
				tt.ctx,
				tt.client,
				Asset{Name: "asset", URL: "https://example.com/asset"},
				t.TempDir(),
				1024,
			)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("downloadAsset() = %q, %v; want error containing %q", path, err, tt.wantErr)
			}
		})
	}
}

func TestDownloadAssetValidatesBeforeRequest(t *testing.T) {
	tests := []struct {
		name     string
		asset    Asset
		dir      string
		maxBytes int64
		wantErr  string
	}{
		{name: "empty directory", asset: Asset{Name: "asset", URL: "https://example.com/asset"}, maxBytes: 1024, wantErr: "empty download directory"},
		{name: "empty asset name", asset: Asset{URL: "https://example.com/asset"}, dir: t.TempDir(), maxBytes: 1024, wantErr: "invalid release asset name"},
		{name: "nested asset name", asset: Asset{Name: "../asset", URL: "https://example.com/asset"}, dir: t.TempDir(), maxBytes: 1024, wantErr: "invalid release asset name"},
		{name: "empty asset URL", asset: Asset{Name: "asset"}, dir: t.TempDir(), maxBytes: 1024, wantErr: "release asset URL"},
		{name: "insecure asset URL", asset: Asset{Name: "asset", URL: "http://example.com/asset"}, dir: t.TempDir(), maxBytes: 1024, wantErr: "HTTPS"},
		{name: "zero limit", asset: Asset{Name: "asset", URL: "https://example.com/asset"}, dir: t.TempDir(), wantErr: "invalid download limit"},
		{name: "negative size", asset: Asset{Name: "asset", URL: "https://example.com/asset", Size: -1}, dir: t.TempDir(), maxBytes: 1024, wantErr: "invalid release asset size"},
		{name: "declared oversized", asset: Asset{Name: "asset", URL: "https://example.com/asset", Size: 1025}, dir: t.TempDir(), maxBytes: 1024, wantErr: "exceeds 1024 bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, fmt.Errorf("unexpected request")
			})}
			path, err := downloadAsset(context.Background(), client, tt.asset, tt.dir, tt.maxBytes)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("downloadAsset() = %q, %v; want error containing %q", path, err, tt.wantErr)
			}
			if requests != 0 {
				t.Errorf("HTTP requests = %d, want 0", requests)
			}
		})
	}
}

func TestDownloadAssetRejectsHTTPError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dir := t.TempDir()
	asset := Asset{Name: "asset", URL: server.URL}
	path, err := downloadAsset(context.Background(), server.Client(), asset, dir, 1024)
	if err == nil || !strings.Contains(err.Error(), "HTTP status 503") {
		t.Fatalf("downloadAsset() = %q, %v; want HTTP status error", path, err)
	}
	if _, err := os.Stat(filepath.Join(dir, asset.Name)); !os.IsNotExist(err) {
		t.Fatalf("downloaded file stat error = %v, want not exist", err)
	}
}

func TestDownloadAssetRejectsRedirectToHTTP(t *testing.T) {
	insecureRequests := 0
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		insecureRequests++
		if _, err := w.Write([]byte("unsafe")); err != nil {
			t.Errorf("writing insecure response: %v", err)
		}
	}))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, insecure.URL, http.StatusFound)
	}))
	defer secure.Close()

	dir := t.TempDir()
	asset := Asset{Name: "asset", URL: secure.URL}
	path, err := downloadAsset(context.Background(), secure.Client(), asset, dir, 1024)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("downloadAsset() = %q, %v; want HTTPS redirect error", path, err)
	}
	if insecureRequests != 0 {
		t.Errorf("insecure requests = %d, want redirect rejected before request", insecureRequests)
	}
	if _, err := os.Stat(filepath.Join(dir, asset.Name)); !os.IsNotExist(err) {
		t.Fatalf("downloaded file stat error = %v, want not exist", err)
	}
}

func TestDownloadAssetRejectsInvalidSizeAndRemovesPartialFile(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		contentLength int64
		flushHeaders  bool
		assetSize     int64
		maxBytes      int64
		wantErr       string
	}{
		{
			name:          "declared response too large",
			body:          "12345",
			contentLength: 5,
			maxBytes:      4,
			wantErr:       "exceeds 4 bytes",
		},
		{
			name:          "streamed response too large",
			body:          "12345",
			contentLength: -1,
			flushHeaders:  true,
			maxBytes:      4,
			wantErr:       "exceeds 4 bytes",
		},
		{
			name:          "GitHub asset size mismatch",
			body:          "12345",
			contentLength: 5,
			assetSize:     6,
			maxBytes:      10,
			wantErr:       "size mismatch",
		},
		{
			name:          "truncated response",
			body:          "12345",
			contentLength: 6,
			assetSize:     6,
			maxBytes:      10,
			wantErr:       "writing release asset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.contentLength >= 0 {
					w.Header().Set("Content-Length", strconv.FormatInt(tt.contentLength, 10))
				}
				if tt.flushHeaders {
					flusher, ok := w.(http.Flusher)
					if !ok {
						t.Fatal("response writer does not support flushing")
					}
					flusher.Flush()
				}
				if _, err := w.Write([]byte(tt.body)); err != nil && tt.contentLength <= int64(len(tt.body)) {
					t.Errorf("writing response: %v", err)
				}
			}))
			defer server.Close()

			dir := t.TempDir()
			asset := Asset{Name: "asset", URL: server.URL, Size: tt.assetSize}
			path, err := downloadAsset(context.Background(), server.Client(), asset, dir, tt.maxBytes)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("downloadAsset() = %q, %v; want error containing %q", path, err, tt.wantErr)
			}
			if _, err := os.Stat(filepath.Join(dir, asset.Name)); !os.IsNotExist(err) {
				t.Fatalf("partial file stat error = %v, want not exist", err)
			}
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	archiveName := "ft_0.2.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	archive := []byte("release archive")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatalf("writing archive: %v", err)
	}
	digest := sha256.Sum256(archive)
	checksums := fmt.Sprintf(
		"%064x  ft_0.2.0_linux_arm64.tar.gz\n%x  %s\n",
		1,
		digest,
		archiveName,
	)
	checksumPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksumPath, []byte(checksums), 0o600); err != nil {
		t.Fatalf("writing checksums: %v", err)
	}

	if err := verifyChecksum(archivePath, checksumPath); err != nil {
		t.Fatalf("verifyChecksum() error = %v", err)
	}
}

func TestVerifyChecksumRejectsInvalidChecksum(t *testing.T) {
	archiveName := "ft_0.2.0_linux_amd64.tar.gz"
	archive := []byte("release archive")
	digest := sha256.Sum256(archive)
	validLine := fmt.Sprintf("%x  %s\n", digest, archiveName)
	tests := []struct {
		name      string
		checksums string
		wantErr   string
	}{
		{name: "missing entry", checksums: fmt.Sprintf("%x  other.tar.gz\n", digest), wantErr: "not found"},
		{name: "non-hex digest", checksums: "not-a-hash  " + archiveName + "\n", wantErr: "parsing checksum"},
		{name: "short digest", checksums: "00  " + archiveName + "\n", wantErr: "must be a SHA-256 digest"},
		{name: "duplicate entry", checksums: validLine + validLine, wantErr: "duplicate checksum"},
		{name: "mismatch", checksums: strings.Repeat("0", 64) + "  " + archiveName + "\n", wantErr: "checksum mismatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			archivePath := filepath.Join(dir, archiveName)
			if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
				t.Fatalf("writing archive: %v", err)
			}
			checksumPath := filepath.Join(dir, "checksums.txt")
			if err := os.WriteFile(checksumPath, []byte(tt.checksums), 0o600); err != nil {
				t.Fatalf("writing checksums: %v", err)
			}

			err := verifyChecksum(archivePath, checksumPath)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("verifyChecksum() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
