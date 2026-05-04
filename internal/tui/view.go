package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/rednafi/eon/internal/origin"
)

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 {
		// We haven't received a WindowSizeMsg yet — render a placeholder
		// rather than blowing up width-dependent layout.
		return "loading…"
	}
	switch m.view {
	case viewDetail:
		return m.viewDetail()
	case viewConfirmDelete:
		return m.viewConfirm()
	default:
		return m.viewList()
	}
}

func (m Model) viewList() string {
	header := m.theme.Title.Render("eon") + " " +
		m.theme.Subtle.Render(fmt.Sprintf("· %d job(s)", len(m.filteredIndexes())))
	if m.loadErr != "" {
		header += "  " + m.theme.Error.Render("("+m.loadErr+")")
	}

	// Compute column widths from the data so wide labels don't crash the
	// layout. Terminal width caps everything.
	cols := []string{"ID", "KIND", "NAME", "SCHEDULE", "STATUS"}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	rows := make([][]string, 0, len(m.jobs))
	visible := m.filteredIndexes()
	for _, idx := range visible {
		j := m.jobs[idx]
		row := []string{j.ID, string(j.Kind), j.Name, j.Schedule, j.Status}
		rows = append(rows, row)
		for i, c := range row {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	// Distribute width: ID gets up to 50%, the rest split evenly. We do this
	// before drawing so the table never overflows the terminal.
	avail := m.width
	maxID := avail / 2
	if widths[0] > maxID {
		widths[0] = maxID
	}
	used := 0
	for _, w := range widths {
		used += w + 2
	}
	if used > avail {
		// Trim ID further if we're still over.
		over := used - avail
		if widths[0]-over > 8 {
			widths[0] -= over
		}
	}

	headerLine := m.theme.Header.Render(joinCells(cols, widths))
	body := strings.Builder{}

	// Compute viewport for the list. We reserve 1 line for title,
	// 1 for header, 1 for filter, 1 for help.
	visibleRows := max(3, m.height-4)
	if m.filterOn {
		visibleRows = max(3, m.height-5)
	}
	start := 0
	if m.cursor >= visibleRows {
		start = m.cursor - visibleRows + 1
	}
	end := start + visibleRows
	if end > len(rows) {
		end = len(rows)
	}

	for i := start; i < end; i++ {
		cells := make([]string, len(rows[i]))
		for k, c := range rows[i] {
			cells[k] = truncateMiddle(c, widths[k])
		}
		line := joinCells(cells, widths)
		if i == m.cursor {
			line = m.theme.Selected.Render(line)
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	if len(rows) == 0 {
		body.WriteString(m.theme.Subtle.Render("(no scheduled jobs)\n"))
	}

	footer := m.helpLine()
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		footer = m.theme.Status.Render(m.flash) + "  " + footer
	}

	parts := []string{header, headerLine, strings.TrimRight(body.String(), "\n")}
	if m.filterOn {
		parts = append(parts, m.filter.View())
	}
	parts = append(parts, footer)
	return strings.Join(parts, "\n")
}

func (m Model) viewDetail() string {
	j, ok := m.currentJob()
	if !ok {
		return "no selection"
	}
	header := m.theme.Title.Render("eon") + " " + m.theme.Subtle.Render("· "+j.ID)
	tabs := m.renderTabs()

	var body string
	switch m.detailTab {
	case tabOverview:
		body = m.detailVP.View()
	case tabRaw:
		body = m.rawVP.View()
	case tabLogs:
		body = m.logsVP.View()
	}
	help := m.theme.Help.Render("[tab] switch  [↑/↓ pgup/pgdn] scroll  [d] delete  [r] refresh  [esc] back  [q] quit")
	return strings.Join([]string{header, tabs, body, help}, "\n")
}

func (m Model) viewConfirm() string {
	header := m.theme.Title.Render("eon") + " " + m.theme.Subtle.Render("· confirm delete")
	body := lipgloss.NewStyle().Padding(1, 2).Render(
		fmt.Sprintf(
			"Delete %s?\n\n  schedule: %s\n  command:  %s\n\n[y] confirm   [n/esc] cancel",
			m.theme.Filter.Render(m.pendingDelete.ID),
			m.pendingDelete.Schedule,
			m.pendingDelete.Command,
		),
	)
	return strings.Join([]string{header, body}, "\n")
}

func (m Model) renderTabs() string {
	var out strings.Builder
	for t := detailTab(0); t < tabCount; t++ {
		label := " " + t.String() + " "
		if t == m.detailTab {
			out.WriteString(m.theme.TabActive.Render(label))
		} else {
			out.WriteString(m.theme.TabInactive.Render(label))
		}
	}
	return out.String()
}

func (m Model) helpLine() string {
	return m.theme.Help.Render("[/] filter  [r] refresh  [enter] details  [d] delete  [q] quit  [?] help")
}

// refreshDetailContent recomputes the contents of every detail tab. It runs
// on cursor move, list reload, and window resize so the viewports always show
// the current selection without lagging a frame behind.
func (m *Model) refreshDetailContent() {
	j, ok := m.currentJob()
	if !ok {
		return
	}
	width := m.detailVP.Width
	if width == 0 {
		width = max(20, m.width-2)
	}

	// Overview: key/value table, wrapped to viewport width so a long PATH or
	// command doesn't push the right edge off-screen.
	var overview strings.Builder
	add := func(k, v string) {
		if v == "" {
			return
		}
		key := m.theme.Header.Render(fmt.Sprintf("%-12s", k))
		overview.WriteString(key)
		overview.WriteString(wrap(v, width-12))
		overview.WriteString("\n")
	}
	add("ID", j.ID)
	add("Kind", string(j.Kind))
	add("Name", j.Name)
	add("Schedule", j.Schedule)
	add("Status", j.Status)
	if j.PID != 0 {
		add("PID", fmt.Sprintf("%d", j.PID))
	}
	if j.LastRun != nil {
		add("Last run", j.LastRun.Format(time.RFC3339))
	}
	if j.NextRun != nil {
		add("Next run", j.NextRun.Format(time.RFC3339))
	}
	add("Path", j.Path)
	add("Stdout", j.StdoutPath)
	add("Stderr", j.StderrPath)
	add("Command", j.Command)
	m.detailVP.SetContent(overview.String())

	// Raw: the verbatim definition (plist, crontab line, unit file).
	m.rawVP.SetContent(wrap(j.Raw, width))

	// Logs: tail stdout and stderr if available. We don't aggressively poll —
	// pressing 'r' refreshes everything, including this tab.
	m.logsVP.SetContent(renderLogs(j, width))
}

// renderLogs reads the tail of stdout/stderr and renders them with section
// headers. Non-existent files become a friendly "(no file)" hint instead of
// blowing up — eon is a monitor, not a log shipper.
func renderLogs(j origin.Job, width int) string {
	var out strings.Builder
	for _, p := range []struct{ label, path string }{
		{"stdout", j.StdoutPath},
		{"stderr", j.StderrPath},
	} {
		if p.path == "" {
			continue
		}
		fmt.Fprintf(&out, "── %s · %s ──\n", p.label, p.path)
		if data, err := readTail(p.path, 8*1024); err != nil {
			out.WriteString("  (cannot read: " + err.Error() + ")\n\n")
		} else {
			out.WriteString(wrap(strings.TrimRight(data, "\n"), width))
			out.WriteString("\n\n")
		}
	}
	if out.Len() == 0 {
		return "(no log paths configured)"
	}
	return out.String()
}

func readTail(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := stat.Size()
	off := int64(0)
	if size > int64(maxBytes) {
		off = size - int64(maxBytes)
	}
	buf := make([]byte, size-off)
	if _, err := f.ReadAt(buf, off); err != nil && err.Error() != "EOF" {
		return "", err
	}
	return string(buf), nil
}

// joinCells concatenates cells with column padding. Last cell isn't padded so
// trailing whitespace doesn't bleed into the help line.
func joinCells(cells []string, widths []int) string {
	var b strings.Builder
	for i, c := range cells {
		b.WriteString(c)
		if i == len(cells)-1 {
			break
		}
		pad := widths[i] - lipgloss.Width(c) + 2
		if pad < 1 {
			pad = 1
		}
		b.WriteString(strings.Repeat(" ", pad))
	}
	return b.String()
}
