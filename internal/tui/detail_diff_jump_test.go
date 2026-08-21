package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// twoFileDiff builds a diff with two files of two hunks each, so hunk-level and
// file-level jumps have somewhere to go in both directions.
func twoFileDiff() string {
	var b strings.Builder
	for _, path := range []string{"a.txt", "b.txt"} {
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", path, path, path, path)
		for _, start := range []int{10, 200} {
			fmt.Fprintf(&b, "@@ -%d,3 +%d,4 @@\n", start, start)
			// A deletion paired with an addition, so the side-by-side layout has a
			// row with both columns filled — which is what h/l switch between.
			b.WriteString(" context\n-old line\n+new line\n context\n")
		}
	}
	return b.String()
}

func jumpTestView(t *testing.T) *diffView {
	t.Helper()
	v := newDiffView()
	if err := v.setContent(twoFileDiff(), nil); err != nil {
		t.Fatal(err)
	}
	return v
}

// The reason J/K exist: j/k are one row each, so reaching the next change in a
// long diff means holding a key. J walks the hunks.
func TestJumpHunkWalksEveryHunkBoundary(t *testing.T) {
	v := jumpTestView(t)

	var visited []diffRowKind
	for v.jumpHunk(1) {
		visited = append(visited, v.rows[v.cursor].kind)
	}
	// Two files of two hunks: file, hunk, hunk, file, hunk, hunk — minus the
	// first file header, which the cursor already starts on.
	if len(visited) != 5 {
		t.Fatalf("J visited %d boundaries, want 5: %v", len(visited), visited)
	}
	for i, k := range visited {
		if k != rowHunkHeader && k != rowFileHeader {
			t.Errorf("boundary %d landed on kind %v, not a header", i, k)
		}
	}
}

// A jump that has nowhere to go must stay put. Wrapping to the top would send
// the reviewer back through a file they just finished, and reads as the key
// having done nothing when it in fact moved a long way.
func TestJumpHunkStopsAtTheEnds(t *testing.T) {
	v := jumpTestView(t)
	for v.jumpHunk(1) {
	}
	last := v.cursor
	if v.jumpHunk(1) {
		t.Error("J moved past the last hunk")
	}
	if v.cursor != last {
		t.Errorf("cursor moved from %d to %d at the end", last, v.cursor)
	}

	for v.jumpHunk(-1) {
	}
	first := v.cursor
	if v.jumpHunk(-1) {
		t.Error("K moved above the first hunk")
	}
	if v.cursor != first {
		t.Errorf("cursor moved from %d to %d at the start", first, v.cursor)
	}
}

// K has to be J's inverse: land back on the boundary you came from.
func TestJumpHunkBackwardsRetracesTheSamePath(t *testing.T) {
	v := jumpTestView(t)
	var forward []int
	for v.jumpHunk(1) {
		forward = append(forward, v.cursor)
	}
	for i := len(forward) - 2; i >= 0; i-- {
		if !v.jumpHunk(-1) {
			t.Fatalf("K refused to move from %d", v.cursor)
		}
		if v.cursor != forward[i] {
			t.Errorf("K landed on %d, want %d", v.cursor, forward[i])
		}
	}
}

// { and } skip whole files, for the case where the interesting change is three
// files down and the hunks in between do not matter.
func TestJumpFileVisitsOnlyFileHeaders(t *testing.T) {
	v := jumpTestView(t)

	if !v.jumpFile(1) {
		t.Fatal("} refused to move to the second file")
	}
	r := v.rows[v.cursor]
	if r.kind != rowFileHeader || r.path != "b.txt" {
		t.Errorf("} landed on %v %q, want the b.txt header", r.kind, r.path)
	}
	if v.jumpFile(1) {
		t.Error("} moved past the last file")
	}
	if !v.jumpFile(-1) {
		t.Fatal("{ refused to move back")
	}
	if r := v.rows[v.cursor]; r.kind != rowFileHeader || r.path != "a.txt" {
		t.Errorf("{ landed on %v %q, want the a.txt header", r.kind, r.path)
	}
}

// A folded file collapses to its header, so { and } become a table of contents.
// A jump must not land inside a file that is not showing its lines.
func TestJumpFileWorksWithAFoldedFile(t *testing.T) {
	v := jumpTestView(t)
	v.folded["a.txt"] = true
	v.rebuild()

	if !v.jumpFile(1) {
		t.Fatal("} refused to move past the folded file")
	}
	if r := v.rows[v.cursor]; r.path != "b.txt" {
		t.Errorf("} landed on %q, want b.txt", r.path)
	}
}

// A jump goes through setCursor, which invalidates both scroll offsets so the
// render centres on the destination. Without that the paired layout keeps its
// own offset and the jump appears to do nothing.
func TestJumpInvalidatesBothScrollOffsets(t *testing.T) {
	v := jumpTestView(t)
	v.offset, v.sideOffset = 0, 0
	if !v.jumpHunk(1) {
		t.Fatal("J refused to move")
	}
	if v.offset != -1 || v.sideOffset != -1 {
		t.Errorf("offsets are %d/%d after a jump, want -1/-1 so the view centres",
			v.offset, v.sideOffset)
	}
}

// A jump crosses a hunk boundary by definition, and a comment range that does
// is not one GitHub accepts. Rather than silently collapsing the selection, the
// jump keys do nothing while a range is open — but they stay consumed, so
// nothing else acts on them mid-selection.
func TestJumpKeysAreInertInVisualMode(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.activeTab = tabDiff
	if err := a.detail.diff.setContent(twoFileDiff(), nil); err != nil {
		t.Fatal(err)
	}
	v := a.detail.diff
	// Put the cursor on a commentable line and open a range.
	for i, r := range v.rows {
		if r.kind == rowDiffLine && r.anchorLine != 0 {
			v.cursor = i
			break
		}
	}
	a.handleKey(keyMsg("v"))
	if !v.visual {
		t.Fatal("v did not open a visual range")
	}
	before := v.cursor

	for _, key := range []string{"J", "K", "{", "}"} {
		if _, handled := a.detail.update(keyMsg(key)); !handled {
			t.Errorf("%s was not consumed in visual mode", key)
		}
		if v.cursor != before {
			t.Errorf("%s moved the cursor from %d to %d during a selection",
				key, before, v.cursor)
		}
	}
	if !v.visual {
		t.Error("a jump key cancelled the visual range")
	}
}

// The new keys must not take anything j/k/h/l already owned.
func TestJumpKeysDoNotDisturbLineNavigation(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.activeTab = tabDiff
	if err := a.detail.diff.setContent(twoFileDiff(), nil); err != nil {
		t.Fatal(err)
	}
	v := a.detail.diff

	v.cursor = 0
	a.handleKey(keyMsg("j"))
	if v.cursor != 1 {
		t.Errorf("j moved the cursor to %d, want 1", v.cursor)
	}
	a.handleKey(keyMsg("k"))
	if v.cursor != 0 {
		t.Errorf("k moved the cursor to %d, want 0", v.cursor)
	}

	// h and l are untouched: the unified layout has no column to switch between,
	// so the diff tab leaves them to whatever else claims them — which is what
	// it did before the jump keys existed.
	for _, key := range []string{"h", "l"} {
		if _, handled := a.detail.update(keyMsg(key)); handled {
			t.Errorf("the diff tab now consumes %s in the unified layout", key)
		}
	}

	// In the paired layout h/l pick a column, and that still works. It needs a
	// row with both halves filled, which is what the paired deletion/addition in
	// each hunk provides.
	v.sideBySide = true
	for i, r := range v.rows {
		if r.kind == rowDiffLine && r.line.Kind == pr.DiffLineAddition {
			v.cursor = i
			break
		}
	}
	v.syncSideFocus()
	if _, handled := a.detail.update(keyMsg("h")); !handled {
		t.Error("h no longer switches columns in the paired layout")
	}
	if v.sideFocus != sideLeft {
		t.Error("h did not move the focus to the left column")
	}
}

// Only the diff tab claims the jump keys; elsewhere they must fall through so a
// future binding can have them.
func TestJumpKeysAreDiffTabOnly(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")

	for _, tab := range a.detail.tabs {
		if tab == tabDiff {
			continue
		}
		a.detail.activeTab = tab
		for _, key := range []string{"J", "{", "}"} {
			if _, handled := a.detail.update(keyMsg(key)); handled {
				t.Errorf("the %s tab consumed %s", detailTabNames[tab], key)
			}
		}
	}
}
