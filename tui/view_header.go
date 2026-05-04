package tui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// renderHeader builds the top split panel: info on the left, keymap on the
// right. Each half consumes lipgloss-counted cells of the terminal width;
// any rounding leftover goes to the right panel.
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

// bodyDims is the panel-size calculator shared by every body view. Single
// source of truth for the chrome math.
func (m Model) bodyDims() (width, height, contentWidth int) {
	width = m.width
	height = max(panelChromeY+3, m.height-headerHeight-statusHeight)
	contentWidth = max(20, width-panelChromeX)
	return
}

// modal wraps a body string in the standard modal chrome: header strip,
// bordered main panel sized to the body area, status bar at the bottom.
// Every modal view (confirm, read-only, future) goes through this so the
// chrome can't drift between them.
func (m Model) modal(context, body string) string {
	pw, ph, _ := m.bodyDims()
	panel := m.theme.MainPanel.Width(pw).Height(ph).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(context), panel, m.renderStatusBar())
}

func (m Model) renderStatusBar() string {
	now := time.Now()
	left := m.theme.Subtle.Render(" eon · ready ")
	if m.flash != "" && now.Before(m.flashUntil) {
		left = m.theme.Status.Render(" " + m.flash + " ")
	} else if m.loadErr != "" {
		left = m.theme.Error.Render(" " + m.loadErr + " ")
	}
	right := m.theme.Subtle.Render(now.Format("15:04:05") + " ")
	pad := max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", pad) + right
}
