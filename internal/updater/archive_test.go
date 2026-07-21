package updater

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	header tar.Header
	data   string
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "ft.tar.gz")
	writeTestArchive(t, archivePath, []archiveEntry{
		{header: tar.Header{Name: "LICENSE", Mode: 0o644, Typeflag: tar.TypeReg}, data: "license"},
		{header: tar.Header{Name: "ft", Mode: 0o755, Typeflag: tar.TypeReg}, data: "new ft binary"},
	})
	extractDir := filepath.Join(dir, "extract")
	if err := os.Mkdir(extractDir, 0o700); err != nil {
		t.Fatalf("creating extract directory: %v", err)
	}

	path, err := extractBinary(archivePath, extractDir, 1024)
	if err != nil {
		t.Fatalf("extractBinary() error = %v", err)
	}
	if want := filepath.Join(extractDir, "ft"); path != want {
		t.Errorf("extractBinary() path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading extracted binary: %v", err)
	}
	if got, want := string(data), "new ft binary"; got != want {
		t.Errorf("extracted binary = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("extracted mode = %o, want 700", got)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "LICENSE")); !os.IsNotExist(err) {
		t.Errorf("ignored LICENSE stat error = %v, want not exist", err)
	}
}

func TestExtractBinaryRejectsUnsafeArchiveEntries(t *testing.T) {
	validBinary := archiveEntry{header: tar.Header{Name: "ft", Mode: 0o755, Typeflag: tar.TypeReg}, data: "binary"}
	tests := []struct {
		name    string
		entries []archiveEntry
		wantErr string
	}{
		{
			name: "absolute path",
			entries: []archiveEntry{
				{header: tar.Header{Name: "/tmp/payload", Typeflag: tar.TypeReg}, data: "payload"},
				validBinary,
			},
			wantErr: "unsafe archive path",
		},
		{
			name: "parent path",
			entries: []archiveEntry{
				{header: tar.Header{Name: "../payload", Typeflag: tar.TypeReg}, data: "payload"},
				validBinary,
			},
			wantErr: "unsafe archive path",
		},
		{
			name: "parent directory entry",
			entries: []archiveEntry{
				{header: tar.Header{Name: "..", Typeflag: tar.TypeReg}, data: "payload"},
				validBinary,
			},
			wantErr: "unsafe archive path",
		},
		{
			name: "nested path",
			entries: []archiveEntry{
				{header: tar.Header{Name: "nested/payload", Typeflag: tar.TypeReg}, data: "payload"},
				validBinary,
			},
			wantErr: "unsafe archive path",
		},
		{
			name: "backslash path",
			entries: []archiveEntry{
				{header: tar.Header{Name: `nested\payload`, Typeflag: tar.TypeReg}, data: "payload"},
				validBinary,
			},
			wantErr: "unsafe archive path",
		},
		{
			name: "symbolic link",
			entries: []archiveEntry{
				{header: tar.Header{Name: "payload", Typeflag: tar.TypeSymlink, Linkname: "/tmp/payload"}},
				validBinary,
			},
			wantErr: "unsupported archive entry",
		},
		{
			name: "unsafe entry after binary",
			entries: []archiveEntry{
				validBinary,
				{header: tar.Header{Name: "payload", Typeflag: tar.TypeSymlink, Linkname: "/tmp/payload"}},
			},
			wantErr: "unsupported archive entry",
		},
		{
			name: "hard link",
			entries: []archiveEntry{
				{header: tar.Header{Name: "payload", Typeflag: tar.TypeLink, Linkname: "ft"}},
				validBinary,
			},
			wantErr: "unsupported archive entry",
		},
		{
			name: "directory",
			entries: []archiveEntry{
				{header: tar.Header{Name: "payload", Typeflag: tar.TypeDir}},
				validBinary,
			},
			wantErr: "unsupported archive entry",
		},
		{
			name:    "duplicate ft binary",
			entries: []archiveEntry{validBinary, validBinary},
			wantErr: "duplicate ft binary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "ft.tar.gz")
			writeTestArchive(t, archivePath, tt.entries)
			extractDir := filepath.Join(dir, "extract")
			if err := os.Mkdir(extractDir, 0o700); err != nil {
				t.Fatalf("creating extract directory: %v", err)
			}

			path, err := extractBinary(archivePath, extractDir, 1024)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("extractBinary() = %q, %v; want error containing %q", path, err, tt.wantErr)
			}
			if _, err := os.Stat(filepath.Join(extractDir, "ft")); !os.IsNotExist(err) {
				t.Fatalf("partial binary stat error = %v, want not exist", err)
			}
		})
	}
}

func TestExtractBinaryValidatesInput(t *testing.T) {
	tests := []struct {
		name        string
		archivePath string
		dir         string
		maxBytes    int64
		wantErr     string
	}{
		{name: "empty archive path", dir: t.TempDir(), maxBytes: 1024, wantErr: "empty archive path"},
		{name: "empty extract directory", archivePath: filepath.Join(t.TempDir(), "missing"), maxBytes: 1024, wantErr: "empty extract directory"},
		{name: "zero limit", archivePath: filepath.Join(t.TempDir(), "missing"), dir: t.TempDir(), wantErr: "invalid binary size limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := extractBinary(tt.archivePath, tt.dir, tt.maxBytes)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("extractBinary() = %q, %v; want error containing %q", path, err, tt.wantErr)
			}
		})
	}
}

func TestExtractBinaryRejectsInvalidContent(t *testing.T) {
	tests := []struct {
		name     string
		entries  []archiveEntry
		maxBytes int64
		wantErr  string
	}{
		{
			name:     "missing binary",
			entries:  []archiveEntry{{header: tar.Header{Name: "LICENSE", Typeflag: tar.TypeReg}, data: "license"}},
			maxBytes: 1024,
			wantErr:  "does not contain ft binary",
		},
		{
			name:     "empty binary",
			entries:  []archiveEntry{{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg}}},
			maxBytes: 1024,
			wantErr:  "empty ft binary",
		},
		{
			name:     "oversized binary",
			entries:  []archiveEntry{{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg}, data: "12345"}},
			maxBytes: 4,
			wantErr:  "exceeds 4 bytes",
		},
		{
			name: "oversized ignored entry",
			entries: []archiveEntry{
				{header: tar.Header{Name: "README.md", Typeflag: tar.TypeReg}, data: "12345"},
				{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg}, data: "1"},
			},
			maxBytes: 4,
			wantErr:  "archive content exceeds 4 bytes",
		},
		{
			name: "cumulative expanded content",
			entries: []archiveEntry{
				{header: tar.Header{Name: "LICENSE", Typeflag: tar.TypeReg}, data: "123"},
				{header: tar.Header{Name: "README.md", Typeflag: tar.TypeReg}, data: "456"},
				{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg}, data: "7"},
			},
			maxBytes: 6,
			wantErr:  "archive content exceeds 6 bytes",
		},
		{
			name: "oversized extended metadata",
			entries: []archiveEntry{
				{
					header: tar.Header{
						Name:       "ft",
						Typeflag:   tar.TypeReg,
						PAXRecords: map[string]string{"GOLANG.test": strings.Repeat("x", 80<<10)},
					},
					data: "1",
				},
			},
			maxBytes: 4,
			wantErr:  "expanded data exceeds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "ft.tar.gz")
			writeTestArchive(t, archivePath, tt.entries)
			extractDir := filepath.Join(dir, "extract")
			if err := os.Mkdir(extractDir, 0o700); err != nil {
				t.Fatalf("creating extract directory: %v", err)
			}

			path, err := extractBinary(archivePath, extractDir, tt.maxBytes)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("extractBinary() = %q, %v; want error containing %q", path, err, tt.wantErr)
			}
			if _, err := os.Stat(filepath.Join(extractDir, "ft")); !os.IsNotExist(err) {
				t.Fatalf("partial binary stat error = %v, want not exist", err)
			}
		})
	}
}

func TestExtractBinaryRejectsExcessiveEntryCount(t *testing.T) {
	entries := []archiveEntry{
		{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg}, data: "binary"},
	}
	for i := 0; i < 64; i++ {
		entries = append(entries, archiveEntry{
			header: tar.Header{Name: fmt.Sprintf("file-%d", i), Typeflag: tar.TypeReg},
			data:   "x",
		})
	}

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "ft.tar.gz")
	writeTestArchive(t, archivePath, entries)
	extractDir := filepath.Join(dir, "extract")
	if err := os.Mkdir(extractDir, 0o700); err != nil {
		t.Fatalf("creating extract directory: %v", err)
	}

	path, err := extractBinary(archivePath, extractDir, 1024)
	if err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("extractBinary() = %q, %v; want entry count error", path, err)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "ft")); !os.IsNotExist(err) {
		t.Fatalf("partial binary stat error = %v, want not exist", err)
	}
}

func TestExtractBinaryRejectsCorruptGzip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "ft.tar.gz")
	if err := os.WriteFile(archivePath, []byte("not gzip data"), 0o600); err != nil {
		t.Fatalf("writing corrupt archive: %v", err)
	}

	path, err := extractBinary(archivePath, dir, 1024)
	if err == nil || !strings.Contains(err.Error(), "opening release gzip stream") {
		t.Fatalf("extractBinary() = %q, %v; want gzip error", path, err)
	}
}

func TestExtractBinaryRejectsCorruptTar(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "ft.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("creating corrupt tar archive: %v", err)
	}
	gzipWriter := gzip.NewWriter(file)
	if _, err := gzipWriter.Write([]byte(strings.Repeat("not-tar", 100))); err != nil {
		t.Fatalf("writing corrupt tar data: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("closing corrupt tar archive: %v", err)
	}

	path, err := extractBinary(archivePath, dir, 1024)
	if err == nil || !strings.Contains(err.Error(), "reading release archive") {
		t.Fatalf("extractBinary() = %q, %v; want corrupt tar error", path, err)
	}
}

func TestExtractBinaryPreservesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "ft.tar.gz")
	writeTestArchive(t, archivePath, []archiveEntry{
		{header: tar.Header{Name: "ft", Typeflag: tar.TypeReg}, data: "new binary"},
	})
	extractDir := filepath.Join(dir, "extract")
	if err := os.Mkdir(extractDir, 0o700); err != nil {
		t.Fatalf("creating extract directory: %v", err)
	}
	existingPath := filepath.Join(extractDir, "ft")
	if err := os.WriteFile(existingPath, []byte("existing binary"), 0o700); err != nil {
		t.Fatalf("writing existing destination: %v", err)
	}

	path, err := extractBinary(archivePath, extractDir, 1024)
	if err == nil || !strings.Contains(err.Error(), "creating extracted ft binary") {
		t.Fatalf("extractBinary() = %q, %v; want destination exists error", path, err)
	}
	if path != "" {
		t.Errorf("extractBinary() path = %q, want empty path on failure", path)
	}
	data, readErr := os.ReadFile(existingPath)
	if readErr != nil {
		t.Fatalf("reading existing destination: %v", readErr)
	}
	if got, want := string(data), "existing binary"; got != want {
		t.Errorf("existing destination = %q, want %q", got, want)
	}
}

func writeTestArchive(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating archive: %v", err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := entry.header
		header.Size = int64(len(entry.data))
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatalf("writing tar header: %v", err)
		}
		if _, err := tarWriter.Write([]byte(entry.data)); err != nil {
			t.Fatalf("writing tar data: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("closing tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("closing archive: %v", err)
	}
}
