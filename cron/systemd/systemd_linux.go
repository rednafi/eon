//go:build linux

package systemd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"cmp"
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
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config")
		}
	}
	return &Systemd{
		Dir:       filepath.Join(dir, "systemd", "user"),
		Tag:       "user",
		Systemctl: DefaultSystemctlRunner,
	}
}

// Name implements cron.Source.
func (s *Systemd) Name() string { return "systemd-" + s.Tag }

// cron.Scope implements cron.Source. ReadOnly marks the /etc and /usr/lib system
// timer dirs; the per-user dir stays writable.
func (s *Systemd) Scope() cron.Scope {
	if s.ReadOnly {
		return cron.ScopeSystem
	}
	return cron.ScopeUser
}

// List implementsSource. We read every *.timer file in Dir, then optionally
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
	if command == "" {
		command = "(systemd unit: " + label + ")"
	}
	schedule := timer["Timer.OnCalendar"]
	if schedule == "" {
		if v := timer["Timer.OnUnitActiveSec"]; v != "" {
			schedule = "every " + v
		} else if v := timer["Timer.OnBootSec"]; v != "" {
			schedule = "boot+" + v
		} else {
			schedule = "(no schedule)"
		}
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

// parseUnit reads a systemd unit file into a flat map keyed by "Section.Key".
// Multi-line values and continuations aren't supported — eon doesn't need
// them, and ignoring them keeps the parser tiny and predictable.
func parseUnit(content string) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			out[section+"."+k] = v
		}
	}
	return out
}

// Delete implementsSource. We stop+disable the timer (best-effort), then
// remove the .timer and matching .service file from disk. The unit is no
// longer scheduled after this even if `daemon-reload` hasn't run, because
// the file backing it is gone.
func (s *Systemd) Delete(ctx context.Context, id string) error {
	prefix := "systemd-" + s.Tag + ":"
	if !strings.HasPrefix(id, prefix) {
		return cron.ErrNotFound
	}
	if s.ReadOnly {
		return fmt.Errorf("%s is read-only", s.Name())
	}
	label := strings.TrimPrefix(id, prefix)
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
