//go:build darwin

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDarwinE2E builds eon for the host and exercises the full
// user-visible surface natively on macOS, including Darwin's
// exec/flock/signaling behavior.
func TestDarwinE2E(t *testing.T) {
	requireGOOS(t, "darwin")
	runE2E(t)
}

// TestDarwinInstallWritesPlist checks launchd discovery without
// bootstrapping launchctl. A real bootstrap would require a user GUI
// session and would pollute the host, so the test writes the expected
// LaunchAgent path under a sandboxed HOME.
func TestDarwinInstallWritesPlist(t *testing.T) {
	requireGOOS(t, "darwin")

	bin := buildBinary(t)
	fakeHome := t.TempDir()
	dataDir := t.TempDir()

	// Sanity: status should still work under a fake HOME.
	t.Setenv("HOME", fakeHome)
	mustRun(t, bin, dataDir, "status")

	// The Install bootstrap step needs a real user session, so verify the
	// canonical launchd LaunchAgents path directly.
	plistDir := filepath.Join(fakeHome, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		t.Fatalf("mkdir LaunchAgents: %v", err)
	}
	// IsSupervised checks this exact path, so a marker file proves discovery.
	plistPath := filepath.Join(plistDir, "dev.eon.eond.plist")
	if err := os.WriteFile(plistPath, []byte("<plist/>\n"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	out := mustRun(t, bin, dataDir, "status")
	if !strings.Contains(out, "supervised=yes") {
		t.Errorf("status output = %q, want substring %q", out, "supervised=yes")
	}
}
