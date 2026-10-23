package admin

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/selection"
	"github.com/keyolk/tuikit/tuitest"

	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// Coordinates come from where View drew text (at). The Cmd a release returns
// runs against a clipboard stub (captureCopies), never the developer's
// clipboard.

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

func TestDragSelectsListRowsAndAKeyClearsIt(t *testing.T) {
	c := captureCopies(t)
	// The highlight drawn as upper case, so the test sees which cells it
	// covers without depending on the color profile.
	old := tui.SelectionStyle
	tui.SelectionStyle = lipgloss.NewStyle().Transform(strings.ToUpper)
	t.Cleanup(func() { tui.SelectionStyle = old })
	a := collabApp(t, manyCollaborators(20))
	clockAt(a)
	x0, y0 := at(t, a, "user002")
	_, y1 := at(t, a, "user004")
	if drag(a, x0, y0, x0+6, y1) == nil {
		t.Fatal("a drag copies on release")
	}
	if got := lastCopy(c); !strings.HasPrefix(got, "user002") || !strings.Contains(got, "user003") {
		t.Fatalf("copy = %q", got)
	}
	if !strings.Contains(xansi.Strip(a.View()), "USER003") {
		t.Fatal("the selection is drawn over the rows it covers")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if a.sel.Active() {
		t.Fatal("a key ends the selection")
	}
	if strings.Contains(xansi.Strip(a.View()), "USER003") {
		t.Fatal("the highlight stays after the key cleared the selection")
	}
}

func TestPressAndReleaseIsStillAClickAndTheSecondDrillsIn(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "core", Permission: "push"}, {Slug: "infra", Permission: "admin"}})
	clockAt(a)
	x, y := at(t, a, "infra")
	mouseAt(a, x, y, tea.MouseActionPress)
	if cmd := mouseAt(a, x, y, tea.MouseActionRelease); cmd != nil || a.sel.Active() {
		t.Fatal("a release without a drag selects and copies nothing")
	}
	if a.pane.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", a.pane.Cursor)
	}
	a.View()
	mouseAt(a, x, y, tea.MouseActionPress)
	if a.teamSlug != "infra" {
		t.Fatal("the second click on a selected team is still Enter (members)")
	}
}

func TestConfirmOpenLeavesTheListUnselectable(t *testing.T) {
	c := captureCopies(t)
	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}, {Login: "bobby", RoleName: "read", Direct: true}})
	clockAt(a)
	a.handleKey(keyMsg("d"))
	if a.confirm == nil {
		t.Fatal("setup: d opens the removal confirmation")
	}
	// Drag over the question, the footer line: it selects, and confirms nothing.
	x, y := at(t, a, "alice")
	if y != a.height-1 {
		// The first "alice" on screen is the list row; the question names her
		// too, on the last row.
		row := xansi.Strip(strings.Split(a.View(), "\n")[a.height-1])
		x, y = xansi.StringWidth(row[:strings.Index(row, "alice")]), a.height-1
	}
	drag(a, x, y, x+4, y)
	if got := lastCopy(c); got != "alice" {
		t.Fatalf("a drag on the question selects it, got %q", got)
	}
	if a.confirm == nil || a.busy {
		t.Fatal("a drag on the question must neither decline nor confirm")
	}
	// The list beneath is not selectable while the question is open.
	x, y = at(t, a, "bobby")
	mouseAt(a, x, y, tea.MouseActionPress)
	mouseAt(a, x+3, y, tea.MouseActionMotion)
	if a.sel.Active() {
		t.Fatalf("the list under the confirmation was selectable: %q", lastCopy(c))
	}
}

func TestLoginPromptDragNeverEdits(t *testing.T) {
	c := captureCopies(t)
	a := collabApp(t, manyCollaborators(5))
	a.handleKey(keyMsg("a"))
	typeKeys(a, "bobcat")
	x, y := at(t, a, "bobcat")
	drag(a, x, y, x+2, y)
	if got := lastCopy(c); got != "bob" {
		t.Fatalf("copy = %q, want bob", got)
	}
	if a.prompt.kind != promptLogin || a.prompt.value != "bobcat" {
		t.Fatalf("a drag edited or closed the prompt: %q", a.prompt.value)
	}
}

func TestCopiedMsgShowsAToast(t *testing.T) {
	a := collabApp(t, manyCollaborators(3))
	a.Update(selection.CopiedMsg{Text: "abcd"})
	if !strings.Contains(xansi.Strip(a.View()), "copied 4 chars") {
		t.Fatal("a finished copy reports its length")
	}
	a.Update(selection.CopiedMsg{Err: errors.New("no tty"), Text: "x"})
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
