// Package cli implements eon's non-interactive subcommands.
//
// The TUI is the default surface, but a CLI is essential for piping into
// shell scripts and running over SSH where a TUI is unwelcome. Every command
// here works with `--json` so other tools can consume eon's output.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rednafi/eon/internal/origin"
)

// Run dispatches a subcommand. argv must NOT include the program name.
// stdin/stdout/stderr are explicit so tests can capture them.
func Run(ctx context.Context, mgr *origin.Manager, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		return runList(ctx, mgr, nil, stdout, stderr)
	}
	cmd, rest := argv[0], argv[1:]
	switch cmd {
	case "list", "ls":
		return runList(ctx, mgr, rest, stdout, stderr)
	case "show":
		return runShow(ctx, mgr, rest, stdout, stderr)
	case "logs", "log":
		return runLogs(ctx, mgr, rest, stdout, stderr)
	case "delete", "rm":
		return runDelete(ctx, mgr, rest, stdin, stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, helpText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, helpText)
		return 2
	}
}

// Version is the build-time-injected eon version. Defaults to "dev" so local
// builds don't lie about being a release.
var Version = "dev"

// reorderFlags moves all -- and - prefixed args to the front so that the
// stdlib flag package sees them. We do this rather than switch to a heavier
// parser because the only thing eon needs is "flags can appear after
// positionals" and that's three lines of code.
func reorderFlags(argv []string) []string {
	var flags, pos []string
	skipNext := false
	for i, a := range argv {
		if skipNext {
			flags = append(flags, a)
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" && a != "--" {
			flags = append(flags, a)
			// If the flag name has no "=" and the next token isn't another
			// flag, it might be a value. We can't perfectly know without
			// the FlagSet, so we just keep both contiguous; flag.Parse
			// handles them.
			if !strings.Contains(a, "=") && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				skipNext = true
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

const helpText = `eon — local cron monitor

Usage:
  eon                       launch the TUI (default)
  eon list [--all] [--json] list known crons (user-scope by default)
  eon show <id> [--json]    show details for a single cron
  eon logs <id> [-n <N>]    print the last N lines of a cron's stdout/stderr
  eon delete <id> [--yes]   stop and remove a cron (prompts unless --yes)
  eon version
  eon help

By default eon shows only user-scope jobs (your crontab, your launchd/systemd
user units). Pass --all to also surface read-only system jobs from
/Library/Launch*, /etc/crontab, /etc/cron.d, and /etc/systemd/system.

IDs:
  Pass either the full ID (e.g. "launchd-user:com.foo.bar") or any unique
  case-insensitive substring of the ID, name, or command (e.g. "stremio").
`

func runList(ctx context.Context, mgr *origin.Manager, argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	all := fs.Bool("all", false, "include read-only system jobs (/Library/Launch*, /etc/cron.d, /etc/systemd/system)")
	if err := fs.Parse(reorderFlags(argv)); err != nil {
		return 2
	}
	jobs, errs := mgr.List(ctx)
	for _, e := range errs {
		fmt.Fprintf(stderr, "warning: %v\n", e)
	}
	if !*all {
		jobs = filterUser(jobs)
	}
	if *asJSON {
		return encodeJSON(stdout, jobs)
	}
	renderTable(stdout, jobs)
	return 0
}

// filterUser drops Job.System=true entries, leaving only the read-write
// user-scope jobs.
func filterUser(jobs []origin.Job) []origin.Job {
	out := make([]origin.Job, 0, len(jobs))
	for _, j := range jobs {
		if !j.System {
			out = append(out, j)
		}
	}
	return out
}

func runShow(ctx context.Context, mgr *origin.Manager, argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of formatted text")
	if err := fs.Parse(reorderFlags(argv)); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "show: missing <id>")
		return 2
	}
	j, err := mgr.Find(ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "show: %v\n", err)
		return 1
	}
	if *asJSON {
		return encodeJSON(stdout, j)
	}
	renderJobDetail(stdout, j)
	return 0
}

func runLogs(ctx context.Context, mgr *origin.Manager, argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 100, "max lines per stream")
	if err := fs.Parse(reorderFlags(argv)); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "logs: missing <id>")
		return 2
	}
	j, err := mgr.Find(ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "logs: %v\n", err)
		return 1
	}
	wrote := false
	for _, p := range []struct{ label, path string }{
		{"stdout", j.StdoutPath},
		{"stderr", j.StderrPath},
	} {
		if p.path == "" {
			continue
		}
		fmt.Fprintf(stdout, "── %s (%s) ──\n", p.label, p.path)
		if err := tail(stdout, p.path, *n); err != nil {
			fmt.Fprintf(stderr, "logs: %s: %v\n", p.label, err)
		}
		wrote = true
	}
	if !wrote {
		fmt.Fprintf(stdout, "no log paths configured for %s\n", j.ID)
	}
	return 0
}

func runDelete(ctx context.Context, mgr *origin.Manager, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := fs.Parse(reorderFlags(argv)); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "delete: missing <id>")
		return 2
	}
	j, err := mgr.Find(ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "delete: %v\n", err)
		return 1
	}
	if !*yes {
		fmt.Fprintf(stdout, "Delete %s (%s)? [y/N] ", j.ID, j.Schedule)
		if !confirm(stdin) {
			fmt.Fprintln(stdout, "aborted")
			return 1
		}
	}
	if err := mgr.Delete(ctx, j.ID); err != nil {
		fmt.Fprintf(stderr, "delete: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "deleted %s\n", j.ID)
	return 0
}

// confirm reads a single line from stdin and returns true on "y"/"yes".
func confirm(r io.Reader) bool {
	if r == nil {
		return false
	}
	buf := make([]byte, 4)
	n, _ := r.Read(buf)
	if n == 0 {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(string(buf[:n])))
	return resp == "y" || resp == "yes"
}

// renderTable prints the job list as a fixed-width table sized to the data.
// We deliberately avoid pulling in a tablewriter dependency — the column
// layout is fixed (5 columns, predictable widths) so a hand-rolled writer is
// trivial and keeps eon's binary small.
func renderTable(w io.Writer, jobs []origin.Job) {
	if len(jobs) == 0 {
		fmt.Fprintln(w, "(no scheduled jobs)")
		return
	}
	headers := []string{"ID", "KIND", "NAME", "SCHEDULE", "STATUS"}
	rows := make([][]string, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, []string{j.ID, string(j.Kind), j.Name, j.Schedule, j.Status})
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	// Cap the ID column at 56 so a runaway label doesn't wreck the layout.
	if widths[0] > 56 {
		widths[0] = 56
	}
	writeRow := func(cells []string) {
		var b strings.Builder
		for i, c := range cells {
			if len(c) > widths[i] {
				c = c[:widths[i]-1] + "…"
			}
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len(c)+2))
			}
		}
		fmt.Fprintln(w, b.String())
	}
	writeRow(headers)
	for _, r := range rows {
		writeRow(r)
	}
}

func renderJobDetail(w io.Writer, j origin.Job) {
	fmt.Fprintf(w, "ID:        %s\n", j.ID)
	fmt.Fprintf(w, "Kind:      %s\n", j.Kind)
	fmt.Fprintf(w, "Name:      %s\n", j.Name)
	fmt.Fprintf(w, "Schedule:  %s\n", j.Schedule)
	fmt.Fprintf(w, "Status:    %s\n", j.Status)
	if j.PID != 0 {
		fmt.Fprintf(w, "PID:       %d\n", j.PID)
	}
	if j.LastRun != nil {
		fmt.Fprintf(w, "Last run:  %s\n", j.LastRun.Format(time.RFC3339))
	}
	if j.NextRun != nil {
		fmt.Fprintf(w, "Next run:  %s\n", j.NextRun.Format(time.RFC3339))
	}
	if j.Path != "" {
		fmt.Fprintf(w, "Path:      %s\n", j.Path)
	}
	if j.StdoutPath != "" {
		fmt.Fprintf(w, "Stdout:    %s\n", j.StdoutPath)
	}
	if j.StderrPath != "" {
		fmt.Fprintf(w, "Stderr:    %s\n", j.StderrPath)
	}
	fmt.Fprintf(w, "Command:   %s\n", j.Command)
}

func encodeJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// tail prints the last n lines of path. We chunk-read from the end so a
// 1 GB log doesn't get slurped into memory just to print 100 lines.
func tail(w io.Writer, path string, n int) error {
	if n <= 0 {
		n = 100
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	const chunk = 16 * 1024
	size := stat.Size()
	var (
		buf   []byte
		lines int
		off   = size
	)
	for off > 0 && lines <= n {
		read := min(int64(chunk), off)
		off -= read
		piece := make([]byte, read)
		if _, err := f.ReadAt(piece, off); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		buf = append(piece, buf...)
		lines = strings.Count(string(buf), "\n")
	}
	all := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, line := range all {
		fmt.Fprintln(w, line)
	}
	return nil
}

// SortedJobs is a small helper used by tests to assert deterministic order.
func SortedJobs(jobs []origin.Job) []origin.Job {
	cp := append([]origin.Job(nil), jobs...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	return cp
}

// LogPathsFor returns a human-friendly hint for "logs" output when no log
// paths are configured. Exposed for the TUI to share the same heuristic.
func LogPathsFor(j origin.Job) []string {
	var paths []string
	for _, p := range []string{j.StdoutPath, j.StderrPath} {
		if p == "" {
			continue
		}
		if filepath.IsAbs(p) {
			paths = append(paths, p)
		}
	}
	return paths
}
