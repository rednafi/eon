package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rednafi/eon"
)

// writeJSON serialises v with a trailing newline. Stdout-only: errors
// emitted to JSON go to stderr through a separate helper so machine
// consumers can rely on stdout being exclusively data.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// nameColMax caps the NAME column in the ls table. Default names
// derived from long shell commands would otherwise blow out the
// alignment; the full text is still queryable via `eon show` and
// `eon ls --json`.
const nameColMax = 32

// writeJobsTable formats jobs as a tab-aligned table. Times are
// rendered in the local timezone; ID and Kind columns stay narrow.
//
// Columns: STATUS is the *job* state (enabled/disabled/done);
// RESULT is the *last run* state (ok/fail/skipped_overlap). LAST
// RUN is the timestamp of that last run.
func writeJobsTable(w io.Writer, jobs []eon.Job) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tNAME\tSCHEDULE\tSTATUS\tLAST RUN\tRESULT")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Kind, truncate(j.Name, nameColMax), scheduleSummary(j),
			j.Status, fmtTimeOrDash(j.LastRunAt), orDash(string(j.LastStatus)),
		)
	}
	tw.Flush()
}

// truncate clamps s to at most max runes, using a trailing ellipsis
// when it overflows. Operates on runes so multi-byte characters
// (CJK, emoji) don't get sliced mid-character.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

func writeJobDetail(w io.Writer, j eon.Job) {
	fmt.Fprintf(w, "id:           %s\n", j.ID)
	fmt.Fprintf(w, "kind:         %s\n", j.Kind)
	fmt.Fprintf(w, "name:         %s\n", j.Name)
	fmt.Fprintf(w, "command:      %s\n", strings.Join(j.Command, " "))
	fmt.Fprintf(w, "schedule:     %s\n", scheduleSummary(j))
	fmt.Fprintf(w, "status:       %s\n", j.Status)
	fmt.Fprintf(w, "last run:     %s\n", fmtTimeOrDash(j.LastRunAt))
	fmt.Fprintf(w, "last result:  %s\n", orDash(string(j.LastStatus)))
	fmt.Fprintf(w, "created:      %s\n", j.CreatedAt.Local().Format(time.RFC3339))
	fmt.Fprintf(w, "updated:      %s\n", j.UpdatedAt.Local().Format(time.RFC3339))
}

func writeStatus(w io.Writer, s eon.Status) {
	supervised := "no"
	if s.Daemon.Supervised {
		supervised = "yes"
	}
	if s.Daemon.Running {
		fmt.Fprintf(w, "daemon:     running  pid=%d started=%s supervised=%s\n",
			s.Daemon.PID, s.Daemon.StartedAt.Local().Format(time.RFC3339), supervised)
	} else {
		fmt.Fprintf(w, "daemon:     stopped  supervised=%s\n", supervised)
	}
	fmt.Fprintf(w, "data dir:   %s\n", s.DataDir)
	fmt.Fprintf(w, "database:   %s\n", s.DBPath)
	fmt.Fprintf(w, "jobs:       total=%d  cron=%d  oneshot pending=%d  oneshot done=%d\n",
		s.Jobs.Total, s.Jobs.Cron, s.Jobs.OneshotPending, s.Jobs.OneshotDone)
}

func scheduleSummary(j eon.Job) string {
	switch j.Kind {
	case eon.KindCron:
		return j.Cron
	case eon.KindOneshot:
		return "at " + j.FireAt.Local().Format(time.RFC3339)
	}
	return ""
}

func fmtTimeOrDash(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.RFC3339)
}

func orDash(s string) string { return cmp.Or(s, "-") }
