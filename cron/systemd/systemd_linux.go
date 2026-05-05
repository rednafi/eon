//go:build linux

package systemd

import (
	"cmp"
	"context"
	"fmt"
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
		if os.IsNotExist(err) {
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
	timer := parseUnit(string(raw))
	label := strings.TrimSuffix(filepath.Base(path), ".timer")
	servicePath := strings.TrimSuffix(path, ".timer") + ".service"
	command := ""
	if svcRaw, err := os.ReadFile(servicePath); err == nil {
		svc := parseUnit(string(svcRaw))
		command = svc["Service.ExecStart"]
	}
	command = cmp.Or(command, "(systemd unit: "+label+")")
	schedule := cmp.Or(
		timer["Timer.OnCalendar"],
		prefixed("every ", timer["Timer.OnUnitActiveSec"]),
		prefixed("boot+", timer["Timer.OnBootSec"]),
		"(no schedule)",
	)
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
	if s.ReadOnly {
		return cron.Job{}, fmt.Errorf("%s is read-only", s.Name())
	}
	if err := validateSpec(spec); err != nil {
		return cron.Job{}, err
	}
	interval, err := cron.ParseScheduleInterval(spec.Schedule)
	if err != nil {
		return cron.Job{}, err
	}
	label := systemdLabel(spec.Command)
	timerPath := filepath.Join(s.Dir, label+".timer")
	if _, err := os.Stat(timerPath); err == nil {
		return cron.Job{}, fmt.Errorf("a timer for %q already exists at %s; use eon edit", label, timerPath)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return cron.Job{}, err
	}
	if err := os.WriteFile(timerPath, []byte(renderTimer(label, interval)), 0o644); err != nil {
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
	if s.ReadOnly {
		return cron.Job{}, fmt.Errorf("%s is read-only", s.Name())
	}
	if err := validateSpec(spec); err != nil {
		return cron.Job{}, err
	}
	interval, err := cron.ParseScheduleInterval(spec.Schedule)
	if err != nil {
		return cron.Job{}, err
	}
	timerPath := filepath.Join(s.Dir, label+".timer")
	if _, err := os.Stat(timerPath); os.IsNotExist(err) {
		return cron.Job{}, cron.ErrNotFound
	} else if err != nil {
		return cron.Job{}, err
	}
	if err := os.WriteFile(timerPath, []byte(renderTimer(label, interval)), 0o644); err != nil {
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

func systemdLabel(command string) string {
	short := cron.CommandShortName(command)
	short = strings.ReplaceAll(short, "/", "-")
	if short == "" {
		short = "job"
	}
	return "eon-" + short
}

// renderTimer emits a minimal [Unit]+[Timer]+[Install] body. We prefer
// OnCalendar for descriptors (systemd parses "hourly", "daily" etc.
// natively) and OnUnitActiveSec for "@every <duration>".
func renderTimer(label string, interval cron.ScheduleInterval) string {
	var sched string
	switch {
	case interval.Every > 0:
		sched = fmt.Sprintf("OnUnitActiveSec=%s\nOnBootSec=%s", interval.Every, interval.Every)
	case interval.Descriptor != "":
		sched = "OnCalendar=" + interval.Descriptor
	}
	return fmt.Sprintf(`[Unit]
Description=eon-managed timer for %s

[Timer]
%s
Persistent=true

[Install]
WantedBy=timers.target
`, label, sched)
}

func renderService(label, command string) string {
	return fmt.Sprintf(`[Unit]
Description=eon-managed service for %s

[Service]
Type=oneshot
ExecStart=%s
`, label, command)
}

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
	if _, err := os.Stat(timerPath); os.IsNotExist(err) {
		return cron.ErrNotFound
	} else if err != nil {
		return err
	}
	if s.Systemctl != nil {
		_, _ = s.Systemctl(ctx, []string{"stop", label + ".timer"})
		_, _ = s.Systemctl(ctx, []string{"disable", label + ".timer"})
	}
	if err := os.Remove(timerPath); err != nil {
		return fmt.Errorf("remove %s: %w", timerPath, err)
	}
	servicePath := filepath.Join(s.Dir, label+".service")
	if _, err := os.Stat(servicePath); err == nil {
		_ = os.Remove(servicePath)
	}
	if s.Systemctl != nil {
		_, _ = s.Systemctl(ctx, []string{"daemon-reload"})
	}
	return nil
}
