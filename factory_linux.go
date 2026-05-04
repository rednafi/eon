//go:build linux

package main

import (
	"github.com/rednafi/eon/cron"
	"github.com/rednafi/eon/cron/crontab"
	"github.com/rednafi/eon/cron/etccron"
	"github.com/rednafi/eon/cron/systemd"
)

// systemSystemdDirs lists the read-only directories where system-scope timer
// units live. /etc takes precedence over /usr/lib in systemd's resolution
// rules; for a flat monitor we list both with distinct tags so a duplicate
// label produces two distinct rows.
var systemSystemdDirs = []struct{ tag, dir string }{
	{"etc", "/etc/systemd/system"},
	{"lib", "/usr/lib/systemd/system"},
}

// platformSources is the composition root for Linux: user crontab and user
// systemd timers (writable), plus /etc/crontab, /etc/cron.d, and the system
// systemd directories (read-only). Returns pure cron.Source values; main
// wires them into a cron.Manager.
func platformSources() ([]cron.Source, []error) {
	sources := []cron.Source{
		crontab.New(),
		systemd.NewUser(),
		etccron.New(),
	}
	for _, e := range systemSystemdDirs {
		sources = append(sources, &systemd.Systemd{
			Dir:       e.dir,
			Tag:       e.tag,
			ReadOnly:  true,
			Systemctl: nil, // never invoke systemctl on system units
		})
	}
	return sources, nil
}
