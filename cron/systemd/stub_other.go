//go:build !linux

// Package systemd contains the Linux systemd-timer cron Source. On
// non-linux builds the package is intentionally empty — main.go's
// factory_<os>.go decides whether to construct systemd, and only the
// linux factory does.
package systemd
