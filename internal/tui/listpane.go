package tui

import (
	"fmt"
	"strings"
)

// Shared scrolling list for the subcommand TUIs (admin, actions). They each
// grew their own render loop that wrote every item to the frame and let
// bubbletea sort it out. bubbletea keeps the LAST height lines of an
// overflowing frame, so the rows it drops are the ones at the top — the title
// and the tab strip. The main PR app was fixed this way in #15 (contentRows +
// fitRows); the subcommands were left rendering unbounded.
//
// ListPane is the missing half: it owns the scroll offset, so a list longer
// than the pane scrolls inside its own rows instead of pushing the header off
// the screen.

// ListPane tracks the cursor and scroll offset for one list of rows.
type ListPane struct {
	Cursor int
	Offset int
}

// Move steps the cursor by delta, clamped to the row count.
func (p *ListPane) Move(delta, count int) {
	if count == 0 {
		p.Cursor, p.Offset = 0, 0
		return
	}
	p.Cursor = clamp(p.Cursor+delta, 0, count-1)
}

// Top and Bottom are the g / G jumps.
func (p *ListPane) Top()         { p.Cursor = 0 }
func (p *ListPane) Bottom(n int) { p.Cursor = max(n-1, 0) }

// Reset returns to the first row, e.g. after switching tabs or filtering.
func (p *ListPane) Reset() { p.Cursor, p.Offset = 0, 0 }

// scrollTo brings the cursor into a window of h rows, moving the offset by the
// smallest amount that does it — so paging down does not recentre a cursor that
// was already visible.
func (p *ListPane) scrollTo(count, h int) {
	if h < 1 {
		h = 1
	}
	p.Cursor = clamp(p.Cursor, 0, max(count-1, 0))
	if p.Cursor < p.Offset {
		p.Offset = p.Cursor
	}
	if p.Cursor >= p.Offset+h {
		p.Offset = p.Cursor - h + 1
	}
	// A shrinking list can leave the offset past the end, which would render a
	// blank pane for rows that exist.
	p.Offset = clamp(p.Offset, 0, max(count-h, 0))
}

// RenderList draws exactly h rows: the slice of rows the offset selects, the
// cursor row highlighted across the full width, and padding to h so a short
// list does not let the footer float up the screen.
//
// rows are plain strings; the caller styles their contents and RenderList adds
// only the selection band, which has to wrap an already-styled line as a whole
// (see the note in prlist_render.go on why cell-by-cell backgrounds stripe).
func (p *ListPane) RenderList(rows []string, w, h int) string {
	if h < 1 {
		h = 1
	}
	p.scrollTo(len(rows), h)
	end := min(p.Offset+h, len(rows))

	out := make([]string, 0, h)
	for i := p.Offset; i < end; i++ {
		line := rows[i]
		if i == p.Cursor {
			line = selectedRowStyle.Render(padTo(line, w))
		} else {
			line, _ = truncateExact(line, w)
		}
		out = append(out, line)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// ScrollHint describes the position in a list too long to show at once, for the
// footer. Empty when everything fits — a scrollbar that is always full says
// nothing and costs a footer slot.
func (p *ListPane) ScrollHint(count, h int) string {
	if count <= h || h < 1 {
		return ""
	}
	return fmt.Sprintf("%d/%d", min(p.Cursor+1, count), count)
}

// padTo pads s out to w cells, measuring display width rather than bytes.
func padTo(s string, w int) string {
	s, _ = truncateExact(s, w)
	if pad := w - lipglossWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// filterRows returns the indices of the rows matching every whitespace-separated
// term in query, so adding a term narrows rather than widens — the same rule the
// PR list's search uses. An empty query keeps everything.
func filterRows(query string, n int, haystack func(int) string) []int {
	q := strings.TrimSpace(strings.ToLower(query))
	terms := strings.Fields(q)
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if len(terms) == 0 {
			out = append(out, i)
			continue
		}
		hay := strings.ToLower(haystack(i))
		match := true
		for _, term := range terms {
			if !strings.Contains(hay, term) {
				match = false
				break
			}
		}
		if match {
			out = append(out, i)
		}
	}
	return out
}

// renderSearchBar is the one-line prompt shown in place of the tab strip while
// a filter is being typed. It replaces the strip rather than adding a row: an
// extra row would resize the pane mid-search, and the tabs are not reachable
// while the query owns the keyboard anyway.
func renderSearchBar(query string, w int) string {
	return renderPromptBar("Search", query, w, "enter", "apply", "esc", "clear")
}

// renderPromptBar is renderSearchBar generalized: any one-line prompt that
// takes over the tab strip's row while it owns the keyboard.
//
// It exists because the admin panel's writes need input — a login to add, a
// permission to grant — and reusing the search bar would label every one of
// them "Search:". The row it occupies is the same row for the same reason: an
// extra line would resize the list under the cursor mid-prompt.
func renderPromptBar(label, value string, w int, hints ...string) string {
	line := helpKeyStyle.Render(label+": ") + value + blockCursor()
	if len(hints) > 0 {
		line += "  " + fmtHints(hints...)
	}
	line, _ = truncateExact(line, w)
	return line
}
