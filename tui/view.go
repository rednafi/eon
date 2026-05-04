package tui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rednafi/eon/cron"
)

const (
	headerHeight = 5 // info + keymap row, including borders
	statusHeight = 1
	// Panel chrome: 1 border + 1 padding on each side horizontally; 1 border
	// each side vertically. Centralised so width math stays consistent.
	panelChromeX = 4
	panelChromeY = 2
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// render is the string-returning sibling of View. Tests compare against it
// directly to avoid unwrapping tea.View on every assertion.
func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
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
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader("Crons"),
		m.renderListPanel(),
		m.renderStatusBar(),
	)
}

func (m Model) renderHeader(context string) string {
	leftWidth := clamp(m.width/2, 30, m.width-30)
	rightWidth := m.width - leftWidth
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderInfoPanel(leftWidth, context),
		m.renderKeymapPanel(rightWidth),
	)
}

func (m Model) renderInfoPanel(width int, context string) string {
	plat := runtime.GOOS
	switch plat {
	case "darwin":
		plat = "macOS"
	case "linux":
		plat = "Linux"
	}
	count := fmt.Sprintf("%d", len(m.jobs))
	if len(m.visibleIdx) != len(m.jobs) {
		count = fmt.Sprintf("%d / %d", len(m.visibleIdx), len(m.jobs))
	}
	scope := "user"
	if m.showSystem {
		scope = "user + system"
	}
	rows := []string{
		m.theme.Title.Render("eon") + " " + m.theme.Subtle.Render("· local cron monitor"),
		m.kv("Context:", context),
		m.kv("Scope:", scope+"  ("+plat+")"),
		m.kv("Jobs:", count),
	}
	return m.theme.Panel.Width(width).Render(strings.Join(rows, "\n"))
}

func (m Model) renderKeymapPanel(width int) string {
	col := func(pairs [][2]string) string {
		var lines []string
		for _, p := range pairs {
			lines = append(lines, m.theme.HelpKey.Render(p[0])+" "+m.theme.Help.Render(p[1]))
		}
		return strings.Join(lines, "\n")
	}
	left := col([][2]string{{"↑/↓", "navigate"}, {"/", "filter"}, {"a", "all/user"}})
	right := col([][2]string{{"⏎", "open"}, {"d", "delete"}, {"r", "refresh"}})
	body := m.theme.Header.Render("Shortcuts") + "\n" +
		lipgloss.JoinHorizontal(lipgloss.Top, left, "    ", right)
	return m.theme.Panel.Width(width).Render(body)
}

func (m Model) kv(k, v string) string {
	return m.theme.Subtle.Render(fmt.Sprintf("%-9s ", k)) + m.theme.HelpKey.Render(v)
}

// bodyDims returns the panel dimensions for the area between header and
// status bar. Single source of truth for the chrome math.
func (m Model) bodyDims() (width, height, contentWidth int) {
	width = m.width
	height = max(panelChromeY+3, m.height-headerHeight-statusHeight)
	contentWidth = max(20, width-panelChromeX)
	return
}

func (m Model) renderListPanel() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()

	cols := []string{"ID", "KIND", "NAME", "SCHEDULE", "STATUS"}
	widths := m.colWidths
	if widths == nil {
		widths = computeColumnWidths(cols, m.jobs, contentWidth)
	}
	headerStyle := m.theme.HeaderCell
	headerLine := renderRow(cols, widths, &headerStyle, nil)
	rule := m.theme.Subtle.Render(strings.Repeat("─", contentWidth))

	listRows := panelHeight - panelChromeY - 2
	filterRow := ""
	if m.filterOn || m.filter.Value() != "" {
		filterRow = m.renderFilterChip()
		listRows--
	}
	if listRows < 1 {
		listRows = 1
	}

	start, end := windowedRange(m.cursor, listRows, len(m.visibleIdx))
	rows := make([]string, 0, listRows)
	for i := start; i < end; i++ {
		j := m.jobs[m.visibleIdx[i]]
		cells := []string{j.ID, string(j.Kind), j.Name, j.Schedule, j.Status}
		statusStyle := m.theme.statusStyle(j.Status)
		overrides := []*lipgloss.Style{nil, nil, nil, nil, &statusStyle}
		line := renderRow(cells, widths, nil, overrides)
		if i == m.cursor {
			line = m.theme.Selected.Width(contentWidth).Render(line)
		}
		rows = append(rows, line)
	}
	if len(m.visibleIdx) == 0 {
		rows = append([]string{m.theme.Subtle.Render("(no scheduled jobs)")}, rows...)
	}
	for len(rows) < listRows {
		rows = append(rows, "")
	}

	parts := []string{headerLine, rule}
	if filterRow != "" {
		parts = append(parts, filterRow)
	}
	parts = append(parts, rows...)
	return m.theme.MainPanel.Width(panelWidth).Height(panelHeight).Render(strings.Join(parts, "\n"))
}

func (m Model) renderFilterChip() string {
	if m.filterOn {
		return m.theme.Filter.Render("filter ") + m.filter.View()
	}
	return m.theme.Filter.Render("filter ") + m.filter.Value() + "  " + m.theme.Subtle.Render("(esc clears)")
}

func (m Model) renderStatusBar() string {
	left := m.theme.Subtle.Render(" eon · ready ")
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		left = m.theme.Status.Render(" " + m.flash + " ")
	} else if m.loadErr != "" {
		left = m.theme.Error.Render(" " + m.loadErr + " ")
	}
	right := m.theme.Subtle.Render(time.Now().Format("15:04:05") + " ")
	pad := max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", pad) + right
}

func (m Model) viewDetail() string {
	j, ok := m.currentJob()
	if !ok {
		return m.viewList()
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader("Detail · "+j.Name),
		m.renderDetailPanel(),
		m.renderStatusBar(),
	)
}

func (m Model) renderDetailPanel() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()
	tabs := m.renderTabs()
	rule := m.theme.Subtle.Render(strings.Repeat("─", contentWidth))
	help := m.theme.Help.Render("[tab] switch  [↑/↓ pgup/pgdn] scroll  [d] delete  [esc] back")
	body := strings.Join([]string{tabs, rule, m.activeVPCopy().View(), help}, "\n")
	return m.theme.MainPanel.Width(panelWidth).Height(panelHeight).Render(body)
}

// activeVPCopy returns the active viewport by value, for read-only access in
// View. Update uses *Model.activeVP for in-place mutation.
func (m Model) activeVPCopy() viewport.Model {
	switch m.detailTab {
	case tabRaw:
		return m.rawVP
	case tabLogs:
		return m.logsVP
	default:
		return m.detailVP
	}
}

func (m Model) viewConfirm() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.theme.Header.Render("Delete this cron?"),
		"",
		m.kv("ID:", m.pendingDelete.ID),
		m.kv("Schedule:", m.pendingDelete.Schedule),
		m.kv("Command:", truncateMiddle(m.pendingDelete.Command, contentWidth-12)),
		"",
		m.theme.HelpKey.Render("y")+" "+m.theme.Help.Render("confirm")+"   "+
			m.theme.HelpKey.Render("n/esc")+" "+m.theme.Help.Render("cancel"),
	)
	panel := m.theme.MainPanel.Width(panelWidth).Height(panelHeight).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader("Confirm"), panel, m.renderStatusBar())
}

func (m Model) renderTabs() string {
	var out strings.Builder
	for t := detailTab(0); t < tabCount; t++ {
		if t == m.detailTab {
			out.WriteString(m.theme.TabActive.Render(t.String()))
		} else {
			out.WriteString(m.theme.TabInactive.Render(t.String()))
		}
	}
	return out.String()
}

// refreshDetailContent rebuilds every detail tab's contents. Skips work when
// the cursor's job hasn't changed since the last refresh.
func (m *Model) refreshDetailContent() {
	j, ok := m.currentJob()
	if !ok {
		m.lastDetailID = ""
		return
	}
	if j.ID == m.lastDetailID {
		return
	}
	m.lastDetailID = j.ID

	width := m.detailVP.Width()
	if width == 0 {
		width = max(20, m.width-panelChromeX-2)
	}

	var ov strings.Builder
	add := func(k, v string) {
		if v == "" {
			return
		}
		ov.WriteString(m.theme.Header.Render(fmt.Sprintf("%-12s", k)))
		ov.WriteString(wrap(v, width-12))
		ov.WriteString("\n")
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
	m.detailVP.SetContent(ov.String())
	m.rawVP.SetContent(wrap(j.Raw, width))
	m.logsVP.SetContent(renderLogs(j, width))
}

func renderLogs(j cron.Job, width int) string {
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

// readTail returns the last maxBytes of path. We Seek to the end to size the
// file rather than calling Stat — saves a syscall and avoids the TOCTOU gap
// between stat and read.
func readTail(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return "", err
	}
	off := int64(0)
	if size > int64(maxBytes) {
		off = size - int64(maxBytes)
	}
	buf := make([]byte, size-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return "", err
	}
	return string(buf), nil
}

// renderRow joins cells into one space-padded line. Per-cell styles in
// overrides take precedence over base; nil entries fall back to base. Padding
// is measured against rendered cell width so ANSI codes don't shift columns.
func renderRow(cells []string, widths []int, base *lipgloss.Style, overrides []*lipgloss.Style) string {
	var b strings.Builder
	for i, c := range cells {
		text := truncateMiddle(c, widths[i])
		var s *lipgloss.Style
		if i < len(overrides) && overrides[i] != nil {
			s = overrides[i]
		} else {
			s = base
		}
		styled := text
		if s != nil {
			styled = s.Render(text)
		}
		b.WriteString(styled)
		if i == len(cells)-1 {
			break
		}
		pad := widths[i] - lipgloss.Width(styled) + 2
		if pad < 1 {
			pad = 1
		}
		b.WriteString(strings.Repeat(" ", pad))
	}
	return b.String()
}

// computeColumnWidths sizes each column to its widest cell, then squeezes
// ID → NAME → KIND if the row exceeds the available width. SCHEDULE/STATUS
// stay at their natural width since they're short and most informative.
func computeColumnWidths(headers []string, jobs []cron.Job, available int) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, j := range jobs {
		row := [5]string{j.ID, string(j.Kind), j.Name, j.Schedule, j.Status}
		for i, c := range row {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	gutter := 2 * (len(headers) - 1)
	used := gutter
	for _, w := range widths {
		used += w
	}
	for _, idx := range []int{0, 2, 1} {
		if used <= available {
			break
		}
		over := used - available
		shrink := over
		if widths[idx]-shrink < 8 {
			shrink = max(0, widths[idx]-8)
		}
		widths[idx] -= shrink
		used -= shrink
	}
	return widths
}

func windowedRange(cursor, capacity, total int) (int, int) {
	if total <= capacity {
		return 0, total
	}
	start := cursor - capacity/2
	if start < 0 {
		start = 0
	}
	if start+capacity > total {
		start = total - capacity
	}
	return start, start + capacity
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *Model) recomputeColWidths() {
	_, _, contentWidth := m.bodyDims()
	cols := []string{"ID", "KIND", "NAME", "SCHEDULE", "STATUS"}
	m.colWidths = computeColumnWidths(cols, m.jobs, contentWidth)
}
