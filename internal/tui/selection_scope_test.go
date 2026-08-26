package tui

import (
	"testing"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// A mark outlives the view that made it: switching tabs or narrowing the filter
// hides a row without unmarking it. Acting on a mark the user cannot see is how
// an old selection came back and opened alongside the new one, so the targets
// are scoped to what is on screen — while the mark itself survives, ready where
// it was left.

func targetNumbers(targets []actionTarget) []int {
	out := make([]int, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.number)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// addTab appends a second source with its own rows, as selectTab expects.
func addTab(t *testing.T, a *App, name string, rows []pr.Summary) {
	t.Helper()
	l := a.list
	l.sources = append(l.sources, config.SourceDef{Name: name, Query: "state:open"})
	l.caches = append(l.caches, rows)
	l.loadings = append(l.loadings, false)
	l.generations = append(l.generations, 0)
	l.errs = append(l.errs, nil)
}

func TestActionTargetsIgnoreSelectionsHiddenByFilter(t *testing.T) {
	a := testApp(t, sampleRows)

	a.list.update(keyMsg("space")) // mark #101
	a.list.applyQuery("second")    // #101 is no longer on screen
	a.list.update(keyMsg("space")) // mark #202, the only visible row

	targets, ok := a.actionTargets()
	if !ok {
		t.Fatal("no targets after marking the visible row")
	}
	if got := targetNumbers(targets); !equalInts(got, []int{202}) {
		t.Errorf("targets = %v, want only the visible #202", got)
	}

	// Clearing the filter brings the older mark back rather than discarding it.
	a.list.applyQuery("")
	targets, _ = a.actionTargets()
	if got := targetNumbers(targets); !equalInts(got, []int{101, 202}) {
		t.Errorf("targets = %v after clearing the filter, want both marks back", got)
	}
}

func TestActionTargetsIgnoreSelectionsOnOtherTabs(t *testing.T) {
	a := testApp(t, sampleRows)
	addTab(t, a, "second tab", []pr.Summary{
		{Number: 303, Title: "third pr", Repo: "acme/three", State: "OPEN"},
	})

	a.list.update(keyMsg("space")) // mark #101 on the first tab
	a.list.selectTab(1)
	a.list.update(keyMsg("space")) // mark #303 on the second

	targets, ok := a.actionTargets()
	if !ok {
		t.Fatal("no targets on the second tab")
	}
	if got := targetNumbers(targets); !equalInts(got, []int{303}) {
		t.Errorf("targets = %v, want only this tab's #303", got)
	}

	a.list.selectTab(0)
	targets, _ = a.actionTargets()
	if got := targetNumbers(targets); !equalInts(got, []int{101}) {
		t.Errorf("targets = %v back on the first tab, want its own #101", got)
	}
}

// With every mark off-screen the list looks unmarked, so it must behave that
// way: the focused row is the target, not the invisible selection.
func TestActionTargetsFallBackToFocusWhenEverySelectionIsHidden(t *testing.T) {
	a := testApp(t, sampleRows)
	addTab(t, a, "second tab", []pr.Summary{
		{Number: 303, Title: "third pr", Repo: "acme/three", State: "OPEN"},
	})

	a.list.update(keyMsg("space")) // mark #101 on the first tab
	a.list.selectTab(1)            // nothing is marked here

	targets, ok := a.actionTargets()
	if !ok {
		t.Fatal("no targets with the focused row available")
	}
	if got := targetNumbers(targets); !equalInts(got, []int{303}) {
		t.Errorf("targets = %v, want the focused #303", got)
	}
}

// The count has to describe what an action will take, and say so when marks are
// waiting out of sight.
func TestTitleCountsVisibleSelectionsAndNamesTheHiddenOnes(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.update(keyMsg("space")) // mark #101

	if got := a.list.title(); !contains(got, "1 selected") {
		t.Errorf("title = %q, want a visible selection count", got)
	}
	if got := a.list.title(); contains(got, "marked elsewhere") {
		t.Errorf("title = %q, want no hidden-mark note while the mark is on screen", got)
	}

	a.list.applyQuery("second")
	got := a.list.title()
	if contains(got, "1 selected") {
		t.Errorf("title = %q, want no visible count once the mark is filtered out", got)
	}
	if !contains(got, "1 marked elsewhere") {
		t.Errorf("title = %q, want the hidden mark named", got)
	}
}
