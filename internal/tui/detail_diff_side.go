package tui

import (
	"fmt"
	"strings"

	"github.com/keyolk/ghx/internal/pr"
)

// Side-by-side rendering pairs each hunk's deletions with its additions so the
// before/after of an edit sit on the same screen row.
//
// The row model is deliberately left alone: the cursor, visual range, and
// comment anchoring all key off a single row index, and that logic is verified.
// Pairing is computed as a separate view over those same rows, so switching
// layout changes only what is drawn, never what `c` would comment on.

// sidePair is one screen row of a side-by-side hunk: the left row index, the
// right row index, or both. -1 means that half is blank.
type sidePair struct {
	left  int // index into diffView.rows, or -1
	right int // index into diffView.rows, or -1
	// wrapLine is which visual line of a wrapped row this screen row shows.
	// A comment is far longer than a column is wide, so it occupies several
	// screen rows rather than being truncated to one.
	wrapLine int
}

// half returns the row index for one column, or -1 when that half is blank.
func (p sidePair) half(which halfSide) int {
	if which == sideLeft {
		return p.left
	}
	return p.right
}

// pairHunkRows groups a contiguous run of diff rows into left/right pairs.
//
// Within a hunk, git emits a change as a block of deletions followed by a block
// of additions. Those blocks are zipped positionally — deletion i against
// addition i — which is what makes a modified line show its old and new text on
// one row. Context lines occupy both halves. A pure insertion or deletion leaves
// the opposite half blank.
func pairHunkRows(rows []diffRow, from, to int) []sidePair {
	var out []sidePair
	// Pending deletions awaiting additions to zip against.
	var dels []int

	flushDels := func() {
		for _, d := range dels {
			out = append(out, sidePair{left: d, right: -1})
		}
		dels = dels[:0]
	}

	i := from
	for i < to {
		r := rows[i]
		if r.kind != rowDiffLine {
			// A thread belongs to the side its comment is anchored to, so it goes
			// in that column. Headers belong to neither and span the full width.
			flushDels()
			if r.kind == rowThread && r.side == "LEFT" {
				out = append(out, sidePair{left: i, right: -1})
			} else if r.kind == rowThread {
				out = append(out, sidePair{left: -1, right: i})
			} else {
				out = append(out, sidePair{left: i, right: i})
			}
			i++
			continue
		}
		switch r.line.Kind {
		case pr.DiffLineDeletion:
			dels = append(dels, i)
			i++
		case pr.DiffLineAddition:
			if len(dels) > 0 {
				// Zip against the oldest pending deletion: this addition is the
				// replacement for it.
				out = append(out, sidePair{left: dels[0], right: i})
				dels = dels[1:]
			} else {
				out = append(out, sidePair{left: -1, right: i})
			}
			i++
		default: // context
			flushDels()
			out = append(out, sidePair{left: i, right: i})
			i++
		}
	}
	flushDels()
	return out
}

// sideRows builds the full side-by-side row list for the current rows, keeping
// file and hunk headers as full-width rows between the paired blocks.
//
// halfWidth is the column width; it decides how many screen rows a comment
// needs. Pass 0 when the caller only needs the pairing (navigation), not the
// wrapped layout — every row then occupies exactly one screen row.
func (v *diffView) sideRowsWidth(halfWidth int) []sidePair {
	out := make([]sidePair, 0, len(v.rows))
	i := 0
	for i < len(v.rows) {
		r := v.rows[i]
		if r.kind == rowFileHeader || r.kind == rowHunkHeader {
			out = append(out, sidePair{left: i, right: i})
			i++
			continue
		}
		// Collect the run of rows belonging to this hunk and pair it as a unit:
		// zipping must not reach across a hunk boundary, where the old and new
		// line numbers jump independently.
		start := i
		hunk, file := r.hunkIdx, r.fileIdx
		for i < len(v.rows) {
			n := v.rows[i]
			if n.kind == rowFileHeader || n.kind == rowHunkHeader ||
				n.hunkIdx != hunk || n.fileIdx != file {
				break
			}
			i++
		}
		out = append(out, v.pairHunk(start, i, halfWidth)...)
	}
	return out
}

// sideRows pairs rows without wrapping, for navigation and tests.
func (v *diffView) sideRows() []sidePair {
	return v.sideRowsWidth(0)
}

// pairHunk pairs a hunk's rows and expands any that need more than one screen
// row to render.
func (v *diffView) pairHunk(from, to, halfWidth int) []sidePair {
	pairs := pairHunkRows(v.rows, from, to)
	if halfWidth <= 0 {
		return pairs
	}
	out := make([]sidePair, 0, len(pairs))
	for _, p := range pairs {
		n := v.pairWrapCount(p, halfWidth)
		for line := 0; line < n; line++ {
			wp := p
			wp.wrapLine = line
			out = append(out, wp)
		}
	}
	return out
}

// pairWrapCount is how many screen rows a pair needs. Only comments wrap; code
// lines are truncated, because a wrapped code line would break the alignment
// the layout exists to provide.
func (v *diffView) pairWrapCount(p sidePair, halfWidth int) int {
	rowIdx := p.left
	if rowIdx < 0 || (p.right >= 0 && v.rows[p.right].kind == rowThread) {
		rowIdx = p.right
	}
	if rowIdx < 0 || v.rows[rowIdx].kind != rowThread {
		return 1
	}
	// Headers spanning both halves get the full width; a one-sided comment gets
	// its own column.
	width := halfWidth
	if p.left == p.right {
		width = halfWidth * 2
	}
	return max(len(wrapText(v.threadText(v.rows[rowIdx]), max(width-4, 20))), 1)
}

// threadText is a thread row's display text, including the note that marks a
// comment whose anchor is not in this diff.
func (v *diffView) threadText(r diffRow) string {
	if r.orphan && r.commentIdx == 0 {
		return fmt.Sprintf("%s (line %d, not in this diff)", r.text, r.anchorLine)
	}
	return r.text
}

// renderSideBySide draws the paired layout. The gutter columns and a divider
// take fixed width; the rest is split evenly between the two halves.
func (v *diffView) renderSideBySide(width, height int) string {
	pairs := v.sideRows()
	if len(pairs) == 0 {
		return dimStyle.Render("No diff.")
	}
	// Keep the cursor's screen row visible, and remember which pair holds it so
	// the highlight lands on the right half.
	cursorPair := v.cursorPairIndex(pairs)
	v.clampSideOffset(cursorPair, len(pairs), height)

	const divider = " │ "
	half := (width - lipglossWidth(divider)) / 2
	if half < 12 {
		// Callers gate on width before choosing this layout, so reaching here
		// means the terminal shrank mid-render. Say so instead of drawing
		// unreadable slivers — and do not call back into render(), which would
		// recurse straight back here.
		return dimStyle.Render("Terminal too narrow for side-by-side — press s for unified.")
	}

	// Pair with the column width so comments wrap inside their own column
	// instead of being cut off at one line.
	pairs = v.sideRowsWidth(half)
	cursorPair = v.cursorPairIndex(pairs)
	v.clampSideOffset(cursorPair, len(pairs), height)

	var b strings.Builder
	end := min(v.sideOffset+height, len(pairs))
	for i := v.sideOffset; i < end; i++ {
		b.WriteString(v.renderSideRow(pairs[i], half, divider))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// threadHalf reports the column a one-sided comment occupies, if this pair is
// one. A comment anchored to a line belongs beside that line, not across both.
func (p sidePair) threadHalf(v *diffView) (rowIdx int, which halfSide, ok bool) {
	if p.left >= 0 && p.right < 0 && v.rows[p.left].kind == rowThread {
		return p.left, sideLeft, true
	}
	if p.right >= 0 && p.left < 0 && v.rows[p.right].kind == rowThread {
		return p.right, sideRight, true
	}
	return 0, sideLeft, false
}

// renderThreadWrapped draws one visual line of a comment, wrapped to width.
// Comments are prose and far longer than a column, so truncating them to a
// single line hides the finding the reviewer needs to read.
func (v *diffView) renderThreadWrapped(rowIdx, width, wrapLine int) string {
	r := v.rows[rowIdx]
	lines := wrapText(v.threadText(r), max(width-4, 20))
	text := ""
	if wrapLine < len(lines) {
		text = lines[wrapLine]
	}
	// Continuation lines are indented past the marker so the comment reads as
	// one block rather than several unrelated remarks.
	indent := "  "
	if wrapLine > 0 {
		indent = "    "
	}
	plain := padCell(fitCell(indent+text, width), width)

	selected := rowIdx == v.cursor
	if selected {
		return diffCursorStyle.Render(plain)
	}
	// The row's own state, not a substring of its text — a comment quoting
	// "[resolved]" used to render as a resolved thread.
	if r.state == threadDone {
		return threadResolved.Render(plain)
	}
	return threadStyle.Render(plain)
}

// renderSideRow draws one screen row: left half, divider, right half.
func (v *diffView) renderSideRow(p sidePair, half int, divider string) string {
	// A header belongs to neither column, so it spans the full width. Context
	// lines also point both halves at one row, but they must appear in both
	// columns — that is what keeps the two sides aligned around a change.
	if p.left >= 0 && p.left == p.right && v.rows[p.left].kind != rowDiffLine {
		full := half*2 + lipglossWidth(divider)
		if v.rows[p.left].kind == rowThread {
			return v.renderThreadWrapped(p.left, full, p.wrapLine)
		}
		return v.renderRow(p.left, full)
	}
	// A one-sided comment stays in its column, so the opposite side keeps
	// showing code rather than being pushed out of the way.
	if idx, which, ok := p.threadHalf(v); ok {
		cell := v.renderThreadWrapped(idx, half, p.wrapLine)
		blank := strings.Repeat(" ", half)
		if which == sideLeft {
			return cell + dimStyle.Render(divider) + blank
		}
		return blank + dimStyle.Render(divider) + cell
	}
	left := v.renderHalf(p.left, half, sideLeft)
	right := v.renderHalf(p.right, half, sideRight)
	return left + dimStyle.Render(divider) + right
}

type halfSide int

const (
	sideLeft halfSide = iota
	sideRight
)

// renderHalf draws one side's cell, padded to exactly width cells. A -1 row
// index renders as blank space so the opposite half stays aligned.
func (v *diffView) renderHalf(rowIdx, width int, which halfSide) string {
	if rowIdx < 0 {
		return strings.Repeat(" ", width)
	}
	r := v.rows[rowIdx]
	// The cursor occupies one column. A modification pairs two diff rows on one
	// screen row, and marking both would misstate where a comment would land.
	selected := rowIdx == v.cursor && which == v.sideFocus
	// A visual range lives on a single side, so only that column is highlighted.
	inVisual := v.visual && which == v.sideFocus && v.rowInSelection(rowIdx)

	lineNo := r.line.OldLineNo
	if which == sideRight {
		lineNo = r.line.NewLineNo
	}
	marker := " "
	switch r.line.Kind {
	case pr.DiffLineAddition:
		marker = "+"
	case pr.DiffLineDeletion:
		marker = "-"
	}

	gutter := gutterCell(lineNo) + marker
	// Tabs must become the spaces the terminal would draw, measured from where
	// the content actually starts. Counting a tab as one cell makes the padding
	// below too short, and every column after this one shifts.
	content := expandTabs(r.line.Content, lipglossWidth(gutter))
	plain := padCell(fitCell(gutter+content, width), width)

	if selected {
		return diffCursorStyle.Render(plain)
	}
	if inVisual {
		return selectedRowStyle.Render(plain)
	}
	// Unselected cells keep the +/- coloring so the eye can still scan changes.
	switch r.line.Kind {
	case pr.DiffLineAddition:
		return diffAddStyle.Render(plain)
	case pr.DiffLineDeletion:
		return diffDelStyle.Render(plain)
	}
	return diffCtxStyle.Render(plain)
}

// gutterCell right-aligns a line number in a fixed gutter, blank when absent.
func gutterCell(n int) string {
	return leftPadCell(lineNoStr(n), 5) + " "
}

// cursorPairIndex finds which screen row holds the cursor.
func (v *diffView) cursorPairIndex(pairs []sidePair) int {
	for i, p := range pairs {
		if p.left == v.cursor || p.right == v.cursor {
			return i
		}
	}
	return 0
}

// --- side-by-side navigation ---
//
// In the paired layout j/k must step one screen row, not one diff row: a
// modification occupies two diff rows on a single screen row, so stepping the
// flat index would move the cursor without the view appearing to change.

// moveSideCursor steps delta screen rows, keeping the focused column where
// possible so vertical movement does not drift sideways.
func (v *diffView) moveSideCursor(delta int) {
	pairs := v.sideRows()
	if len(pairs) == 0 || delta == 0 {
		return
	}
	from := v.cursorPairIndex(pairs)

	// While extending a selection the cursor has to stay on one side, or the
	// range would mix LEFT and RIGHT lines and stop being postable. Step to the
	// next row whose anchor is genuinely on the focused side — a context row
	// occupies both columns but its anchor is RIGHT, so landing there from a
	// LEFT run would silently switch sides.
	if v.visual {
		step := 1
		if delta < 0 {
			step = -1
		}
		wantSide := "RIGHT"
		if v.sideFocus == sideLeft {
			wantSide = "LEFT"
		}
		for i := from + step; i >= 0 && i < len(pairs); i += step {
			idx := pairs[i].half(v.sideFocus)
			if idx < 0 {
				continue
			}
			r := v.rows[idx]
			if r.kind != rowDiffLine || r.side != wantSide {
				// Reached the end of the run on this side; extending further would
				// change what the range means.
				return
			}
			v.cursor = idx
			return
		}
		return
	}

	i := clamp(from+delta, 0, len(pairs)-1)
	v.landOnPair(pairs[i])
}

// landOnPair puts the cursor on a screen row, preferring the focused column and
// falling back to the other when that half is blank.
func (v *diffView) landOnPair(p sidePair) {
	want, other := p.right, p.left
	if v.sideFocus == sideLeft {
		want, other = p.left, p.right
	}
	switch {
	case want >= 0:
		v.cursor = want
	case v.visual:
		// Extending a selection must not hop columns: the range would then mix
		// LEFT and RIGHT lines and stop being postable. Stay put instead — the
		// footer already reports what the range currently covers.
		return
	case other >= 0:
		// The preferred column is empty on this row (a pure insertion or
		// deletion). Move to the side that exists rather than refusing to move,
		// and remember it so the next step continues from there.
		v.cursor = other
		v.sideFocus = otherSide(v.sideFocus)
	}
}

// focusSide moves the cursor to the given column of the current screen row.
// Reports false when that half is blank, so the caller can leave the key to
// whatever else claims it.
func (v *diffView) focusSide(which halfSide) bool {
	pairs := v.sideRows()
	if len(pairs) == 0 {
		return false
	}
	p := pairs[v.cursorPairIndex(pairs)]
	target := p.right
	if which == sideLeft {
		target = p.left
	}
	if target < 0 {
		return false
	}
	// A full-width row (header or thread) has no columns to switch between.
	if p.left == p.right {
		return false
	}
	v.sideFocus = which
	v.cursor = target
	return true
}

func otherSide(s halfSide) halfSide {
	if s == sideLeft {
		return sideRight
	}
	return sideLeft
}

// syncSideFocus derives the focused column from where the cursor actually is,
// so a jump from another tab or a layout switch does not leave the two
// disagreeing.
func (v *diffView) syncSideFocus() {
	if len(v.rows) == 0 || v.cursor >= len(v.rows) {
		return
	}
	switch v.rows[v.cursor].side {
	case "LEFT":
		v.sideFocus = sideLeft
	case "RIGHT":
		v.sideFocus = sideRight
	}
}

// clampSideOffset scrolls the paired view to keep the cursor's row visible. A
// negative offset means a jump moved the cursor, so centre on it.
func (v *diffView) clampSideOffset(cursorPair, total, height int) {
	if height <= 0 {
		return
	}
	maxOffset := max(total-height, 0)
	if v.sideOffset < 0 {
		v.sideOffset = clamp(cursorPair-height/2, 0, maxOffset)
		return
	}
	if cursorPair < v.sideOffset {
		v.sideOffset = cursorPair
	}
	if cursorPair >= v.sideOffset+height {
		v.sideOffset = cursorPair - height + 1
	}
	v.sideOffset = clamp(v.sideOffset, 0, maxOffset)
}
