package updater

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

const (
	maxArchiveEntries             = 64
	maxArchiveStreamOverheadBytes = int64(maxArchiveEntries+2) * 1024
)

func extractBinary(archivePath, dir string, maxBytes int64) (path string, resultErr error) {
	if strings.TrimSpace(archivePath) == "" {
		return "", fmt.Errorf("empty archive path")
	}
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("empty extract directory")
	}
	if maxBytes < 1 {
		return "", fmt.Errorf("invalid binary size limit %d", maxBytes)
	}

	var createdPath string
	defer func() {
		if resultErr == nil || createdPath == "" {
			return
		}
		if err := os.Remove(createdPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("removing partial extracted binary %q: %w", createdPath, err))
		}
		path = ""
	}()

	archive, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("opening release archive: %w", err)
	}
	defer func() {
		if err := archive.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("closing release archive: %w", err))
		}
	}()

	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("opening release gzip stream: %w", err)
	}
	defer func() {
		if err := gzipReader.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("closing release gzip stream: %w", err))
		}
	}()

	if maxBytes > (1<<63-1)-maxArchiveStreamOverheadBytes-1 {
		return "", fmt.Errorf("invalid binary size limit %d", maxBytes)
	}
	streamLimit := maxBytes + maxArchiveStreamOverheadBytes
	expandedStream := &io.LimitedReader{R: gzipReader, N: streamLimit + 1}
	tarReader := tar.NewReader(expandedStream)
	entryCount := 0
	var contentBytes int64
	for {
		header, err := tarReader.Next()
		if expandedStream.N == 0 {
			return "", fmt.Errorf("release archive expanded data exceeds %d bytes", streamLimit)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading release archive: %w", err)
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return "", fmt.Errorf("release archive contains too many entries (maximum %d)", maxArchiveEntries)
		}
		if header.Name == "" || header.Name == "." || header.Name == ".." || pathpkg.IsAbs(header.Name) || pathpkg.Clean(header.Name) != header.Name || strings.ContainsAny(header.Name, `/\`) {
			return "", fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != 0 {
			return "", fmt.Errorf("unsupported archive entry %q with type %d", header.Name, header.Typeflag)
		}
		if header.Size < 0 {
			return "", fmt.Errorf("archive entry %q has invalid size %d", header.Name, header.Size)
		}
		if header.Name == "ft" && header.Size > maxBytes {
			return "", fmt.Errorf("ft binary exceeds %d bytes", maxBytes)
		}
		if header.Size > maxBytes-contentBytes {
			return "", fmt.Errorf("release archive content exceeds %d bytes", maxBytes)
		}
		contentBytes += header.Size
		if header.Name != "ft" {
			continue
		}
		if createdPath != "" {
			return "", fmt.Errorf("release archive contains duplicate ft binary")
		}
		if header.Size == 0 {
			return "", fmt.Errorf("release archive contains empty ft binary")
		}

		binaryPath := filepath.Join(dir, "ft")
		file, err := os.OpenFile(binaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return "", fmt.Errorf("creating extracted ft binary: %w", err)
		}
		path = binaryPath
		createdPath = binaryPath
		written, copyErr := io.Copy(file, io.LimitReader(tarReader, maxBytes+1))
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil {
			return "", cleanupDownload(path, fmt.Errorf("extracting ft binary: %w", copyErr))
		}
		if syncErr != nil {
			return "", cleanupDownload(path, fmt.Errorf("syncing extracted ft binary: %w", syncErr))
		}
		if closeErr != nil {
			return "", cleanupDownload(path, fmt.Errorf("closing extracted ft binary: %w", closeErr))
		}
		if written > maxBytes {
			return "", cleanupDownload(path, fmt.Errorf("ft binary exceeds %d bytes", maxBytes))
		}
	}
	if path == "" {
		return "", fmt.Errorf("release archive does not contain ft binary")
	}
	return path, nil
}
