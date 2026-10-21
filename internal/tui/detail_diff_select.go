package tui

import (
	"github.com/charmbracelet/x/ansi"
)

// Drag selection over the diff needs the whole diff as screen lines, not just
// the window render draws: a drag past the pane edge scrolls, and the copy
// keeps the lines that scrolled out. These are built only while the mouse is
// working on a selection (selectionPanes asks for them), so an idle frame of a
// multi-thousand-line diff still styles only the rows on screen.

// unifiedLines is every screen line of the unified layout, and offset the
// index of the line drawn at the top of the pane. It reads the offset render
// last settled on, so call it after a render at this width.
func (v *diffView) unifiedLines(width int) (lines []string, offset int) {
	top := max(v.offset, 0)
	for i := range v.rows {
		if i == top {
			offset = len(lines)
		}
		lines = append(lines, v.rowLines(i, width)...)
	}
	return lines, offset
}

// sideLines is every screen line of the paired layout cut into its two
// columns, so each column is a pane of its own and a drag never crosses the
// divider. offset is the pair drawn at the top.
func (v *diffView) sideLines(width int) (left, right []string, offset int) {
	half := sideHalfWidth(width)
	rightX := half + lipglossWidth(sideDivider)
	for _, p := range v.sideRowsWidth(half) {
		line := v.renderSideRow(p, half, sideDivider)
		left = append(left, ansi.Cut(line, 0, half))
		right = append(right, ansi.Cut(line, rightX, width))
	}
	return left, right, max(v.sideOffset, 0)
}

// scrollLines moves the window delta rows (negative is up) without leaving the
// cursor behind: render pulls the window back to the cursor, so the cursor
// moves along whenever the window would leave it. It is what a drag past the
// top or bottom edge of the diff asks for.
func (v *diffView) scrollLines(delta, width, height int) {
	if len(v.rows) == 0 || delta == 0 {
		return
	}
	if v.sideBySide && width >= 80 {
		v.scrollSide(delta, width, height)
		return
	}
	v.offset = clamp(max(v.offset, 0)+delta, 0, len(v.rows)-1)
	if v.cursor < v.offset {
		v.clickRow(v.offset)
		return
	}
	// The last row the window shows from the new offset, measured the way
	// render measures it.
	used, last := 0, v.offset
	for i := v.offset; i < len(v.rows); i++ {
		used += v.screenLines(i, width)
		if used > height {
			break
		}
		last = i
	}
	if v.cursor > last {
		v.clickRow(last)
	}
}

func (v *diffView) scrollSide(delta, width, height int) {
	pairs := v.sideRowsWidth(sideHalfWidth(width))
	if len(pairs) == 0 {
		return
	}
	v.sideOffset = clamp(max(v.sideOffset, 0)+delta, 0, max(len(pairs)-height, 0))
	cur := v.cursorPairIndex(pairs)
	switch {
	case cur < v.sideOffset:
		v.landOnPair(pairs[v.sideOffset])
	case cur >= v.sideOffset+height:
		v.landOnPair(pairs[min(v.sideOffset+height, len(pairs))-1])
	}
}
