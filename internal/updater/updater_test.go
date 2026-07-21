package updater

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateSkipsAssetDownloadsWhenLocalVersionIsCurrentOrNewer(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
	}{
		{name: "same version", currentVersion: "0.2.0"},
		{name: "local version newer", currentVersion: "0.3.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/latest" {
					t.Errorf("unexpected asset request %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				body := fmt.Sprintf(`{
  "tag_name":"v0.2.0",
  "draft":false,
  "prerelease":false,
  "assets":[
    {"name":"checksums.txt","browser_download_url":"%s/checksums","size":188},
    {"name":"ft_0.2.0_linux_amd64.tar.gz","browser_download_url":"%s/archive","size":1200}
  ]
}`, serverURL(r), serverURL(r))
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("writing latest release: %v", err)
				}
			}))
			defer server.Close()

			dir := t.TempDir()
			executable := filepath.Join(dir, "ft")
			if err := os.WriteFile(executable, []byte("current binary"), 0o755); err != nil {
				t.Fatalf("writing current executable: %v", err)
			}
			privilegedCalls := 0
			result, err := Update(context.Background(), Options{
				CurrentVersion: tt.currentVersion,
				GOOS:           "linux",
				GOARCH:         "amd64",
				ExecutablePath: executable,
				Client:         Client{HTTPClient: server.Client(), LatestURL: server.URL + "/latest"},
				PrivilegedRun: func(context.Context, []string, io.Reader) error {
					privilegedCalls++
					return nil
				},
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if result.Updated {
				t.Errorf("Update() result = %#v, want no update", result)
			}
			if result.CurrentVersion != "v"+tt.currentVersion || result.LatestVersion != "v0.2.0" {
				t.Errorf("Update() versions = %q -> %q", result.CurrentVersion, result.LatestVersion)
			}
			wantPath, err := filepath.EvalSymlinks(executable)
			if err != nil {
				t.Fatalf("resolving expected executable: %v", err)
			}
			if result.Path != wantPath {
				t.Errorf("Update() path = %q, want %q", result.Path, wantPath)
			}
			if requests != 1 {
				t.Errorf("HTTP requests = %d, want latest metadata only", requests)
			}
			if privilegedCalls != 0 {
				t.Errorf("privileged calls = %d, want 0", privilegedCalls)
			}
			data, err := os.ReadFile(executable)
			if err != nil {
				t.Fatalf("reading unchanged executable: %v", err)
			}
			if got, want := string(data), "current binary"; got != want {
				t.Errorf("unchanged executable = %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateDownloadsVerifiesAndInstallsNewRelease(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "ft")
	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing current executable: %v", err)
	}
	archiveName := "ft_0.2.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	writeTestArchive(t, archivePath, []archiveEntry{
		{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg, Mode: 0o755}, data: "new binary"},
	})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading test archive: %v", err)
	}
	digest := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%x  %s\n", digest, archiveName))

	var requests []string
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/latest":
			body := fmt.Sprintf(`{
  "tag_name":"v0.2.0",
  "draft":false,
  "prerelease":false,
  "assets":[
    {"name":"checksums.txt","browser_download_url":"%s/checksums","size":%d},
    {"name":"%s","browser_download_url":"%s/archive","size":%d}
  ]
}`, server.URL, len(checksums), archiveName, server.URL, len(archive))
			if _, err := w.Write([]byte(body)); err != nil {
				t.Errorf("writing latest release: %v", err)
			}
		case "/checksums":
			if _, err := w.Write(checksums); err != nil {
				t.Errorf("writing checksums: %v", err)
			}
		case "/archive":
			if _, err := w.Write(archive); err != nil {
				t.Errorf("writing archive: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	privilegedCalls := 0
	result, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		GOOS:           "linux",
		GOARCH:         "amd64",
		ExecutablePath: executable,
		Client:         Client{HTTPClient: server.Client(), LatestURL: server.URL + "/latest"},
		PrivilegedRun: func(context.Context, []string, io.Reader) error {
			privilegedCalls++
			return fmt.Errorf("privileged runner must not be called")
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !result.Updated || result.CurrentVersion != "v0.1.0" || result.LatestVersion != "v0.2.0" {
		t.Errorf("Update() result = %#v", result)
	}
	if privilegedCalls != 0 {
		t.Errorf("privileged calls = %d, want 0", privilegedCalls)
	}
	if len(requests) != 3 || requests[0] != "/latest" {
		t.Errorf("requests = %v, want latest plus two assets", requests)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("reading updated executable: %v", err)
	}
	if got, want := string(data), "new binary"; got != want {
		t.Errorf("updated executable = %q, want %q", got, want)
	}
}

func TestUpdatePreservesExecutableWhenInstallFails(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatalf("creating target directory: %v", err)
	}
	executable := filepath.Join(targetDir, "ft")
	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing current executable: %v", err)
	}

	archiveName := "ft_0.2.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	writeTestArchive(t, archivePath, []archiveEntry{
		{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg, Mode: 0o755}, data: "new binary"},
	})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading test archive: %v", err)
	}
	digest := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%x  %s\n", digest, archiveName))

	movedTargetDir := filepath.Join(dir, "moved-bin")
	var mutationErr error
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			if mutationErr == nil {
				mutationErr = os.Rename(targetDir, movedTargetDir)
			}
			if mutationErr == nil {
				mutationErr = os.WriteFile(targetDir, []byte("not a directory"), 0o600)
			}
			if mutationErr != nil {
				http.Error(w, mutationErr.Error(), http.StatusInternalServerError)
				return
			}
			body := fmt.Sprintf(`{
  "tag_name":"v0.2.0",
  "draft":false,
  "prerelease":false,
  "assets":[
    {"name":"checksums.txt","browser_download_url":"%s/checksums","size":%d},
    {"name":"%s","browser_download_url":"%s/archive","size":%d}
  ]
}`, server.URL, len(checksums), archiveName, server.URL, len(archive))
			if _, err := w.Write([]byte(body)); err != nil {
				t.Errorf("writing latest release: %v", err)
			}
		case "/checksums":
			if _, err := w.Write(checksums); err != nil {
				t.Errorf("writing checksums: %v", err)
			}
		case "/archive":
			if _, err := w.Write(archive); err != nil {
				t.Errorf("writing archive: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	privilegedCalls := 0
	result, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		GOOS:           "linux",
		GOARCH:         "amd64",
		ExecutablePath: executable,
		Client:         Client{HTTPClient: server.Client(), LatestURL: server.URL + "/latest"},
		PrivilegedRun: func(context.Context, []string, io.Reader) error {
			privilegedCalls++
			return nil
		},
	})
	if mutationErr != nil {
		t.Fatalf("preparing install failure: %v", mutationErr)
	}
	if err == nil || !strings.Contains(err.Error(), "replacing installed ft executable") {
		t.Fatalf("Update() = %#v, %v; want install failure", result, err)
	}
	if privilegedCalls != 0 {
		t.Errorf("privileged runner calls = %d, want 0 for non-permission failure", privilegedCalls)
	}
	data, readErr := os.ReadFile(filepath.Join(movedTargetDir, "ft"))
	if readErr != nil {
		t.Fatalf("reading preserved executable: %v", readErr)
	}
	if got, want := string(data), "old binary"; got != want {
		t.Errorf("preserved executable = %q, want %q", got, want)
	}
}

func TestUpdatePreservesExecutableWhenChecksumFails(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "ft")
	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("writing current executable: %v", err)
	}
	archiveName := "ft_0.2.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	writeTestArchive(t, archivePath, []archiveEntry{
		{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg, Mode: 0o755}, data: "new binary"},
	})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("reading test archive: %v", err)
	}
	checksums := []byte(strings.Repeat("0", 64) + "  " + archiveName + "\n")

	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			body := fmt.Sprintf(`{
  "tag_name":"v0.2.0",
  "draft":false,
  "prerelease":false,
  "assets":[
    {"name":"checksums.txt","browser_download_url":"%s/checksums","size":%d},
    {"name":"%s","browser_download_url":"%s/archive","size":%d}
  ]
}`, server.URL, len(checksums), archiveName, server.URL, len(archive))
			if _, err := w.Write([]byte(body)); err != nil {
				t.Errorf("writing latest release: %v", err)
			}
		case "/checksums":
			if _, err := w.Write(checksums); err != nil {
				t.Errorf("writing checksums: %v", err)
			}
		case "/archive":
			if _, err := w.Write(archive); err != nil {
				t.Errorf("writing archive: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	privilegedCalls := 0
	result, err := Update(context.Background(), Options{
		CurrentVersion: "0.1.0",
		GOOS:           "linux",
		GOARCH:         "amd64",
		ExecutablePath: executable,
		Client:         Client{HTTPClient: server.Client(), LatestURL: server.URL + "/latest"},
		PrivilegedRun: func(context.Context, []string, io.Reader) error {
			privilegedCalls++
			return errors.New("privileged runner must not be called")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Update() = %#v, %v; want checksum mismatch", result, err)
	}
	if privilegedCalls != 0 {
		t.Errorf("privileged calls = %d, want 0", privilegedCalls)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("reading preserved executable: %v", err)
	}
	if got, want := string(data), "old binary"; got != want {
		t.Errorf("preserved executable = %q, want %q", got, want)
	}
}

func TestUpdateRejectsInvalidLocalStateBeforeNetwork(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		wantErr string
	}{
		{
			name: "development build",
			options: Options{
				CurrentVersion: "dev",
				GOOS:           "linux",
				GOARCH:         "amd64",
				ExecutablePath: "/missing",
			},
			wantErr: "development build",
		},
		{
			name: "unsupported platform",
			options: Options{
				CurrentVersion: "0.1.0",
				GOOS:           "darwin",
				GOARCH:         "arm64",
				ExecutablePath: "/missing",
			},
			wantErr: "unsupported update platform",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			tt.options.Client = Client{
				HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					requests++
					return nil, fmt.Errorf("unexpected request")
				})},
				LatestURL: "https://api.github.com/repos/ABT-Tech-Limited/ft/releases/latest",
			}
			_, err := Update(context.Background(), tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Update() error = %v, want error containing %q", err, tt.wantErr)
			}
			if requests != 0 {
				t.Errorf("HTTP requests = %d, want 0", requests)
			}
		})
	}
}

func serverURL(r *http.Request) string {
	return "https://" + r.Host
}
