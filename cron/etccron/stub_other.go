//go:build !linux

// Package etccron contains the /etc/crontab + /etc/cron.d Source. On
// non-linux builds the package is intentionally empty — only the linux
// factory in main constructs it.
package etccron
