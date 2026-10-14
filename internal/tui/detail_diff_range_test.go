package tui

import (
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// twoHunkDiff has a gap between hunks: RIGHT lines 11-12 exist, 13..49 do not,
// then 50-51 exist. A range spanning the gap describes lines that are not in the
// diff at all, which GitHub rejects.
const twoHunkDiff = `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -10,2 +10,3 @@
 context a
+added one
+added two
@@ -50,2 +50,3 @@
 context b
+added three
+added four
`

func TestVisualRangeWithinOneHunkIsAllowed(t *testing.T) {
	v := newDiffView()
	if err := v.setContent(twoHunkDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	cursorToLine(t, v, "RIGHT", 11)
	v.visual = true
	v.visualStart = v.cursor
	cursorToLine(t, v, "RIGHT", 12)

	_, _, line, startLine, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok")
	}
	if startLine != 11 || line != 12 {
		t.Errorf("span = %d-%d, want 11-12", startLine, line)
	}
}

// Selecting across the hunk boundary must not be sent as a range: the lines
// between the hunks are not part of the diff, so GitHub refuses the comment.
func TestVisualRangeAcrossHunksFallsBackToSingleLine(t *testing.T) {
	v := newDiffView()
	if err := v.setContent(twoHunkDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	cursorToLine(t, v, "RIGHT", 11) // first hunk
	v.visual = true
	v.visualStart = v.cursor
	cursorToLine(t, v, "RIGHT", 51) // second hunk

	_, _, line, startLine, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok")
	}
	if line != 51 {
		t.Errorf("line = %d, want 51 (the cursor's own line)", line)
	}
	if startLine != 0 {
		t.Errorf("startLine = %d, want 0 — a cross-hunk selection is not a valid range", startLine)
	}
}

// Threads anchored in either hunk must both be placed correctly; a naive
// index built from a single hunk's counters would misplace the second.
func TestThreadOverlayAcrossHunks(t *testing.T) {
	threads := []pr.ReviewThread{
		{ID: "A", Path: "app.go", Line: 11, DiffSide: "RIGHT",
			Comments: []pr.ThreadComment{{Body: "first hunk"}}},
		{ID: "B", Path: "app.go", Line: 51, DiffSide: "RIGHT",
			Comments: []pr.ThreadComment{{Body: "second hunk"}}},
	}
	v := newDiffView()
	if err := v.setContent(twoHunkDiff, threads); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	for _, want := range []struct {
		id     string
		anchor int
	}{{"A", 11}, {"B", 51}} {
		idx := -1
		for i, r := range v.rows {
			if r.kind == rowDiffLine && r.side == "RIGHT" && r.anchorLine == want.anchor {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("no row for RIGHT:%d", want.anchor)
		}
		if idx+1 >= len(v.rows) || v.rows[idx+1].kind != rowThread ||
			v.rows[idx+1].threadID != want.id {
			t.Errorf("thread %s is not placed under RIGHT:%d", want.id, want.anchor)
		}
	}
}

// A selection that starts on a thread row (they sit between diff lines) has no
// anchor of its own, so it must not silently become a bogus range.
func TestVisualRangeStartingOnThreadRow(t *testing.T) {
	threads := []pr.ReviewThread{{
		ID: "T", Path: "app.go", Line: 11, DiffSide: "RIGHT",
		Comments: []pr.ThreadComment{{Body: "note"}},
	}}
	v := newDiffView()
	if err := v.setContent(twoHunkDiff, threads); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	// Park visualStart on the thread row itself.
	threadIdx := -1
	for i, r := range v.rows {
		if r.kind == rowThread {
			threadIdx = i
			break
		}
	}
	if threadIdx < 0 {
		t.Fatal("no thread row rendered")
	}
	v.visual = true
	v.visualStart = threadIdx
	cursorToLine(t, v, "RIGHT", 12)

	_, _, line, startLine, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok")
	}
	if line != 12 || startLine != 0 {
		t.Errorf("got start=%d line=%d, want start=0 line=12", startLine, line)
	}
}

// Deletions live only on the LEFT side; a range of them must report LEFT lines.
func TestVisualRangeOnDeletions(t *testing.T) {
	const delDiff = `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -10,3 +10,1 @@
 keep
-gone one
-gone two
`
	v := newDiffView()
	if err := v.setContent(delDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	cursorToLine(t, v, "LEFT", 11)
	v.visual = true
	v.visualStart = v.cursor
	cursorToLine(t, v, "LEFT", 12)

	_, side, line, startLine, ok := v.commentTarget()
	if !ok {
		t.Fatal("commentTarget: not ok")
	}
	if side != "LEFT" {
		t.Errorf("side = %q, want LEFT", side)
	}
	if startLine != 11 || line != 12 {
		t.Errorf("span = %d-%d, want 11-12", startLine, line)
	}
}

// The highlight must describe the range that would actually be posted. When the
// selection crosses a hunk boundary the range collapses to one line, so only
// that line may look selected.
func TestSelectionHighlightMatchesPostableRange(t *testing.T) {
	v := newDiffView()
	if err := v.setContent(twoHunkDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}

	t.Run("within a hunk highlights the span", func(t *testing.T) {
		cursorToLine(t, v, "RIGHT", 11)
		v.visual = true
		v.visualStart = v.cursor
		cursorToLine(t, v, "RIGHT", 12)

		var highlighted int
		for i := range v.rows {
			if v.rowInSelection(i) {
				highlighted++
			}
		}
		if highlighted != 2 {
			t.Errorf("highlighted %d rows, want 2 (lines 11 and 12)", highlighted)
		}
	})

	t.Run("across hunks highlights only the cursor", func(t *testing.T) {
		cursorToLine(t, v, "RIGHT", 11)
		v.visual = true
		v.visualStart = v.cursor
		cursorToLine(t, v, "RIGHT", 51)

		for i := range v.rows {
			if v.rowInSelection(i) && i != v.cursor {
				t.Fatalf("row %d is highlighted but cannot be part of the comment", i)
			}
		}
		if !v.rowInSelection(v.cursor) {
			t.Error("the cursor row should still read as selected")
		}
	})
}

// The footer has to name the real range so the operator knows what c will do.
func TestVisualHelpLineNamesTheRange(t *testing.T) {
	v := newDiffView()
	if err := v.setContent(twoHunkDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}

	cursorToLine(t, v, "RIGHT", 11)
	v.visual = true
	v.visualStart = v.cursor
	cursorToLine(t, v, "RIGHT", 12)
	if got := v.helpLine(footerBudget); !contains(got, "lines 11-12") {
		t.Errorf("help line should state the span, got: %s", got)
	}

	cursorToLine(t, v, "RIGHT", 51)
	if got := v.helpLine(footerBudget); !contains(got, "line 51 only") {
		t.Errorf("a cross-hunk selection should say it is one line, got: %s", got)
	}
}
