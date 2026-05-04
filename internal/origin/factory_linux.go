//go:build linux

package origin

// DefaultManager builds the platform-default Manager: user crontab plus
// systemd user timers. System units in /etc/systemd/system aren't monitored —
// they're typically distro-managed, and eon is meant for the crons you create.
func DefaultManager() (*Manager, []error) {
	origins := []Origin{
		NewCrontab(),
		NewUserSystemd(),
	}
	return NewManager(origins...), nil
}
