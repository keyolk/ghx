package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/mouse"
)

// diffRowAt maps a click inside r — the rect the diff body is drawn in — to
// the diff row under the pointer, or reports false for a blank cell, a stale
// scroll position, or an open visual range.
//
// A visual range stays keyboard-only: a click would move one end of it, and a
// range whose ends sit on different sides is not one GitHub accepts.
func (v *diffView) diffRowAt(r mouse.Rect, msg tea.MouseMsg) (int, bool) {
	if len(v.rows) == 0 || v.visual {
		return 0, false
	}
	if v.sideBySide && r.W >= 80 {
		return v.sideRowAt(r, msg)
	}
	// A negative offset is a jump the next render has not centred yet; the
	// window the user sees is not the one the numbers describe.
	if v.offset < 0 {
		return 0, false
	}
	// Rows are not one line each: a thread wraps, a long line unfolds. Measure
	// the rows exactly as render draws them, so the second line of a wrapped
	// comment hits the comment and not whatever a one-per-line count lands on.
	heights := make([]int, 0, r.H)
	for i := v.offset; i < len(v.rows) && len(heights) < r.H; i++ {
		heights = append(heights, len(v.rowLines(i, r.W)))
	}
	hit := mouse.BlockHit(r, 0, heights, msg)
	if !hit.HasItem {
		return 0, false
	}
	return v.offset + hit.Index, true
}

// sideRowAt is diffRowAt for the paired layout, where one screen line holds up
// to two rows and the column under the pointer picks which.
func (v *diffView) sideRowAt(r mouse.Rect, msg tea.MouseMsg) (int, bool) {
	if v.sideOffset < 0 || !r.Contains(msg.X, msg.Y) {
		return 0, false
	}
	half := sideHalfWidth(r.W)
	pairs := v.sideRowsWidth(half)
	i := v.sideOffset + msg.Y - r.Y
	if i < 0 || i >= len(pairs) {
		return 0, false
	}
	p := pairs[i]
	want, other := p.right, p.left
	if msg.X-r.X < half {
		want, other = p.left, p.right
	}
	switch {
	case want >= 0:
		return want, true
	case other >= 0:
		return other, true
	}
	return 0, false
}

// clickRow puts the cursor on row i, the mouse form of moving there with j/k.
func (v *diffView) clickRow(i int) {
	v.cursor = i
	// The focused column follows the row, as it does after a jump.
	v.syncSideFocus()
}
