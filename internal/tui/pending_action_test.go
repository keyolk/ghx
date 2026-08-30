package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// Pressing M closed the confirmation and then nothing changed on screen for up
// to a minute while the merge ran. A queue that looks identical before and
// after a keypress reads as a key that did nothing.

func mergeTarget(n int) actionTarget {
	return actionTarget{number: n, repo: "acme/one", state: "OPEN"}
}

// confirmAndRun answers the prompt, discarding the command so no gh runs.
func confirmAndRun(t *testing.T, a *App, kind confirmKind, targets []actionTarget) {
	t.Helper()
	if cmd := a.askConfirmTargets(kind, targets); cmd != nil {
		t.Fatalf("askConfirmTargets refused the targets: %v", cmd())
	}
	a.handleConfirmKey(keyMsg("y"))
}

func TestFooterSaysWhatIsInFlight(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(2)})

	got := stripANSI(a.helpLine())
	if !strings.Contains(got, "merging") {
		t.Errorf("footer = %q, want it to say a merge is in flight", got)
	}
	// Past tense would claim the merge already happened.
	if strings.Contains(got, "merged") {
		t.Errorf("footer = %q, want the present continuous, not a result", got)
	}
}

// A bulk action says how many, because the count is what the rows cannot show
// at a glance.
func TestFooterCountsABulkAction(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	a.list.selected[selectionKey(pr.Summary{Repo: "acme/one", Number: 1})] = pr.Summary{}
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(1), mergeTarget(2)})

	if got := stripANSI(a.helpLine()); !strings.Contains(got, "2 pull requests") {
		t.Errorf("footer = %q, want the count of what is being merged", got)
	}
}

// The row is where "which one" can be said. The footer cannot name a dozen PRs.
func TestOnlyTheTargetedRowsAreMarked(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(2)})

	if !a.list.isPending(pr.Summary{Repo: "acme/one", Number: 2}) {
		t.Error("the PR being merged is not marked")
	}
	if a.list.isPending(pr.Summary{Repo: "acme/one", Number: 1}) {
		t.Error("a PR that is not being merged was marked")
	}

	body := stripANSI(a.View())
	if !strings.Contains(body, spinnerFrames[0]) {
		t.Errorf("no in-flight marker in the rendered list:\n%s", body)
	}
}

// The marker is animated, and the tick that animates it only runs while
// something reports being busy.
func TestPendingActionKeepsTheSpinnerRunning(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	if a.anyLoading() {
		t.Fatal("setup: nothing should be loading yet")
	}
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(2)})
	if !a.anyLoading() {
		t.Error("an action in flight did not keep the spinner ticking — the " +
			"marker would sit frozen for the whole merge")
	}
}

// A row left spinning after the action settles claims work that stopped.
func TestMarkerClearsWhenTheActionSettles(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(2)})

	a.Update(actionDoneMsg{label: "merged #2"})

	if a.pending != nil {
		t.Error("the in-flight state survived the result")
	}
	if a.list.isPending(pr.Summary{Repo: "acme/one", Number: 2}) {
		t.Error("the row is still marked after the merge finished")
	}
	if got := stripANSI(a.helpLine()); strings.Contains(got, "merging") {
		t.Errorf("footer = %q, still claiming a merge is running", got)
	}
}

// Failure clears it too: a row spinning forever after an error is worse than
// no marker at all.
func TestMarkerClearsOnFailure(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(2)})

	a.Update(actionDoneMsg{err: errors.New("merge conflict")})

	if a.pending != nil {
		t.Error("a failed action left the row marked as still running")
	}
}

// Approve reports through reviewPostedMsg rather than actionDoneMsg, so it is
// its own path to the same clearing.
func TestApproveClearsTheMarker(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	confirmAndRun(t, a, confirmApprove, []actionTarget{mergeTarget(2)})
	if got := stripANSI(a.helpLine()); !strings.Contains(got, "approving") {
		t.Errorf("footer = %q, want it to say an approval is in flight", got)
	}

	a.Update(reviewPostedMsg{action: "approve"})
	if a.pending != nil {
		t.Error("the approve path did not clear the in-flight marker")
	}
}

// A bulk result comes back as bulkActionDoneMsg — the third path in.
func TestBulkResultClearsTheMarker(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(1), mergeTarget(2)})

	a.Update(bulkActionDoneMsg{label: "merged 2 PRs", completed: []string{"acme/one#1"}})
	if a.pending != nil {
		t.Error("the bulk path did not clear the in-flight marker")
	}
}

// The in-flight marker and the selection tick share one cell, so while a row is
// being acted on the marker wins it — that it is being acted on is the more
// urgent of the two things to know.
func TestInFlightMarkerOutranksTheSelectionTick(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	row := pr.Summary{Repo: "acme/one", Number: 2}
	a.list.selected[selectionKey(row)] = row
	if !strings.Contains(stripANSI(a.View()), iconCheck) {
		t.Fatal("setup: a selected row should show the tick")
	}

	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(2)})

	body := stripANSI(a.View())
	if !strings.Contains(body, spinnerFrames[0]) {
		t.Error("the in-flight marker did not take the mark cell")
	}
	if strings.Contains(body, iconCheck) {
		t.Errorf("the selection tick is still drawn over the in-flight row:\n%s", body)
	}
}

// ...and the selection itself survives underneath, returning when it settles.
func TestSelectionSurvivesTheInFlightMarker(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	row := pr.Summary{Repo: "acme/one", Number: 2}
	a.list.selected[selectionKey(row)] = row

	confirmAndRun(t, a, confirmMerge, []actionTarget{mergeTarget(2)})
	if !a.list.isSelected(row) {
		t.Error("marking a row in flight dropped its selection")
	}

	a.Update(actionDoneMsg{label: "merged #2"})
	if !a.list.isSelected(row) {
		t.Error("the selection did not come back when the action settled")
	}
}
