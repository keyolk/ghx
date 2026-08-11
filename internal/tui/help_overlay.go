package tui

import (
	"fmt"
	"strings"
)

// The `?` overlay is the discoverability backstop: the footer only has room for
// the handful of keys you need right now, so everything else lives here.

// helpSection is a titled group of key/description pairs.
type helpSection struct {
	title string
	rows  [][2]string
}

func helpSections() []helpSection {
	return []helpSection{
		{"Navigation", [][2]string{
			{"j / k", "move down / up"},
			{"g / G", "jump to top / bottom"},
			{"^f / ^b", "page down / up"},
			{"h / l", "cycle tabs (detail) · pane focus (list)"},
			{"[ / ]", "resize the split"},
			{"1-9", "jump to source tab (list) or detail tab"},
			{"enter", "open PR · select file/thread/check"},
			{"esc", "back / close overlay"},
		}},
		{"Review", [][2]string{
			{"c", "comment on the diff line under the cursor"},
			{"v", "visual range, then c to comment on it"},
			{"s", "unified / side-by-side diff"},
			{"C", "PR-level comment"},
			{"a", "approve (asks first)"},
			{"r", "request changes"},
			{"o", "open in browser (fold file, in the diff tab)"},
		}},
		{"PR actions", [][2]string{
			{"space / A", "toggle row / all visible rows for multi-select"},
			{"a / x / M", "approve · close/reopen · squash-merge selected PRs"},
			{"L", "edit labels on the focused row"},
			{":checkout", "check the branch out locally"},
			{":ready", "toggle ready-for-review"},
			{":merge", "merge with strategy and confirmation"},
		}},
		{"Views", [][2]string{
			{"1 Overview", "metadata and description"},
			{"2 Files", "changed files with +/- counts"},
			{"3 Diff", "unified diff with inline threads"},
			{"4 Comments", "review threads"},
			{"5 Commits", "commit list"},
			{"6 Checks", "CI results; enter opens the log"},
		}},
		{"Filters & status", [][2]string{
			{"M / A / C / U", "merged · approved · changes requested · unresolved"},
			{"f", "filter by one or more PR statuses"},
			{"/", "search text; combines with status filters"},
			{"esc", "clear text, then status, then selection"},
		}},
		{"Other", [][2]string{
			{"1-9", "source tab · * marks the repo you are in"},
			{":", "command palette"},
			{"R", "refresh"},
			{"?", "toggle this help"},
			{"q", "quit"},
		}},
	}
}

// renderHelpOverlay lays the sections out in as many columns as fit.
func renderHelpOverlay(width, height int) string {
	sections := helpSections()
	// Below this width a two-column layout truncates descriptions into
	// uselessness, so fall back to a single scrollable column.
	cols := 2
	if width < 96 {
		cols = 1
	}
	colW := (width - 6) / cols

	rendered := make([]string, 0, len(sections))
	for _, s := range sections {
		rendered = append(rendered, renderHelpSection(s, colW))
	}

	var body string
	if cols == 1 {
		body = strings.Join(rendered, "\n")
	} else {
		body = joinColumns(rendered, colW, cols)
	}
	// Trim to the available height rather than overflowing the terminal.
	lines := strings.Split(body, "\n")
	maxLines := max(height-4, 3)
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines-1]
		truncated = true
	}
	body = strings.Join(lines, "\n")
	if truncated {
		body += "\n" + dimStyle.Render("… resize the terminal to see the rest")
	}
	return decoratedPane("ghx help", body, width, min(len(strings.Split(body, "\n"))+2, height), true)
}

func renderHelpSection(s helpSection, width int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(s.title) + "\n")
	keyW := 0
	for _, r := range s.rows {
		if w := lipglossWidth(r[0]); w > keyW {
			keyW = w
		}
	}
	for _, r := range s.rows {
		key := helpKeyStyle.Render(r[0])
		pad := strings.Repeat(" ", max(keyW-lipglossWidth(r[0]), 0))
		desc := r[1]
		avail := width - keyW - 3
		if avail > 0 && lipglossWidth(desc) > avail {
			desc, _ = truncateExact(desc, avail)
		}
		b.WriteString(fmt.Sprintf("  %s%s  %s\n", key, pad, helpStyle.Render(desc)))
	}
	return b.String()
}

// joinColumns distributes blocks across columns, balancing by line count.
func joinColumns(blocks []string, colW, cols int) string {
	columns := make([][]string, cols)
	heights := make([]int, cols)
	for _, blk := range blocks {
		// Place each block in the currently shortest column.
		target := 0
		for i := 1; i < cols; i++ {
			if heights[i] < heights[target] {
				target = i
			}
		}
		columns[target] = append(columns[target], blk)
		heights[target] += len(strings.Split(blk, "\n"))
	}

	colLines := make([][]string, cols)
	maxH := 0
	for i, col := range columns {
		colLines[i] = strings.Split(strings.Join(col, "\n"), "\n")
		if len(colLines[i]) > maxH {
			maxH = len(colLines[i])
		}
	}
	var b strings.Builder
	for row := 0; row < maxH; row++ {
		for c := 0; c < cols; c++ {
			cell := ""
			if row < len(colLines[c]) {
				cell = colLines[c][row]
			}
			cell, w := truncateExact(cell, colW)
			b.WriteString(cell)
			if c < cols-1 {
				b.WriteString(strings.Repeat(" ", max(colW-w, 0)))
			}
		}
		if row < maxH-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
