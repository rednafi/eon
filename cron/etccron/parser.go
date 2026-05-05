// Pure parsing helpers for /etc/crontab and /etc/cron.d. No filesystem
// access lives here — etccron.go is the imperative shell that reads
// files and feeds bytes in.

package etccron

import (
	"fmt"
	"strings"
	"time"

	cronspec "github.com/robfig/cron/v3"

	"github.com/rednafi/eon/cron"
)

// utf8BOM is stripped per line so files saved by editors that prepend a
// byte-order mark don't poison the first key. strings.TrimSpace doesn't
// remove it.
const utf8BOM = "\uFEFF"

// parseEtcCrontab pulls cron lines out of either /etc/crontab or a
// /etc/cron.d fragment. Both share the format: comments, blank lines,
// ENV=value assignments (skipped), and "<schedule> <user> <command>"
// entries. The robfig parser is taken as a value so the caller controls
// which syntax is enabled.
//
// The returned error is whatever the line scanner reported (typically
// bufio.ErrTooLong on lines > 1MB) so callers can surface "your file was
// truncated" instead of silently returning a partial parse.
func parseEtcCrontab(p cronspec.Parser, path string, data []byte, group string) ([]cron.Job, error) {
	var jobs []cron.Job
	scanner := cron.LineScanner(string(data))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, utf8BOM))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Skip env-var assignments. They look like KEY=value with no
		// spaces before the =.
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
		if sched, err := p.Parse(schedule); err == nil {
			next := sched.Next(time.Now())
			j.NextRun = &next
		}
		jobs = append(jobs, j)
	}
	return jobs, scanner.Err()
}

// splitEtcCrontabLine pulls schedule | user | command out of a six-field
// system crontab line. Both descriptor (@daily root cmd) and 5-field
// forms are supported.
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
