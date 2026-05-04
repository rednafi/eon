//go:build linux

package etccron

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"cmp"
	"slices"
	"strings"
	"time"

	cronspec "github.com/robfig/cron/v3"

	"github.com/rednafi/eon/cron"
)

// EtcCron is a read-only cron.Source for the /etc/crontab spool and /etc/cron.d
// drop-in directory. These crontabs use a six-field syntax — the same five
// schedule fields plus an explicit user column — which the per-user crontab
// parser doesn't handle. We keep this source distinct so that subtle parser
// bugs in one don't bleed into the other.
type EtcCron struct {
	// MainPath is the /etc/crontab single-file source.
	MainPath string
	// DropInDir is the /etc/cron.d directory of fragment files.
	DropInDir string
	parser    cronspec.Parser
}

// New returns a source for the standard system locations.
func New() *EtcCron {
	return &EtcCron{
		MainPath:  "/etc/crontab",
		DropInDir: "/etc/cron.d",
		parser:    cronspec.NewParser(cronspec.Minute | cronspec.Hour | cronspec.Dom | cronspec.Month | cronspec.Dow | cronspec.Descriptor),
	}
}

// Name implements cron.Source.
func (e *EtcCron) Name() string { return "crontab-system" }

// cron.Scope implements cron.Source. /etc/crontab and /etc/cron.d are owned by root or
// the package manager, so we never offer to mutate them.
func (e *EtcCron) Scope() cron.Scope { return cron.ScopeSystem }

// List implementsSource. Errors reading individual files are tolerated —
// surfacing the readable ones is more useful than failing the whole list.
func (e *EtcCron) List(_ context.Context) ([]cron.Job, error) {
	var jobs []cron.Job
	if data, err := os.ReadFile(e.MainPath); err == nil {
		jobs = append(jobs, e.parseFile(e.MainPath, data, "main")...)
	}
	if entries, err := os.ReadDir(e.DropInDir); err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			// Cron's run-parts skips anything with a "." in the name (config
			// files etc) — match that behaviour to avoid surfacing things
			// cron itself ignores.
			if strings.Contains(ent.Name(), ".") {
				continue
			}
			full := filepath.Join(e.DropInDir, ent.Name())
			if data, err := os.ReadFile(full); err == nil {
				jobs = append(jobs, e.parseFile(full, data, ent.Name())...)
			}
		}
	}
	slices.SortFunc(jobs, func(a, b cron.Job) int { return cmp.Compare(a.ID, b.ID) })
	return jobs, nil
}

// Delete is a no-op: /etc/crontab and /etc/cron.d entries are owned by root
// or the system package manager. Returning cron.ErrNotFound rather than a
// permission error lets the cron.Manager fall through to whichever source does
// own the ID.
func (e *EtcCron) Delete(_ context.Context, _ string) error {
	return cron.ErrNotFound
}

// parseFile pulls cron lines out of either /etc/crontab or a /etc/cron.d
// fragment. Both share the format: comments, blank lines, ENV=value
// assignments (skipped), and "<schedule> <user> <command>" entries.
func (e *EtcCron) parseFile(path string, data []byte, group string) []cron.Job {
	var jobs []cron.Job
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Skip env-var assignments. They look like KEY=value with no spaces
		// before the =.
		if i := strings.Index(trimmed, "="); i > 0 && !strings.ContainsAny(trimmed[:i], " \t") {
			continue
		}
		schedule, user, command, ok := splitEtcCrontabLine(trimmed)
		if !ok {
			continue
		}
		j := cron.Job{
			ID:       "crontab-system:" + cron.ShortHash(group+"|"+line),
			Kind:     cron.KindCrontab,
			Name:     group + ":" + cron.CommandShortName(command),
			Schedule: schedule,
			Command:  fmt.Sprintf("[%s] %s", user, command),
			Status:   "scheduled",
			Path:     path,
			Raw:      line,
		}
		if sched, err := e.parser.Parse(schedule); err == nil {
			next := sched.Next(time.Now())
			j.NextRun = &next
		}
		jobs = append(jobs, j)
	}
	return jobs
}

// splitEtcCrontabLine pulls schedule | user | command out of a six-field
// system crontab line. Both descriptor (@daily root cmd) and 5-field forms
// are supported.
func splitEtcCrontabLine(line string) (schedule, user, command string, ok bool) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "@") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			return "", "", "", false
		}
		return parts[0], parts[1], strings.Join(parts[2:], " "), true
	}
	parts := strings.Fields(line)
	if len(parts) < 7 {
		return "", "", "", false
	}
	schedule = strings.Join(parts[:5], " ")
	user = parts[5]
	command = strings.Join(parts[6:], " ")
	return schedule, user, command, true
}
