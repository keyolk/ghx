package admin

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/mouse"

	"github.com/keyolk/ghx/internal/gh"
)

// at finds where View drew text, so a click lands on the cell the user sees.
func at(t *testing.T, a *App, text string) (x, y int) {
	t.Helper()
	for i, line := range strings.Split(a.View(), "\n") {
		s := xansi.Strip(line)
		if j := strings.Index(s, text); j >= 0 {
			return xansi.StringWidth(s[:j]), i
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, xansi.Strip(a.View()))
	return 0, 0
}

func press(a *App, x, y int, b tea.MouseButton) {
	a.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
}

func click(a *App, x, y int) { press(a, x, y, tea.MouseButtonLeft) }

func clockAt(a *App) {
	t0 := time.Unix(1000, 0)
	a.now = func() time.Time { return t0 }
}

func TestClickOnATabSwitchesCategory(t *testing.T) {
	a := collabApp(t, manyCollaborators(5))
	clockAt(a)
	x, y := at(t, a, "4 Releases")
	click(a, x, y)
	if a.active != catReleases {
		t.Fatalf("active = %v, want Releases", a.active)
	}
	if !a.loading {
		t.Fatal("switching to a category loads it, as the number key does")
	}
}

func TestClickOnANarrowTabStrip(t *testing.T) {
	a := collabApp(t, manyCollaborators(5))
	a.width = 40
	strip := xansi.Strip(strings.Split(a.View(), "\n")[headerRow])
	x := xansi.StringWidth(strip[:strings.Index(strip, "6")])
	click(a, x, headerRow)
	if a.active != catTags {
		t.Fatalf("active = %v, want Tags", a.active)
	}
}

func TestClickSelectsARow(t *testing.T) {
	a := collabApp(t, manyCollaborators(20))
	clockAt(a)
	x, y := at(t, a, "user007")
	click(a, x, y)
	if a.pane.Cursor != 7 {
		t.Fatalf("cursor = %d, want 7", a.pane.Cursor)
	}
}

func TestClickOnAScrolledListHitsTheRowThatIsDrawn(t *testing.T) {
	a := collabApp(t, manyCollaborators(60))
	clockAt(a)
	a.pane.Cursor = 50
	x, y := at(t, a, "user048")
	click(a, x, y)
	if a.pane.Cursor != 48 {
		t.Fatalf("cursor = %d, want 48", a.pane.Cursor)
	}
}

func TestSecondClickOnATeamDrillsIn(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "core", Permission: "push"}, {Slug: "infra", Permission: "admin"}})
	clockAt(a)
	x, y := at(t, a, "infra")
	click(a, x, y)
	if a.teamSlug != "" {
		t.Fatal("one click selects; it must not drill in")
	}
	if a.pane.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", a.pane.Cursor)
	}
	a.View()
	click(a, x, y)
	if a.teamSlug != "infra" {
		t.Fatalf("teamSlug = %q: a second click is Enter (members)", a.teamSlug)
	}
}

func TestSecondClickWhileLoadingDoesNotDrillIn(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "core"}, {Slug: "infra"}})
	clockAt(a)
	x, y := at(t, a, "infra")
	click(a, x, y)
	a.loading = true
	click(a, x, y)
	if a.teamSlug != "" {
		t.Fatal("members is disabled while loading, for the key and for the click")
	}
}

func TestClicksOnDifferentRowsAreNotADoubleClick(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "core"}, {Slug: "infra"}})
	clockAt(a)
	x, y := at(t, a, "core")
	click(a, x, y)
	x, y = at(t, a, "infra")
	click(a, x, y)
	if a.teamSlug != "" {
		t.Fatal("clicks on two rows are not a double click")
	}
}

func TestSlowSecondClickIsNotADoubleClick(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "core"}, {Slug: "infra"}})
	clockAt(a)
	x, y := at(t, a, "infra")
	click(a, x, y)
	later := a.now().Add(mouse.DoubleClickWindow + time.Second)
	a.now = func() time.Time { return later }
	click(a, x, y)
	if a.teamSlug != "" {
		t.Fatal("a click after the window is not a double click")
	}
}

func TestWheelMovesTheSelectionByThree(t *testing.T) {
	a := collabApp(t, manyCollaborators(20))
	press(a, 5, 6, tea.MouseButtonWheelDown)
	if a.pane.Cursor != 3 {
		t.Fatalf("cursor = %d, want 3", a.pane.Cursor)
	}
	press(a, 5, 6, tea.MouseButtonWheelUp)
	if a.pane.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", a.pane.Cursor)
	}
}

func TestReleaseMotionAndOtherButtonsAreIgnored(t *testing.T) {
	a := collabApp(t, manyCollaborators(20))
	clockAt(a)
	x, y := at(t, a, "user005")
	for _, act := range []tea.MouseAction{tea.MouseActionRelease, tea.MouseActionMotion} {
		a.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: act})
	}
	press(a, x, y, tea.MouseButtonRight)
	press(a, x, y, tea.MouseButtonMiddle)
	if a.pane.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", a.pane.Cursor)
	}
}

func TestClickOnEmptyAreasDoesNothing(t *testing.T) {
	a := collabApp(t, manyCollaborators(2))
	clockAt(a)
	for _, y := range []int{0, 15, a.height - 1} {
		click(a, 5, y)
	}
	if a.pane.Cursor != 0 {
		t.Fatalf("cursor = %d after clicks on blank space", a.pane.Cursor)
	}
}

func TestSearchBarOwnsTheMouse(t *testing.T) {
	a := collabApp(t, manyCollaborators(20))
	clockAt(a)
	a.handleKey(keyMsg("/"))
	typeKeys(a, "user01")
	x, y := at(t, a, "Search:")
	click(a, x, y)
	if !a.searching || a.query != "user01" {
		t.Fatal("a click on the bar must neither close it nor type")
	}
	x, y = at(t, a, "user010")
	click(a, x, y)
	if a.searching || a.query != "" {
		t.Fatal("a click outside the bar is its Esc")
	}
	if a.pane.Cursor != 0 {
		t.Fatal("the closing click also selected a row")
	}
}

func TestConfirmOwnsTheMouseAndAClickNeverConfirms(t *testing.T) {
	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}})
	clockAt(a)
	a.handleKey(keyMsg("d"))
	if a.confirm == nil {
		t.Fatal("d opens the removal confirmation")
	}
	_, last := a.height-1, a.height-1
	click(a, 5, last) // the question itself
	if a.confirm == nil || a.busy {
		t.Fatal("a click on the question must do nothing")
	}
	x, y := at(t, a, "alice")
	click(a, x, y)
	if a.confirm != nil || a.busy {
		t.Fatal("a click elsewhere declines, as n does, and never confirms")
	}
}

func TestLoginPromptClosesOnAnOutsideClickOnlyWhileEmpty(t *testing.T) {
	a := collabApp(t, manyCollaborators(5))
	a.handleKey(keyMsg("a"))
	click(a, 5, headerRow)
	if a.prompt.kind != promptLogin {
		t.Fatal("a click on the prompt bar closed it")
	}
	typeKeys(a, "bob")
	click(a, 5, 10)
	if a.prompt.kind != promptLogin || a.prompt.value != "bob" {
		t.Fatal("a stray click must not discard a typed login")
	}
	a.prompt.value = ""
	click(a, 5, 10)
	if a.prompt.open() {
		t.Fatal("an empty prompt closes on a click outside, as esc does")
	}
}

func TestPermissionPromptClickPicksAChoice(t *testing.T) {
	a := collabApp(t, manyCollaborators(5))
	a.handleKey(keyMsg("a"))
	typeKeys(a, "bob")
	a.handleKey(keyMsg("enter"))
	if a.prompt.kind != promptPermission {
		t.Fatal("setup: expected the permission prompt")
	}
	// Click the last choice: tab would cycle there.
	last := a.prompt.choices[len(a.prompt.choices)-1]
	// (The title line says "admin" too, so look on the prompt's own row.)
	bar := xansi.Strip(strings.Split(a.View(), "\n")[headerRow])
	x := xansi.StringWidth(bar[:strings.Index(bar, last)])
	click(a, x, headerRow)
	if a.prompt.choice != len(a.prompt.choices)-1 {
		t.Fatalf("choice = %d, want %d", a.prompt.choice, len(a.prompt.choices)-1)
	}
	if a.confirm != nil {
		t.Fatal("picking a level must not confirm the grant")
	}
	click(a, 5, 10)
	if a.prompt.open() {
		t.Fatal("a click outside cancels the permission prompt")
	}
}

func TestListShownNeverClicksTheProtectionSummary(t *testing.T) {
	a := testApp(t, 120, 24)
	a.active = catBranchProtection
	deliver(a, dataMsg{cat: catBranchProtection, protection: &gh.BranchProtection{}})
	click(a, 5, 3)
	if a.pane.Cursor != 0 {
		t.Fatal("the protection summary is text, not a list")
	}
}
