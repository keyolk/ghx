package tui

import (
	"fmt"
	"strings"
	"testing"
)

// In the paired layout one screen row can hold two diff rows — a modification's
// deletion on the left, its addition on the right. The cursor is a single row
// index, so without an explicit notion of which column it is in, both halves
// look selected and a range spanning them mixes LEFT and RIGHT lines (which
// GitHub rejects). These tests pin down the column handling.

// modDiff has a two-line modification: LEFT 10-11 against RIGHT 10-11, plus a
// pure insertion at RIGHT 12 whose left half is blank.
const modDiff = `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -10,2 +10,3 @@
-old one
-old two
+new one
+new two
+added only
`

func sideViewAt(t *testing.T, raw, side string, line int) *diffView {
	t.Helper()
	v := newDiffView()
	if err := v.setContent(raw, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	v.sideBySide = true
	cursorToLine(t, v, side, line)
	v.syncSideFocus()
	return v
}

// h/l must move between the two halves of a modification, and the comment
// target must follow — that is the whole point of having a column focus.
func TestSideFocusMovesBetweenColumns(t *testing.T) {
	v := sideViewAt(t, modDiff, "RIGHT", 10)
	if v.sideFocus != sideRight {
		t.Fatalf("focus = %v, want right after landing on a RIGHT row", v.sideFocus)
	}

	if !v.focusSide(sideLeft) {
		t.Fatal("h should move to the left column of a modification")
	}
	_, side, line, _, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok on the left column")
	}
	if side != "LEFT" || line != 10 {
		t.Errorf("target = %s:%d, want LEFT:10", side, line)
	}

	if !v.focusSide(sideRight) {
		t.Fatal("l should move back to the right column")
	}
	_, side, line, _, ok = v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok on the right column")
	}
	if side != "RIGHT" || line != 10 {
		t.Errorf("target = %s:%d, want RIGHT:10", side, line)
	}
}

// A pure insertion has no left half, so h has nothing to move to and must leave
// the key for whatever else claims it (cycling tabs).
func TestFocusSideRefusesWhenHalfIsBlank(t *testing.T) {
	v := sideViewAt(t, modDiff, "RIGHT", 12) // "added only"
	if v.focusSide(sideLeft) {
		t.Error("h must not claim the key when the left half is blank")
	}
	// The cursor must not have moved.
	_, side, line, _, _ := v.commentTarget()
	if side != "RIGHT" || line != 12 {
		t.Errorf("cursor moved to %s:%d, want RIGHT:12", side, line)
	}
}

// Only the focused column may look selected. Highlighting both halves of a
// modification misstates where a comment would land — the reported symptom.
func TestOnlyFocusedColumnIsHighlighted(t *testing.T) {
	forceColor(t)
	v := sideViewAt(t, modDiff, "RIGHT", 10)

	out := v.renderSideBySide(160, 12)
	row := lineContaining(out, "new one")
	if row == "" {
		t.Fatal("could not find the modification row")
	}
	if !strings.Contains(stripANSISeqs(row), "old one") {
		t.Fatal("expected the pair on one screen row")
	}
	// Exactly one highlighted span: the focused half.
	if n := len(bgOpen.FindAllString(row, -1)); n != 1 {
		t.Errorf("row has %d highlighted spans, want 1 (only the focused column)\n%q", n, row)
	}
	// And it must be the right-hand one.
	halves := strings.SplitN(row, "│", 2)
	if len(halves) != 2 {
		t.Fatalf("row is not split by the divider: %q", row)
	}
	if bgOpen.MatchString(halves[0]) {
		t.Errorf("the unfocused left column is highlighted:\n%q", halves[0])
	}
	if !bgOpen.MatchString(halves[1]) {
		t.Errorf("the focused right column is not highlighted:\n%q", halves[1])
	}
}

// j/k must step one screen row. Stepping the flat row index would move the
// cursor within a pair, so the view would not appear to move at all.
func TestSideCursorStepsScreenRows(t *testing.T) {
	v := sideViewAt(t, modDiff, "RIGHT", 10)
	pairs := v.sideRows()
	before := v.cursorPairIndex(pairs)

	v.moveDown(1)
	after := v.cursorPairIndex(v.sideRows())
	if after != before+1 {
		t.Errorf("one j moved %d screen rows, want 1", after-before)
	}
	// The column should be preserved.
	if v.sideFocus != sideRight {
		t.Errorf("focus drifted to %v while moving down", v.sideFocus)
	}
	_, side, _, _, _ := v.commentTarget()
	if side != "RIGHT" {
		t.Errorf("side = %q after moving down, want RIGHT", side)
	}
}

// Moving onto a row whose focused half is blank should still move — refusing
// would trap the cursor — and adopt the side that exists.
func TestSideCursorFallsToOtherHalfWhenBlank(t *testing.T) {
	v := sideViewAt(t, modDiff, "LEFT", 11) // last deletion
	v.moveDown(1)                           // next screen row has no left half

	if v.sideFocus != sideRight {
		t.Errorf("focus = %v, want right once the left half runs out", v.sideFocus)
	}
	_, side, line, _, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok after falling to the other half")
	}
	if side != "RIGHT" || line != 12 {
		t.Errorf("target = %s:%d, want RIGHT:12", side, line)
	}
}

// A visual selection lives on one side. Extending it must not hop columns, or
// the range would mix LEFT and RIGHT lines and stop being postable — the
// "line N only" the footer reported in the screenshot.
func TestVisualRangeStaysOnOneSideInPairedLayout(t *testing.T) {
	v := sideViewAt(t, modDiff, "RIGHT", 10)
	v.visual = true
	v.visualStart = v.cursor
	v.syncSideFocus()

	v.moveDown(1)

	_, side, line, startLine, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok")
	}
	if side != "RIGHT" {
		t.Errorf("side = %q, want RIGHT throughout the selection", side)
	}
	if startLine == 0 {
		t.Fatalf("range collapsed to a single line (%d) instead of spanning", line)
	}
	lo, hi := startLine, line
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo != 10 || hi != 11 {
		t.Errorf("range = %d-%d, want 10-11", lo, hi)
	}
	if !contains(v.helpLine(footerBudget), "lines 10-11") {
		t.Errorf("footer should report the span: %s", v.helpLine(footerBudget))
	}
}

// The same on the left column: a range of deletions must report LEFT lines.
func TestVisualRangeOnLeftColumn(t *testing.T) {
	v := sideViewAt(t, modDiff, "LEFT", 10)
	v.visual = true
	v.visualStart = v.cursor
	v.syncSideFocus()

	v.moveDown(1)

	_, side, line, startLine, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok")
	}
	if side != "LEFT" {
		t.Errorf("side = %q, want LEFT", side)
	}
	if startLine != 10 || line != 11 {
		t.Errorf("range = %d-%d, want 10-11", startLine, line)
	}
}

// Switching layout must leave the focus agreeing with the row the cursor is on,
// or the paired view would highlight the opposite half.
func TestLayoutToggleSyncsFocus(t *testing.T) {
	v := newDiffView()
	if err := v.setContent(modDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	cursorToLine(t, v, "LEFT", 10)
	v.sideFocus = sideRight // stale

	v.sideBySide = true
	v.syncSideFocus()
	if v.sideFocus != sideLeft {
		t.Errorf("focus = %v, want left to match the LEFT row under the cursor", v.sideFocus)
	}
}

// Jumping in from another tab has to set the focus too.
func TestJumpToSyncsFocus(t *testing.T) {
	v := newDiffView()
	if err := v.setContent(modDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	v.sideBySide = true
	v.sideFocus = sideLeft

	v.jumpTo("app.go", 12) // RIGHT-only line
	if v.sideFocus != sideRight {
		t.Errorf("focus = %v after jumping to a RIGHT line, want right", v.sideFocus)
	}
}

// The footer says which column is focused, so it is clear which side of a
// modification a comment would attach to.
func TestFooterNamesFocusedColumn(t *testing.T) {
	v := sideViewAt(t, modDiff, "RIGHT", 10)
	if got := v.helpLine(footerBudget); !contains(got, "[new]") {
		t.Errorf("footer should mark the new side: %s", got)
	}
	v.focusSide(sideLeft)
	if got := v.helpLine(footerBudget); !contains(got, "[old]") {
		t.Errorf("footer should mark the old side: %s", got)
	}
	// The unified layout has no columns, so it must not claim one.
	v.sideBySide = false
	if got := v.helpLine(footerBudget); contains(got, "[old]") || contains(got, "[new]") {
		t.Errorf("unified layout should not name a column: %s", got)
	}
}

// h/l on a header or thread row has no column to switch to; the key must fall
// through so it can still cycle tabs.
func TestFocusSideIgnoresFullWidthRows(t *testing.T) {
	v := newDiffView()
	if err := v.setContent(modDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	v.sideBySide = true
	v.cursor = 0 // file header
	if v.focusSide(sideLeft) || v.focusSide(sideRight) {
		t.Error("a full-width row has no columns to move between")
	}
}

// lineContaining returns the first rendered line holding want.
func lineContaining(out, want string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(stripANSISeqs(line), want) {
			return line
		}
	}
	return ""
}

// A block of three modified lines: the columns each hold a run of same-side
// lines, which is what a multi-line range needs.
const threeLineModDiff = `diff --git a/x.json b/x.json
--- a/x.json
+++ b/x.json
@@ -5288,6 +5300,6 @@
 keep a
-old 1
-old 2
-old 3
+new 1
+new 2
+new 3
 keep b
`

// Extending down a run of deletions must produce a LEFT range, and the same for
// additions on the right. This is the case the paired layout exists for.
func TestVisualRangeSpansRunOnEitherColumn(t *testing.T) {
	cases := []struct {
		name      string
		side      string
		startLine int
		wantLo    int
		wantHi    int
	}{
		{"deletions on the left", "LEFT", 5289, 5289, 5291},
		{"additions on the right", "RIGHT", 5301, 5301, 5303},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := sideViewAt(t, threeLineModDiff, c.side, c.startLine)
			v.visual = true
			v.visualStart = v.cursor
			v.syncSideFocus()

			v.moveDown(1)
			v.moveDown(1)

			_, side, line, start, ok := v.commentTarget()
			if !ok {
				t.Fatal("commentTarget: not ok")
			}
			if side != c.side {
				t.Errorf("side = %q, want %q for the whole range", side, c.side)
			}
			lo, hi := start, line
			if lo > hi {
				lo, hi = hi, lo
			}
			if lo != c.wantLo || hi != c.wantHi {
				t.Errorf("range = %d-%d, want %d-%d", lo, hi, c.wantLo, c.wantHi)
			}
			want := fmt.Sprintf("lines %d-%d", c.wantLo, c.wantHi)
			if !contains(v.helpLine(footerBudget), want) {
				t.Errorf("footer should report %q, got: %s", want, stripANSISeqs(v.helpLine(footerBudget)))
			}
		})
	}
}

// Only the focused column's rows may look selected while a range is open — the
// other side is not part of the comment.
func TestVisualRangeHighlightsOneColumnOnly(t *testing.T) {
	forceColor(t)
	v := sideViewAt(t, threeLineModDiff, "LEFT", 5289)
	v.visual = true
	v.visualStart = v.cursor
	v.syncSideFocus()
	v.moveDown(1)

	out := v.renderSideBySide(160, 14)
	for _, text := range []string{"old 1", "old 2"} {
		row := lineContaining(out, text)
		if row == "" {
			t.Fatalf("row for %q not rendered", text)
		}
		halves := strings.SplitN(row, "│", 2)
		if len(halves) != 2 {
			t.Fatalf("row not split by the divider: %q", row)
		}
		if !bgOpen.MatchString(halves[0]) {
			t.Errorf("%q should be highlighted on the left:\n%q", text, row)
		}
		if bgOpen.MatchString(halves[1]) {
			t.Errorf("the right column must stay unhighlighted for a LEFT range:\n%q", row)
		}
	}
}

// A run that ends must not silently extend past its side: stepping beyond the
// last same-side row leaves the range where it was.
func TestVisualRangeStopsAtEndOfRun(t *testing.T) {
	v := sideViewAt(t, threeLineModDiff, "LEFT", 5291) // last deletion
	v.visual = true
	v.visualStart = v.cursor
	v.syncSideFocus()

	v.moveDown(1) // nothing below on the left within this run

	_, side, line, start, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok")
	}
	if side != "LEFT" {
		t.Errorf("side = %q, want LEFT (the selection must not hop columns)", side)
	}
	if start != 0 || line != 5291 {
		t.Errorf("got start=%d line=%d, want a single LEFT line 5291", start, line)
	}
}
