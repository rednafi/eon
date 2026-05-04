//go:build !darwin

// Package launchd contains the macOS launchd cron Source. On non-darwin
// builds the package is intentionally empty so importers don't break, but
// callers should never reach this code path — main.go's factory_<os>.go is
// build-tagged so only the matching platform constructs launchd.
package launchd
