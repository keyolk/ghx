package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/keyolk/ghx/internal/pr"
)

// Draft was visible only as a dimmed title, which is indistinguishable from a
// dim theme and says nothing on the cursor row (where the whole line is themed
// as one). It now has its own marker in the status cell and its own filter.

func TestDraftMarkerAppearsInTheStatusCell(t *testing.T) {
	forceColor(t)
	p := pr.Summary{Number: 1, Title: "wip", Repo: "acme/one", State: "OPEN", IsDraft: true}

	if got := prStatusMark(p); !strings.HasPrefix(got, "D ") {
		t.Errorf("status mark = %q, want it to lead with D", got)
	}
	if !strings.Contains(stripANSI(prStatusCell(p)), "D") {
		t.Errorf("styled status cell has no D: %q", prStatusCell(p))
	}

	// And the row a reviewer actually reads.
	items := []list.Item{prListItem{pr: p}}
	l := list.New(items, prListDelegate{}, 120, 10)
	initListBase(&l)
	var b strings.Builder
	prListDelegate{}.Render(&b, l, 0, items[0])
	if !strings.Contains(stripANSI(b.String()), "D") {
		t.Errorf("the rendered row does not show the draft marker: %q", b.String())
	}
}

// The marker must be absent when the PR is ready, or it says nothing.
func TestReadyPRHasNoDraftMarker(t *testing.T) {
	p := pr.Summary{Number: 1, Title: "ready", Repo: "acme/one", State: "OPEN"}
	if got := prStatusMark(p); !strings.HasPrefix(got, "· ") {
		t.Errorf("a ready PR shows a draft marker: %q", got)
	}
}

// Draft joins the f picker so a queue can be narrowed to (or away from) WIP.
func TestDraftIsAFilterableStatus(t *testing.T) {
	rows := []pr.Summary{
		{Number: 1, Title: "wip", Repo: "acme/one", State: "OPEN", IsDraft: true},
		{Number: 2, Title: "ready", Repo: "acme/one", State: "OPEN"},
	}
	a := testApp(t, rows)
	a.list.statusFilters[statusDraft] = true
	a.list.syncListItems()

	visible := a.list.list.VisibleItems()
	if len(visible) != 1 {
		t.Fatalf("%d rows visible, want only the draft", len(visible))
	}
	if !visible[0].(prListItem).pr.IsDraft {
		t.Error("the draft filter kept the wrong row")
	}
}

// Every option must fit inside the picker's box. It used to be a literal
// height, so adding a status silently clipped the last one off the bottom.
func TestStatusFilterBoxFitsEveryOption(t *testing.T) {
	a := testApp(t, sampleRows)
	a.openStatusFilter()
	out := stripANSI(a.renderStatusFilter(80, 40))
	for _, status := range prStatusOptions {
		if !strings.Contains(out, string(status)) {
			t.Errorf("the picker does not show %q:\n%s", status, out)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && (r == 'm' || r == 'G' || r == 'K'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}
