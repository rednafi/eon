// Package daemon contains daemon-lifecycle helpers.
//
// It provides:
//   - Per-user DataDir resolution.
//   - The flock-based single-instance lock.
//   - Supervisor unit install helpers.
package daemon

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

// DataDir returns the per-user directory for eon state.
//
//   - macOS: ~/Library/Application Support/eon
//   - Linux/other: $XDG_DATA_HOME/eon, falling back to ~/.local/share/eon
//
// It does not create the directory.
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

// AcquireRunLock takes the daemon-lifetime exclusive lock.
//
// Behavior:
//   - It creates $dataDir/eon.lock if needed.
//   - It writes pid and start time into the file.
//   - The release closure unlocks and closes the file.
//   - The OS releases the flock if the process exits.
//
// It returns (nil, nil) if another live daemon holds the lock.
// Callers should then use ProbeRunLock and exit with a conflict.
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
	body := fmt.Sprintf("%d\n%d\n", os.Getpid(), time.Now().UnixNano())
	// We own the lock now. Replace the file contents atomically with
	// our pid + start time so probers always see complete data.
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

// ProbeRunLock reports whether a daemon holds the lock.
//
// If a daemon is running, it returns the recorded pid and start time.
// A successful non-blocking lock means no daemon is running.
// In that case the probe releases the lock immediately.
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
		// We got the lock, so no daemon was holding it. Release.
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
	pidText, startedText, _ := strings.Cut(strings.TrimSpace(string(buf)), "\n")
	if pidText == "" {
		return 0, time.Time{}, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(pidText))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parse pid: %w", err)
	}
	var startedAt time.Time
	startedText = strings.TrimSpace(startedText)
	if startedText != "" {
		ns, err := strconv.ParseInt(startedText, 10, 64)
		if err != nil {
			return 0, time.Time{}, fmt.Errorf("parse start time: %w", err)
		}
		if ns > 0 {
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

// StopDaemon asks the daemon at dataDir to exit.
//
// Behavior:
//   - It returns (false, nil) when no daemon is running.
//   - It sends SIGTERM first.
//   - It polls the lock until timeout.
//   - It escalates to SIGKILL if the daemon still holds the lock.
//
// The gracefully result is true when SIGTERM was enough.
func StopDaemon(dataDir string, timeout time.Duration) (running, gracefully bool, err error) {
	pid, _, running, err := ProbeRunLock(dataDir)
	if err != nil || !running {
		return running, false, err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return true, false, fmt.Errorf("signal SIGTERM to %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, _, alive, err := ProbeRunLock(dataDir)
		if err != nil {
			return true, false, fmt.Errorf("probe daemon: %w", err)
		}
		if !alive {
			return true, true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return true, false, fmt.Errorf("signal SIGKILL to %d: %w", pid, err)
	}
	return true, false, nil
}
