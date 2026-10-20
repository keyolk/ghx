package actions

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

func mouseApp(t *testing.T) *App {
	t.Helper()
	a := testApp(t, 120, 24)
	a.runs = manyRuns(40)
	a.workflows = []gh.Workflow{{ID: 1, Name: "ci", State: "active"}, {ID: 2, Name: "release", State: "active"}}
	t0 := time.Unix(1000, 0)
	a.now = func() time.Time { return t0 }
	a.View()
	return a
}

func TestClickOnATabSwitchesIt(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "2 Workflows")
	click(a, x, y)
	if a.active != tabWorkflows {
		t.Fatalf("active = %v, want Workflows", a.active)
	}
	x, y = at(t, a, "1 Runs")
	click(a, x, y)
	if a.active != tabRuns {
		t.Fatalf("active = %v, want Runs", a.active)
	}
}

func TestClickOnANarrowTabStrip(t *testing.T) {
	a := mouseApp(t)
	a.width = 40
	a.View()
	strip := xansi.Strip(strings.Split(a.View(), "\n")[headerRow])
	x := xansi.StringWidth(strip[:strings.Index(strip, "2")])
	click(a, x, headerRow)
	if a.active != tabWorkflows {
		t.Fatalf("active = %v, want Workflows", a.active)
	}
}

func TestClickSelectsARowAndASecondClickOpensItsLogs(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "build-004")
	click(a, x, y)
	if a.pane.Cursor != 4 {
		t.Fatalf("cursor = %d, want 4", a.pane.Cursor)
	}
	if a.logBusy {
		t.Fatal("one click selects; it must not fetch logs")
	}
	a.View()
	click(a, x, y)
	if !a.logBusy {
		t.Fatal("a second click is Enter: the run's logs")
	}
}

// A key between two clicks breaks the pair. Click a run, j, k back to it,
// click again inside the double-click window: that is a fresh first click,
// so it selects and must not fetch the run's logs.
func TestKeyBetweenClicksBreaksTheDoubleClick(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "build-004")
	click(a, x, y)
	a.Update(keyMsg("j"))
	a.Update(keyMsg("k"))
	if a.pane.Cursor != 4 {
		t.Fatalf("cursor = %d after j, k; want back on 4", a.pane.Cursor)
	}
	a.View()
	click(a, x, y)
	if a.logBusy {
		t.Fatal("a key between the clicks must break the double click; logs were fetched")
	}
	if a.pane.Cursor != 4 {
		t.Fatalf("cursor = %d, want 4 still selected", a.pane.Cursor)
	}
}

func TestSecondClickOnAWorkflowDoesNothing(t *testing.T) {
	a := mouseApp(t)
	a.run("tab", 1)
	a.View()
	x, y := at(t, a, "release")
	click(a, x, y)
	click(a, x, y)
	if a.pane.Cursor != 1 || a.logBusy {
		t.Fatalf("cursor = %d busy = %v: workflows have no Enter action", a.pane.Cursor, a.logBusy)
	}
}

func TestClicksOnTwoRowsAreNotADoubleClick(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "build-002")
	click(a, x, y)
	x, y = at(t, a, "build-005")
	click(a, x, y)
	if a.logBusy || a.pane.Cursor != 5 {
		t.Fatalf("cursor = %d busy = %v", a.pane.Cursor, a.logBusy)
	}
}

func TestClickOnAScrolledListHitsTheRowThatIsDrawn(t *testing.T) {
	a := mouseApp(t)
	a.pane.Cursor = 30
	a.View()
	x, y := at(t, a, "build-028")
	click(a, x, y)
	if a.pane.Cursor != 28 {
		t.Fatalf("cursor = %d, want 28", a.pane.Cursor)
	}
}

func TestWheelMovesTheSelectionByThree(t *testing.T) {
	a := mouseApp(t)
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
	a := mouseApp(t)
	x, y := at(t, a, "build-003")
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
	a := mouseApp(t)
	a.runs = manyRuns(2)
	a.View()
	for _, y := range []int{0, 15, a.height - 1} {
		click(a, 5, y)
	}
	if a.pane.Cursor != 0 || a.logBusy {
		t.Fatal("a click on blank space acted")
	}
}

func TestLogViewerOwnsTheMouse(t *testing.T) {
	a := mouseApp(t)
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "log line"
	}
	a.logView = strings.Join(lines, "\n")
	a.View()
	press(a, 5, 6, tea.MouseButtonWheelDown)
	if a.logOff != 3 {
		t.Fatalf("logOff = %d, want 3", a.logOff)
	}
	click(a, 5, 6)
	if a.logView == "" || a.pane.Cursor != 0 {
		t.Fatal("a click in the log must not leave it or move the list behind it")
	}
}

func TestSearchBarOwnsTheMouse(t *testing.T) {
	a := mouseApp(t)
	a.handleKey(keyMsg("/"))
	typeKeys(a, "build-01")
	a.View()
	x, y := at(t, a, "Search:")
	click(a, x, y)
	if !a.searching || a.query != "build-01" {
		t.Fatal("a click on the bar must neither close it nor type")
	}
	// A click on a row is outside the bar: Esc, and nothing is selected.
	x, y = at(t, a, "build-010")
	click(a, x, y)
	if a.searching || a.query != "" {
		t.Fatal("a click outside the bar is its Esc")
	}
	if a.pane.Cursor != 0 {
		t.Fatal("the closing click also selected a row")
	}
}

func TestDoubleClickNeedsTheSameRowWithinTheWindow(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "build-004")
	click(a, x, y)
	later := a.now().Add(mouse.DoubleClickWindow + time.Second)
	a.now = func() time.Time { return later }
	a.View()
	click(a, x, y)
	if a.logBusy {
		t.Fatal("a slow second click is not a double click")
	}
}
