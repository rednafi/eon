package launchd

import (
	"bytes"
	"cmp"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"howett.net/plist"

	"github.com/rednafi/eon/cron"
)

// plistDoc is the subset of launchd plist keys we care about. Apple's full
// schema is huge; we only need scheduling, paths, and identity.
//
// StartCalendarInterval is `any` because launchd accepts both a single dict
// (one trigger) and an array of dicts (multiple triggers). The git-scm
// plists use the array form, and we'd silently drop them if we typed it
// as `map[string]any` only.
type plistDoc struct {
	Label                 string   `plist:"Label"`
	Program               string   `plist:"Program"`
	ProgramArguments      []string `plist:"ProgramArguments"`
	StartInterval         int      `plist:"StartInterval"`
	StartCalendarInterval any      `plist:"StartCalendarInterval"`
	StandardOutPath       string   `plist:"StandardOutPath"`
	StandardErrorPath     string   `plist:"StandardErrorPath"`
	Disabled              bool     `plist:"Disabled"`
	RunAtLoad             bool     `plist:"RunAtLoad"`
}

// parsePlist decodes raw plist bytes into a cron.Job. Pure: takes bytes
// and the source-tagging metadata, returns a Job. The Source's readPlist
// adapter wraps this with the os.ReadFile call and an os.Stat for
// LastRun.
func parsePlist(raw []byte, tag, path string) (cron.Job, error) {
	var doc plistDoc
	if err := plist.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err != nil {
		return cron.Job{}, err
	}
	label := cmp.Or(doc.Label, strings.TrimSuffix(filepath.Base(path), ".plist"))
	cmd := cmp.Or(strings.Join(doc.ProgramArguments, " "), doc.Program, "(no command)")
	j := cron.Job{
		ID:         "launchd-" + tag + ":" + label,
		Kind:       cron.KindLaunchd,
		Name:       label,
		Command:    cmd,
		Schedule:   formatLaunchdSchedule(doc),
		Path:       path,
		StdoutPath: doc.StandardOutPath,
		StderrPath: doc.StandardErrorPath,
		Raw:        string(raw),
		Status:     launchdStatus(doc),
	}
	if doc.StartInterval > 0 {
		// Best-effort next run from interval — we don't know the load time
		// so we project from "now". Better than nothing; runtime data from
		// `launchctl print` would override.
		next := time.Now().Add(time.Duration(doc.StartInterval) * time.Second)
		j.NextRun = &next
	}
	return j, nil
}

func launchdStatus(doc plistDoc) string {
	if doc.Disabled {
		return "disabled"
	}
	return "loaded"
}

// formatLaunchdSchedule renders the plist's schedule keys into a one-line
// description suitable for the list-view "SCHEDULE" column. When *both*
// StartInterval and StartCalendarInterval are set (rare but legal),
// surface the additional calendar trigger count so the user isn't
// misled about how many times the agent fires.
func formatLaunchdSchedule(doc plistDoc) string {
	if doc.StartInterval > 0 {
		base := formatInterval(doc.StartInterval)
		if extras := calendarTriggers(doc.StartCalendarInterval); extras > 0 {
			return fmt.Sprintf("%s + %d cal", base, extras)
		}
		return base
	}
	switch v := doc.StartCalendarInterval.(type) {
	case map[string]any:
		if len(v) > 0 {
			return formatCalendar(v)
		}
	case []any:
		// Skip empty-dict entries: an `[{}]` plist is technically a
		// calendar trigger that never fires, but rendering it as
		// "* * * * *" would imply "every minute" which is the opposite
		// of what launchd does.
		if len(v) == 1 {
			if m, ok := v[0].(map[string]any); ok && len(m) > 0 {
				return formatCalendar(m)
			}
		}
		if n := calendarTriggers(v); n > 1 {
			return fmt.Sprintf("%d triggers", n)
		}
	}
	if doc.RunAtLoad {
		return "at load"
	}
	return "on-demand"
}

// calendarTriggers counts non-empty StartCalendarInterval entries. Returns
// 0 for nil/missing values, 1 for a single-dict map, or N for the array
// form (skipping empty dicts in the array).
func calendarTriggers(v any) int {
	switch x := v.(type) {
	case map[string]any:
		if len(x) > 0 {
			return 1
		}
	case []any:
		n := 0
		for _, e := range x {
			if m, ok := e.(map[string]any); ok && len(m) > 0 {
				n++
			}
		}
		return n
	}
	return 0
}

func formatInterval(s int) string {
	d := time.Duration(s) * time.Second
	switch {
	case d%(time.Hour*24) == 0:
		return fmt.Sprintf("every %dd", int(d/(time.Hour*24)))
	case d%time.Hour == 0:
		return fmt.Sprintf("every %dh", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("every %dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("every %ds", s)
	}
}

func formatCalendar(m map[string]any) string {
	// StartCalendarInterval mirrors cron fields. Render as "min hour dom
	// mon dow" with "*" for missing fields, so it's familiar to anyone
	// who reads cron.
	get := func(k string) string {
		v, ok := m[k]
		if !ok {
			return "*"
		}
		switch x := v.(type) {
		case int:
			return strconv.Itoa(x)
		case int64:
			return strconv.FormatInt(x, 10)
		case uint64:
			return strconv.FormatUint(x, 10)
		case float64:
			return strconv.Itoa(int(x))
		default:
			return fmt.Sprintf("%v", x)
		}
	}
	return strings.Join([]string{
		get("Minute"), get("Hour"), get("Day"), get("Month"), get("Weekday"),
	}, " ")
}

// launchdLabel derives a reverse-DNS-ish label from a command — eon's
// plists are prefixed with "eon." so the source is obvious in `launchctl
// list`.
func launchdLabel(command string) string {
	return cron.LabelFromCommand(command, "eon.", "job")
}

// plistOut is the encoder-side counterpart to plistDoc — only the keys
// eon writes when materialising a new plist. Defined separately from
// plistDoc so we don't have to put `omitempty` on every decoder field
// (which would change decoder semantics).
type plistOut struct {
	Label            string   `plist:"Label"`
	ProgramArguments []string `plist:"ProgramArguments"`
	StartInterval    int      `plist:"StartInterval"`
}

// renderPlist generates a minimal launchd plist (label, program
// arguments, schedule). We split the command on whitespace for
// ProgramArguments — preserves quoting only as well as Fields() does,
// but launchd doesn't honour shell quoting anyway, so a power user
// wanting `bash -c '...'` is expected to author the plist by hand.
//
// Marshalling goes through howett.net/plist's encoder so XML escaping,
// DOCTYPE, and indentation all match what the decoder accepts on the
// other end — no hand-written XML to drift.
func renderPlist(label, command string, interval cron.ScheduleInterval) string {
	out := plistOut{
		Label:            label,
		ProgramArguments: strings.Fields(command),
		StartInterval:    interval.Seconds(),
	}
	body, err := plist.MarshalIndent(&out, plist.XMLFormat, "  ")
	if err != nil {
		// The encoder only fails on unrepresentable types — and our
		// shape is plain strings/ints. A failure here is a bug in the
		// library, not in the input.
		panic(fmt.Sprintf("encode plist: %v", err))
	}
	return string(body) + "\n"
}

