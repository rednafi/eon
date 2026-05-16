package eon

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser is the standard 5-field crontab with the @descriptor and
// @every shortcuts. Kept package-private so callers can't accidentally
// drift the accepted syntax across the codebase.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow |
		cron.Descriptor,
)

// cronCache memoises ParseCron results.
//
// cron.Schedule is immutable.
// The scheduler can hit this once per second for @every 1s jobs.
// The cache is bounded by distinct cron expressions in the database.
var cronCache sync.Map // map[string]cron.Schedule

// ParseCron validates a cron expression.
//
// Important behavior:
//   - It returns a schedule that computes next-fire times.
//   - Parser errors wrap ErrInvalidCron.
//   - @every durations must be positive.
//
// robfig/cron rounds sub-second @every values up to one second.
// Pre-validation keeps `@every 0s` visible as a user error.
func ParseCron(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("%w: empty expression", ErrInvalidCron)
	}
	if v, ok := cronCache.Load(expr); ok {
		return v.(cron.Schedule), nil
	}
	if rest, ok := strings.CutPrefix(expr, "@every "); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("%w: @every duration: %s", ErrInvalidCron, err.Error())
		}
		if d <= 0 {
			return nil, fmt.Errorf("%w: @every duration must be positive (got %v)",
				ErrInvalidCron, d)
		}
	}
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidCron, err.Error())
	}
	cronCache.Store(expr, sched)
	return sched, nil
}

// ParseAt resolves a one-shot time specifier relative to now. It
// accepts:
//
//   - RFC3339:        "2026-05-12T15:30:00-07:00"
//   - relative offset: "+30m", "+2h", "+45s", "+3d"
//   - today HH:MM:     "today 17:00", "today 5:00pm"
//   - tomorrow HH:MM:  "tomorrow 9am", "tomorrow 09:30"
//
// All "today"/"tomorrow" anchors use now's [time.Location]. Returns
// [ErrInvalidTime] on parse failure or when the resolved instant is
// in the past (we do not schedule retroactively).
func ParseAt(spec string, now time.Time) (time.Time, error) {
	raw := strings.TrimSpace(spec)
	if raw == "" {
		return time.Time{}, fmt.Errorf("%w: empty", ErrInvalidTime)
	}

	got, err := parseAtRaw(raw, now)
	if err != nil {
		return time.Time{}, err
	}
	if !got.After(now) {
		return time.Time{}, fmt.Errorf("%w: %q resolves to %s, not in the future",
			ErrInvalidTime, spec, got.Format(time.RFC3339))
	}
	return got, nil
}

func parseAtRaw(raw string, now time.Time) (time.Time, error) {
	// RFC3339 and the looser variants Go accepts.
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}

	// Relative offset: +30m, +2h, +45s, +3d.
	if strings.HasPrefix(raw, "+") {
		return parseOffset(raw[1:], now)
	}

	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "today "):
		return parseClock(lower[len("today "):], now, 0)
	case strings.HasPrefix(lower, "tomorrow "):
		return parseClock(lower[len("tomorrow "):], now, 1)
	}

	return time.Time{}, fmt.Errorf("%w: %q", ErrInvalidTime, raw)
}

// parseOffset handles "+30m", "+2h", "+3d", and "+1h30m".
//
// time.ParseDuration does not handle "d".
// Whole days are promoted to hours before delegating.
func parseOffset(s string, now time.Time) (time.Time, error) {
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: bad offset %q", ErrInvalidTime, s)
		}
		if n <= 0 {
			return time.Time{}, fmt.Errorf("%w: offset must be positive", ErrInvalidTime)
		}
		return now.Add(time.Duration(n) * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: bad offset %q", ErrInvalidTime, s)
	}
	if d <= 0 {
		return time.Time{}, fmt.Errorf("%w: offset must be positive", ErrInvalidTime)
	}
	return now.Add(d), nil
}

// parseClock accepts "17:00", "5:30pm", "9am" and applies them to the
// day anchored on (now + dayOffset), preserving now's location.
func parseClock(s string, now time.Time, dayOffsetDays int) (time.Time, error) {
	s = strings.TrimSpace(s)
	hour, min, err := parseHourMinute(s)
	if err != nil {
		return time.Time{}, err
	}
	anchor := now.AddDate(0, 0, dayOffsetDays)
	y, m, d := anchor.Date()
	return time.Date(y, m, d, hour, min, 0, 0, now.Location()), nil
}

func parseHourMinute(s string) (hour, minute int, err error) {
	low := strings.ToLower(s)
	ampm := ""
	switch {
	case strings.HasSuffix(low, "am"):
		ampm, low = "am", strings.TrimSpace(strings.TrimSuffix(low, "am"))
	case strings.HasSuffix(low, "pm"):
		ampm, low = "pm", strings.TrimSpace(strings.TrimSuffix(low, "pm"))
	}

	if h, m, ok := strings.Cut(low, ":"); ok {
		hh, herr := strconv.Atoi(strings.TrimSpace(h))
		mm, merr := strconv.Atoi(strings.TrimSpace(m))
		if herr != nil || merr != nil {
			return 0, 0, fmt.Errorf("%w: bad clock %q", ErrInvalidTime, s)
		}
		hour, minute = hh, mm
	} else {
		hh, err := strconv.Atoi(strings.TrimSpace(low))
		if err != nil {
			return 0, 0, fmt.Errorf("%w: bad clock %q", ErrInvalidTime, s)
		}
		hour = hh
	}

	switch ampm {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour != 12 {
			hour += 12
		}
	}

	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("%w: out-of-range clock %q", ErrInvalidTime, s)
	}
	return hour, minute, nil
}

// NextFire returns the next time the given spec should fire after now.
//
// One-shot jobs:
//   - Status == StatusDone: zero time (already ran).
//   - FireAt > now: FireAt.
//   - FireAt <= now: now.
//
// Past-due one-shots fire on the next tick.
// That handles the daemon being down at the scheduled time.
//
// Cron jobs delegate to the parsed schedule's Next.
func NextFire(j Job, now time.Time) time.Time {
	switch j.Kind {
	case KindOneshot:
		if j.Status == StatusDone {
			return time.Time{}
		}
		if j.FireAt.After(now) {
			return j.FireAt
		}
		return now
	case KindCron:
		sched, err := ParseCron(j.Cron)
		if err != nil {
			return time.Time{}
		}
		return sched.Next(now)
	}
	return time.Time{}
}

// Validate enforces the JobSpec invariants required before insert.
func (s JobSpec) Validate(now time.Time) error {
	if s.Name == "" {
		return fmt.Errorf("%w: name required", ErrInvalidSpec)
	}
	if len(s.Command) == 0 || s.Command[0] == "" {
		return fmt.Errorf("%w: command required", ErrInvalidSpec)
	}
	hasCron := s.Cron != ""
	hasAt := !s.FireAt.IsZero()
	if hasCron == hasAt {
		return fmt.Errorf("%w: exactly one of cron or fire-at must be set", ErrInvalidSpec)
	}
	if hasCron {
		if _, err := ParseCron(s.Cron); err != nil {
			return err
		}
	}
	if hasAt && !s.FireAt.After(now) {
		return fmt.Errorf("%w: fire time %s not in the future",
			ErrInvalidTime, s.FireAt.Format(time.RFC3339))
	}
	return nil
}
