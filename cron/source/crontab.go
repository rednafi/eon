package source

import "github.com/rednafi/eon/cron"

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"

	cronspec "github.com/robfig/cron/v3"
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

// NewCrontab returns a Crontab source using the default shell runner.
func NewCrontab() *Crontab {
	return &Crontab{
		Runner: DefaultCrontabRunner,
		// Standard 5-field crontab parser with descriptors (@daily, etc).
		parser: cronspec.NewParser(cronspec.Minute | cronspec.Hour | cronspec.Dom | cronspec.Month | cronspec.Dow | cronspec.Descriptor),
	}
}

// Name implements cron.Source.
func (c *Crontab) Name() string { return "crontab" }

// cron.Scope implements cron.Source. The user's own crontab is always writable.
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
			ID:       "crontab:" + shortHash(line),
			Kind:     cron.KindCrontab,
			Name:     commandShortName(command),
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

// Delete implements cron.Source. The line is identified by its ID hash so we don't
// rely on positional indices that change as users edit the crontab manually.
func (c *Crontab) Delete(ctx context.Context, id string) error {
	if !strings.HasPrefix(id, "crontab:") {
		return cron.ErrNotFound
	}
	target := strings.TrimPrefix(id, "crontab:")
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
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && shortHash(line) == target {
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
func splitCrontabLine(line string) (schedule, command string, ok bool) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "@") {
		// "@daily cmd" — schedule is the first token.
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			return "", "", false
		}
		return parts[0], strings.TrimSpace(parts[1]), true
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

// commandShortName returns a readable name for a crontab command. We strip
// `env=...` prefixes and take the basename of the first program token.
func commandShortName(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if strings.Contains(tok, "=") {
			continue
		}
		if i := strings.LastIndex(tok, "/"); i >= 0 {
			return tok[i+1:]
		}
		return tok
	}
	return cmd
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:4])
}
