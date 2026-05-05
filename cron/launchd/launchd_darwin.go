//go:build darwin

package launchd

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"howett.net/plist"

	"github.com/rednafi/eon/cron"
)

// Compile-time guards: Launchd satisfies cron.Source AND, when not
// read-only, cron.Mutator. Failed builds are preferable to "missing
// method" runtime panics.
var (
	_ cron.Source  = (*Launchd)(nil)
	_ cron.Mutator = (*Launchd)(nil)
)

// LaunchctlRunner executes launchctl with the given args. It returns combined
// output and an error. Tests inject a fake to avoid mutating system state.
type LaunchctlRunner func(ctx context.Context, args []string) ([]byte, error)

// Launchd is a cron.Source backed by user launchd agents in a directory of plist
// files (default ~/Library/LaunchAgents). Multiple Launchd instances may be
// composed (one per directory) — see NewUserLaunchd / NewSystemLaunchd.
type Launchd struct {
	// Dir is the directory containing the .plist files this source manages.
	Dir string
	// Tag is appended to job IDs to disambiguate sources reading from
	// different directories ("user", "system", "daemons").
	Tag string
	// ReadOnly disables Delete. System-level directories use this.
	ReadOnly bool
	// Runner runs launchctl. May be nil to skip launchctl entirely (useful
	// for tests over a tmpdir).
	Runner LaunchctlRunner
}

// NewUser returns a source for the calling user's LaunchAgents.
func NewUser() (*Launchd, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Launchd{
		Dir:    filepath.Join(home, "Library", "LaunchAgents"),
		Tag:    "user",
		Runner: DefaultLaunchctlRunner,
	}, nil
}

// NewSystem returns a read-only source for /Library/LaunchAgents.
func NewSystem() *Launchd {
	return &Launchd{
		Dir:      "/Library/LaunchAgents",
		Tag:      "system",
		ReadOnly: true,
		Runner:   DefaultLaunchctlRunner,
	}
}

// Name implements cron.Source.
func (l *Launchd) Name() string { return "launchd-" + l.Tag }

// Scope implements cron.Source. ReadOnly distinguishes the user's LaunchAgents
// (writable) from the /Library and /System/Library locations (system-scope).
func (l *Launchd) Scope() cron.Scope {
	if l.ReadOnly {
		return cron.ScopeSystem
	}
	return cron.ScopeUser
}

// plistDoc is the subset of launchd plist keys we care about. Apple's full
// schema is huge; we only need scheduling, paths, and identity.
//
// StartCalendarInterval is `any` because launchd accepts both a single dict
// (one trigger) and an array of dicts (multiple triggers). The git-scm
// plists use the array form, and we'd silently drop them if we typed it as
// `map[string]any` only.
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

// List implements cron.Source. Missing or unreadable plists are skipped silently;
// a partial directory shouldn't break listing.
func (l *Launchd) List(ctx context.Context) ([]cron.Job, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", l.Dir, err)
	}
	var jobs []cron.Job
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		full := filepath.Join(l.Dir, e.Name())
		j, err := l.readPlist(full)
		if err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	// Enrich with launchctl-derived runtime state if the runner is set.
	if l.Runner != nil {
		l.enrich(ctx, jobs)
	}
	slices.SortFunc(jobs, func(a, b cron.Job) int { return cmp.Compare(a.Name, b.Name) })
	return jobs, nil
}

func (l *Launchd) readPlist(path string) (cron.Job, error) {
	f, err := os.Open(path)
	if err != nil {
		return cron.Job{}, err
	}
	defer func() { _ = f.Close() }()
	raw, err := os.ReadFile(path)
	if err != nil {
		return cron.Job{}, err
	}
	var doc plistDoc
	dec := plist.NewDecoder(f)
	if err := dec.Decode(&doc); err != nil {
		return cron.Job{}, err
	}
	label := cmp.Or(doc.Label, strings.TrimSuffix(filepath.Base(path), ".plist"))
	cmd := cmp.Or(strings.Join(doc.ProgramArguments, " "), doc.Program, "(no command)")
	j := cron.Job{
		ID:         "launchd-" + l.Tag + ":" + label,
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
		// Best-effort next run from interval — we don't know the load time so
		// we project from "now". Better than nothing; runtime data from
		// `launchctl print` would override.
		next := time.Now().Add(time.Duration(doc.StartInterval) * time.Second)
		j.NextRun = &next
	}
	if info, err := os.Stat(doc.StandardOutPath); err == nil {
		t := info.ModTime()
		j.LastRun = &t
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
// description suitable for the list-view "SCHEDULE" column.
func formatLaunchdSchedule(doc plistDoc) string {
	if doc.StartInterval > 0 {
		return formatInterval(doc.StartInterval)
	}
	switch v := doc.StartCalendarInterval.(type) {
	case map[string]any:
		if len(v) > 0 {
			return formatCalendar(v)
		}
	case []any:
		if len(v) == 1 {
			if m, ok := v[0].(map[string]any); ok {
				return formatCalendar(m)
			}
		}
		if len(v) > 1 {
			return fmt.Sprintf("%d triggers", len(v))
		}
	}
	if doc.RunAtLoad {
		return "at load"
	}
	return "on-demand"
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
	// StartCalendarInterval mirrors cron fields. Render as "min hour dom mon dow"
	// with "*" for missing fields, so it's familiar to anyone who reads cron.
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

// enrich queries `launchctl list` once and overlays PID/exit-code onto jobs.
// `launchctl list` columns are: PID Status Label.
func (l *Launchd) enrich(ctx context.Context, jobs []cron.Job) {
	out, err := l.Runner(ctx, []string{"list"})
	if err != nil {
		return
	}
	byLabel := map[string]struct {
		PID    int
		Status int
	}{}
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header or blank
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid := 0
		if fields[0] != "-" {
			pid, _ = strconv.Atoi(fields[0])
		}
		status, _ := strconv.Atoi(fields[1])
		byLabel[fields[2]] = struct {
			PID    int
			Status int
		}{PID: pid, Status: status}
	}
	for i := range jobs {
		if e, ok := byLabel[jobs[i].Name]; ok {
			jobs[i].PID = e.PID
			jobs[i].LastExitCode = e.Status
			switch {
			case e.PID > 0:
				jobs[i].Status = "running"
			case e.Status != 0:
				jobs[i].Status = fmt.Sprintf("exited %d", e.Status)
			}
		}
	}
}

// Add implements cron.Mutator. We translate the portable schedule DSL
// (`@every <duration>` or `@hourly`/`@daily`/...) into a StartInterval
// plist and write it. Cron-style 5-field schedules return a clear error
// — they don't have a clean launchd equivalent and the user should
// target the crontab source instead.
func (l *Launchd) Add(_ context.Context, spec cron.JobSpec) (cron.Job, error) {
	if l.ReadOnly {
		return cron.Job{}, fmt.Errorf("%s is read-only", l.Name())
	}
	if err := validateSpec(spec); err != nil {
		return cron.Job{}, err
	}
	interval, err := cron.ParseScheduleInterval(spec.Schedule)
	if err != nil {
		return cron.Job{}, err
	}
	label := launchdLabel(spec.Command)
	path := filepath.Join(l.Dir, label+".plist")
	if _, err := os.Stat(path); err == nil {
		return cron.Job{}, fmt.Errorf("a plist for %q already exists at %s; use eon edit", label, path)
	}
	body := renderPlist(label, spec.Command, interval)
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return cron.Job{}, err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return cron.Job{}, err
	}
	return l.readPlist(path)
}

// Edit implements cron.Mutator. We rewrite the plist for the given ID with
// the new schedule and command, preserving the file path.
func (l *Launchd) Edit(_ context.Context, id string, spec cron.JobSpec) (cron.Job, error) {
	label, ok := strings.CutPrefix(id, "launchd-"+l.Tag+":")
	if !ok {
		return cron.Job{}, cron.ErrNotFound
	}
	if l.ReadOnly {
		return cron.Job{}, fmt.Errorf("%s is read-only", l.Name())
	}
	if err := validateSpec(spec); err != nil {
		return cron.Job{}, err
	}
	interval, err := cron.ParseScheduleInterval(spec.Schedule)
	if err != nil {
		return cron.Job{}, err
	}
	path := filepath.Join(l.Dir, label+".plist")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cron.Job{}, cron.ErrNotFound
	} else if err != nil {
		return cron.Job{}, err
	}
	body := renderPlist(label, spec.Command, interval)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return cron.Job{}, err
	}
	return l.readPlist(path)
}

// validateSpec catches obviously-broken inputs before we touch disk.
// launchd would silently load a plist with an empty Command field —
// which is a worse failure mode than a noisy error.
func validateSpec(spec cron.JobSpec) error {
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

// launchdLabel derives a reverse-DNS-ish label from a command. Real users
// pick their own labels; for eon-created plists we prefix with
// "eon.<basename-of-first-token>" so the source is obvious in `launchctl list`.
func launchdLabel(command string) string {
	short := cron.CommandShortName(command)
	short = strings.ReplaceAll(short, "/", "-")
	if short == "" {
		short = "job"
	}
	return "eon." + short
}

// renderPlist generates a minimal launchd plist (label, program arguments,
// schedule). We split the command on whitespace for ProgramArguments —
// preserves quoting only as well as Fields() does, but launchd doesn't
// honour shell quoting anyway, so a power user wanting `bash -c '...'` is
// expected to author the plist by hand.
func renderPlist(label, command string, interval cron.ScheduleInterval) string {
	args := strings.Fields(command)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0"><dict>` + "\n")
	fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n", label)
	b.WriteString("<key>ProgramArguments</key><array>\n")
	for _, a := range args {
		fmt.Fprintf(&b, "  <string>%s</string>\n", a)
	}
	b.WriteString("</array>\n")
	seconds := intervalSeconds(interval)
	fmt.Fprintf(&b, "<key>StartInterval</key><integer>%d</integer>\n", seconds)
	b.WriteString("</dict></plist>\n")
	return b.String()
}

// intervalSeconds collapses ScheduleInterval into seconds for StartInterval.
// launchd's StartInterval is the only schedule key that's *truly* portable
// across the descriptors we accept; calendar-based schedules need
// StartCalendarInterval which is per-day-only.
func intervalSeconds(s cron.ScheduleInterval) int {
	if s.Every > 0 {
		return max(1, int(s.Every.Seconds()))
	}
	switch s.Descriptor {
	case "hourly":
		return 3600
	case "daily":
		return 86400
	case "weekly":
		return 7 * 86400
	case "monthly":
		return 30 * 86400 // approximate; launchd has no calendar-month interval
	case "yearly":
		return 365 * 86400
	}
	return 0
}

// Delete implements cron.Source. ReadOnly sources reject the call.
func (l *Launchd) Delete(ctx context.Context, id string) error {
	label, ok := strings.CutPrefix(id, "launchd-"+l.Tag+":")
	if !ok {
		return cron.ErrNotFound
	}
	if l.ReadOnly {
		return fmt.Errorf("%s is read-only", l.Name())
	}
	path := filepath.Join(l.Dir, label+".plist")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cron.ErrNotFound
	} else if err != nil {
		return err
	}
	// Best-effort unload: ignore failure (the agent may not be loaded).
	if l.Runner != nil {
		_, _ = l.Runner(ctx, []string{"unload", path})
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
