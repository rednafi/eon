//go:build darwin

package origin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"howett.net/plist"
)

// LaunchctlRunner executes launchctl with the given args. It returns combined
// output and an error. Tests inject a fake to avoid mutating system state.
type LaunchctlRunner func(ctx context.Context, args []string) ([]byte, error)

// Launchd is a Source backed by user launchd agents in a directory of plist
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

// NewUserLaunchd returns a source for the calling user's LaunchAgents.
func NewUserLaunchd() (*Launchd, error) {
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

// NewSystemLaunchd returns a read-only source for /Library/LaunchAgents.
func NewSystemLaunchd() *Launchd {
	return &Launchd{
		Dir:      "/Library/LaunchAgents",
		Tag:      "system",
		ReadOnly: true,
		Runner:   DefaultLaunchctlRunner,
	}
}

// Name implements Source.
func (l *Launchd) Name() string { return "launchd-" + l.Tag }

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

// List implements Source. Missing or unreadable plists are skipped silently;
// a partial directory shouldn't break listing.
func (l *Launchd) List(ctx context.Context) ([]Job, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", l.Dir, err)
	}
	var jobs []Job
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
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].Name < jobs[k].Name })
	return jobs, nil
}

func (l *Launchd) readPlist(path string) (Job, error) {
	f, err := os.Open(path)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = f.Close() }()
	raw, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	var doc plistDoc
	dec := plist.NewDecoder(f)
	if err := dec.Decode(&doc); err != nil {
		return Job{}, err
	}
	label := doc.Label
	if label == "" {
		label = strings.TrimSuffix(filepath.Base(path), ".plist")
	}
	cmd := strings.Join(doc.ProgramArguments, " ")
	if cmd == "" {
		cmd = doc.Program
	}
	if cmd == "" {
		cmd = "(no command)"
	}
	j := Job{
		ID:         "launchd-" + l.Tag + ":" + label,
		Kind:       KindLaunchd,
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
func (l *Launchd) enrich(ctx context.Context, jobs []Job) {
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

// Delete implements Source. ReadOnly sources reject the call.
func (l *Launchd) Delete(ctx context.Context, id string) error {
	prefix := "launchd-" + l.Tag + ":"
	if !strings.HasPrefix(id, prefix) {
		return ErrNotFound
	}
	if l.ReadOnly {
		return fmt.Errorf("%s is read-only", l.Name())
	}
	label := strings.TrimPrefix(id, prefix)
	path := filepath.Join(l.Dir, label+".plist")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ErrNotFound
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
