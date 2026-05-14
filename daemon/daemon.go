package daemon

// daemon-lifecycle helpers: per-user [DataDir] resolution and the
// flock-based single-instance lock that the running daemon holds for
// its lifetime. Supervisor unit install lives in [install.go].

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DataDir is the per-user directory eon stores its database and
// lock file in:
//
//   - macOS: ~/Library/Application Support/eon
//   - Linux/other: $XDG_DATA_HOME/eon, falling back to ~/.local/share/eon
//
// Not created here; callers that need it should call os.MkdirAll.
func DataDir() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "eon"), nil
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "eon"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "eon"), nil
}

func lockPath(dataDir string) string { return filepath.Join(dataDir, "eon.lock") }
func logPath(dataDir string) string  { return filepath.Join(dataDir, "eon.log") }

// AcquireRunLock takes the daemon-lifetime exclusive flock on
// $dataDir/eon.lock and writes "<pid>\n<unix_nano_started_at>\n"
// into the file. The release closure unlocks and closes the fd;
// callers typically `defer release()`. On any process exit (graceful
// or crash) the OS releases the flock automatically, so missing the
// defer is not catastrophic.
//
// Returns (nil, nil) if another live daemon already holds the lock;
// the caller should then read the existing pid with [ProbeRunLock]
// and exit with a conflict.
func AcquireRunLock(dataDir string) (release func(), err error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}
	f, err := os.OpenFile(lockPath(dataDir), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, fmt.Errorf("flock: %w", err)
	}
	// We own the lock now. Replace the file contents atomically with
	// our pid + start time so probers always see complete data.
	body := fmt.Sprintf("%d\n%d\n", os.Getpid(), time.Now().UnixNano())
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("truncate lock: %w", err)
	}
	if _, err := f.WriteAt([]byte(body), 0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("write lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// ProbeRunLock reports whether a daemon currently holds the lock and,
// if so, the pid and start time it recorded. The probe attempts a
// non-blocking exclusive lock — if it succeeds, no daemon is running
// (the lock is released immediately).
func ProbeRunLock(dataDir string) (pid int, startedAt time.Time, running bool, err error) {
	f, err := os.OpenFile(lockPath(dataDir), os.O_RDWR, 0o644)
	if errors.Is(err, os.ErrNotExist) {
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, fmt.Errorf("open lock: %w", err)
	}
	defer f.Close()

	if lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr == nil {
		// We got the lock → no daemon was holding it. Release.
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0, time.Time{}, false, nil
	}

	pid, startedAt, err = readLockContents(f)
	if err != nil {
		return 0, time.Time{}, true, err
	}
	return pid, startedAt, true, nil
}

func readLockContents(f *os.File) (int, time.Time, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, time.Time{}, err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return 0, time.Time{}, err
	}
	lines := strings.SplitN(strings.TrimSpace(string(buf)), "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		return 0, time.Time{}, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parse pid: %w", err)
	}
	var startedAt time.Time
	if len(lines) > 1 {
		if ns, _ := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); ns > 0 {
			startedAt = time.Unix(0, ns).UTC()
		}
	}
	return pid, startedAt, nil
}

// SignalDaemon sends sig to the running daemon, if any. Returns
// (false, nil) when no daemon is running.
func SignalDaemon(dataDir string, sig syscall.Signal) (sent bool, err error) {
	pid, _, running, err := ProbeRunLock(dataDir)
	if err != nil || !running || pid <= 0 {
		return false, err
	}
	if err := syscall.Kill(pid, sig); err != nil {
		return false, fmt.Errorf("signal %d to %d: %w", sig, pid, err)
	}
	return true, nil
}

// StopDaemon asks the daemon at dataDir to exit. Returns (false, nil)
// if no daemon was running. Otherwise sends SIGTERM and polls the
// flock; if the daemon hasn't released within timeout, escalates to
// SIGKILL. The returned gracefully bool reports whether the daemon
// exited under SIGTERM (true) or had to be killed (false).
func StopDaemon(dataDir string, timeout time.Duration) (running, gracefully bool, err error) {
	pid, _, running, err := ProbeRunLock(dataDir)
	if err != nil || !running {
		return running, false, err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return true, false, fmt.Errorf("SIGTERM %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, _, alive, _ := ProbeRunLock(dataDir); !alive {
			return true, true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return true, false, nil
}
