//go:build darwin

package cron

// systemLaunchdDirs lists the read-only locations macOS installs background
// jobs into. Order matters only for tag stability — IDs are
// "launchd-<tag>:<label>" so each must be unique.
var systemLaunchdDirs = []struct{ tag, dir string }{
	{"system", "/Library/LaunchAgents"},
	{"daemons", "/Library/LaunchDaemons"},
	{"apple-agents", "/System/Library/LaunchAgents"},
	{"apple-daemons", "/System/Library/LaunchDaemons"},
}

// DefaultManager builds the platform-default Manager: user crontab plus the
// user's LaunchAgents (read-write) and a snapshot of every system Launch*
// directory (read-only). System jobs are visible but tagged Job.System=true
// so the CLI/TUI can hide them by default.
func DefaultManager() (*Manager, []error) {
	var (
		origins []Origin
		errs    []error
	)
	origins = append(origins, NewCrontab())
	if l, err := NewUserLaunchd(); err == nil {
		origins = append(origins, l)
	} else {
		errs = append(errs, err)
	}
	for _, e := range systemLaunchdDirs {
		origins = append(origins, &Launchd{
			Dir:      e.dir,
			Tag:      e.tag,
			ReadOnly: true,
			Runner:   DefaultLaunchctlRunner,
		})
	}
	return NewManager(origins...), errs
}
