//go:build darwin

package main

import (
	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/cron/crontab"
	"github.com/rednafi/eon/cron/launchd"
)

// systemLaunchdDirs lists the read-only directories macOS installs
// background jobs into. The tag flows into the Job ID
// ("launchd-<tag>:<label>"), so each must be unique.
var systemLaunchdDirs = []struct{ tag, dir string }{
	{"system", "/Library/LaunchAgents"},
	{"daemons", "/Library/LaunchDaemons"},
	{"apple-agents", "/System/Library/LaunchAgents"},
	{"apple-daemons", "/System/Library/LaunchDaemons"},
}

// platformSources is the composition root for macOS: user crontab plus the
// user's LaunchAgents (writable) and a snapshot of every system Launch*
// directory (read-only). Each Source is a pure cron.Source — main wires them
// into a cron.Manager, never naming the concrete types.
func platformSources() ([]cron.Source, []error) {
	var (
		sources []cron.Source
		errs    []error
	)
	sources = append(sources, crontab.New())
	if l, err := launchd.NewUser(); err == nil {
		sources = append(sources, l)
	} else {
		errs = append(errs, err)
	}
	for _, e := range systemLaunchdDirs {
		sources = append(sources, &launchd.Launchd{
			Dir:      e.dir,
			Tag:      e.tag,
			ReadOnly: true,
			Runner:   launchd.DefaultLaunchctlRunner,
		})
	}
	return sources, errs
}
