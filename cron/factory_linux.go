//go:build linux

package cron

// systemSystemdDirs lists the read-only locations where system-scope timer
// units live. /etc takes precedence over /usr/lib in real systemd's resolution
// rules, but for a flat monitor we just list both — they're surfaced with
// different tags so a duplicate label produces two distinct rows.
var systemSystemdDirs = []struct{ tag, dir string }{
	{"etc", "/etc/systemd/system"},
	{"lib", "/usr/lib/systemd/system"},
}

// DefaultManager builds the platform-default Manager: user crontab and user
// systemd timers (read-write), plus /etc/crontab, /etc/cron.d, and the
// system systemd directories (read-only). System jobs are tagged
// Job.System=true so callers can hide them behind a flag.
func DefaultManager() (*Manager, []error) {
	origins := []Source{
		NewCrontab(),
		NewUserSystemd(),
		NewEtcCron(),
	}
	for _, e := range systemSystemdDirs {
		origins = append(origins, &Systemd{
			Dir:       e.dir,
			Tag:       e.tag,
			ReadOnly:  true,
			Systemctl: nil, // never invoke systemctl on system units
		})
	}
	return NewManager(origins...), nil
}
