package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// R reloads the visible source. It already did, but nothing said so: the
// full-pane spinner only draws on an empty source, so pressing R over a list
// that already had rows changed nothing on screen and read as a dead key.

func TestRefreshKeyIsAdvertisedInTheFooter(t *testing.T) {
	a := testApp(t, sampleRows)
	if got := a.list.helpLine(); !contains(got, "R") || !contains(got, "refresh") {
		t.Errorf("footer = %q, want it to mention R:refresh", got)
	}
}

func TestRefreshKeyReloadsTheVisibleSource(t *testing.T) {
	a := testApp(t, sampleRows)
	before := a.list.generations[a.list.curTab]

	cmd := a.list.update(keyMsg("R"))
	if cmd == nil {
		t.Fatal("R produced no reload command")
	}
	if !a.list.loadings[a.list.curTab] {
		t.Error("R did not mark the source as loading")
	}
	if a.list.generations[a.list.curTab] == before {
		t.Error("R did not start a new fetch generation")
	}
}

// A reload over existing rows must be visible. The rows stay put — blanking a
// good list to show a spinner would be worse than the silence — so the tab
// carries the indicator instead.
func TestReloadOverExistingRowsShowsInTheTab(t *testing.T) {
	a := testApp(t, sampleRows)
	quiet := a.list.renderTabs(160)

	a.list.update(keyMsg("R"))
	busy := a.list.renderTabs(160)

	if busy == quiet {
		t.Errorf("tab strip unchanged while reloading: %q", busy)
	}
	if !strings.ContainsAny(busy, strings.Join(spinnerFrames, "")) {
		t.Errorf("tab strip has no spinner while reloading: %q", busy)
	}
	// The rows themselves must survive the reload.
	if len(a.list.list.VisibleItems()) != len(sampleRows) {
		t.Errorf("visible rows = %d, want %d kept during reload",
			len(a.list.list.VisibleItems()), len(sampleRows))
	}
}

// The spinner has to actually animate: prListModel.setSpinnerFrame existed but
// nothing ever called it, so every list-side spinner sat frozen on one glyph.
func TestListSpinnerAdvancesWhileLoading(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.update(keyMsg("R"))

	first := a.list.spinner
	for i := 0; i < 3; i++ {
		a.Update(spinnerTickMsg{})
	}
	if a.list.spinner == first {
		t.Errorf("list spinner frame stayed at %d across ticks", first)
	}
}

// An idle list must not animate — the tick only rearms while something loads.
func TestIdleListShowsNoSpinner(t *testing.T) {
	a := testApp(t, sampleRows)
	if got := a.list.renderTabs(160); strings.ContainsAny(got, strings.Join(spinnerFrames, "")) {
		t.Errorf("idle tab strip shows a spinner: %q", got)
	}
}

// An empty source keeps the full-pane spinner; the tab marker is for the case
// the pane cannot cover.
func TestEmptySourceStillUsesThePaneSpinner(t *testing.T) {
	a := testApp(t, []pr.Summary{})
	a.list.update(keyMsg("R"))
	if got := a.list.view(160, 40); !contains(got, "Loading") {
		t.Errorf("empty source should show the pane spinner: %q", got)
	}
}
