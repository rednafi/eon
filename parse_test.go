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
		{"rfc3339 future", "2026-05-13T12:00:00-07:00", mustTime(t, time.RFC3339, "2026-05-13T12:00:00-07:00")},
		{"offset minutes", "+30m", now.Add(30 * time.Minute)},
		{"offset hours", "+2h", now.Add(2 * time.Hour)},
		{"offset seconds", "+45s", now.Add(45 * time.Second)},
		{"offset days", "+3d", now.Add(3 * 24 * time.Hour)},
		{"today HH:MM", "today 17:00", time.Date(2026, 5, 13, 17, 0, 0, 0, now.Location())},
		{"today 5:30pm", "today 5:30pm", time.Date(2026, 5, 13, 17, 30, 0, 0, now.Location())},
		{"tomorrow 9am", "tomorrow 9am", time.Date(2026, 5, 14, 9, 0, 0, 0, now.Location())},
		{"tomorrow 09:30", "tomorrow 09:30", time.Date(2026, 5, 14, 9, 30, 0, 0, now.Location())},
		{"tomorrow 12am", "tomorrow 12am", time.Date(2026, 5, 14, 0, 0, 0, 0, now.Location())},
		{"tomorrow 12pm", "tomorrow 12pm", time.Date(2026, 5, 14, 12, 0, 0, 0, now.Location())},
		{"uppercase shortcut", "TOMORROW 9am", time.Date(2026, 5, 14, 9, 0, 0, 0, now.Location())},
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

func TestParseAtRejected(t *testing.T) {
	t.Helper()
	now := mustTime(t, time.RFC3339, "2026-05-13T10:00:00-07:00")
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"gibberish", "next thursday-ish"},
		{"past rfc3339", "2020-01-01T00:00:00Z"},
		{"past offset zero", "+0s"},
		{"negative offset", "+-1h"},
		{"bad offset unit", "+5y"},
		{"today past", "today 09:00"},
		{"tomorrow bad clock", "tomorrow 25:00"},
		{"tomorrow non-numeric", "tomorrow abc"},
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

func TestParseCronRejected(t *testing.T) {
	t.Helper()
	for _, expr := range []string{"", "   ", "not-a-cron", "0 0 * *", "@nope"} {
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

	t.Run("cron next", func(t *testing.T) {
		j := Job{Kind: KindCron, Cron: "*/15 * * * *"}
		got := NextFire(j, now)
		want := time.Date(2026, 5, 13, 10, 15, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("NextFire cron = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	})

	t.Run("oneshot future", func(t *testing.T) {
		at := now.Add(2 * time.Hour)
		j := Job{Kind: KindOneshot, FireAt: at}
		if got := NextFire(j, now); !got.Equal(at) {
			t.Errorf("NextFire oneshot = %s, want %s", got, at)
		}
	})

	t.Run("oneshot past enabled fires immediately", func(t *testing.T) {
		j := Job{Kind: KindOneshot, FireAt: now.Add(-1 * time.Hour), Status: StatusEnabled}
		if got := NextFire(j, now); !got.Equal(now) {
			t.Errorf("NextFire missed oneshot = %s, want %s", got, now)
		}
	})

	t.Run("oneshot done returns zero", func(t *testing.T) {
		j := Job{Kind: KindOneshot, FireAt: now.Add(-1 * time.Hour), Status: StatusDone}
		if got := NextFire(j, now); !got.IsZero() {
			t.Errorf("NextFire done oneshot = %s, want zero", got)
		}
	})

	t.Run("cron invalid returns zero", func(t *testing.T) {
		j := Job{Kind: KindCron, Cron: "garbage"}
		if got := NextFire(j, now); !got.IsZero() {
			t.Errorf("NextFire invalid cron = %s, want zero", got)
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
		{"ok cron", JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@hourly"}, nil},
		{"ok oneshot", JobSpec{Name: "x", Command: []string{"echo"}, FireAt: future}, nil},
		{"missing name", JobSpec{Command: []string{"echo"}, Cron: "@hourly"}, ErrInvalidSpec},
		{"missing command", JobSpec{Name: "x", Cron: "@hourly"}, ErrInvalidSpec},
		{"empty argv[0]", JobSpec{Name: "x", Command: []string{""}, Cron: "@hourly"}, ErrInvalidSpec},
		{"both cron and at", JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@hourly", FireAt: future}, ErrInvalidSpec},
		{"neither cron nor at", JobSpec{Name: "x", Command: []string{"echo"}}, ErrInvalidSpec},
		{"bad cron", JobSpec{Name: "x", Command: []string{"echo"}, Cron: "garbage"}, ErrInvalidCron},
		{"past oneshot", JobSpec{Name: "x", Command: []string{"echo"}, FireAt: now.Add(-time.Minute)}, ErrInvalidTime},
		// Pathological @every durations are rejected upstream by
		// ParseCron because they would make the scheduler tight-loop.
		{"@every 0s", JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@every 0s"}, ErrInvalidCron},
		{"@every -1s", JobSpec{Name: "x", Command: []string{"echo"}, Cron: "@every -1s"}, ErrInvalidCron},
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
