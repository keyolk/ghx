package tui

import (
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

func foldTestApp(t *testing.T) (*App, *diffView) {
	t.Helper()
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.detail = &pr.Detail{Number: 1, URL: "https://x/1"}
	a.detail.activeTab = tabDiff
	if err := a.detail.diff.setContent(twoFileDiff(), nil); err != nil {
		t.Fatal(err)
	}
	return a, a.detail.diff
}

// h folds and l unfolds the file whose header the cursor is on. o toggles, but
// a toggle gives no way to say "collapse this" without first knowing the
// current state — which is exactly what you do not know while skimming.
func TestFoldWithHAndUnfoldWithL(t *testing.T) {
	a, v := foldTestApp(t)
	v.cursor = 0
	path := v.rows[0].path

	a.handleKey(keyMsg("h"))
	if !v.folded[path] {
		t.Fatalf("h did not fold %s", path)
	}
	a.handleKey(keyMsg("l"))
	if v.folded[path] {
		t.Errorf("l did not unfold %s", path)
	}
}

// The cursor must land on the header it acted on: folding removes every row
// below it, and leaving the cursor at a stale index would put it in a different
// file.
func TestFoldKeepsTheCursorOnTheHeader(t *testing.T) {
	_, v := foldTestApp(t)
	// Start inside the first file's second hunk.
	for i, r := range v.rows {
		if r.kind == rowHunkHeader && r.hunkIdx == 1 {
			v.cursor = i
			break
		}
	}
	path := v.rows[v.cursor].path
	v.setFold(path, true)
	r := v.rows[v.cursor]
	if r.kind != rowFileHeader || r.path != path {
		t.Errorf("cursor landed on %v %q, want the %s header", r.kind, r.path, path)
	}
}

// h and l keep their old meanings everywhere the fold has nothing to act on.
// In the paired layout they choose the column a comment would attach to, which
// is the only way to aim at a deleted line — a modification puts both sides on
// one screen row.
func TestFoldDoesNotTakeTheColumnKeys(t *testing.T) {
	a, v := foldTestApp(t)
	v.sideBySide = true
	for i, r := range v.rows {
		if r.kind == rowDiffLine && r.line.Kind == pr.DiffLineAddition {
			v.cursor = i
			break
		}
	}
	v.syncSideFocus()
	if _, handled := a.detail.update(keyMsg("h")); !handled {
		t.Fatal("h no longer switches columns in the paired layout")
	}
	if v.sideFocus != sideLeft {
		t.Error("h did not move the focus to the left column")
	}
	if len(v.folded) != 0 {
		t.Error("h folded a file while the cursor was on a diff line")
	}
}

// Pressing h on an already-folded file must not swallow the key: with nothing
// to do, it should still cycle tabs.
func TestFoldFallsThroughWhenNothingChanges(t *testing.T) {
	a, v := foldTestApp(t)
	v.cursor = 0
	v.setFold(v.rows[0].path, true)
	if _, handled := a.detail.update(keyMsg("h")); handled {
		t.Error("h was consumed on an already-folded file")
	}
	// And l on an unfolded one.
	v.setFold(v.rows[0].path, false)
	v.cursor = 0
	if _, handled := a.detail.update(keyMsg("l")); handled {
		t.Error("l was consumed on an already-unfolded file")
	}
}

// H and L move file to file, pairing with J/K for hunks. { and } stay as
// aliases rather than being taken away from anyone used to them.
func TestFileJumpOnHAndL(t *testing.T) {
	a, v := foldTestApp(t)
	v.cursor = 0

	a.handleKey(keyMsg("L"))
	if r := v.rows[v.cursor]; r.kind != rowFileHeader || r.path != "b.txt" {
		t.Errorf("L landed on %v %q, want the b.txt header", r.kind, r.path)
	}
	a.handleKey(keyMsg("H"))
	if r := v.rows[v.cursor]; r.kind != rowFileHeader || r.path != "a.txt" {
		t.Errorf("H landed on %v %q, want the a.txt header", r.kind, r.path)
	}

	for _, key := range []string{"}", "{"} {
		before := v.cursor
		a.handleKey(keyMsg(key))
		if v.cursor == before {
			t.Errorf("%s no longer moves between files", key)
		}
	}
}

// L in the diff tab now moves files rather than opening the label picker. That
// is a real cost, so it is asserted rather than left to be discovered: the
// picker must still be reachable from every other tab.
func TestLabelPickerStillReachableOutsideTheDiffTab(t *testing.T) {
	for _, tab := range []detailTabKind{tabOverview, tabFiles, tabComments, tabCommits, tabChecks} {
		a, _ := foldTestApp(t)
		a.detail.activeTab = tab
		a.handleKey(keyMsg("L"))
		if a.labels == nil {
			t.Errorf("L no longer opens the label picker from the %s tab",
				detailTabNames[tab])
		}
	}
	// And from the diff tab, via the palette.
	a, _ := foldTestApp(t)
	if cmd := a.runPalette("labels"); cmd != nil {
		cmd()
	}
}
