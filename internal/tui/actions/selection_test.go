package actions

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/selection"
	"github.com/keyolk/tuikit/tuitest"

	"github.com/keyolk/ghx/internal/tui"
)

// Coordinates come from where View drew text (at), never from the pane
// arithmetic under test. The Cmd a release returns runs against a clipboard
// stub (captureCopies), never the developer's clipboard.

func mouseAt(a *App, x, y int, act tea.MouseAction) tea.Cmd {
	b := tea.MouseButtonLeft
	if act == tea.MouseActionRelease {
		b = tea.MouseButtonNone
	}
	_, cmd := a.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: act})
	if act == tea.MouseActionRelease {
		// Run the copy the way Bubble Tea would, into the clipboard stub.
		tuitest.Drain(cmd, 0)
	}
	return cmd
}

func drag(a *App, x0, y0, x1, y1 int) tea.Cmd {
	mouseAt(a, x0, y0, tea.MouseActionPress)
	mouseAt(a, x1, y1, tea.MouseActionMotion)
	return mouseAt(a, x1, y1, tea.MouseActionRelease)
}

func numberedLog(a *App, n int) {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("log line %03d", i)
	}
	a.logView = strings.Join(lines, "\n")
	a.View()
}

func TestLogDragPastTheBottomScrollsAndKeepsTheLinesThatLeft(t *testing.T) {
	c := captureCopies(t)
	a := mouseApp(t)
	numberedLog(a, 200)
	x, y := at(t, a, "log line 002")
	mouseAt(a, x, y, tea.MouseActionPress)
	for range 5 {
		mouseAt(a, a.width-1, a.height+3, tea.MouseActionMotion)
	}
	if a.logOff != 5 {
		t.Fatalf("logOff = %d, want 5: each drag step past the bottom scrolls one line", a.logOff)
	}
	if cmd := mouseAt(a, a.width-1, a.height+3, tea.MouseActionRelease); cmd == nil {
		t.Fatal("the release copies")
	}
	got := lastCopy(c)
	if !strings.HasPrefix(got, "log line 002") {
		t.Fatalf("the copy lost the lines that scrolled out: %q", got[:min(len(got), 40)])
	}
	lastRow := strings.TrimSpace(xansi.Strip(strings.Split(a.View(), "\n")[a.height-1]))
	if !strings.HasSuffix(got, lastRow) {
		t.Fatalf("the copy should end at the bottom row %q, got ...%q", lastRow, got[max(len(got)-30, 0):])
	}
}

func TestPressAndReleaseOnARowIsStillAClickAndTheSecondIsEnter(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "build-004")
	mouseAt(a, x, y, tea.MouseActionPress)
	if cmd := mouseAt(a, x, y, tea.MouseActionRelease); cmd != nil {
		t.Fatal("a release without a drag copies nothing")
	}
	if a.pane.Cursor != 4 || a.sel.Active() {
		t.Fatalf("cursor = %d, selection %v: a press that does not move is a click", a.pane.Cursor, a.sel.Active())
	}
	mouseAt(a, x, y, tea.MouseActionPress)
	if !a.logBusy {
		t.Fatal("the second click on the selected run is still Enter (logs)")
	}
}

func TestDragInTheListThenAKeyClearsIt(t *testing.T) {
	// The highlight drawn as upper case: the copy must still be lower case,
	// read from the frame before the highlight.
	old := tui.SelectionStyle
	tui.SelectionStyle = lipgloss.NewStyle().Transform(strings.ToUpper)
	t.Cleanup(func() { tui.SelectionStyle = old })
	c := captureCopies(t)
	a := mouseApp(t)
	x0, y0 := at(t, a, "build-002")
	x1, y1 := at(t, a, "build-005")
	if drag(a, x0, y0, x1+4, y1) == nil {
		t.Fatal("a drag copies on release")
	}
	if got := lastCopy(c); !strings.Contains(got, "build-003") || !strings.HasPrefix(got, "build-002") {
		t.Fatalf("copy = %q", got)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if a.sel.Active() {
		t.Fatal("a key ends the selection")
	}
}

func TestSearchBarOpenLeavesTheListUnselectable(t *testing.T) {
	c := captureCopies(t)
	a := mouseApp(t)
	a.handleKey(keyMsg("/"))
	typeKeys(a, "build")
	a.View()
	x, y := at(t, a, "build-003")
	// The rows sit outside the bar; the press is the bar's Esc, as before,
	// and the drag that follows must not select the list.
	if cmd := drag(a, x, y, x+5, y+2); cmd != nil || a.sel.Active() {
		t.Fatalf("a drag over the list under the search bar selected %q", lastCopy(c))
	}

	a.handleKey(keyMsg("/"))
	typeKeys(a, "build-01")
	a.View()
	x, y = at(t, a, "Search:")
	drag(a, x, y, x+20, y)
	if got := lastCopy(c); !strings.Contains(got, "build-01") {
		t.Fatalf("a drag in the bar selects its text, got %q", got)
	}
	if !a.searching || a.query != "build-01" {
		t.Fatalf("a drag in the bar must not edit or close it: %q", a.query)
	}
}

func TestCopiedMsgShowsAToast(t *testing.T) {
	a := mouseApp(t)
	if _, cmd := a.Update(selection.CopiedMsg{Text: "abc"}); cmd == nil {
		t.Fatal("a success toast schedules its expiry")
	}
	if !strings.Contains(xansi.Strip(a.View()), "copied 3 chars") {
		t.Fatal("a finished copy reports its length")
	}
	a.Update(selection.CopiedMsg{Text: "abc", Err: errors.New("no tty")})
	if !strings.Contains(xansi.Strip(a.View()), "copy failed: no tty") {
		t.Fatal("a failed copy says so")
	}
}

// captureCopies replaces the clipboard with a recorder, so a test asserts
// what a release copied, through the same command the app returns, and never
// touches the real clipboard.
func captureCopies(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := selection.Clipboard
	selection.Clipboard = func(s string) error {
		got = append(got, s)
		return nil
	}
	t.Cleanup(func() { selection.Clipboard = old })
	return &got
}

// lastCopy is the text of the most recent copy, or "".
func lastCopy(c *[]string) string {
	if len(*c) == 0 {
		return ""
	}
	return (*c)[len(*c)-1]
}
