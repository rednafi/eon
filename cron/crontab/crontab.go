// Package crontab is a cron.Source over the user's crontab spool. Pure
// interface implementation — main composes it; cli/tui never see the type.
package crontab

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	cronspec "github.com/robfig/cron/v3"

	"github.com/rednafi/eon/cron"
)

// Compile-time guards: Crontab satisfies cron.Source and cron.Mutator. If
// a method is renamed or its signature drifts, the package fails to build
// instead of surfacing as "missing method" at the first cron.NewManager
// or cron.Manager.Add call.
var (
	_ cron.Source  = (*Crontab)(nil)
	_ cron.Mutator = (*Crontab)(nil)
)

// CrontabRunner runs the `crontab` binary. The function returns the bytes of
// stdout for read-style invocations and may also write stdin for replace-style
// invocations. Tests inject a fake to avoid touching the real user crontab.
type CrontabRunner func(ctx context.Context, args []string, stdin string) ([]byte, error)

// DefaultCrontabRunner shells out to /usr/bin/crontab.
func DefaultCrontabRunner(ctx context.Context, args []string, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "crontab", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// `crontab -l` exits 1 with "no crontab for $user" when empty. Treat
		// that as an empty list rather than an error.
		if strings.Contains(string(out), "no crontab") {
			return nil, nil
		}
		return out, fmt.Errorf("crontab %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Crontab is a cron.Source backed by the user crontab.
type Crontab struct {
	Runner CrontabRunner
	parser cronspec.Parser
}

// New returns a Crontab source using the default shell runner.
func New() *Crontab {
	return &Crontab{
		Runner: DefaultCrontabRunner,
		// Standard 5-field crontab parser with descriptors (@daily, etc).
		parser: cronspec.NewParser(cronspec.Minute | cronspec.Hour | cronspec.Dom | cronspec.Month | cronspec.Dow | cronspec.Descriptor),
	}
}

// Name implements cron.Source.
func (c *Crontab) Name() string { return "crontab" }

// Scope implements cron.Source. The user's own crontab is always writable.
func (c *Crontab) Scope() cron.Scope { return cron.ScopeUser }

// List implements cron.Source. Each non-comment, non-blank line in the user crontab
// becomes a cron.Job. The ID is "crontab:<sha1(line)[:8]>" so deletes survive
// reordering of unrelated lines.
func (c *Crontab) List(ctx context.Context) ([]cron.Job, error) {
	out, err := c.Runner(ctx, []string{"-l"}, "")
	if err != nil {
		return nil, err
	}
	return c.parse(string(out)), nil
}

func (c *Crontab) parse(content string) []cron.Job {
	var jobs []cron.Job
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		schedule, command, ok := splitCrontabLine(trimmed)
		if !ok {
			continue
		}
		j := cron.Job{
			ID:       "crontab:" + cron.ShortHash(line),
			Kind:     cron.KindCrontab,
			Name:     cron.CommandShortName(command),
			Schedule: schedule,
			Command:  command,
			Status:   "scheduled",
			Raw:      line,
		}
		if sched, err := c.parser.Parse(schedule); err == nil {
			next := sched.Next(time.Now())
			j.NextRun = &next
		}
		jobs = append(jobs, j)
	}
	return jobs
}

// Add implements cron.Mutator. The new line is appended to the user's
// crontab and identified by its ShortHash on subsequent List() calls.
func (c *Crontab) Add(ctx context.Context, spec cron.JobSpec) (cron.Job, error) {
	if err := validateSpec(c.parser, spec); err != nil {
		return cron.Job{}, err
	}
	out, err := c.Runner(ctx, []string{"-l"}, "")
	if err != nil {
		return cron.Job{}, err
	}
	existing := strings.TrimRight(string(out), "\n")
	line := strings.TrimSpace(spec.Schedule) + " " + strings.TrimSpace(spec.Command)
	body := existing
	if body != "" {
		body += "\n"
	}
	body += line + "\n"
	if _, err := c.Runner(ctx, []string{"-"}, body); err != nil {
		return cron.Job{}, err
	}
	jobs := c.parse(line + "\n")
	if len(jobs) == 0 {
		return cron.Job{}, fmt.Errorf("crontab accepted line but reparse failed: %q", line)
	}
	return jobs[0], nil
}

// Edit implements cron.Mutator. We locate the line by its hash, replace it
// in place (preserving position relative to other lines and comments), and
// rewrite the crontab.
func (c *Crontab) Edit(ctx context.Context, id string, spec cron.JobSpec) (cron.Job, error) {
	target, ok := strings.CutPrefix(id, "crontab:")
	if !ok {
		return cron.Job{}, cron.ErrNotFound
	}
	if err := validateSpec(c.parser, spec); err != nil {
		return cron.Job{}, err
	}
	out, err := c.Runner(ctx, []string{"-l"}, "")
	if err != nil {
		return cron.Job{}, err
	}
	newLine := strings.TrimSpace(spec.Schedule) + " " + strings.TrimSpace(spec.Command)
	var (
		kept    []string
		matched bool
	)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !matched && trimmed != "" && !strings.HasPrefix(trimmed, "#") && cron.ShortHash(line) == target {
			kept = append(kept, newLine)
			matched = true
			continue
		}
		kept = append(kept, line)
	}
	if !matched {
		return cron.Job{}, cron.ErrNotFound
	}
	replacement := strings.Join(kept, "\n") + "\n"
	if _, err := c.Runner(ctx, []string{"-"}, replacement); err != nil {
		return cron.Job{}, err
	}
	jobs := c.parse(newLine + "\n")
	if len(jobs) == 0 {
		return cron.Job{}, fmt.Errorf("crontab accepted line but reparse failed: %q", newLine)
	}
	return jobs[0], nil
}

// validateSpec rejects empty fields and unparseable schedules. Sources
// must catch this *before* writing — silently accepting a bad spec is
// worse than failing fast.
func validateSpec(p cronspec.Parser, spec cron.JobSpec) error {
	if strings.TrimSpace(spec.Schedule) == "" {
		return fmt.Errorf("schedule must not be empty")
	}
	if strings.TrimSpace(spec.Command) == "" {
		return fmt.Errorf("command must not be empty")
	}
	if strings.ContainsAny(spec.Command, "\r\n") {
		return fmt.Errorf("command must not contain newlines")
	}
	if _, err := p.Parse(strings.TrimSpace(spec.Schedule)); err != nil {
		return fmt.Errorf("invalid schedule %q: %w", spec.Schedule, err)
	}
	return nil
}

// Delete implements cron.Source. The line is identified by its ID hash so we don't
// rely on positional indices that change as users edit the crontab manually.
func (c *Crontab) Delete(ctx context.Context, id string) error {
	target, ok := strings.CutPrefix(id, "crontab:")
	if !ok {
		return cron.ErrNotFound
	}
	out, err := c.Runner(ctx, []string{"-l"}, "")
	if err != nil {
		return err
	}
	var (
		kept    []string
		matched bool
	)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && cron.ShortHash(line) == target {
			matched = true
			continue
		}
		kept = append(kept, line)
	}
	if !matched {
		return cron.ErrNotFound
	}
	// crontab needs a trailing newline or it complains on some systems.
	replacement := strings.Join(kept, "\n")
	if replacement != "" {
		replacement += "\n"
	}
	if replacement == "" {
		// `crontab -r` removes the crontab entirely.
		_, err = c.Runner(ctx, []string{"-r"}, "")
		return err
	}
	_, err = c.Runner(ctx, []string{"-"}, replacement)
	return err
}

// splitCrontabLine separates the schedule expression from the command. It
// supports both 5-field and descriptor (@daily, @reboot, ...) syntax.
// utf8BOM is the byte-order mark some editors prepend to UTF-8 files.
// strings.TrimSpace doesn't remove it, so we strip it explicitly.
const utf8BOM = "\uFEFF"

func splitCrontabLine(line string) (schedule, command string, ok bool) {
	line = strings.TrimSpace(strings.TrimPrefix(line, utf8BOM))
	if strings.HasPrefix(line, "@") {
		// "@daily<sep>cmd" — split on first run of whitespace (space OR tab),
		// not just space; some editors save crontabs with TAB separators.
		i := strings.IndexAny(line, " \t")
		if i < 0 {
			return "", "", false
		}
		cmd := strings.TrimSpace(line[i:])
		if cmd == "" {
			return "", "", false
		}
		return line[:i], cmd, true
	}
	// 5 fields then command. Fields can contain commas/dashes/slashes but not
	// spaces, so a simple field-walk is sufficient. We must use a C-style
	// loop here — `for i := range len(line)` ignores mutations to i, which we
	// rely on for the whitespace skip.
	fields := 0
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			continue
		}
		j := i
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		fields++
		if fields == 5 {
			return strings.Join(strings.Fields(line[:i]), " "), strings.TrimSpace(line[j:]), true
		}
		i = j - 1
	}
	return "", "", false
}
