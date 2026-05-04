//go:build darwin

package origin

// DefaultManager builds the platform-default Manager: user crontab plus the
// user's LaunchAgents directory. Read-only system locations are intentionally
// excluded — eon is a one-stop monitor for *your* crons, and surfacing every
// system daemon would drown out signal.
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
	return NewManager(origins...), errs
}
