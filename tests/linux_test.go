//go:build linux

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLinuxE2E builds eon for the host and exercises the full
// user-visible surface natively on Linux, including Linux's
// exec/flock/signaling behavior.
func TestLinuxE2E(t *testing.T) {
	requireGOOS(t, "linux")
	runE2E(t)
}

// TestLinuxInstallWritesUnit verifies the systemd --user unit discovery path.
// We don't invoke systemctl --user daemon-reload because CI may not have a
// user dbus session, so the test asserts the file path IsSupervised relies on.
func TestLinuxInstallWritesUnit(t *testing.T) {
	requireGOOS(t, "linux")

	bin := buildBinary(t)
	fakeXDG := t.TempDir()
	dataDir := t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", fakeXDG)
	mustRun(t, bin, dataDir, "status")

	unitDir := filepath.Join(fakeXDG, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatalf("mkdir systemd dir: %v", err)
	}
	unitPath := filepath.Join(unitDir, "eond.service")
	if err := os.WriteFile(unitPath, []byte("[Unit]\nDescription=test\n"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	out := mustRun(t, bin, dataDir, "status")
	if !strings.Contains(out, "supervised=yes") {
		t.Fatalf("status should report supervised when unit present:\n%s", out)
	}
}
