package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/mouse"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// at finds where View drew text, so a click lands on the cell the user sees
// rather than on a coordinate the test computed with the same arithmetic as
// the code under test. It looks for the nth (0-based) occurrence from the top.
func atNth(t *testing.T, a *App, text string, n int) (x, y int) {
	t.Helper()
	for i, line := range strings.Split(a.View(), "\n") {
		plain := ansi.Strip(line)
		if j := strings.Index(plain, text); j >= 0 {
			if n == 0 {
				return ansi.StringWidth(plain[:j]), i
			}
			n--
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, ansi.Strip(a.View()))
	return 0, 0
}

func at(t *testing.T, a *App, text string) (x, y int) {
	t.Helper()
	return atNth(t, a, text, 0)
}

func onScreen(a *App, text string) bool {
	return strings.Contains(ansi.Strip(a.View()), text)
}

func send(a *App, msg tea.MouseMsg) tea.Cmd {
	_, cmd := a.Update(msg)
	return cmd
}

func click(a *App, x, y int) tea.Cmd {
	return send(a, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func rightClick(a *App, x, y int) tea.Cmd {
	return send(a, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonRight, Action: tea.MouseActionPress})
}

func wheel(a *App, x, y int, b tea.MouseButton) tea.Cmd {
	return send(a, tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
}

// clickText clicks the cell View drew text at.
func clickText(t *testing.T, a *App, text string) tea.Cmd {
	t.Helper()
	x, y := at(t, a, text)
	return click(a, x, y)
}

// msgOf runs a command that only wraps a message (no I/O) and returns it.
func msgOf(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command was returned")
	}
	return cmd()
}

// mouseApp is a wide list app with a clock the test owns and an isolated HOME,
// so nothing reads or writes the developer's ghx state.
func mouseApp(t *testing.T, numbers ...int) *App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if len(numbers) == 0 {
		numbers = []int{1, 2, 3, 4, 5, 6, 7, 8}
	}
	a := listApp(t, rowsFor(numbers...))
	a.width, a.height = 120, 30
	a.list.resize(120, 30)
	t0 := time.Unix(1000, 0)
	a.nowFunc = func() time.Time { return t0 }
	a.View()
	return a
}

func TestClickSelectsTheRowUnderThePointer(t *testing.T) {
	a := mouseApp(t)
	cmd := clickText(t, a, "pull request 3")
	if got := cursorNumber(t, a); got != 3 {
		t.Fatalf("selected #%d, want #3", got)
	}
	if cmd != nil {
		if _, ok := cmd().(openDetailMsg); ok {
			t.Fatal("one click selects; it must not open the PR")
		}
	}
	if a.state != viewPRList {
		t.Fatal("one click left the list")
	}
}

func TestSecondClickOnTheSelectedRowOpensThePR(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "pull request 4")
	click(a, x, y)
	cmd := click(a, x, y)
	if _, ok := msgOf(t, cmd).(openDetailMsg); !ok {
		t.Fatalf("a second click should open the PR (the Enter action), got %#v", cmd())
	}
}

func TestClicksOnTwoRowsAreNotADoubleClick(t *testing.T) {
	a := mouseApp(t)
	click(a, 5, rowY(t, a, "pull request 2"))
	cmd := click(a, 5, rowY(t, a, "pull request 5"))
	if cmd != nil {
		if _, ok := cmd().(openDetailMsg); ok {
			t.Fatal("clicks on different rows must not open anything")
		}
	}
	if got := cursorNumber(t, a); got != 5 {
		t.Fatalf("selected #%d, want #5", got)
	}
}

func TestSlowSecondClickIsNotADoubleClick(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "pull request 2")
	click(a, x, y)
	later := a.now().Add(mouse.DoubleClickWindow + time.Second)
	a.nowFunc = func() time.Time { return later }
	if cmd := click(a, x, y); cmd != nil {
		if _, ok := cmd().(openDetailMsg); ok {
			t.Fatal("a click after the double-click window must not open the PR")
		}
	}
}

func rowY(t *testing.T, a *App, text string) int {
	t.Helper()
	_, y := at(t, a, text)
	return y
}

func TestWheelOverTheListMovesTheSelectionByThree(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "pull request 1")
	wheel(a, x, y, tea.MouseButtonWheelDown)
	if got := cursorNumber(t, a); got != 4 {
		t.Fatalf("after one notch down: #%d, want #4", got)
	}
	wheel(a, x, y, tea.MouseButtonWheelUp)
	if got := cursorNumber(t, a); got != 1 {
		t.Fatalf("after one notch up: #%d, want #1", got)
	}
}

func TestClickOnAScrolledListHitsTheRowThatIsDrawn(t *testing.T) {
	// 60 rows in a 30-line terminal: the window starts well past row 1, so an
	// offset that forgot the scroll would select a row near the top.
	numbers := make([]int, 60)
	for i := range numbers {
		numbers[i] = 1000 + i
	}
	a := mouseApp(t, numbers...)
	a.list.list.Select(50)
	a.View()
	x, y := at(t, a, "pull request 1048")
	click(a, x, y)
	if got := cursorNumber(t, a); got != 1048 {
		t.Fatalf("selected #%d, want #1048", got)
	}
}

func TestSelectedRowDoubleClickUsesTheRowNotItsPosition(t *testing.T) {
	// After the first click the list is re-sorted under the pointer (a poll
	// landed). The second click on the same spot is a different PR: no open.
	a := mouseApp(t)
	x, y := at(t, a, "pull request 2")
	click(a, x, y)
	a.list.caches[0] = rowsFor(1, 9, 2, 3, 4, 5, 6, 7, 8)
	a.list.syncListItems()
	a.View()
	if cmd := click(a, x, y); cmd != nil {
		if _, ok := cmd().(openDetailMsg); ok {
			t.Fatal("the click landed on a different PR and must not open it")
		}
	}
}

func TestRightClickMarksTheRow(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "pull request 3")
	rightClick(a, x, y)
	if got := cursorNumber(t, a); got != 3 {
		t.Fatalf("selected #%d, want #3", got)
	}
	if n := a.list.visibleSelectedCount(); n != 1 {
		t.Fatalf("%d rows marked, want the clicked one", n)
	}
	rightClick(a, x, y)
	if n := a.list.visibleSelectedCount(); n != 0 {
		t.Fatal("a second right click toggles the mark off, as space does")
	}
}

func TestReleaseAndMotionAreIgnored(t *testing.T) {
	a := mouseApp(t)
	x, y := at(t, a, "pull request 3")
	for _, act := range []tea.MouseAction{tea.MouseActionRelease, tea.MouseActionMotion} {
		send(a, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: act})
		send(a, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonNone, Action: act})
	}
	if got := cursorNumber(t, a); got != 1 {
		t.Fatalf("release/motion moved the selection to #%d", got)
	}
}

func TestClickOnEmptyAreasDoesNothing(t *testing.T) {
	a := mouseApp(t, 1, 2)
	// Below the last row, on the title line and on the footer.
	for _, p := range [][2]int{{5, 20}, {5, 0}, {5, a.height - 1}} {
		if cmd := click(a, p[0], p[1]); cmd != nil {
			t.Fatalf("click at %v returned a command", p)
		}
		if got := cursorNumber(t, a); got != 1 {
			t.Fatalf("click at %v moved the selection to #%d", p, got)
		}
	}
}

func TestClickOnAnEmptyRowBreaksADoubleClick(t *testing.T) {
	a := mouseApp(t, 1, 2)
	x, y := at(t, a, "pull request 1")
	click(a, x, y)
	click(a, 5, 20) // blank space below the rows
	if cmd := click(a, x, y); cmd != nil {
		if _, ok := cmd().(openDetailMsg); ok {
			t.Fatal("a blank click between two row clicks must break the double click")
		}
	}
}

func TestPreviewClickFocusesItAndListClickTakesFocusBack(t *testing.T) {
	a := mouseApp(t)
	a.list.pane.Show = true
	a.View()
	g := a.list.pane.geometry(a.width, a.height-1, a.cfg.DiffSplitRatio)
	if !g.split {
		t.Fatal("setup: the preview should be drawn at this size")
	}
	before := cursorNumber(t, a)
	click(a, g.preview.X+2, bodyTop+1)
	if !a.list.pane.Focus {
		t.Fatal("a click on the preview focuses it")
	}
	if got := cursorNumber(t, a); got != before {
		t.Fatal("a click on the preview must not move the list selection")
	}
	clickText(t, a, "#2")
	if a.list.pane.Focus {
		t.Fatal("a click on the list takes focus back")
	}
	if got := cursorNumber(t, a); got != 2 {
		t.Fatalf("selected #%d, want #2", got)
	}
}

func TestWheelOverThePreviewDoesNotMoveTheList(t *testing.T) {
	a := mouseApp(t)
	a.list.pane.Show = true
	a.View()
	g := a.list.pane.geometry(a.width, a.height-1, a.cfg.DiffSplitRatio)
	wheel(a, g.preview.X+2, bodyTop+1, tea.MouseButtonWheelDown)
	if got := cursorNumber(t, a); got != 1 {
		t.Fatalf("a wheel over the preview moved the list to #%d", got)
	}
}

func TestNarrowListHasNoPreviewAndStillTakesClicks(t *testing.T) {
	a := mouseApp(t)
	a.width = 38 // under 40 columns the preview is not drawn
	a.list.resize(38, 30)
	a.list.pane.Show = true
	a.View()
	if a.list.pane.geometry(a.width, a.height-1, a.cfg.DiffSplitRatio).split {
		t.Fatal("setup: the narrow layout should be list-only")
	}
	x, y := at(t, a, "#3")
	click(a, x, y)
	if got := cursorNumber(t, a); got != 3 {
		t.Fatalf("selected #%d, want #3", got)
	}
}

// --- tab strip ---

func tabbedApp(t *testing.T, width int) *App {
	t.Helper()
	a := mouseApp(t)
	a.cfg.Sources = []config.SourceDef{
		{Name: "reviews", Query: "state:open"},
		{Name: "authored", Query: "state:open"},
		{Name: "mentioned", Query: "state:open"},
	}
	a.list = newPRListModelWithRepo(a.cfg, a.client, a.km, nil)
	a.list.fileCache = &prFileCache{dir: t.TempDir()}
	for i := range a.list.caches {
		a.list.caches[i] = rowsFor(10*i+1, 10*i+2)
	}
	a.list.syncListItems()
	a.width = width
	a.list.resize(width, 30)
	a.View()
	return a
}

func TestClickOnATabSwitchesSource(t *testing.T) {
	a := tabbedApp(t, 120)
	cmd := clickText(t, a, "2 authored")
	if a.list.curTab != 1 {
		t.Fatalf("curTab = %d, want 1", a.list.curTab)
	}
	_ = cmd
	clickText(t, a, "3 mentioned")
	if a.list.curTab != 2 {
		t.Fatalf("curTab = %d, want 2", a.list.curTab)
	}
}

func TestTabClickOnANarrowTerminalHitsTheShrunkenTab(t *testing.T) {
	// At 32 columns the labels no longer fit and each shrinks toward its index;
	// the click must follow the cells that were drawn, not the full labels.
	a := tabbedApp(t, 32)
	strip := strings.Split(a.View(), "\n")[tabStripRow]
	plain := ansi.Strip(strip)
	if strings.Contains(plain, "mentioned") {
		t.Fatalf("setup: the strip should have shrunk its labels: %q", plain)
	}
	x := ansi.StringWidth(plain[:strings.Index(plain, "3")])
	click(a, x, tabStripRow)
	if a.list.curTab != 2 {
		t.Fatalf("curTab = %d, want 2", a.list.curTab)
	}
}

func TestClickOnTheTitleLineDoesNothing(t *testing.T) {
	a := tabbedApp(t, 120)
	click(a, 3, 0)
	if a.list.curTab != 0 {
		t.Fatal("the title line is not a tab")
	}
}

// --- detail ---

func detailApp(t *testing.T) *App {
	t.Helper()
	a := mouseApp(t)
	a.width, a.height = 120, 30
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 7, "acme/one")
	a.detail.resize(120, 30)
	a.state = viewPRDetail
	d := a.detail
	files := make([]pr.FileStat, 12)
	for i := range files {
		files[i] = pr.FileStat{Path: fmt.Sprintf("src/file%02d.go", i), Additions: i, Deletions: 1}
	}
	commits := make([]pr.Commit, 40)
	for i := range commits {
		commits[i] = pr.Commit{OID: fmt.Sprintf("%07d", i), MessageHeadline: fmt.Sprintf("commit number %d", i)}
	}
	d.detail = &pr.Detail{
		Number: 7, Title: "a pull request", State: "OPEN",
		Body:  strings.Repeat("a line of the description\n", 60),
		Files: files, Commits: commits,
	}
	d.rawDiff = mouseDiff
	if err := d.diff.setContent(mouseDiff, mouseThreads()); err != nil {
		t.Fatal(err)
	}
	d.comments.setThreads(mouseThreads())
	d.comments.setConversations([]pr.Conversation{
		{ID: "C1", Kind: "comment", Body: "a pull request level remark", Author: pr.User{Login: "bot"}},
	})
	d.checks.setChecks([]pr.Check{
		{Name: "build", Bucket: "pass", Link: "https://github.com/acme/one/actions/runs/11/job/22"},
		{Name: "lint", Bucket: "fail", Link: "https://github.com/acme/one/actions/runs/33/job/44"},
		{Name: "unit", Bucket: "pass"},
	})
	a.View()
	return a
}

const mouseDiff = `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -1,4 +1,4 @@
 first context line
-old line here
+new line here
 last context line
`

func mouseThreads() []pr.ReviewThread {
	return []pr.ReviewThread{{
		ID: "T1", Path: "app.go", Line: 2, DiffSide: "RIGHT",
		Comments: []pr.ThreadComment{
			{Body: strings.Repeat("this finding is long enough to wrap onto a second line ", 6), Author: pr.User{Login: "claude"}},
			{Body: "a reply", Author: pr.User{Login: "me"}},
		},
	}}
}

func TestClickOnADetailTabSwitchesIt(t *testing.T) {
	a := detailApp(t)
	clickText(t, a, "3 Diff")
	if a.detail.activeTab != tabDiff {
		t.Fatalf("activeTab = %v, want the diff", a.detail.activeTab)
	}
	clickText(t, a, "6 Checks")
	if a.detail.activeTab != tabChecks {
		t.Fatalf("activeTab = %v, want checks", a.detail.activeTab)
	}
}

func TestDetailTabClickOnANarrowTerminal(t *testing.T) {
	a := detailApp(t)
	a.width = 40
	a.detail.resize(40, 30)
	x, y := at(t, a, "5")
	click(a, x, y)
	if a.detail.activeTab != tabCommits {
		t.Fatalf("activeTab = %v, want commits", a.detail.activeTab)
	}
}

func TestWheelScrollsTheOverviewAndDoesNotBankPastTheEnd(t *testing.T) {
	a := detailApp(t)
	for range 100 {
		wheel(a, 5, 6, tea.MouseButtonWheelDown)
		a.View()
	}
	banked := a.detail.overviewOff
	if banked > 80 {
		t.Fatalf("overviewOff = %d: a hundred notches banked scroll past the end", banked)
	}
	for range 3 {
		wheel(a, 5, 6, tea.MouseButtonWheelUp)
		a.View()
	}
	if a.detail.overviewOff != banked-9 {
		t.Fatalf("three notches up moved %d lines, want 9", banked-a.detail.overviewOff)
	}
}

func TestWheelScrollsCommitsByThree(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabCommits)
	a.View()
	wheel(a, 5, 6, tea.MouseButtonWheelDown)
	if a.detail.commitsOff != 3 {
		t.Fatalf("commitsOff = %d, want 3", a.detail.commitsOff)
	}
}

func TestFilesClickSelectsAndSecondClickOpensTheDiffAtThatFile(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabFiles)
	a.View()
	x, y := at(t, a, "src/file05.go")
	click(a, x, y)
	if a.detail.filesCursor != 5 {
		t.Fatalf("filesCursor = %d, want 5", a.detail.filesCursor)
	}
	if a.detail.activeTab != tabFiles {
		t.Fatal("one click selects; it must not leave the Files tab")
	}
	a.View()
	click(a, x, y)
	if a.detail.activeTab != tabDiff {
		t.Fatalf("a second click is the Enter action; activeTab = %v", a.detail.activeTab)
	}
}

func TestFilesWheelMovesTheCursorByThree(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabFiles)
	a.View()
	wheel(a, 5, 6, tea.MouseButtonWheelDown)
	if a.detail.filesCursor != 3 {
		t.Fatalf("filesCursor = %d, want 3", a.detail.filesCursor)
	}
}

func TestFilesClickBelowTheLastFileDoesNothing(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabFiles)
	a.View()
	click(a, 5, 25)
	if a.detail.filesCursor != 0 {
		t.Fatalf("filesCursor = %d after a click on blank space", a.detail.filesCursor)
	}
}

func TestDiffClickSelectsTheLineUnderThePointer(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabDiff)
	a.View()
	clickText(t, a, "new line here")
	row := a.detail.diff.rows[a.detail.diff.cursor]
	if !strings.Contains(a.detail.diff.rowPlainText(row), "new line here") {
		t.Fatalf("cursor is on %q", a.detail.diff.rowPlainText(row))
	}
}

func TestDiffClickOnAWrappedCommentsSecondLineSelectsTheComment(t *testing.T) {
	// The finding wraps onto several lines. A one-line-per-row count would put
	// a click on its second line on the row after it.
	a := detailApp(t)
	a.detail.setTab(tabDiff)
	a.View()
	x, y := at(t, a, "this finding")
	click(a, x, y+1)
	if _, ok := a.detail.diff.threadUnderCursor(); !ok {
		t.Fatalf("cursor row %d is not the thread", a.detail.diff.cursor)
	}
}

func TestDiffSideBySideClickPicksTheColumn(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabDiff)
	a.detail.diff.sideBySide = true
	a.View()
	x, y := at(t, a, "old line here")
	click(a, x, y)
	if a.detail.diff.sideFocus != sideLeft {
		t.Fatal("a click in the left column focuses it")
	}
	if got := a.detail.diff.rows[a.detail.diff.cursor]; got.side != "LEFT" {
		t.Fatalf("cursor row side = %q, want LEFT", got.side)
	}
	a.View()
	x, y = at(t, a, "new line here")
	click(a, x, y)
	if got := a.detail.diff.rows[a.detail.diff.cursor]; got.side != "RIGHT" {
		t.Fatalf("cursor row side = %q, want RIGHT", got.side)
	}
}

func TestDiffClickIsIgnoredInAVisualRange(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabDiff)
	a.View()
	clickText(t, a, "first context line")
	a.detail.updateDiff("visual")
	before := a.detail.diff.cursor
	a.View()
	clickText(t, a, "new line here")
	if a.detail.diff.cursor != before {
		t.Fatal("a click moved one end of a visual range")
	}
}

func TestDiffWheelMovesTheCursorByThree(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabDiff)
	a.detail.diff.cursor = 0
	a.View()
	wheel(a, 5, 6, tea.MouseButtonWheelDown)
	if a.detail.diff.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", a.detail.diff.cursor)
	}
}

func TestCommentsClickSelectsAndSecondClickExpands(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabComments)
	a.View()
	x, y := at(t, a, "a pull request level remark")
	click(a, x, y)
	if a.detail.comments.cursor != 1 {
		t.Fatalf("cursor = %d, want the conversation row (1)", a.detail.comments.cursor)
	}
	a.View()
	click(a, x, y)
	if !a.detail.comments.expanded[conversationIdentity(a.detail.comments.conversations[0])] {
		t.Fatal("a second click is the expand action")
	}
}

func TestCommentsClickOnAnExpandedThreadsBodyLineSelectsThatThread(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabComments)
	a.detail.comments.cursor = 1
	a.detail.comments.expanded[threadIdentity(mouseThreads()[0])] = true
	a.View()
	x, y := at(t, a, "a reply")
	click(a, x, y)
	if a.detail.comments.cursor != 0 {
		t.Fatalf("cursor = %d, want the thread (0)", a.detail.comments.cursor)
	}
}

func TestChecksClickSelectsAndSecondClickOpensTheLog(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabChecks)
	a.View()
	x, y := at(t, a, "lint")
	click(a, x, y)
	if a.detail.checks.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", a.detail.checks.cursor)
	}
	if a.detail.checks.logBusy {
		t.Fatal("one click selects; it must not fetch the log")
	}
	a.View()
	cmd := click(a, x, y)
	if cmd == nil || !a.detail.checks.logBusy || a.detail.checks.logCheck != "lint" {
		t.Fatal("a second click is the logs action for the selected check")
	}
}

func TestChecksLogWheelScrollsTheLog(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabChecks)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("log line %d", i)
	}
	a.detail.checks.setLogs("build", strings.Join(lines, "\n"))
	a.View()
	wheel(a, 5, 6, tea.MouseButtonWheelDown)
	if a.detail.checks.logOff != 3 {
		t.Fatalf("logOff = %d, want 3", a.detail.checks.logOff)
	}
	// A click in the log does not select a check behind it.
	clickText(t, a, "log line 5")
	if a.detail.checks.cursor != 0 {
		t.Fatal("a click in the log moved the check cursor")
	}
}

func TestDetailIgnoresTheMouseOutsideTheBody(t *testing.T) {
	a := detailApp(t)
	a.detail.setTab(tabFiles)
	a.View()
	click(a, 5, 0)
	click(a, 5, a.height-1)
	if a.detail.filesCursor != 0 || a.detail.activeTab != tabFiles {
		t.Fatal("the title and footer lines are not part of the body")
	}
}
