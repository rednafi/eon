package daemon

// Supervisor install: writes the platform's user-level service unit
// (launchd LaunchAgent on macOS, systemd --user unit on Linux) so the
// daemon starts at login and respawns on crash. Both platforms are
// compiled in; runtime.GOOS picks which one runs.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/rednafi/eon"
)

const (
	launchLabel = "dev.eon.eond"   // launchd plist Label (macOS)
	unitName    = "eond.service"   // systemd --user unit filename (linux)
)

// Install writes the platform supervisor unit and starts it. The
// installed return is true when this call actually wrote a new unit;
// false when one was already in place (true no-op). Returns
// [ErrUnsupportedOS] on platforms other than darwin or linux.
func Install(binary, dataDir string) (installed bool, err error) {
	if IsSupervised() {
		return false, nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return false, fmt.Errorf("mkdir data dir: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return true, installLaunchd(binary, dataDir)
	case "linux":
		return true, installSystemd(binary, dataDir)
	default:
		return false, eon.ErrUnsupportedOS
	}
}

// Uninstall removes the platform supervisor unit. The removed return
// is true when this call actually removed a unit; false when none
// was installed (true no-op). Returns [ErrUnsupportedOS] on
// unsupported platforms.
func Uninstall() (removed bool, err error) {
	if !IsSupervised() {
		return false, nil
	}
	switch runtime.GOOS {
	case "darwin":
		return true, uninstallLaunchd()
	case "linux":
		return true, uninstallSystemd()
	default:
		return false, eon.ErrUnsupportedOS
	}
}

// IsSupervised reports whether a supervisor unit is currently
// installed (regardless of running state).
func IsSupervised() bool {
	p, err := unitPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// unitPath returns the on-disk location of the platform unit file.
// Returns "" + ErrUnsupportedOS on platforms we don't handle.
func unitPath() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist"), nil
	case "linux":
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("home dir: %w", err)
			}
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "systemd", "user", unitName), nil
	default:
		return "", eon.ErrUnsupportedOS
	}
}

// launchctlTarget is the user domain selector launchctl wants for
// bootstrap/bootout operations.
func launchctlTarget() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func installLaunchd(binary, dataDir string) error {
	p, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents: %w", err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>%s</string>
    <key>ProgramArguments</key>
    <array>
      <string>%s</string>
      <string>daemon</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>%s</string>
    <key>StandardErrorPath</key><string>%s</string>
  </dict>
</plist>
`, launchLabel, binary, logPath(dataDir), logPath(dataDir))
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	if out, err := exec.Command("launchctl", "bootstrap", launchctlTarget(), p).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, out)
	}
	return nil
}

func uninstallLaunchd() error {
	p, err := unitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_ = exec.Command("launchctl", "bootout", launchctlTarget(), p).Run()
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

func installSystemd(binary, dataDir string) error {
	p, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("mkdir systemd user dir: %w", err)
	}
	unit := fmt.Sprintf(`[Unit]
Description=eon job scheduler daemon
After=default.target

[Service]
Type=simple
ExecStart=%s daemon
Restart=on-failure
RestartSec=2
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, binary, logPath(dataDir), logPath(dataDir))
	if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", unitName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable --now: %w: %s", err, out)
	}
	return nil
}

func uninstallSystemd() error {
	p, err := unitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_ = exec.Command("systemctl", "--user", "disable", "--now", unitName).Run()
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove unit: %w", err)
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}
