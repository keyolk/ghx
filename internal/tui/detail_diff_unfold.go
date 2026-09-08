package tui

import (
	"fmt"
	"strings"

	"github.com/keyolk/ghx/internal/pr"
)

// Unfolding escaped strings.
//
// A config value is often a single YAML/JSON scalar holding a whole document,
// with its line breaks written as `\n` escapes. The diff then carries that
// document as one line hundreds of cells wide: truncated, the change is
// invisible; a helm values.yaml whose `updatedNodePool:` string grew by 90
// lines shows up as one row ending in "…".
//
// So the line is drawn at its escapes, one segment per screen row. Only the
// drawing changes: the row model, the line numbers, and the comment anchor stay
// whole-line, because a whole line is what GitHub can attach a comment to. That
// is also why a continuation carries no line number — it is not a line of the
// file, it is more of the one above it.

// minEscapedBreaks is how many `\n` escapes a line needs before it is treated
// as an embedded document. One is what an ordinary format string has
// (`fmt.Errorf("...\n")`); splitting those would double the row count of a
// normal Go diff for nothing.
const minEscapedBreaks = 2

// Gutter widths, needed to know how much room the content actually has. Both
// are the width of the gutter the corresponding renderer builds — unified is
// `%5s %5s ` plus the +/- marker, a side-by-side half is one number plus it.
const (
	unifiedGutterCells = 13
	sideGutterCells    = 7
)

// splitEscapedNewlines breaks a string at its literal `\n` escapes and resolves
// the other escapes in each segment. Returns nil when there are no breaks.
//
// The scan consumes the character after a backslash as part of the escape, so a
// doubled backslash cannot produce a phantom break: `\\n` is a backslash
// followed by the letter n (a Windows path, a regex), not a newline. The same
// pass unquotes `\"`, `\\` and `\t`, because a segment still wearing them reads
// no better than the folded line did — the escaping is an artifact of the value
// being a scalar, which is exactly what the break marker already says.
func splitEscapedNewlines(s string) []string {
	if !strings.Contains(s, `\n`) {
		return nil
	}
	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			cur.WriteByte(c)
			continue
		}
		switch s[i+1] {
		case 'n':
			out = append(out, cur.String())
			cur.Reset()
		case '"':
			cur.WriteByte('"')
		case '\\':
			cur.WriteByte('\\')
		case 't':
			// A real tab, so expandTabs lines the segment up the way the file
			// would; two literal characters would not indent anything.
			cur.WriteByte('\t')
		case 'r':
			// A CRLF document would otherwise show two stray characters at the
			// end of every segment.
		default:
			cur.WriteByte(c)
			cur.WriteByte(s[i+1])
		}
		i++
	}
	return append(out, cur.String())
}

// unfoldContent returns the segments a line should be drawn as, or nil to keep
// it on one row.
//
// avail is the cells left for content after the gutter. A line that already
// fits is left alone: no text is being lost to truncation there, so the extra
// rows would only push the rest of the diff off the screen.
func unfoldContent(content string, avail int) []string {
	segs := splitEscapedNewlines(content)
	if len(segs) < minEscapedBreaks+1 {
		return nil
	}
	if avail <= 0 || lipglossWidth(content) <= avail {
		return nil
	}
	return segs
}

// unfoldUnified returns the segments row i renders as in the unified layout,
// or nil when it stays on one row.
func (v *diffView) unfoldUnified(i, width int) []string {
	if i < 0 || i >= len(v.rows) || v.rows[i].kind != rowDiffLine {
		return nil
	}
	return unfoldContent(v.rows[i].line.Content, width-unifiedGutterCells)
}

// unfoldHalf is the same for one column of the paired layout.
func (v *diffView) unfoldHalf(i, halfWidth int) []string {
	if i < 0 || i >= len(v.rows) || v.rows[i].kind != rowDiffLine {
		return nil
	}
	return unfoldContent(v.rows[i].line.Content, halfWidth-sideGutterCells)
}

// segLine is the synthetic diff line one segment renders as. The real line
// numbers go on the first segment only — a continuation belongs to the line
// above it, and numbering it would claim the file has lines it does not.
func segLine(r diffRow, seg int, segs []string) pr.DiffLine {
	l := pr.DiffLine{Kind: r.line.Kind, Content: segs[seg]}
	if seg == 0 {
		l.OldLineNo, l.NewLineNo = r.line.OldLineNo, r.line.NewLineNo
	}
	return l
}

// renderUnfoldedLine draws one segment of an unfolded row in the unified layout.
func (v *diffView) renderUnfoldedLine(i, width, seg int, segs []string) string {
	r := v.rows[i]
	selected := i == v.cursor
	inVisual := v.visual && v.rowInSelection(i)

	// Same reason as renderRow: a highlighted row is built from plain text so
	// the background covers the whole line instead of stopping at the first
	// reset sequence inside it.
	if selected || inVisual {
		plain, _ := truncateExact(unfoldedPlainText(r, seg, segs), width)
		plain = padCell(plain, width)
		if selected {
			return diffCursorStyle.Render(plain)
		}
		return selectedRowStyle.Render(plain)
	}

	s := renderDiffLine(r.path, segLine(r, seg, segs))
	if seg < len(segs)-1 {
		// Mark where the escape was, so the block still reads as one string
		// rather than as lines the file contains.
		s += dimStyle.Render(iconEscapedBreak)
	}
	s, _ = truncateExact(s, width)
	return padCell(s, width)
}

// unfoldedPlainText is one segment without styling, for rows that get a
// background instead. Mirrors rowPlainText's diff-line branch.
func unfoldedPlainText(r diffRow, seg int, segs []string) string {
	l := segLine(r, seg, segs)
	gutter := fmt.Sprintf("%5s %5s %s",
		lineNoStr(l.OldLineNo), lineNoStr(l.NewLineNo), diffMarker(l.Kind))
	s := gutter + expandTabs(l.Content, lipglossWidth(gutter))
	if seg < len(segs)-1 {
		s += iconEscapedBreak
	}
	return s
}

// diffMarker is the +/- column for a line kind.
func diffMarker(k pr.DiffLineKind) string {
	switch k {
	case pr.DiffLineAddition:
		return "+"
	case pr.DiffLineDeletion:
		return "-"
	}
	return " "
}

// screenLines is how many screen rows row i occupies in the unified layout,
// without rendering it. clampOffset needs the count, not the text.
func (v *diffView) screenLines(i, width int) int {
	r := v.rows[i]
	switch r.kind {
	case rowDiffLine:
		if segs := v.unfoldUnified(i, width); segs != nil {
			return len(segs)
		}
	case rowThread:
		return max(len(wrapText(v.threadText(r), max(width-6, 20))), 1)
	}
	return 1
}

// minOffsetFor is the highest scroll position that still leaves the cursor's
// row on screen.
//
// The unified offset is a row index while the window is measured in screen
// rows, and one unfolded row can be taller than the whole window. Without this
// floor the cursor sits inside [offset, offset+height) by index and off the
// bottom of the terminal in fact — j appears to stop working.
func (v *diffView) minOffsetFor(width, height int) int {
	used := 0
	i := v.cursor
	for ; i >= 0; i-- {
		n := v.screenLines(i, width)
		if i < v.cursor && used+n > height {
			break
		}
		used += n
	}
	return i + 1
}
