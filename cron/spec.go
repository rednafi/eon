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

// PrepareIntervalSpec runs the standard pre-flight for a writable Source's
// Add/Edit: refuses to touch a system-scope Source, validates the spec via
// ValidateSpec, and parses the schedule into a ScheduleInterval. Backends
// for non-cron-expression schedulers (launchd, systemd) open Add/Edit with
// a single call here so the read-only/empty-schedule/parse-failure error
// paths are uniform.
func PrepareIntervalSpec(s Source, spec JobSpec) (ScheduleInterval, error) {
	if s.Scope() == ScopeSystem {
		return ScheduleInterval{}, fmt.Errorf("%s: %w", s.Name(), ErrReadOnly)
	}
	if err := ValidateSpec(spec); err != nil {
		return ScheduleInterval{}, err
	}
	return ParseScheduleInterval(spec.Schedule)
}

// Descriptor is a calendar-name shortcut for a recurring schedule. The
// closed set of values is parsed by ParseScheduleInterval and consumed
// by every backend that doesn't natively speak 5-field cron — typing
// the field rather than passing raw strings means a typo in a backend
// switch fails at compile time, not at runtime.
type Descriptor string

const (
	DescNone    Descriptor = ""
	DescHourly  Descriptor = "hourly"
	DescDaily   Descriptor = "daily"
	DescWeekly  Descriptor = "weekly"
	DescMonthly Descriptor = "monthly"
	DescYearly  Descriptor = "yearly"
)

// ScheduleInterval is the portable view of a JobSpec.Schedule for
// backends that don't natively speak 5-field cron (launchd, systemd).
// Exactly one of Every / Descriptor is set after a successful
// ParseScheduleInterval; both empty means the input was unportable.
//
//	"@every <Go duration>"   →  Every: <duration>, Descriptor: DescNone
//	"@hourly|daily|weekly"   →  Descriptor: Desc<Name>, Every: 0
type ScheduleInterval struct {
	Every      time.Duration
	Descriptor Descriptor
}

// IsEmpty returns true when neither Every nor Descriptor was set — used
// by callers that want a default branch in switches.
func (s ScheduleInterval) IsEmpty() bool { return s.Every == 0 && s.Descriptor == DescNone }

// Seconds returns the interval as a count of seconds, suitable for
// schedulers that take a single duration (launchd's StartInterval).
// Monthly and yearly use approximate conversions because the
// representation is calendar-blind.
func (s ScheduleInterval) Seconds() int {
	if s.Every > 0 {
		secs := int(s.Every.Seconds())
		if secs < 1 {
			return 1
		}
		return secs
	}
	switch s.Descriptor {
	case DescHourly:
		return 3600
	case DescDaily:
		return 86400
	case DescWeekly:
		return 7 * 86400
	case DescMonthly:
		return 30 * 86400
	case DescYearly:
		return 365 * 86400
	}
	return 0
}

// ParseScheduleInterval translates "@every <duration>" or a calendar
// descriptor into a ScheduleInterval. Returns a non-nil error for cron
// expressions and anything else the simple DSL doesn't cover — backends
// that need 5-field semantics (currently only crontab) shouldn't be
// calling this in the first place.
func ParseScheduleInterval(schedule string) (ScheduleInterval, error) {
	s := strings.TrimSpace(schedule)
	switch s {
	case "@hourly":
		return ScheduleInterval{Descriptor: DescHourly}, nil
	case "@daily", "@midnight":
		return ScheduleInterval{Descriptor: DescDaily}, nil
	case "@weekly":
		return ScheduleInterval{Descriptor: DescWeekly}, nil
	case "@monthly":
		return ScheduleInterval{Descriptor: DescMonthly}, nil
	case "@yearly", "@annually":
		return ScheduleInterval{Descriptor: DescYearly}, nil
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
