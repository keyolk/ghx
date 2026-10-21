package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/selection"
)

// Every coordinate below comes from where View drew some text (at, atNth),
// never from the layout arithmetic under test.

func press(a *App, x, y int) tea.Cmd {
	return send(a, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func motion(a *App, x, y int) tea.Cmd {
	return send(a, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
}

func release(a *App, x, y int) tea.Cmd {
	return send(a, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
}

// drag presses at (x0, y0), moves to (x1, y1) and releases there, returning
// the release's command (the copy).
func drag(a *App, x0, y0, x1, y1 int) tea.Cmd {
	press(a, x0, y0)
	a.View()
	motion(a, x1, y1)
	a.View()
	return release(a, x1, y1)
}

// markSelection draws the highlight as upper case, so a test can see which
// cells it covers without depending on the terminal's color profile.
func markSelection(t *testing.T) {
	t.Helper()
	old := SelectionStyle
	SelectionStyle = lipgloss.NewStyle().Transform(strings.ToUpper)
	t.Cleanup(func() { SelectionStyle = old })
}

func screenLine(a *App, y int) string {
	return ansi.Strip(strings.Split(a.View(), "\n")[y])
}

func TestDragFromTheListTowardThePreviewStaysInTheList(t *testing.T) {
	c := captureCopies(t)
	markSelection(t)
	a := mouseApp(t)
	a.list.pane.Show = true
	a.View()
	x0, y0 := at(t, a, "#2")
	_, yPrev := at(t, a, "Repo:")
	px, _ := at(t, a, "Repo:")
	cmd := drag(a, x0, y0, px+8, yPrev)
	if cmd == nil {
		t.Fatal("the release that ends a drag copies")
	}
	got := lastCopy(c)
	if strings.Contains(got, "acme") || strings.Contains(got, "Repo") || strings.Contains(got, "│") {
		t.Fatalf("the copy reached past the list into the preview or its border:\n%q", got)
	}
	// Lower case: the copy is read from the frame before the (upper-casing)
	// highlight is drawn, so a style never leaks into the clipboard.
	if !strings.Contains(got, "#3") || !strings.Contains(got, "pull requ") {
		t.Fatalf("the copy should hold the list rows dragged over, unstyled:\n%q", got)
	}
	// The highlight stops at the list's edge: the preview on that row is
	// drawn as it was.
	line := screenLine(a, yPrev)
	if !strings.Contains(line, "Repo:      acme/one") {
		t.Fatalf("the highlight reached into the preview: %q", line)
	}
	if !strings.Contains(line, "PULL REQU") {
		t.Fatalf("the list cells on the last row should be highlighted: %q", line)
	}
}

func TestDragInTheSideBySideDiffStaysInItsColumn(t *testing.T) {
	c := captureCopies(t)
	a := detailApp(t)
	a.detail.setTab(tabDiff)
	a.detail.diff.sideBySide = true
	a.View()
	x0, y0 := at(t, a, "old line here")
	_, y1 := at(t, a, "last context line")
	// The right column's copy of the line: the last occurrence on that row.
	row := screenLine(a, y1)
	x1 := ansi.StringWidth(row[:strings.LastIndex(row, "last context line")])
	drag(a, x0, y0, x1+10, y1)
	got := lastCopy(c)
	if strings.Contains(got, "new line here") || strings.Contains(got, "│") || strings.Contains(got, "claude") {
		t.Fatalf("a drag in the old column copied the new column or the divider:\n%q", got)
	}
	if !strings.Contains(got, "old line here") || !strings.Contains(got, "last context line") {
		t.Fatalf("copy = %q", got)
	}
}

// overviewApp is the detail view on an Overview whose description lines are
// numbered, so a copy shows which lines it holds.
func overviewApp(t *testing.T) *App {
	t.Helper()
	a := detailApp(t)
	var b strings.Builder
	for i := range 80 {
		fmt.Fprintf(&b, "description line %02d\n\n", i)
	}
	a.detail.detail.Body = b.String()
	a.View()
	return a
}

func TestDragPastTheBottomScrollsAndKeepsTheLinesThatLeft(t *testing.T) {
	c := captureCopies(t)
	a := overviewApp(t)
	x0, y0 := at(t, a, "Author:")
	_, footer := at(t, a, "q quit")
	press(a, x0, y0)
	a.View()
	right := a.width - 1
	for range 10 {
		motion(a, right, footer)
		a.View()
	}
	if a.detail.overviewOff == 0 {
		t.Fatal("dragging past the bottom of the overview should scroll it")
	}
	if onScreen(a, "Author:") {
		t.Fatal("setup: the first line should have scrolled out")
	}
	if cmd := release(a, right, footer); cmd == nil {
		t.Fatal("the release copies")
	}
	got := lastCopy(c)
	if !strings.HasPrefix(got, "Author:") {
		t.Fatalf("the copy lost the lines that scrolled out: %q", got)
	}
	lastVisible := ""
	for _, l := range strings.Split(ansi.Strip(a.View()), "\n") {
		if strings.Contains(l, "description line") {
			lastVisible = strings.TrimSpace(l)
		}
	}
	if !strings.Contains(got, lastVisible) {
		t.Fatalf("the copy should run to the bottom of the pane (%q):\n%q", lastVisible, got)
	}
}

func TestPressWithoutMotionIsStillAClick(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "pull request 3")
	press(a, x, y)
	if cmd := release(a, x, y); cmd != nil {
		t.Fatal("a release without a drag copies nothing")
	}
	if a.sel.Active() {
		t.Fatal("a click draws no selection")
	}
	if got := cursorNumber(t, a); got != 3 {
		t.Fatalf("selected #%d, want #3: a press that does not move is the app's click", got)
	}
}

func TestSecondClickOnAListRowIsStillEnter(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "pull request 4")
	press(a, x, y)
	release(a, x, y)
	a.View()
	cmd := press(a, x, y)
	if _, ok := msgOf(t, cmd).(openDetailMsg); !ok {
		t.Fatal("the second click on the selected list row opens it, selection or not")
	}
}

func TestDoubleClickInTheDiffSelectsAWordNotTheRowAction(t *testing.T) {
	c := captureCopies(t)
	a := detailApp(t)
	a.detail.setTab(tabDiff)
	a.View()
	x, y := at(t, a, "this finding")
	press(a, x, y)
	release(a, x, y)
	a.View()
	// The thread has replies, so its row action (Enter) is to expand it.
	if !a.detail.diff.threadHasReplies() {
		t.Fatal("setup: the first click should put the cursor on a thread with replies")
	}
	id, _ := a.detail.diff.threadUnderCursor()
	cmd := press(a, x, y)
	if cmd == nil {
		t.Fatal("a double click in a text pane copies the word")
	}
	if got := lastCopy(c); got != "this" {
		t.Fatalf("word = %q, want %q", got, "this")
	}
	if a.detail.diff.expanded[id] {
		t.Fatal("the double click also ran the row's Enter (expand the thread)")
	}
	release(a, x, y)
	cmd = press(a, x, y)
	if cmd == nil || !strings.Contains(lastCopy(c), "this finding is long") {
		t.Fatalf("a triple click selects the line, got %q", lastCopy(c))
	}
}

func TestAKeyClearsTheSelection(t *testing.T) {
	a := overviewApp(t)
	x0, y0 := at(t, a, "Author:")
	_, y1 := at(t, a, "State:")
	drag(a, x0, y0, x0+5, y1)
	if !a.sel.Active() {
		t.Fatal("setup: the drag should leave a selection")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if a.sel.Active() {
		t.Fatal("a key ends the selection")
	}
}

func TestOpenOverlayLeavesTheBaseUnselectable(t *testing.T) {
	c := captureCopies(t)
	a := mouseApp(t)
	a.handleKey(keyMsg("?"))
	a.View()
	// The title line is base, outside the help box.
	x0, y0 := at(t, a, "ghx")
	cmd := drag(a, x0, y0, x0+20, y0)
	if cmd != nil || a.sel.Active() {
		t.Fatalf("a drag on the base under an open overlay selected %q", lastCopy(c))
	}
	if a.helpOpen {
		// The press outside is help's Esc, as before.
		t.Fatal("a click outside help still closes it")
	}

	a.handleKey(keyMsg("?"))
	a.View()
	x, y := at(t, a, "ghx help")
	drag(a, x, y+1, x+30, y+2)
	got := lastCopy(c)
	if got == "" || strings.Contains(got, "│") || strings.Contains(got, "pull request") {
		t.Fatalf("a drag in help should select help text only, got %q", got)
	}
	if !a.helpOpen {
		t.Fatal("a drag inside help must not close it")
	}
}

func TestDragInTheComposerNeverEdits(t *testing.T) {
	c := captureCopies(t)
	a := detailApp(t)
	a.composer.open(composerTarget{path: "app.go", line: 2, side: "RIGHT"}, a.width, a.height)
	a.composer.ta.SetValue("draft text here")
	a.View()
	x, y := at(t, a, "draft text here")
	drag(a, x, y, x+5, y)
	if a.composer.ta.Value() != "draft text here" || !a.composer.active {
		t.Fatalf("a drag edited or closed the composer: %q", a.composer.ta.Value())
	}
	if got := lastCopy(c); got != "draft" {
		t.Fatalf("copy = %q", got)
	}
}

func TestCopiedMsgShowsAToast(t *testing.T) {
	a := mouseApp(t)
	a.Update(selection.CopiedMsg{Text: "héllo"})
	if !onScreen(a, "copied 5 chars") {
		t.Fatal("a finished copy reports its length")
	}
	a.Update(selection.CopiedMsg{Text: "x", Err: errors.New("no clipboard")})
	if !onScreen(a, "copy failed: no clipboard") {
		t.Fatal("a failed copy says so")
	}
}

func TestDragPastTheBottomOfTheDiffScrollsAndKeepsTheLinesThatLeft(t *testing.T) {
	c := captureCopies(t)
	a := detailApp(t)
	var b strings.Builder
	b.WriteString("diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@ -1,80 +1,80 @@\n")
	for i := range 80 {
		fmt.Fprintf(&b, " context number %02d\n", i)
	}
	if err := a.detail.diff.setContent(b.String(), nil); err != nil {
		t.Fatal(err)
	}
	a.detail.setTab(tabDiff)
	a.View()
	x0, y0 := at(t, a, "context number 01")
	_, footer := at(t, a, "q quit")
	press(a, x0, y0)
	for range 8 {
		a.View()
		motion(a, a.width-1, footer)
	}
	a.View()
	if onScreen(a, "context number 01") {
		t.Fatal("dragging past the bottom of the diff should scroll it")
	}
	if cmd := release(a, a.width-1, footer); cmd == nil {
		t.Fatal("the release copies")
	}
	got := lastCopy(c)
	if !strings.Contains(got, "context number 01") {
		t.Fatalf("the copy lost the lines that scrolled out: %q", got)
	}
	lastVisible := ""
	for _, l := range strings.Split(ansi.Strip(a.View()), "\n") {
		if strings.Contains(l, "context number") {
			lastVisible = strings.TrimSpace(l)
		}
	}
	if !strings.HasSuffix(got, lastVisible) {
		t.Fatalf("the copy should end at the pane's bottom row %q:\n%q", lastVisible, got)
	}
}

// captureCopies swaps the clipboard command for one that records the text, so
// a test asserts what a release would copy and never touches the clipboard.
func captureCopies(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := copySelection
	copySelection = func(s string) tea.Cmd {
		got = append(got, s)
		return func() tea.Msg { return nil }
	}
	t.Cleanup(func() { copySelection = old })
	return &got
}

// lastCopy is the text of the most recent copy, or "".
func lastCopy(c *[]string) string {
	if len(*c) == 0 {
		return ""
	}
	return (*c)[len(*c)-1]
}
