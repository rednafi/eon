package daemon

import (
	"os"
	"syscall"
	"testing"
)

func TestDataDir(t *testing.T) {
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if dir == "" {
		t.Errorf("DataDir() = %q, want non-empty", dir)
	}
}

func TestAcquireRunLockHappyPath(t *testing.T) {
	dir := t.TempDir()

	release, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	if release == nil {
		t.Fatalf("AcquireRunLock returned nil release")
	}
	t.Cleanup(release)

	pid, _, running, err := ProbeRunLock(dir)
	if err != nil {
		t.Fatalf("ProbeRunLock: %v", err)
	}
	if !running {
		t.Fatalf("ProbeRunLock saw no holder")
	}
	if pid != os.Getpid() {
		t.Errorf("ProbeRunLock pid = %d, want %d", pid, os.Getpid())
	}
}

func TestAcquireRunLockConflict(t *testing.T) {
	dir := t.TempDir()

	first, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	t.Cleanup(first)

	second, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("second acquire error: %v", err)
	}
	if second != nil {
		t.Errorf("second AcquireRunLock returned release, want nil")
	}
}

func TestProbeRunLockEmpty(t *testing.T) {
	dir := t.TempDir()
	pid, _, running, err := ProbeRunLock(dir)
	if err != nil {
		t.Fatalf("ProbeRunLock empty: %v", err)
	}
	if running || pid != 0 {
		t.Errorf("ProbeRunLock empty = running %v, pid %d; want false, 0", running, pid)
	}
}

func TestProbeRunLockAfterRelease(t *testing.T) {
	dir := t.TempDir()
	release, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	release()

	_, _, running, err := ProbeRunLock(dir)
	if err != nil {
		t.Fatalf("ProbeRunLock after release: %v", err)
	}
	if running {
		t.Errorf("ProbeRunLock after release running = %v, want false", running)
	}
}

func TestSignalDaemonNoDaemon(t *testing.T) {
	dir := t.TempDir()
	sent, err := SignalDaemon(dir, syscall.SIGHUP)
	if err != nil {
		t.Fatalf("SignalDaemon: %v", err)
	}
	if sent {
		t.Errorf("SignalDaemon sent = %v, want false", sent)
	}
}
