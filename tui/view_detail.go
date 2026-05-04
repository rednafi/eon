package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"

	"github.com/rednafi/eon/cron"
)

func (m Model) viewDetail() string {
	j, ok := m.currentJob()
	if !ok {
		return m.viewList()
	}
	return m.frame("Detail · "+j.Name, m.renderDetailPanel())
}

func (m Model) renderDetailPanel() string {
	panelWidth, panelHeight, contentWidth := m.bodyDims()
	tabs := m.renderTabs()
	rule := m.theme.Subtle.Render(strings.Repeat("─", contentWidth))
	help := m.theme.Help.Render("[tab] switch  [↑/↓ pgup/pgdn] scroll  [d] delete  [esc] back")
	body := strings.Join([]string{tabs, rule, m.activeVPCopy().View(), help}, "\n")
	return m.theme.MainPanel.Width(panelWidth).Height(panelHeight).Render(body)
}

// activeVPCopy returns the active viewport by value for read-only access in
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

func (m Model) renderTabs() string {
	var out strings.Builder
	for i := range int(tabCount) {
		t := detailTab(i)
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
	add("Scope", string(j.Scope))
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

// readTail returns the last maxBytes of path. Seek-to-end gets the size
// without a separate Stat — saves a syscall and avoids the TOCTOU gap.
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
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(buf), nil
}
