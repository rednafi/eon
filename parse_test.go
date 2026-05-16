package eon

import (
	"errors"
	"testing"
	"time"
)

func mustTime(t *testing.T, layout, value string) time.Time {
	t.Helper()
	got, err := time.Parse(layout, value)
	if err != nil {
		t.Fatalf("setup: parse %q: %v", value, err)
	}
	return got
}

func TestParseAtAccepted(t *testing.T) {
	t.Helper()
	now := mustTime(t, time.RFC3339, "2026-05-13T10:00:00-07:00")
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{name: "rfc3339_future", in: "2026-05-13T12:00:00-07:00", want: mustTime(t, time.RFC3339, "2026-05-13T12:00:00-07:00")},
		{name: "offset_minutes", in: "+30m", want: now.Add(30 * time.Minute)},
		{name: "offset_hours", in: "+2h", want: now.Add(2 * time.Hour)},
		{name: "offset_seconds", in: "+45s", want: now.Add(45 * time.Second)},
		{name: "offset_days", in: "+3d", want: now.Add(3 * 24 * time.Hour)},
		{name: "today_hhmm", in: "today 17:00", want: time.Date(2026, 5, 13, 17, 0, 0, 0, now.Location())},
		{name: "today_pm", in: "today 5:30pm", want: time.Date(2026, 5, 13, 17, 30, 0, 0, now.Location())},
		{name: "tomorrow_am", in: "tomorrow 9am", want: time.Date(2026, 5, 14, 9, 0, 0, 0, now.Location())},
		{name: "tomorrow_hhmm", in: "tomorrow 09:30", want: time.Date(2026, 5, 14, 9, 30, 0, 0, now.Location())},
		{name: "tomorrow_midnight", in: "tomorrow 12am", want: time.Date(2026, 5, 14, 0, 0, 0, 0, now.Location())},
		{name: "tomorrow_noon", in: "tomorrow 12pm", want: time.Date(2026, 5, 14, 12, 0, 0, 0, now.Location())},
		{name: "uppercase_shortcut", in: "TOMORROW 9am", want: time.Date(2026, 5, 14, 9, 0, 0, 0, now.Location())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAt(tc.in, now)
			if err != nil {
				t.Fatalf("ParseAt(%q): %v", tc.in, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("ParseAt(%q) = %s, want %s", tc.in, got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
		})
	}
}

func TestParseAtTomorrowUsesCalendarDayAcrossDSTFallback(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// "tomorrow" must advance the calendar date, not add a fixed 24h.
	now := time.Date(2026, 10, 25, 0, 30, 0, 0, loc)

	got, err := ParseAt("tomorrow 09:00", now)
	if err != nil {
		t.Fatalf("ParseAt: %v", err)
	}
	want := time.Date(2026, 10, 26, 9, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("ParseAt tomorrow across DST fallback = %s, want %s",
			got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestParseAtRejected(t *testing.T) {
	t.Helper()
	now := mustTime(t, time.RFC3339, "2026-05-13T10:00:00-07:00")
	cases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "blank", in: "   "},
		{name: "gibberish", in: "next thursday-ish"},
		{name: "past_rfc3339", in: "2020-01-01T00:00:00Z"},
		{name: "past_offset_zero", in: "+0s"},
		{name: "zero_days", in: "+0d"},
		{name: "bad_days", in: "+xd"},
		{name: "negative_offset", in: "+-1h"},
		{name: "bad_offset_unit", in: "+5y"},
		{name: "today_past", in: "today 09:00"},
		{name: "tomorrow_bad_clock", in: "tomorrow 25:00"},
		{name: "tomorrow_bad_minute", in: "tomorrow 9:xx"},
		{name: "tomorrow_non_numeric", in: "tomorrow abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAt(tc.in, now)
			if err == nil {
				t.Fatalf("ParseAt(%q) = no error, want one", tc.in)
			}
			if !errors.Is(err, ErrInvalidTime) {
				t.Errorf("ParseAt(%q) error %v: want errors.Is(ErrInvalidTime)", tc.in, err)
			}
		})
	}
}

func TestParseCronAccepted(t *testing.T) {
	t.Helper()
	for _, expr := range []string{
		"* * * * *",
		"*/5 * * * *",
		"0 9 * * 1-5",
		"@hourly",
		"@daily",
		"@every 30s",
		"@every 5m",
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := ParseCron(expr); err != nil {
				t.Fatalf("ParseCron(%q): %v", expr, err)
			}
		})
	}
}

func TestParseCronCachesExpression(t *testing.T) {
	expr := "*/5 * * * *"
	if _, err := ParseCron(expr); err != nil {
		t.Fatalf("initial ParseCron(%q): %v", expr, err)
	}
	if _, err := ParseCron(expr); err != nil {
		t.Fatalf("cached ParseCron(%q): %v", expr, err)
	}
}

func TestParseCronRejected(t *testing.T) {
	t.Helper()
	for _, expr := range []string{"", "   ", "not-a-cron", "0 0 * *", "@nope", "@every nope"} {
		t.Run(expr, func(t *testing.T) {
			_, err := ParseCron(expr)
			if err == nil {
				t.Fatalf("ParseCron(%q) = no error, want one", expr)
			}
			if !errors.Is(err, ErrInvalidCron) {
				t.Errorf("ParseCron(%q) error %v: want errors.Is(ErrInvalidCron)", expr, err)
			}
		})
	}
}

func TestNextFire(t *testing.T) {
	t.Helper()
	now := mustTime(t, time.RFC3339, "2026-05-13T10:00:00Z")

	t.Run("cron_next", func(t *testing.T) {
		j := Job{Kind: KindCron, Cron: "*/15 * * * *"}
		got := NextFire(j, now)
		want := time.Date(2026, 5, 13, 10, 15, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("NextFire cron = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	})

	t.Run("oneshot_future", func(t *testing.T) {
		at := now.Add(2 * time.Hour)
		j := Job{Kind: KindOneshot, FireAt: at}
		if got := NextFire(j, now); !got.Equal(at) {
			t.Errorf("NextFire oneshot = %s, want %s", got, at)
		}
	})

	t.Run("oneshot_past_enabled", func(t *testing.T) {
		j := Job{Kind: KindOneshot, FireAt: now.Add(-1 * time.Hour), Status: StatusEnabled}
		if got := NextFire(j, now); !got.Equal(now) {
			t.Errorf("NextFire missed oneshot = %s, want %s", got, now)
		}
	})

	t.Run("oneshot_done", func(t *testing.T) {
		j := Job{Kind: KindOneshot, FireAt: now.Add(-1 * time.Hour), Status: StatusDone}
		if got := NextFire(j, now); !got.IsZero() {
			t.Errorf("NextFire done oneshot = %s, want zero", got)
		}
	})

	t.Run("cron_invalid", func(t *testing.T) {
		j := Job{Kind: KindCron, Cron: "garbage"}
		if got := NextFire(j, now); !got.IsZero() {
			t.Errorf("NextFire invalid cron = %s, want zero", got)
		}
	})

	t.Run("unknown_kind", func(t *testing.T) {
		j := Job{Kind: JobKind("mystery")}
		if got := NextFire(j, now); !got.IsZero() {
			t.Errorf("NextFire unknown kind = %s, want zero", got)
		}
	})
}

func TestJobSpecValidate(t *testing.T) {
	t.Helper()
	now := mustTime(t, time.RFC3339, "2026-05-13T10:00:00Z")
	future := now.Add(time.Hour)

	cases := []struct {
		name    string
		spec    JobSpec
		wantErr error
	}{
		{name: "ok_cron", spec: JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@hourly"}},
		{name: "ok_oneshot", spec: JobSpec{Name: "x", Command: []string{"echo"}, FireAt: future}},
		{name: "missing_name", spec: JobSpec{Command: []string{"echo"}, Cron: "@hourly"}, wantErr: ErrInvalidSpec},
		{name: "missing_command", spec: JobSpec{Name: "x", Cron: "@hourly"}, wantErr: ErrInvalidSpec},
		{name: "empty_argv0", spec: JobSpec{Name: "x", Command: []string{""}, Cron: "@hourly"}, wantErr: ErrInvalidSpec},
		{name: "both_cron_and_at", spec: JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@hourly", FireAt: future}, wantErr: ErrInvalidSpec},
		{name: "neither_cron_nor_at", spec: JobSpec{Name: "x", Command: []string{"echo"}}, wantErr: ErrInvalidSpec},
		{name: "bad_cron", spec: JobSpec{Name: "x", Command: []string{"echo"}, Cron: "garbage"}, wantErr: ErrInvalidCron},
		{name: "past_oneshot", spec: JobSpec{Name: "x", Command: []string{"echo"}, FireAt: now.Add(-time.Minute)}, wantErr: ErrInvalidTime},
		// Zero or negative @every schedules would tight-loop the scheduler.
		{name: "every_zero", spec: JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@every 0s"}, wantErr: ErrInvalidCron},
		{name: "every_negative", spec: JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@every -1s"}, wantErr: ErrInvalidCron},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate(now)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("Validate: %v, want nil", err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("Validate error %v: want errors.Is(%v)", err, tc.wantErr)
			}
		})
	}
}
