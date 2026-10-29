package nginxx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ABT-Tech-Limited/ft/internal/cloudflarex"
	"golang.org/x/sys/unix"
)

// Manager applies validated Nginx configuration as a recoverable transaction.
// Root is normally empty; tests can isolate every filesystem operation beneath
// another directory. Run must execute commands against that same environment.
type Manager struct {
	Root       string
	Executable string
	Fetch      cloudflarex.FetchFunc
	Run        func(context.Context, []string) error
}

type storedFile struct {
	path   string
	data   []byte
	mode   fs.FileMode
	exists bool
}

type fileChange struct {
	before storedFile
	after  []byte
}

// Configure optimizes a recognized default configuration and installs the
// Cloudflare refresh job only after Nginx has accepted the configuration.
// A true result with an error means configuration succeeded but cron setup did
// not: callers must report that partial outcome rather than claiming success.
func (m Manager) Configure(ctx context.Context) (changed bool, err error) {
	if err := m.validate(ctx); err != nil {
		return false, err
	}
	cron, err := m.cronContent()
	if err != nil {
		return false, err
	}
	if err := m.trustedExecutable(); err != nil {
		return false, fmt.Errorf("refusing unsafe root cron executable: %w; install ft as a root-owned executable at /usr/local/bin/ft and rerun", err)
	}
	unlock, err := m.lock(ctx)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	main, err := m.read(MainPath)
	if err != nil {
		return false, err
	}
	if !main.exists {
		return false, fmt.Errorf("Nginx configuration %s does not exist; install nginx first", MainPath)
	}
	optimized, err := OptimizeMain(main.data)
	if err != nil {
		return false, err
	}
	http, err := m.managedAsset(HTTPPath, HTTPConfig(), false)
	if err != nil {
		return false, err
	}
	proxy, err := m.managedAsset(ProxyPath, ProxySnippet(), false)
	if err != nil {
		return false, err
	}
	active, err := m.active(false)
	if err != nil {
		return false, err
	}
	cronFile, err := m.read(CronPath)
	if err != nil {
		return false, err
	}
	if cronFile.exists && !recognizedCron(cronFile.data) {
		return false, fmt.Errorf("refusing to overwrite unmanaged cron file %s", CronPath)
	}
	manifest, err := m.fetchSnapshot(ctx)
	if err != nil {
		return false, err
	}
	guards := []storedFile{main, http, proxy, active, cronFile}
	changes := differingChanges([]fileChange{
		{before: active, after: manifest},
		{before: http, after: HTTPConfig()},
		{before: proxy, after: ProxySnippet()},
		{before: main, after: optimized},
	})
	if err := m.apply(ctx, guards, changes, true); err != nil {
		return false, err
	}
	changed = len(changes) > 0

	if err := m.Run(ctx, []string{"systemctl", "enable", "--now", "cron.service"}); err != nil {
		return changed, fmt.Errorf("Nginx configuration is applied, but enabling the 10-minute refresh service failed: %w", err)
	}
	if !cronFile.exists || !bytes.Equal(cronFile.data, cron) {
		if err := m.matches(cronFile); err != nil {
			return changed, fmt.Errorf("Nginx configuration is applied, but cron registration failed: %w", err)
		}
		if err := m.backup([]fileChange{{before: cronFile, after: cron}}); err != nil {
			return changed, fmt.Errorf("Nginx configuration is applied, but cron backup failed: %w", err)
		}
		if err := m.write(CronPath, cron, 0644); err != nil {
			return changed, fmt.Errorf("Nginx configuration is applied, but cron registration failed: %w", err)
		}
		changed = true
	}
	return changed, nil
}

// Sync refreshes only Cloudflare's managed snapshot. It never installs
// packages, registers cron, rewrites sites, or reloads an unchanged snapshot.
func (m Manager) Sync(ctx context.Context) (changed bool, err error) {
	if err := m.validate(ctx); err != nil {
		return false, err
	}
	unlock, err := m.lock(ctx)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	main, err := m.read(MainPath)
	if err != nil {
		return false, err
	}
	optimized, err := OptimizeMain(main.data)
	if err != nil || !main.exists || !bytes.Equal(optimized, main.data) {
		return false, fmt.Errorf("Nginx is not using the recognized ft-managed configuration; run ft install nginx first")
	}
	http, err := m.managedAsset(HTTPPath, HTTPConfig(), true)
	if err != nil {
		return false, err
	}
	proxy, err := m.managedAsset(ProxyPath, ProxySnippet(), true)
	if err != nil {
		return false, err
	}
	active, err := m.active(true)
	if err != nil {
		return false, err
	}
	manifest, err := m.fetchSnapshot(ctx)
	if err != nil {
		return false, err
	}
	guards := []storedFile{main, http, proxy, active}
	changes := differingChanges([]fileChange{{before: active, after: manifest}})
	if err := m.apply(ctx, guards, changes, false); err != nil {
		return false, err
	}
	return len(changes) > 0, nil
}

func (m Manager) validate(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nil Nginx command context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.Fetch == nil || m.Run == nil {
		return fmt.Errorf("Nginx manager requires fetch and command dependencies")
	}
	if m.Root != "" && !filepath.IsAbs(m.Root) {
		return fmt.Errorf("Nginx filesystem root must be absolute")
	}
	return nil
}

func (m Manager) local(path string) string {
	root := m.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, strings.TrimPrefix(path, "/"))
}

// directory checks each component rather than following a pre-existing
// symlink into a user-controlled location during privileged writes.
func (m Manager) directory(path string, create bool) error {
	current := m.Root
	if current == "" {
		current = "/"
	}
	for _, component := range strings.Split(strings.Trim(path, "/"), "/") {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := os.Mkdir(current, 0755); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("creating %s: %w", current, err)
			}
			info, err = os.Lstat(current)
		}
		if errors.Is(err, os.ErrNotExist) && !create {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", current, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing non-directory or symlink component %s", current)
		}
		if err := m.trustedPermissions(info, current); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) trustedPermissions(info os.FileInfo, path string) error {
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("refusing group- or world-writable Nginx path %s", path)
	}
	if m.Root == "" || filepath.Clean(m.Root) == "/" {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return fmt.Errorf("refusing Nginx path not owned by root: %s", path)
		}
	}
	return nil
}

// A cron command must not point into a user's writable checkout or home. This
// deliberately examines metadata only: release binaries exceed config limits.
func (m Manager) trustedExecutable() error {
	if err := m.directory(filepath.Dir(m.Executable), false); err != nil {
		return err
	}
	info, err := os.Lstat(m.local(m.Executable))
	if err != nil {
		return fmt.Errorf("inspecting ft executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("ft must be a regular executable, not a symlink: %s", m.Executable)
	}
	return m.trustedPermissions(info, m.Executable)
}

func (m Manager) read(path string) (storedFile, error) {
	result := storedFile{path: path, mode: 0644}
	if err := m.directory(filepath.Dir(path), false); err != nil {
		return result, err
	}
	info, err := os.Lstat(m.local(path))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return result, fmt.Errorf("refusing non-regular or oversized configuration file %s", path)
	}
	if err := m.trustedPermissions(info, path); err != nil {
		return result, err
	}
	file, err := os.Open(m.local(path))
	if err != nil {
		return result, fmt.Errorf("opening %s: %w", path, err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		return result, errors.Join(fmt.Errorf("configuration changed while opening %s", path), statErr, file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return result, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) > 4<<20 {
		return result, fmt.Errorf("configuration file %s is too large", path)
	}
	result.data, result.mode, result.exists = data, info.Mode().Perm(), true
	return result, nil
}

func (m Manager) managedAsset(path string, desired []byte, required bool) (storedFile, error) {
	file, err := m.read(path)
	if err != nil {
		return file, err
	}
	if required && !file.exists {
		return file, fmt.Errorf("managed configuration %s is missing; run ft install nginx first", path)
	}
	if file.exists && !bytes.Equal(file.data, desired) {
		return file, fmt.Errorf("refusing to overwrite customized or unmanaged configuration %s", path)
	}
	return file, nil
}

func generationID(realIP, geo []byte) string {
	content := make([]byte, 0, len(realIP)+1+len(geo))
	content = append(content, realIP...)
	content = append(content, 0)
	content = append(content, geo...)
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func snapshotManifest(id string) []byte {
	dir := ManagedDir + "/cloudflare-" + id
	return fmt.Appendf(nil, "%s\ninclude %s/cloudflare-realip.conf;\ngeo $realip_remote_addr $ft_from_cloudflare {\n    default 0;\n    include %s/cloudflare-geo.conf;\n}\n", ManagedHeader, dir, dir)
}

func (m Manager) active(required bool) (storedFile, error) {
	file, err := m.read(ActivePath)
	if err != nil {
		return file, err
	}
	if !file.exists {
		if required {
			return file, fmt.Errorf("Cloudflare configuration is not initialized; run ft install nginx first")
		}
		return file, nil
	}
	prefix := ManagedHeader + "\ninclude " + ManagedDir + "/cloudflare-"
	if !bytes.HasPrefix(file.data, []byte(prefix)) || len(file.data) < len(prefix)+64 {
		return file, fmt.Errorf("refusing unmanaged Cloudflare manifest %s", ActivePath)
	}
	id := string(file.data[len(prefix) : len(prefix)+64])
	if strings.IndexFunc(id, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) >= 0 || !bytes.Equal(file.data, snapshotManifest(id)) {
		return file, fmt.Errorf("refusing customized Cloudflare manifest %s", ActivePath)
	}
	realIP, err := m.read(ManagedDir + "/cloudflare-" + id + "/cloudflare-realip.conf")
	if err != nil {
		return file, err
	}
	geo, err := m.read(ManagedDir + "/cloudflare-" + id + "/cloudflare-geo.conf")
	if err != nil {
		return file, err
	}
	if !realIP.exists || !geo.exists || generationID(realIP.data, geo.data) != id {
		return file, fmt.Errorf("Cloudflare snapshot is missing or has been modified; refusing automatic refresh")
	}
	return file, nil
}

func (m Manager) fetchSnapshot(ctx context.Context) ([]byte, error) {
	ranges, err := cloudflarex.Ranges(ctx, m.Fetch)
	if err != nil {
		return nil, err
	}
	realIP, geo, err := CloudflareFiles(ranges)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := generationID(realIP, geo)
	dir := ManagedDir + "/cloudflare-" + id
	for name, data := range map[string][]byte{"cloudflare-realip.conf": realIP, "cloudflare-geo.conf": geo} {
		path := dir + "/" + name
		stored, err := m.read(path)
		if err != nil {
			return nil, err
		}
		if stored.exists {
			if !bytes.Equal(stored.data, data) {
				return nil, fmt.Errorf("refusing to overwrite modified immutable snapshot %s", path)
			}
			continue
		}
		if err := m.write(path, data, 0644); err != nil {
			return nil, err
		}
	}
	return snapshotManifest(id), nil
}

func differingChanges(changes []fileChange) []fileChange {
	var result []fileChange
	for _, change := range changes {
		if !change.before.exists || !bytes.Equal(change.before.data, change.after) {
			result = append(result, change)
		}
	}
	return result
}

func (m Manager) matches(before storedFile) error {
	current, err := m.read(before.path)
	if err != nil {
		return err
	}
	if current.exists != before.exists || current.mode != before.mode || !bytes.Equal(current.data, before.data) {
		return fmt.Errorf("configuration %s changed concurrently; refusing to overwrite it", before.path)
	}
	return nil
}

func (m Manager) apply(ctx context.Context, guards []storedFile, changes []fileChange, validateUnchanged bool) error {
	for _, guard := range guards {
		if err := m.matches(guard); err != nil {
			return err
		}
	}
	if len(changes) == 0 {
		if validateUnchanged {
			if err := m.Run(ctx, []string{"nginx", "-t"}); err != nil {
				return fmt.Errorf("validating Nginx configuration failed: %w", err)
			}
		}
		return nil
	}
	if err := m.backup(changes); err != nil {
		return err
	}
	var applied []fileChange
	for _, change := range changes {
		if err := errors.Join(ctx.Err(), m.matches(change.before)); err != nil {
			return errors.Join(err, m.restore(applied))
		}
		if err := m.write(change.before.path, change.after, change.before.mode); err != nil {
			return errors.Join(err, m.restore(applied))
		}
		applied = append(applied, change)
	}
	if err := m.Run(ctx, []string{"nginx", "-t"}); err != nil {
		return errors.Join(fmt.Errorf("validating Nginx configuration failed: %w", err), m.restore(applied))
	}
	for _, guard := range guards {
		for _, change := range changes {
			if change.before.path == guard.path {
				guard.exists, guard.data = true, change.after
			}
		}
		if err := m.matches(guard); err != nil {
			return errors.Join(err, m.restore(applied))
		}
	}
	if err := m.Run(ctx, []string{"systemctl", "reload", "nginx.service"}); err != nil {
		restoreErr := m.restore(applied)
		var reloadErr error
		if restoreErr == nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			if checkErr := m.Run(recovery, []string{"nginx", "-t"}); checkErr != nil {
				reloadErr = fmt.Errorf("validating restored configuration failed: %w", checkErr)
			} else if oldErr := m.Run(recovery, []string{"systemctl", "reload", "nginx.service"}); oldErr != nil {
				reloadErr = fmt.Errorf("reloading restored configuration failed: %w", oldErr)
			}
		}
		return errors.Join(fmt.Errorf("reloading Nginx failed; restoring previous configuration: %w", err), restoreErr, reloadErr)
	}
	return nil
}

func (m Manager) restore(changes []fileChange) error {
	var result error
	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		want := storedFile{path: change.before.path, data: change.after, mode: change.before.mode, exists: true}
		if err := m.matches(want); err != nil {
			result = errors.Join(result, fmt.Errorf("rollback preserved concurrent edit: %w", err))
			continue
		}
		var err error
		if change.before.exists {
			err = m.write(change.before.path, change.before.data, change.before.mode)
		} else {
			err = os.Remove(m.local(change.before.path))
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("restoring %s: %w", change.before.path, err))
		}
	}
	return result
}

func (m Manager) backup(changes []fileChange) error {
	var directory string
	for _, change := range changes {
		if !change.before.exists {
			continue
		}
		if directory == "" {
			if err := m.directory(ManagedDir, true); err != nil {
				return err
			}
			var err error
			directory, err = os.MkdirTemp(m.local(ManagedDir), "backup-")
			if err != nil {
				return fmt.Errorf("creating Nginx backup: %w", err)
			}
		}
		path := filepath.Join(directory, filepath.Base(change.before.path))
		if err := os.WriteFile(path, change.before.data, 0600); err != nil {
			return fmt.Errorf("backing up %s: %w", change.before.path, err)
		}
	}
	return nil
}

func (m Manager) write(path string, data []byte, mode fs.FileMode) (err error) {
	if err := m.directory(filepath.Dir(path), true); err != nil {
		return err
	}
	// Reading first also rejects a symlink or non-regular destination.
	if _, err := m.read(path); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(m.local(path)), ".ft-tmp-")
	if err != nil {
		return fmt.Errorf("staging %s: %w", path, err)
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("removing staged configuration: %w", removeErr))
		}
	}()
	_, writeErr := file.Write(data)
	chmodErr := file.Chmod(mode)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("staging %s: %w", path, err)
	}
	if err := os.Rename(file.Name(), m.local(path)); err != nil {
		return fmt.Errorf("activating %s: %w", path, err)
	}
	return nil
}

func (m Manager) cronContent() ([]byte, error) {
	if !filepath.IsAbs(m.Executable) || strings.IndexFunc(m.Executable, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '%' }) >= 0 {
		return nil, fmt.Errorf("ft executable must be an absolute cron-safe path")
	}
	quoted := "'" + strings.ReplaceAll(m.Executable, "'", "'\\''") + "'"
	return fmt.Appendf(nil, "%s\n# Refresh Cloudflare trust every 10 minutes; remove this file to stop.\nSHELL=/bin/sh\nPATH=/usr/sbin:/usr/bin:/sbin:/bin\n*/10 * * * * root %s nginx cloudflare sync >> /var/log/ft-nginx-cloudflare.log 2>&1\n", ManagedHeader, quoted), nil
}

func recognizedCron(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	if len(lines) != 6 {
		return false
	}
	command, ok := strings.CutPrefix(lines[4], "*/10 * * * * root '")
	if !ok {
		return false
	}
	quoted, ok := strings.CutSuffix(command, "' nginx cloudflare sync >> /var/log/ft-nginx-cloudflare.log 2>&1")
	if !ok {
		return false
	}
	executable := strings.ReplaceAll(quoted, "'\\''", "'")
	expected, err := (Manager{Executable: executable}).cronContent()
	return err == nil && bytes.Equal(content, expected)
}

func (m Manager) lock(ctx context.Context) (func() error, error) {
	if err := m.directory(ManagedDir, true); err != nil {
		return nil, err
	}
	path := m.local(ManagedDir + "/.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening Nginx configuration lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("invalid Nginx lock file"), err, file.Close())
	}
	if err := m.trustedPermissions(info, path); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() error { return errors.Join(unix.Flock(fd, unix.LOCK_UN), file.Close()) }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return nil, errors.Join(fmt.Errorf("locking Nginx configuration: %w", err), file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
