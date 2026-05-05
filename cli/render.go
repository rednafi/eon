package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/rednafi/eon/cron"
)

// runeWidth measures s in display cells, not raw runes. CJK glyphs occupy
// two cells and combining marks zero, so utf8.RuneCountInString would
// misalign the table the moment a non-ASCII character appears. mattn's
// runewidth is the same library bubbletea uses internally so the CLI and
// TUI agree on column widths.
func runeWidth(s string) int { return runewidth.StringWidth(s) }

// truncateRunes shortens s to `width` display cells, replacing the
// trailing characters with "…" when truncation actually happens. The
// loop builds the prefix one rune at a time so wide characters don't get
// cut mid-glyph.
func truncateRunes(s string, width int) string {
	if width <= 1 || runeWidth(s) <= width {
		return s
	}
	const ellipsis = "…"
	target := width - runeWidth(ellipsis)
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if used+w > target {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteString(ellipsis)
	return b.String()
}

// renderTable prints jobs as a fixed-width table sized to the data. ID can
// run very long (e.g. "launchd-apple-daemons:com.apple.…"), so we cap that
// column at 56 cells; everything else is sized to the widest cell.
func renderTable(w io.Writer, jobs []cron.Job) {
	if len(jobs) == 0 {
		fmt.Fprintln(w, "(no scheduled jobs)")
		return
	}
	headers := []string{"ID", "KIND", "SCOPE", "NAME", "SCHEDULE", "STATUS"}
	rows := make([][]string, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, []string{j.ID, string(j.Kind), string(j.Scope), j.Name, j.Schedule, j.Status})
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = runeWidth(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if w := runeWidth(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	widths[0] = min(widths[0], 56)

	writeRow := func(cells []string) {
		var b strings.Builder
		for i, c := range cells {
			c = truncateRunes(c, widths[i])
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-runeWidth(c)+2))
			}
		}
		fmt.Fprintln(w, b.String())
	}
	writeRow(headers)
	for _, r := range rows {
		writeRow(r)
	}
}

func renderJobDetail(w io.Writer, j cron.Job) {
	fmt.Fprintf(w, "ID:        %s\n", j.ID)
	fmt.Fprintf(w, "Kind:      %s\n", j.Kind)
	fmt.Fprintf(w, "Scope:     %s\n", j.Scope)
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

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// tail prints the last n lines of path. Reads chunks from the end so a 1GB
// log doesn't get fully slurped into memory just to print 100 lines.
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
	trimmed := strings.TrimRight(string(buf), "\n")
	// Empty file (or whitespace-only) — print nothing rather than a single
	// blank line. strings.Split("", "\n") returns [""] which would echo "".
	if trimmed == "" {
		return nil
	}
	all := strings.Split(trimmed, "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, line := range all {
		fmt.Fprintln(w, line)
	}
	return nil
}
