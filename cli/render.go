package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rednafi/eon/cron"
)

// runeWidth measures s in runes rather than bytes. Cells in the eon CLI are
// plain text (no ANSI), so a rune count is the right cell-count metric for
// alignment. Using len() drops alignment the moment a job name contains a
// multi-byte glyph.
func runeWidth(s string) int { return utf8.RuneCountInString(s) }

// truncateRunes shortens s to width display cells (rune count), replacing
// the trailing rune with "…" when truncation actually happens. Operates on
// runes so we never slice through a multi-byte codepoint.
func truncateRunes(s string, width int) string {
	if width <= 1 || runeWidth(s) <= width {
		return s
	}
	rs := []rune(s)
	return string(rs[:width-1]) + "…"
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
	all := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, line := range all {
		fmt.Fprintln(w, line)
	}
	return nil
}
