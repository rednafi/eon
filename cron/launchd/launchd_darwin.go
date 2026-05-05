//go:build darwin

// Package launchd is a cron.Source over user and system LaunchAgents on
// macOS. The functional core (plist parsing, schedule rendering) lives in
// parser.go and is testable cross-platform; this file is the imperative
// shell that drives os/exec, os.ReadFile, etc.

package launchd

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/rednafi/eon/cron"
)

// Compile-time guards: Launchd satisfies cron.Source AND, when not
// read-only, cron.Mutator. Failed builds are preferable to "missing
// method" runtime panics.
var (
	_ cron.Source  = (*Launchd)(nil)
	_ cron.Mutator = (*Launchd)(nil)
)

// LaunchctlRunner executes launchctl with the given args. It returns
// combined output and an error. Tests inject a fake to avoid mutating
// system state.
type LaunchctlRunner func(ctx context.Context, args []string) ([]byte, error)

// Launchd is a cron.Source backed by user launchd agents in a directory
// of plist files (default ~/Library/LaunchAgents). Multiple Launchd
// instances may be composed (one per directory) — see NewUser /
// NewSystem.
type Launchd struct {
	// Dir is the directory containing the .plist files this source
	// manages.
	Dir string
	// Tag is appended to job IDs to disambiguate sources reading from
	// different directories ("user", "system", "daemons").
	Tag string
	// ReadOnly disables Delete. System-level directories use this.
	ReadOnly bool
	// Runner runs launchctl. May be nil to skip launchctl entirely
	// (useful for tests over a tmpdir).
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

// Scope implements cron.Source. ReadOnly distinguishes the user's
// LaunchAgents (writable) from the /Library and /System/Library
// locations (system-scope).
func (l *Launchd) Scope() cron.Scope {
	if l.ReadOnly {
		return cron.ScopeSystem
	}
	return cron.ScopeUser
}

// List implements cron.Source. Missing or unreadable plists are skipped
// silently; a partial directory shouldn't break listing. Reads run in
// parallel with a small worker pool — /System/Library/LaunchDaemons can
// have hundreds of plists and serial reads dominate startup.
func (l *Launchd) List(ctx context.Context) ([]cron.Job, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", l.Dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		paths = append(paths, filepath.Join(l.Dir, e.Name()))
	}
	results := make([]cron.Job, len(paths))
	ok := make([]bool, len(paths))
	// Each List call gets its own errgroup with cron.FanoutLimit. A
	// per-call budget avoids the nested-acquire deadlock you'd hit if
	// Manager.List and Launchd.List shared a global semaphore: the
	// Manager-level slot would be held while Launchd's children waited
	// for slots in the same pool.
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(cron.FanoutLimit)
	for i, full := range paths {
		eg.Go(func() error {
			j, err := l.readPlist(full)
			if err != nil {
				return nil // skip unreadable plists; partial dir is fine
			}
			results[i] = j
			ok[i] = true
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err // ctx cancelled mid-fanout — surface, don't swallow
	}

	var jobs []cron.Job
	// seen prevents duplicate Job.IDs when two plist files in the same
	// directory share a <Label>. The user can fix the underlying
	// conflict from the filesystem; the listing should at least be
	// deterministic — iterating `paths` in source order keeps it that
	// way regardless of goroutine scheduling.
	seen := map[string]bool{}
	for i, j := range results {
		if !ok[i] || seen[j.ID] {
			continue
		}
		seen[j.ID] = true
		jobs = append(jobs, j)
	}
	if l.Runner != nil {
		l.enrich(ctx, jobs)
	}
	slices.SortFunc(jobs, func(a, b cron.Job) int { return cmp.Compare(a.Name, b.Name) })
	return jobs, nil
}

// readPlist is the imperative wrapper around the pure parsePlist: one
// read, one decode, plus a best-effort os.Stat for LastRun.
func (l *Launchd) readPlist(path string) (cron.Job, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return cron.Job{}, err
	}
	j, err := parsePlist(raw, l.Tag, path)
	if err != nil {
		return cron.Job{}, err
	}
	if j.StdoutPath != "" {
		if info, err := os.Stat(j.StdoutPath); err == nil {
			t := info.ModTime()
			j.LastRun = &t
		}
	}
	return j, nil
}

// enrich queries `launchctl list` once and overlays PID/exit-code onto
// jobs. `launchctl list` columns are: PID Status Label.
func (l *Launchd) enrich(ctx context.Context, jobs []cron.Job) {
	out, err := l.Runner(ctx, []string{"list"})
	if err != nil {
		return
	}
	type runtime struct {
		PID, Status int
		// haveStatus distinguishes "exited 0" (real run, success) from
		// "never run" (status column was "-"). Without it, never-run
		// jobs show as `exited 0`, which is misleading.
		haveStatus bool
	}
	byLabel := map[string]runtime{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	header := true // first non-blank line is the column header
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if header {
			header = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		var r runtime
		if fields[0] != "-" {
			r.PID, _ = strconv.Atoi(fields[0])
		}
		if fields[1] != "-" {
			r.Status, _ = strconv.Atoi(fields[1])
			r.haveStatus = true
		}
		byLabel[fields[2]] = r
	}
	for i := range jobs {
		if e, ok := byLabel[jobs[i].Name]; ok {
			jobs[i].PID = e.PID
			jobs[i].LastExitCode = e.Status
			switch {
			case e.PID > 0:
				jobs[i].Status = "running"
			case !e.haveStatus:
				// Status column was "-": the job has never run since last
				// load.
				jobs[i].Status = "never run"
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
	interval, err := cron.PrepareIntervalSpec(l, spec)
	if err != nil {
		return cron.Job{}, err
	}
	label := launchdLabel(spec.Command)
	path := filepath.Join(l.Dir, label+".plist")
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return cron.Job{}, err
	}
	body := renderPlist(label, spec.Command, interval)
	// O_CREATE|O_EXCL: the kernel atomically refuses to clobber an
	// existing plist, replacing the prior os.Stat-then-WriteFile race.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return cron.Job{}, fmt.Errorf("a plist for %q already exists at %s; use eon edit", label, path)
		}
		return cron.Job{}, err
	}
	if _, err := f.Write([]byte(body)); err != nil {
		f.Close()
		return cron.Job{}, err
	}
	if err := f.Close(); err != nil {
		return cron.Job{}, err
	}
	return l.readPlist(path)
}

// Edit implements cron.Mutator. We rewrite the plist for the given ID
// with the new schedule and command, preserving the file path.
func (l *Launchd) Edit(_ context.Context, id string, spec cron.JobSpec) (cron.Job, error) {
	label, ok := strings.CutPrefix(id, "launchd-"+l.Tag+":")
	if !ok {
		return cron.Job{}, cron.ErrNotFound
	}
	interval, err := cron.PrepareIntervalSpec(l, spec)
	if err != nil {
		return cron.Job{}, err
	}
	path := filepath.Join(l.Dir, label+".plist")
	body := renderPlist(label, spec.Command, interval)
	// O_RDWR|O_TRUNC without O_CREATE refuses to mint a new file —
	// matches the prior os.Stat-then-WriteFile in one atomic step.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cron.Job{}, cron.ErrNotFound
		}
		return cron.Job{}, err
	}
	if _, err := f.Write([]byte(body)); err != nil {
		f.Close()
		return cron.Job{}, err
	}
	if err := f.Close(); err != nil {
		return cron.Job{}, err
	}
	return l.readPlist(path)
}

// Delete implements cron.Source. ReadOnly sources reject the call.
func (l *Launchd) Delete(ctx context.Context, id string) error {
	label, ok := strings.CutPrefix(id, "launchd-"+l.Tag+":")
	if !ok {
		return cron.ErrNotFound
	}
	if l.ReadOnly {
		return fmt.Errorf("%s: %w", l.Name(), cron.ErrReadOnly)
	}
	path := filepath.Join(l.Dir, label+".plist")
	// Best-effort unload: ignore failure (the agent may not be loaded).
	if l.Runner != nil {
		_, _ = l.Runner(ctx, []string{"unload", path})
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cron.ErrNotFound
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
