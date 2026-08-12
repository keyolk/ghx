package tui

import (
	"fmt"
	"strings"
)

// TabLabel is one entry in a tab strip rendered by RenderTabStrip.
type TabLabel struct {
	Name   string
	Active bool
}

// RenderTabStrip draws a tab strip that keeps every index visible on a narrow
// terminal. It is the shared implementation behind the PR list, the PR detail
// view, and the admin/actions subcommands — all three had the same bug, each
// truncated the joined string from the right and dropped the highest-numbered
// tabs.
//
// strategy: full labels when they fit; drop trailing decorations first (the
// caller passes them already-styled, so this stage only shrinks names); then
// cap each tab at an equal share, preserving at least the index.
func RenderTabStrip(tabs []TabLabel, w int) string {
	if len(tabs) == 0 || w <= 0 {
		return ""
	}
	rendered := make([]string, len(tabs))
	widths := make([]int, len(tabs))
	total := 0
	for i, t := range tabs {
		rendered[i] = renderTabEntry(i+1, t.Name, t.Active, true)
		widths[i] = lipglossWidth(rendered[i])
		total += widths[i]
	}
	if total <= w {
		return strings.Join(rendered, "")
	}

	// Stage 2: drop the inter-tab spacing the styled form adds. The index and
	// name stay.
	for i, t := range tabs {
		if widths[i] <= 4 {
			continue
		}
		rendered[i] = renderTabEntry(i+1, t.Name, t.Active, false)
		widths[i] = lipglossWidth(rendered[i])
	}
	total = 0
	for _, w := range widths {
		total += w
	}
	if total <= w {
		return strings.Join(rendered, "")
	}

	// Stage 3: cap each tab at an equal share, preserving at least the index.
	per := w / len(tabs)
	if per < 3 {
		per = 3
	}
	var b strings.Builder
	for i, t := range tabs {
		if widths[i] <= per {
			b.WriteString(rendered[i])
			continue
		}
		trunc, _ := truncateExact(rendered[i], per)
		// Keep inactive tabs separated after truncation.
		if !t.Active && per > 3 && lipglossWidth(trunc) < per {
			trunc += " "
		}
		b.WriteString(trunc)
	}
	return b.String()
}

// renderTabEntry produces one tab's styled string. spaced adds the padding the
// full form relies on between adjacent tabs; dropping it is stage 2.
func renderTabEntry(index int, name string, active, spaced bool) string {
	label := fmt.Sprintf("%d %s", index, name)
	if active {
		return tabActiveStyle.Render("[" + label + "]")
	}
	if spaced {
		return tabDimStyle.Render(" " + label + " ")
	}
	return tabDimStyle.Render(" " + label)
}
