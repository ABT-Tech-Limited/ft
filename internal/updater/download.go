package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func downloadAsset(
	ctx context.Context,
	client *http.Client,
	asset Asset,
	dir string,
	maxBytes int64,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("nil context")
	}
	if client == nil {
		return "", fmt.Errorf("nil HTTP client")
	}
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("empty download directory")
	}
	if asset.Name == "" || filepath.Base(asset.Name) != asset.Name || asset.Name == "." {
		return "", fmt.Errorf("invalid release asset name %q", asset.Name)
	}
	if strings.TrimSpace(asset.URL) == "" {
		return "", fmt.Errorf("empty release asset URL")
	}
	if maxBytes < 1 {
		return "", fmt.Errorf("invalid download limit %d", maxBytes)
	}
	if asset.Size < 0 {
		return "", fmt.Errorf("invalid release asset size %d", asset.Size)
	}
	if asset.Size > maxBytes {
		return "", fmt.Errorf("release asset %q exceeds %d bytes", asset.Name, maxBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", fmt.Errorf("creating download request for %q: %w", asset.Name, err)
	}
	if req.URL.Scheme != "https" {
		return "", fmt.Errorf("release asset URL must use HTTPS")
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "ft-updater")
	resp, err := clientWithHTTPSRedirects(client).Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading release asset %q: %w", asset.Name, err)
	}
	if resp.Request == nil || resp.Request.URL.Scheme != "https" {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return "", fmt.Errorf("release asset redirect must use HTTPS; closing response: %v", closeErr)
		}
		return "", fmt.Errorf("release asset redirect must use HTTPS")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return "", fmt.Errorf("downloading release asset %q: unexpected HTTP status %s; closing response: %v", asset.Name, resp.Status, closeErr)
		}
		return "", fmt.Errorf("downloading release asset %q: unexpected HTTP status %s", asset.Name, resp.Status)
	}
	if resp.ContentLength > maxBytes {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return "", fmt.Errorf("release asset %q exceeds %d bytes; closing response: %v", asset.Name, maxBytes, closeErr)
		}
		return "", fmt.Errorf("release asset %q exceeds %d bytes", asset.Name, maxBytes)
	}

	path := filepath.Join(dir, asset.Name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return "", fmt.Errorf("creating download file for %q: %w; closing response: %v", asset.Name, err, closeErr)
		}
		return "", fmt.Errorf("creating download file for %q: %w", asset.Name, err)
	}

	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxBytes+1))
	bodyCloseErr := resp.Body.Close()
	syncErr := file.Sync()
	fileCloseErr := file.Close()
	if copyErr != nil {
		return "", cleanupDownload(path, fmt.Errorf("writing release asset %q: %w", asset.Name, copyErr))
	}
	if bodyCloseErr != nil {
		return "", cleanupDownload(path, fmt.Errorf("closing release asset %q response: %w", asset.Name, bodyCloseErr))
	}
	if syncErr != nil {
		return "", cleanupDownload(path, fmt.Errorf("syncing release asset %q: %w", asset.Name, syncErr))
	}
	if fileCloseErr != nil {
		return "", cleanupDownload(path, fmt.Errorf("closing release asset %q: %w", asset.Name, fileCloseErr))
	}
	if written > maxBytes {
		return "", cleanupDownload(path, fmt.Errorf("release asset %q exceeds %d bytes", asset.Name, maxBytes))
	}
	if asset.Size > 0 && written != asset.Size {
		return "", cleanupDownload(path, fmt.Errorf("release asset %q size mismatch: downloaded %d bytes, expected %d", asset.Name, written, asset.Size))
	}
	return path, nil
}

func cleanupDownload(path string, cause error) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, fmt.Errorf("removing partial download %q: %w", path, err))
	}
	return cause
}

func verifyChecksum(archivePath, checksumPath string) error {
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return fmt.Errorf("reading release checksums: %w", err)
	}
	archiveName := filepath.Base(archivePath)
	var expected []byte
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archiveName {
			if expected != nil {
				return fmt.Errorf("duplicate checksum for %q", archiveName)
			}
			expected, err = hex.DecodeString(fields[0])
			if err != nil {
				return fmt.Errorf("parsing checksum for %q: %w", archiveName, err)
			}
			if len(expected) != sha256.Size {
				return fmt.Errorf("checksum for %q must be a SHA-256 digest", archiveName)
			}
		}
	}
	if expected == nil {
		return fmt.Errorf("checksum for %q not found", archiveName)
	}

	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening release archive for verification: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, archive)
	closeErr := archive.Close()
	if copyErr != nil {
		return fmt.Errorf("hashing release archive: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing release archive after verification: %w", closeErr)
	}
	if !bytes.Equal(hash.Sum(nil), expected) {
		return fmt.Errorf("checksum mismatch for %q", archiveName)
	}
	return nil
}
