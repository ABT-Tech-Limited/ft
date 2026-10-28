package codexx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Protect commands launched inside the target server even when the official
// daemon stop would succeed; recovery-only checks would run too late.
func checkReloadOrigin(ctx context.Context, socket string) error {
	peer, err := socketPeer(ctx, socket)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ECONNREFUSED) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check reload origin: %w", err)
	}
	ancestor, err := processIsAncestor(int(peer.Pid))
	if err != nil {
		return err
	}
	if ancestor || int(peer.Pid) == os.Getpid() {
		return fmt.Errorf("app-server PID %d owns this command's execution; run 'ft codex reload' from a separate SSH terminal", peer.Pid)
	}
	return nil
}

func terminateSocketServer(ctx context.Context, socket string, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	peer, err := socketPeer(ctx, socket)
	if err != nil {
		return fmt.Errorf("identify app-server listening on %q: %w", socket, err)
	}
	pid := int(peer.Pid)
	if pid <= 1 || peer.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("refusing socket owner PID %d UID %d; expected a non-system process owned by UID %d", pid, peer.Uid, os.Geteuid())
	}
	ancestor, err := processIsAncestor(pid)
	if err != nil {
		return err
	}
	if ancestor {
		return fmt.Errorf("app-server PID %d is this command's ancestor; run 'ft codex reload' from a separate SSH terminal", pid)
	}

	// Bind the signal and exit wait to this process, not a reusable numeric PID.
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return fmt.Errorf("open pidfd for app-server %d (Linux 5.3+ required); refusing an unsafe PID-only kill: %w", pid, err)
	}
	defer unix.Close(fd)
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return fmt.Errorf("verify app-server executable: %w", err)
	}
	command, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return fmt.Errorf("verify app-server arguments: %w", err)
	}
	argv := strings.Split(strings.TrimSuffix(string(command), "\x00"), "\x00")
	if err := validateDirectServer(pid, int(peer.Uid), os.Geteuid(), executable, argv); err != nil {
		return err
	}
	// Reconnect via the original path, resolving any symlink again. A competing
	// client must not redirect recovery to a different server after inspection.
	current, err := socketPeer(ctx, socket)
	if err != nil {
		return fmt.Errorf("recheck control socket owner: %w", err)
	}
	if *current != *peer {
		return fmt.Errorf("control socket owner changed during recovery; refusing to signal PID %d", pid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0); err != nil {
		return fmt.Errorf("send SIGTERM to verified app-server PID %d: %w", pid, err)
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "Sent SIGTERM to verified Codex app-server PID %d; waiting for exit...\n", pid)
	}
	if err := waitForPidfd(ctx, fd); err != nil {
		return fmt.Errorf("waiting for app-server PID %d to exit after SIGTERM; no SIGKILL was sent: %w", pid, err)
	}
	return nil
}

func socketPeer(ctx context.Context, socket string) (*unix.Ucred, error) {
	path, err := filepath.EvalSymlinks(socket)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return nil, err
	}
	var peer *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		peer, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return nil, err
	}
	return peer, socketErr
}

func waitForPidfd(ctx context.Context, fd int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, 50); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return err
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0 {
			return nil
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return fmt.Errorf("invalid process handle while waiting for exit")
		}
	}
}

func validateDirectServer(pid, uid, currentUID int, executable string, argv []string) error {
	if pid <= 1 || uid != currentUID {
		return fmt.Errorf("refusing app-server PID %d UID %d; expected current UID %d", pid, uid, currentUID)
	}
	if filepath.Base(strings.TrimSuffix(executable, " (deleted)")) != "codex" || !directServerArgs(argv) {
		return fmt.Errorf("socket owner PID %d is not a verified direct 'codex app-server --listen unix://' process; stop its owning client/service manually", pid)
	}
	return nil
}

func directServerArgs(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	args := argv[1:]
	for len(args) > 0 && args[0] != "app-server" {
		switch args[0] {
		case "-c", "--config", "--enable", "--disable":
			if len(args) < 2 {
				return false
			}
			args = args[2:]
		case "--strict-config":
			args = args[1:]
		default:
			if !strings.HasPrefix(args[0], "--config=") && !strings.HasPrefix(args[0], "--enable=") && !strings.HasPrefix(args[0], "--disable=") {
				return false
			}
			args = args[1:]
		}
	}
	if len(args) == 0 {
		return false
	}
	args = args[1:]
	listen := ""
	for len(args) > 0 {
		switch args[0] {
		case "--listen", "--code-mode-host":
			if len(args) < 2 {
				return false
			}
			if args[0] == "--listen" {
				if listen != "" {
					return false
				}
				listen = args[1]
			}
			args = args[2:]
		case "--remote-control", "--analytics-default-enabled":
			args = args[1:]
		default:
			if !strings.HasPrefix(args[0], "--listen=") || listen != "" {
				return false
			}
			listen = strings.TrimPrefix(args[0], "--listen=")
			args = args[1:]
		}
	}
	return strings.HasPrefix(listen, "unix://")
}

func processIsAncestor(target int) (bool, error) {
	pid := os.Getpid()
	seen := make(map[int]bool)
	for pid > 1 {
		if pid == target {
			return true, nil
		}
		if seen[pid] {
			return false, fmt.Errorf("cycle in process ancestry at PID %d", pid)
		}
		seen[pid] = true
		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			return false, fmt.Errorf("verify process ancestry: %w", err)
		}
		parent := -1
		for _, line := range strings.Split(string(status), "\n") {
			if value, ok := strings.CutPrefix(line, "PPid:"); ok {
				parent, err = strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					return false, fmt.Errorf("parse process ancestry: %w", err)
				}
				break
			}
		}
		if parent < 0 {
			return false, fmt.Errorf("missing parent for PID %d", pid)
		}
		pid = parent
	}
	return false, nil
}
