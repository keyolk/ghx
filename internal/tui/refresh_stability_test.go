package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/pr"
)

// rowsFor builds a queue in the order a response delivers it: updatedAt
// descending, which is what makes an index unstable across a refresh.
func rowsFor(numbers ...int) []pr.Summary {
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	out := make([]pr.Summary, 0, len(numbers))
	for i, n := range numbers {
		out = append(out, pr.Summary{
			ID:        "PR_" + itoa(n),
			Number:    n,
			Title:     "pull request " + itoa(n),
			Repo:      "acme/one",
			State:     "OPEN",
			UpdatedAt: base.Add(-time.Duration(i) * time.Hour),
		})
	}
	return out
}

func listApp(t *testing.T, rows []pr.Summary) *App {
	t.Helper()
	a := testApp(t, rows)
	a.width, a.height = 120, 20
	a.list.resize(120, 20)
	return a
}

func cursorNumber(t *testing.T, a *App) int {
	t.Helper()
	it, ok := a.list.selectedItem()
	if !ok {
		t.Fatal("no row under the cursor")
	}
	return it.pr.Number
}

// A poll that brings in a newer PR re-sorts the queue. Restoring the cursor by
// index puts it on whatever row slid into that slot, which redraws the preview
// pane for a PR the user never selected.
func TestRefreshKeepsCursorOnTheSamePR(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3, 4, 5))
	a.list.list.Select(2)
	if got := cursorNumber(t, a); got != 3 {
		t.Fatalf("setup: cursor on #%d, want #3", got)
	}

	a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: a.list.generations[0],
		prs:        rowsFor(9, 1, 2, 3, 4, 5),
	})

	if got := cursorNumber(t, a); got != 3 {
		t.Errorf("cursor moved to #%d after a refresh, want #3", got)
	}
}

// The same must hold under an active filter, where the cursor indexes the
// filtered slice rather than the full one.
func TestRefreshKeepsCursorOnTheSamePRWhileFiltered(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3, 4, 5))
	a.list.list.SetFilterText("pull request")
	a.list.list.Select(2)
	want := cursorNumber(t, a)

	a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: a.list.generations[0],
		prs:        rowsFor(9, 1, 2, 3, 4, 5),
	})

	if got := cursorNumber(t, a); got != want {
		t.Errorf("filtered cursor moved to #%d after a refresh, want #%d", got, want)
	}
}

// A PR that leaves the queue — merged, closed — has no row to return to. The
// cursor should stay where the user was reading rather than jump to the top.
func TestRefreshFallsBackWhenTheSelectedPRIsGone(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3, 4, 5))
	a.list.list.Select(2)

	a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: a.list.generations[0],
		prs:        rowsFor(1, 2, 4, 5),
	})

	if got := a.list.list.Index(); got != 2 {
		t.Errorf("cursor index = %d after the selected PR vanished, want 2", got)
	}
}

// A bulk action must not blank the queue the user is working through. The rows
// are seconds stale at worst, and the generation bump is what retires them.
func TestBulkActionKeepsRowsWhileReloading(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	before := len(a.list.list.VisibleItems())

	a.list.invalidateCachesAndRefresh()

	if got := len(a.list.list.VisibleItems()); got != before {
		t.Errorf("visible rows %d during reload, want %d kept on screen", got, before)
	}
	body := a.list.view(120, 20)
	if strings.Contains(body, "Loading") {
		t.Error("the list showed a loading placeholder instead of the rows it had")
	}
}

// A bulk action touches every queue, but no queue may lose its rows for it.
// Emptying the inactive caches moved the blanking rather than removing it: the
// tab looked fine until it was opened, and then showed a spinner.
func TestBulkActionKeepsRowsOnEveryTab(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	addTab(t, a, "second", rowsFor(7, 8))

	a.list.invalidateCachesAndRefresh()
	a.list.selectTab(1)

	if got := len(a.list.list.VisibleItems()); got != 2 {
		t.Errorf("second tab shows %d rows after a bulk action, want its 2", got)
	}
	if body := a.list.view(120, 20); strings.Contains(body, "Loading") {
		t.Error("second tab showed a loading placeholder instead of the rows it had")
	}
}

// It must still refetch, or the rows kept on screen would be the merged PR
// forever.
func TestBulkActionRefetchesTheTabOnItsNextVisit(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	addTab(t, a, "second", rowsFor(7, 8))

	a.list.invalidateCachesAndRefresh()
	if !a.list.dirty[1] {
		t.Fatal("the inactive tab was not flagged for refetch")
	}
	if cmd := a.list.selectTab(1); cmd == nil {
		t.Error("opening the flagged tab did not refetch it")
	}
	if a.list.dirty[1] {
		t.Error("the flag survived the refetch it triggered")
	}
	// A second visit with nothing changed in between must not refetch again.
	a.list.selectTab(0)
	if cmd := a.list.selectTab(1); cmd != nil {
		t.Error("a clean tab refetched on an ordinary visit")
	}
}

// Merging from the detail view leaves the queue one screen back showing a PR
// that is gone. Returning has to reconcile that — while keeping the rows up.
func TestDetailActionRefreshesTheListOnReturn(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 3, "acme/one")

	a.Update(bulkActionDoneMsg{label: "merged 1 PR", completed: []string{"acme/one#3"}})
	if !a.list.currentDirty() {
		t.Fatal("a merge in the detail view left the list believing it was current")
	}

	_, cmd := a.Update(listReturnMsg{})
	if cmd == nil {
		t.Error("returning to the list did not refresh the stale queue")
	}
	if got := len(a.list.list.VisibleItems()); got != 3 {
		t.Errorf("the list showed %d rows on return, want its 3 while refetching", got)
	}
}
