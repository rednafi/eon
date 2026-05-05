package cron

import (
	"fmt"
	"strings"
	"time"
)

// ValidateSpec catches the obvious nonsense every backend rejects: empty
// schedule or command, embedded newlines in the command (which would
// silently corrupt a crontab line or a launchd plist body). Backends layer
// their own checks on top — crontab parses the schedule, launchd checks
// label uniqueness — but they all start here.
func ValidateSpec(spec JobSpec) error {
	if strings.TrimSpace(spec.Schedule) == "" {
		return fmt.Errorf("schedule must not be empty")
	}
	if strings.TrimSpace(spec.Command) == "" {
		return fmt.Errorf("command must not be empty")
	}
	if strings.ContainsAny(spec.Command, "\r\n") {
		return fmt.Errorf("command must not contain newlines")
	}
	return nil
}

// ScheduleInterval is the portable view of a JobSpec.Schedule for backends
// that don't natively speak 5-field cron (launchd, systemd).
//
// We accept two forms:
//
//	"@every <Go duration>"   →  Every: <duration>, Descriptor: ""
//	"@hourly|daily|weekly"   →  Descriptor: <name>, Every: 0
//
// Anything else returns an error so callers can fall through to a
// crontab-backed source (which DOES speak 5-field cron natively).
type ScheduleInterval struct {
	// Every is non-zero when the source string was "@every <duration>".
	Every time.Duration
	// Descriptor is one of "hourly", "daily", "weekly", "monthly", "yearly"
	// when the source string used that descriptor; empty otherwise.
	Descriptor string
}

// IsEmpty returns true when neither Every nor Descriptor was set — used by
// callers that want a default branch in switches.
func (s ScheduleInterval) IsEmpty() bool { return s.Every == 0 && s.Descriptor == "" }

// ParseScheduleInterval translates "@every <duration>" or a calendar
// descriptor into a ScheduleInterval. Returns a non-nil error for cron
// expressions and anything else the simple DSL doesn't cover — backends
// that need 5-field semantics (currently only crontab) shouldn't be
// calling this in the first place.
func ParseScheduleInterval(schedule string) (ScheduleInterval, error) {
	s := strings.TrimSpace(schedule)
	switch s {
	case "@hourly":
		return ScheduleInterval{Descriptor: "hourly"}, nil
	case "@daily", "@midnight":
		return ScheduleInterval{Descriptor: "daily"}, nil
	case "@weekly":
		return ScheduleInterval{Descriptor: "weekly"}, nil
	case "@monthly":
		return ScheduleInterval{Descriptor: "monthly"}, nil
	case "@yearly", "@annually":
		return ScheduleInterval{Descriptor: "yearly"}, nil
	}
	if rest, ok := strings.CutPrefix(s, "@every "); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil {
			return ScheduleInterval{}, fmt.Errorf("invalid @every duration %q: %w", rest, err)
		}
		if d <= 0 {
			return ScheduleInterval{}, fmt.Errorf("@every duration must be positive, got %s", d)
		}
		return ScheduleInterval{Every: d}, nil
	}
	return ScheduleInterval{}, fmt.Errorf("schedule %q is not portable across backends — use a crontab source for 5-field cron", schedule)
}
