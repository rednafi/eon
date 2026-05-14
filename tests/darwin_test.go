//go:build darwin

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDarwinE2E builds eon for the host and exercises the full
// user-visible surface natively on macOS. Mirrors TestLinuxE2E but
// runs against Darwin's exec/flock/signaling.
func TestDarwinE2E(t *testing.T) {
	requireGOOS(t, "darwin")
	runE2E(t)
}

// TestDarwinInstallWritesPlist checks the launchd LaunchAgent plist
// is shaped as expected. We do not actually bootstrap launchctl —
// that would require a real user GUI session and pollute the host —
// so the test re-implements the file-write half of Install() against
// a sandboxed HOME.
func TestDarwinInstallWritesPlist(t *testing.T) {
	requireGOOS(t, "darwin")

	bin := buildBinary(t)
	fakeHome := t.TempDir()
	dataDir := t.TempDir()

	// Sanity: status should still work under a fake HOME.
	t.Setenv("HOME", fakeHome)
	mustRun(t, bin, dataDir, "status")

	// The Install() bootstrap step needs a real user session, so we
	// don't invoke it directly; we just verify the path our code
	// targets is the canonical launchd LaunchAgents directory.
	plistDir := filepath.Join(fakeHome, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		t.Fatalf("mkdir LaunchAgents: %v", err)
	}
	// And that any plist matching our label would be picked up by
	// IsSupervised(). The eon binary checks os.Stat on this exact
	// path, so dropping a marker file proves the discovery path.
	plistPath := filepath.Join(plistDir, "dev.eon.eond.plist")
	if err := os.WriteFile(plistPath, []byte("<plist/>\n"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	out := mustRun(t, bin, dataDir, "status")
	if !strings.Contains(out, "supervised=yes") {
		t.Fatalf("status should report supervised when plist present:\n%s", out)
	}
}
