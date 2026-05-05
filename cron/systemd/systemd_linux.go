//go:build linux

package systemd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rednafi/eon/cron"
)

// SystemctlRunner runs systemctl with the given args. Tests inject a fake.
type SystemctlRunner func(ctx context.Context, args []string) ([]byte, error)

// JournalctlRunner runs journalctl with the given args.
type JournalctlRunner func(ctx context.Context, args []string) ([]byte, error)

// DefaultSystemctlRunner shells out to /usr/bin/systemctl --user.
func DefaultSystemctlRunner(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Compile-time guards: Systemd satisfies cron.Source AND cron.Mutator.
// Failed builds are preferable to "missing method" runtime panics.
var (
	_ cron.Source  = (*Systemd)(nil)
	_ cron.Mutator = (*Systemd)(nil)
)

// Systemd is a cron.Source backed by systemd timer units in a directory. User
// scope reads ~/.config/systemd/user with delete enabled; system scope reads
// /etc/systemd/system or /usr/lib/systemd/system with ReadOnly=true.
type Systemd struct {
	Dir       string
	Tag       string
	ReadOnly  bool
	Systemctl SystemctlRunner
}

// NewUser returns the standard user-scope timer source.
func NewUser() *Systemd {
	home, _ := os.UserHomeDir()
	dir := cmp.Or(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
	return &Systemd{
		Dir:       filepath.Join(dir, "systemd", "user"),
		Tag:       "user",
		Systemctl: DefaultSystemctlRunner,
	}
}

// Name implements cron.Source.
func (s *Systemd) Name() string { return "systemd-" + s.Tag }

// Scope implements cron.Source. ReadOnly marks the /etc and /usr/lib system
// timer dirs; the per-user dir stays writable.
func (s *Systemd) Scope() cron.Scope {
	if s.ReadOnly {
		return cron.ScopeSystem
	}
	return cron.ScopeUser
}

// List implements cron.Source. We read every *.timer file in Dir, then optionally
// enrich with `systemctl list-timers --all` runtime data.
func (s *Systemd) List(ctx context.Context) ([]cron.Job, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", s.Dir, err)
	}
	var jobs []cron.Job
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".timer") {
			continue
		}
		j, err := s.readTimer(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	slices.SortFunc(jobs, func(a, b cron.Job) int { return cmp.Compare(a.Name, b.Name) })
	return jobs, nil
}

// readTimer parses a *.timer file (a tiny INI-shaped format) plus its sibling
// *.service file when present. The full systemd unit grammar is large; we
// extract just the keys eon shows.
func (s *Systemd) readTimer(path string) (cron.Job, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return cron.Job{}, err
	}
	timer, _ := parseUnitMulti(string(raw))
	label := strings.TrimSuffix(filepath.Base(path), ".timer")
	servicePath := strings.TrimSuffix(path, ".timer") + ".service"
	command := ""
	if svcRaw, err := os.ReadFile(servicePath); err == nil {
		svc := parseUnit(string(svcRaw))
		command = svc["Service.ExecStart"]
	}
	command = cmp.Or(command, "(systemd unit: "+label+")")
	// firstOf returns the first non-empty entry from the slice (or "").
	// Used when a key is multi-valued in parseUnitMulti but the renderer
	// only displays one — we still want a deterministic choice.
	firstOf := func(vs []string) string {
		if len(vs) > 0 {
			return vs[0]
		}
		return ""
	}
	schedule := cmp.Or(
		firstOf(timer["Timer.OnCalendar"]),
		prefixed("every ", firstOf(timer["Timer.OnUnitActiveSec"])),
		prefixed("boot+", firstOf(timer["Timer.OnBootSec"])),
		"(no schedule)",
	)
	// Surface the additional triggers that systemd accepts but the column
	// can't show — otherwise the user sees "daily" and assumes that's the
	// only fire, even though the unit may have OnCalendar=daily plus
	// OnCalendar=hourly.
	if extras := len(timer["Timer.OnCalendar"]) - 1; extras > 0 {
		schedule = fmt.Sprintf("%s (+%d more)", schedule, extras)
	}
	return cron.Job{
		ID:       "systemd-" + s.Tag + ":" + label,
		Kind:     cron.KindSystemd,
		Name:     label,
		Schedule: schedule,
		Command:  command,
		Status:   "scheduled",
		Path:     path,
		Raw:      string(raw),
	}, nil
}

// Add implements cron.Mutator. We translate the portable schedule DSL
// into systemd's OnUnitActiveSec or OnCalendar, then write a .timer
// + .service pair into Dir. daemon-reload is best-effort.
func (s *Systemd) Add(ctx context.Context, spec cron.JobSpec) (cron.Job, error) {
	interval, err := cron.PrepareIntervalSpec(s, spec)
	if err != nil {
		return cron.Job{}, err
	}
	label := systemdLabel(spec.Command)
	timerPath := filepath.Join(s.Dir, label+".timer")
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return cron.Job{}, err
	}
	// O_CREATE|O_EXCL atomically refuses to clobber an existing timer.
	tf, err := os.OpenFile(timerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return cron.Job{}, fmt.Errorf("a timer for %q already exists at %s; use eon edit", label, timerPath)
		}
		return cron.Job{}, err
	}
	if _, err := tf.Write([]byte(renderTimer(label, interval.Every, interval.Descriptor))); err != nil {
		tf.Close()
		_ = os.Remove(timerPath)
		return cron.Job{}, err
	}
	if err := tf.Close(); err != nil {
		_ = os.Remove(timerPath)
		return cron.Job{}, err
	}
	servicePath := filepath.Join(s.Dir, label+".service")
	if err := os.WriteFile(servicePath, []byte(renderService(label, spec.Command)), 0o644); err != nil {
		_ = os.Remove(timerPath) // roll back the half-write
		return cron.Job{}, err
	}
	if s.Systemctl != nil {
		_, _ = s.Systemctl(ctx, []string{"daemon-reload"})
	}
	return s.readTimer(timerPath)
}

// Edit implements cron.Mutator. The .timer is rewritten with the new
// schedule and the .service with the new command; daemon-reload is
// best-effort like Delete.
func (s *Systemd) Edit(ctx context.Context, id string, spec cron.JobSpec) (cron.Job, error) {
	label, ok := strings.CutPrefix(id, "systemd-"+s.Tag+":")
	if !ok {
		return cron.Job{}, cron.ErrNotFound
	}
	interval, err := cron.PrepareIntervalSpec(s, spec)
	if err != nil {
		return cron.Job{}, err
	}
	timerPath := filepath.Join(s.Dir, label+".timer")
	// Open without O_CREATE so we get fs.ErrNotExist when the timer is
	// gone, instead of silently minting a new one.
	tf, err := os.OpenFile(timerPath, os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cron.Job{}, cron.ErrNotFound
		}
		return cron.Job{}, err
	}
	if _, err := tf.Write([]byte(renderTimer(label, interval.Every, interval.Descriptor))); err != nil {
		tf.Close()
		return cron.Job{}, err
	}
	if err := tf.Close(); err != nil {
		return cron.Job{}, err
	}
	servicePath := filepath.Join(s.Dir, label+".service")
	if err := os.WriteFile(servicePath, []byte(renderService(label, spec.Command)), 0o644); err != nil {
		return cron.Job{}, err
	}
	if s.Systemctl != nil {
		_, _ = s.Systemctl(ctx, []string{"daemon-reload"})
	}
	return s.readTimer(timerPath)
}

// validateSpec + systemdLabel live in parser.go (functional core).

// renderTimer + renderService live in parser.go (functional core).

// Delete implements cron.Source. We stop+disable the timer (best-effort),
// then remove the .timer and its sibling .service from disk. The unit is no
// longer scheduled after this even if daemon-reload hasn't run, because the
// file backing it is gone.
func (s *Systemd) Delete(ctx context.Context, id string) error {
	label, ok := strings.CutPrefix(id, "systemd-"+s.Tag+":")
	if !ok {
		return cron.ErrNotFound
	}
	if s.ReadOnly {
		return fmt.Errorf("%s is read-only", s.Name())
	}
	timerPath := filepath.Join(s.Dir, label+".timer")
	if s.Systemctl != nil {
		_, _ = s.Systemctl(ctx, []string{"stop", label + ".timer"})
		_, _ = s.Systemctl(ctx, []string{"disable", label + ".timer"})
	}
	if err := os.Remove(timerPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cron.ErrNotFound
		}
		return fmt.Errorf("remove %s: %w", timerPath, err)
	}
	servicePath := filepath.Join(s.Dir, label+".service")
	if err := os.Remove(servicePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", servicePath, err)
	}
	if s.Systemctl != nil {
		_, _ = s.Systemctl(ctx, []string{"daemon-reload"})
	}
	return nil
}
