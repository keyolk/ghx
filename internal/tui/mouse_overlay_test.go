package tui

import (
	"errors"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// While an overlay is open it owns the mouse: nothing a click or the wheel does
// reaches the view under it, and a click outside it is its Esc.

func TestHelpOwnsTheMouse(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg("?"))
	a.View()
	if !a.helpOpen {
		t.Fatal("? opens help")
	}
	before := cursorNumber(t, a)

	click(a, 5, 10)
	if !a.helpOpen || cursorNumber(t, a) != before {
		t.Fatal("a click inside help must not reach the list beneath")
	}
	wheel(a, 5, 10, tea.MouseButtonWheelDown)
	if a.helpOff != 3 {
		t.Fatalf("helpOff = %d, want 3: the wheel scrolls help by three lines", a.helpOff)
	}
	wheel(a, 5, 10, tea.MouseButtonWheelUp)
	if a.helpOff != 0 {
		t.Fatalf("helpOff = %d after wheeling back up", a.helpOff)
	}
	if cursorNumber(t, a) != before {
		t.Fatal("the wheel over help moved the list beneath it")
	}

	click(a, 5, 0)
	if a.helpOpen {
		t.Fatal("a click outside help closes it")
	}
	if cursorNumber(t, a) != before {
		t.Fatal("the closing click must not also select")
	}
}

func TestConfirmOwnsTheMouseAndAClickNeverConfirms(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg("a")) // approve: asks first
	if a.confirm == nil {
		t.Fatal("a opens the confirmation")
	}
	a.View()
	x, y := at(t, a, "Approve")
	if cmd := click(a, x, y); cmd != nil || a.confirm == nil || a.pending != nil {
		t.Fatal("a click inside the confirmation must do nothing")
	}
	// The row visible above the box is outside it: the click only cancels.
	rx, ry := at(t, a, "pull request 3")
	if cmd := click(a, rx, ry); cmd != nil {
		t.Fatal("cancelling must not run a command")
	}
	if a.confirm != nil {
		t.Fatal("a click outside the confirmation cancels it")
	}
	if a.pending != nil {
		t.Fatal("cancelling must not start the action")
	}
	if got := cursorNumber(t, a); got != 1 {
		t.Fatalf("the cancelling click also selected #%d", got)
	}
}

func TestConfirmOnANarrowTerminal(t *testing.T) {
	a := mouseApp(t)
	a.width = 40
	a.list.resize(40, 30)
	a.handleKey(keyMsg("a"))
	a.View()
	x, y := at(t, a, "Approve")
	click(a, x, y)
	if a.confirm == nil {
		t.Fatal("a click inside the box closed it")
	}
	click(a, 0, 0)
	if a.confirm != nil {
		t.Fatal("a click outside the box did not close it")
	}
}

func TestStatusFilterClickSelectsAndSecondClickToggles(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg("f"))
	a.View()
	x, y := at(t, a, "approved")
	click(a, x, y)
	if a.statusFilter == nil || a.statusFilter.cursor != 2 {
		t.Fatalf("cursor = %v, want 2 (approved)", a.statusFilter)
	}
	if a.statusFilter.pending[statusApproved] {
		t.Fatal("one click selects; it must not toggle")
	}
	a.View()
	click(a, x, y)
	if !a.statusFilter.pending[statusApproved] {
		t.Fatal("a second click toggles the row, as space does")
	}
	if a.list.statusFilters[statusApproved] {
		t.Fatal("toggling must not apply the filter; enter does")
	}
}

// Overlays share the list's detector, so a key between two clicks on an
// overlay row breaks the pair too: the second click only selects.
func TestStatusFilterKeyBetweenClicksBreaksTheDoubleClick(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg("f"))
	a.View()
	x, y := at(t, a, "approved")
	click(a, x, y)
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	a.Update(tea.KeyMsg{Type: tea.KeyUp})
	if a.statusFilter == nil || a.statusFilter.cursor != 2 {
		t.Fatalf("cursor = %v after down, up; want back on 2 (approved)", a.statusFilter)
	}
	a.View()
	click(a, x, y)
	if a.statusFilter.pending[statusApproved] {
		t.Fatal("a key between the clicks must break the double click; the row toggled")
	}
	if a.statusFilter.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (approved) still selected", a.statusFilter.cursor)
	}
}

func TestStatusFilterWheelMovesAndOutsideCancels(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg("f"))
	a.View()
	x, y := at(t, a, "draft")
	wheel(a, x, y, tea.MouseButtonWheelDown)
	if a.statusFilter.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", a.statusFilter.cursor)
	}
	// Right of the box, on one of its rows: still outside.
	click(a, a.width-1, y)
	if a.statusFilter != nil {
		t.Fatal("a click beside the box cancels the picker")
	}
}

func labelApp(t *testing.T) *App {
	a := mouseApp(t)
	a.labels = &labelPicker{
		targets: []actionTarget{mergeTarget(1)},
		all: []gh.RepoLabel{
			{Name: "bug"}, {Name: "docs"}, {Name: "infra"}, {Name: "perf"},
		},
		applied: map[string]bool{}, mixed: map[string]bool{}, pending: map[string]bool{},
	}
	a.View()
	return a
}

func TestLabelPickerClickSelectsAndSecondClickToggles(t *testing.T) {
	a := labelApp(t)
	x, y := at(t, a, "infra")
	click(a, x, y)
	if a.labels.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", a.labels.cursor)
	}
	if len(a.labels.pending) != 0 {
		t.Fatal("one click selects; it must not toggle")
	}
	a.View()
	click(a, x, y)
	if !a.labels.pending["infra"] {
		t.Fatal("a second click toggles the label, as space does")
	}
	if a.labels == nil {
		t.Fatal("toggling must not submit; enter does")
	}
}

// The label picker scrolls too; a click must land on the row drawn there.
func TestLabelPickerClickOnAScrolledListHitsTheRowDrawn(t *testing.T) {
	a := labelApp(t)
	a.labels.all = nil
	for i := range 80 {
		a.labels.all = append(a.labels.all, gh.RepoLabel{Name: fmt.Sprintf("lbl%02d", i)})
	}
	a.View()
	for range 70 {
		a.labels.cursor++
		a.View()
	}
	if a.labels.offset == 0 {
		t.Fatal("test setup: the picker did not scroll")
	}
	want := a.labels.visible()[a.labels.offset+1].Name
	x, y := at(t, a, want)
	click(a, x, y)
	if got := a.labels.visible()[a.labels.cursor].Name; got != want {
		t.Fatalf("clicked %s, selected %s", want, got)
	}
}

func TestLabelPickerOutsideCancelsAndInsideNeverTypes(t *testing.T) {
	a := labelApp(t)
	x, y := at(t, a, "filter:")
	click(a, x, y)
	if a.labels == nil || a.labels.query != "" {
		t.Fatal("a click on the filter line must neither close nor type")
	}
	click(a, 0, 0)
	if a.labels != nil {
		t.Fatal("a click outside cancels the picker")
	}
}

func TestRepoPickerClickSelectsAndSecondClickOpens(t *testing.T) {
	a := mouseApp(t)
	a.repos = &repoPicker{all: []repoCandidate{
		{Slug: "acme/alpha", Visits: 3}, {Slug: "acme/beta", Visits: 2}, {Slug: "acme/gamma", Visits: 1},
	}}
	a.View()
	x, y := at(t, a, "acme/beta")
	tabs := len(a.list.sources)
	click(a, x, y)
	if a.repos.cursor != 1 || a.repos == nil {
		t.Fatal("one click selects the row")
	}
	if len(a.list.sources) != tabs {
		t.Fatal("one click must not open the repository")
	}
	a.View()
	click(a, x, y)
	if a.repos != nil {
		t.Fatal("a second click opens the repository, as enter does")
	}
	if len(a.list.sources) != tabs+1 || a.list.sources[a.list.curTab].Repo != "acme/beta" {
		t.Fatalf("sources = %v, want a tab for acme/beta", a.list.sources)
	}
}

// A picker longer than its box scrolls; the row under the pointer is then
// offset+N, not N. Without the offset a click selects a row scrolled away.
func TestRepoPickerClickOnAScrolledListHitsTheRowDrawn(t *testing.T) {
	a := mouseApp(t)
	var all []repoCandidate
	for i := range 80 {
		all = append(all, repoCandidate{Slug: fmt.Sprintf("acme/r%02d", i), Visits: 80 - i})
	}
	a.repos = &repoPicker{all: all}
	a.View()
	for range 70 {
		a.repos.cursor++
		a.View() // the picker re-fits its offset while drawing
	}
	if a.repos.offset == 0 {
		t.Fatal("test setup: the picker did not scroll")
	}
	want := a.repos.visible()[a.repos.offset+1].Slug
	x, y := at(t, a, want)
	click(a, x, y)
	if got := a.repos.visible()[a.repos.cursor].Slug; got != want {
		t.Fatalf("clicked %s, selected %s", want, got)
	}
}

func TestRepoPickerOutsideCancels(t *testing.T) {
	a := mouseApp(t)
	a.repos = &repoPicker{all: []repoCandidate{{Slug: "acme/alpha"}}}
	a.View()
	click(a, 0, 0)
	if a.repos != nil {
		t.Fatal("a click outside cancels the picker")
	}
}

func TestMergePromptClickPicksAStrategyButNeverMerges(t *testing.T) {
	a := detailApp(t)
	a.mergePrompt = &mergePrompt{strategy: "squash"}
	a.View()
	x, y := at(t, a, "rebase")
	click(a, x, y)
	if a.mergePrompt == nil || a.mergePrompt.strategy != "rebase" {
		t.Fatalf("strategy = %v, want rebase", a.mergePrompt)
	}
	a.View()
	x, y = atNth(t, a, "merge", 1) // the first is the box title
	click(a, x, y)
	if a.mergePrompt.strategy != "merge" {
		t.Fatalf("strategy = %q, want merge", a.mergePrompt.strategy)
	}
	// Elsewhere inside the box nothing happens; outside it cancels.
	x, y = at(t, a, "This cannot be undone.")
	click(a, x, y)
	if a.mergePrompt == nil {
		t.Fatal("a click inside the box closed it")
	}
	click(a, 0, 0)
	if a.mergePrompt != nil {
		t.Fatal("a click outside cancels the prompt")
	}
}

func TestPaletteClickSelectsAndSecondClickRuns(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg(":"))
	a.View()
	if !a.palette.active {
		t.Fatal(": opens the palette")
	}
	_, y := at(t, a, "jump to top") // the third match of an empty line
	click(a, 5, y)
	if a.palette.cursor != 2 {
		t.Fatalf("cursor = %d, want 2: a click selects the row it is on", a.palette.cursor)
	}
	if !a.palette.active {
		t.Fatal("one click selects; it must not run")
	}

	// Enter runs the highlighted match of a typed line, and so does a second
	// click. (An empty line runs nothing under either.)
	later := a.now().Add(time.Minute)
	a.nowFunc = func() time.Time { return later }
	a.palette.close()
	a.handleKey(keyMsg(":"))
	for _, r := range "jump to top" {
		a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	a.View()
	_, y = atNth(t, a, "jump to top", 1) // the first is the typed line itself
	click(a, 5, y)
	if !a.palette.active {
		t.Fatal("one click selects; it must not run")
	}
	a.View()
	cmd := click(a, 5, y)
	if a.palette.active {
		t.Fatal("a second click runs the match, as enter does")
	}
	if _, ok := msgOf(t, cmd).(paletteRunMsg); !ok {
		t.Fatal("the run should be the palette's own message")
	}
}

func TestPaletteWheelMovesTheMatchAndOutsideCancels(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg(":"))
	a.View()
	wheel(a, 5, 20, tea.MouseButtonWheelDown)
	if a.palette.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", a.palette.cursor)
	}
	click(a, 5, 0)
	if a.palette.active {
		t.Fatal("a click outside cancels the palette")
	}
}

func TestSearchBarOwnsTheMouse(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg("/"))
	a.handleKey(keyMsg("x"))
	a.View()
	x, y := at(t, a, "Search:")
	click(a, x, y)
	if !a.search.active || a.search.query != "x" {
		t.Fatal("a click on the search line must neither close it nor type")
	}
	click(a, 0, 0)
	if a.search.active {
		t.Fatal("a click outside the search bar is its Esc")
	}
}

func TestMergeFailureBoxOwnsTheMouse(t *testing.T) {
	a := mouseApp(t)
	a.showMergeFailure(&mergeFailure{kind: confirmMerge, items: []mergeFailureItem{
		newMergeFailureItem(confirmMerge, mergeTarget(2), errors.New("Pull request is not mergeable: behind")),
	}})
	a.View()
	x, y := at(t, a, "merge failed")
	click(a, x, y+1)
	if a.mergeFailure == nil || a.pending != nil {
		t.Fatal("a click inside the box must do nothing, not even start the rebase")
	}
	click(a, 0, 0)
	if a.mergeFailure != nil {
		t.Fatal("a click outside dismisses the box")
	}
	if a.pending != nil {
		t.Fatal("dismissing must not start a rebase")
	}
}

func TestComposerClosesOnAnOutsideClickOnlyWhileEmpty(t *testing.T) {
	a := detailApp(t)
	a.composer.open(composerTarget{issue: true}, a.width, a.height)
	a.View()
	x, y := at(t, a, "PR comment")
	click(a, x, y+1)
	if !a.composer.active || a.composer.ta.Value() != "" {
		t.Fatal("a click inside the composer must neither close it nor type")
	}
	a.composer.ta.SetValue("half a review")
	click(a, 0, 0)
	if !a.composer.active || a.composer.ta.Value() != "half a review" {
		t.Fatal("a stray click must not discard a draft")
	}
	a.composer.ta.SetValue("")
	click(a, 0, 0)
	if a.composer.active {
		t.Fatal("an empty composer closes on a click outside, as esc does")
	}
}

func TestSuggestionPromptOwnsTheMouse(t *testing.T) {
	a := detailApp(t)
	a.suggestion = &suggestionPrompt{
		thread:     pr.ReviewThread{Path: "app.go", Line: 2},
		suggestion: pr.Suggestion{Replacement: "fixed"},
		original:   []string{"broken"},
	}
	a.View()
	x, y := at(t, a, "Apply this suggestion")
	click(a, x, y)
	if a.suggestion == nil || a.suggestion.busy {
		t.Fatal("a click inside the prompt must not commit")
	}
	click(a, 0, 0)
	if a.suggestion != nil {
		t.Fatal("a click outside cancels the prompt")
	}
}

func TestOverlayWheelDoesNotScrollTheViewBeneath(t *testing.T) {
	a := detailApp(t)
	a.mergePrompt = &mergePrompt{strategy: "squash"}
	a.View()
	wheel(a, 5, 4, tea.MouseButtonWheelDown)
	if a.detail.overviewOff != 0 {
		t.Fatal("the wheel under an open prompt scrolled the Overview")
	}
}
