package actions

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/link"
	"github.com/keyolk/tuikit/selection"
	"github.com/keyolk/tuikit/tuitest"
)

func stubLauncher(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := link.Launcher
	link.Launcher = func(u string) error {
		got = append(got, u)
		return nil
	}
	t.Cleanup(func() { link.Launcher = old })
	return &got
}

// pressKey runs a key through Update and feeds back what its command
// returned, as the Bubble Tea loop would.
func pressKey(a *App, k string) []tea.Msg {
	_, cmd := a.Update(keyMsg(k))
	msgs := tuitest.Drain(cmd, 0)
	for _, m := range msgs {
		a.Update(m)
	}
	return msgs
}

const run1URL = "https://github.com/acme/ops/actions/runs/1001"

// linkedApp has runs 001 (a URL), 002 (javascript:) and the rest without one.
func linkedApp(t *testing.T) *App {
	t.Helper()
	a := mouseApp(t)
	a.runs[1].URL = run1URL
	a.runs[2].URL = "javascript:alert(1)"
	a.View()
	return a
}

func TestRunNameIsAHyperlinkAndRowsFit(t *testing.T) {
	a := linkedApp(t)
	frame := a.View()
	if !strings.Contains(frame, "\x1b]8;;"+run1URL+"\x1b\\build-001\x1b]8;;\x1b\\") {
		t.Fatalf("the run's workflow name should link to the run:\n%q", frame)
	}
	if strings.Contains(frame, "javascript:") {
		t.Fatal("a javascript: URL from the data was linked")
	}
	for i, l := range strings.Split(frame, "\n") {
		if w := xansi.StringWidth(l); w > a.width {
			t.Fatalf("row %d is %d cells wide in a %d-cell pane", i, w, a.width)
		}
	}
}

func TestOOpensTheSelectedRun(t *testing.T) {
	opened := stubLauncher(t)
	a := linkedApp(t)
	x, y := at(t, a, "build-001")
	click(a, x, y)
	msgs := pressKey(a, "o")
	if len(*opened) != 1 || (*opened)[0] != run1URL {
		t.Fatalf("o opened %q, want %q", *opened, run1URL)
	}
	if len(msgs) != 1 {
		t.Fatalf("o returned %#v, want one OpenedMsg", msgs)
	}
	if m, ok := msgs[0].(link.OpenedMsg); !ok || m.URL != run1URL || m.Err != nil {
		t.Fatalf("o reported %#v", msgs[0])
	}
	if !strings.Contains(xansi.Strip(a.View()), "opened "+run1URL) {
		t.Fatal("the result shows as a toast")
	}
}

func TestOOnARunWithoutAURLOpensNothingAndSaysSo(t *testing.T) {
	opened := stubLauncher(t)
	a := linkedApp(t)
	x, y := at(t, a, "build-003")
	click(a, x, y)
	pressKey(a, "o")
	if len(*opened) != 0 {
		t.Fatalf("o on a run without a URL opened %q", *opened)
	}
	if !strings.Contains(xansi.Strip(a.View()), "no link for this run") {
		t.Fatal("o on a run without a URL should say so")
	}
}

func TestOOnAJavascriptURLIsRefused(t *testing.T) {
	opened := stubLauncher(t)
	a := linkedApp(t)
	x, y := at(t, a, "build-002")
	click(a, x, y)
	pressKey(a, "o")
	if len(*opened) != 0 {
		t.Fatalf("a javascript: URL reached the launcher: %q", *opened)
	}
	if !strings.Contains(xansi.Strip(a.View()), "open failed") {
		t.Fatal("refusing a non-http URL should say so")
	}
}

func TestOpenLinkIsInTheRunsFooterAndHelp(t *testing.T) {
	a := linkedApp(t)
	if !strings.Contains(xansi.Strip(a.View()), "o open") {
		t.Fatalf("the runs footer should offer o:\n%s", xansi.Strip(a.View()))
	}
	help := strings.Join(keys.HelpLines(a.keymap().Help(keys.Opts{}, stackRuns), 100, keys.HelpStyle{}), "\n")
	if !strings.Contains(help, "open link") {
		t.Fatalf("help does not list open link:\n%s", help)
	}
	for _, c := range a.keymap().Conflicts(allStacks...) {
		t.Error(c)
	}
}

// The keys leave logOff unclamped past the end; the selection's Offset is
// set, not added, so a drag past the top still moves the log up a line.
func TestLogDragPastTheTopOfAnOverscrolledLogScrollsUp(t *testing.T) {
	captureCopies(t)
	a := mouseApp(t)
	numberedLog(a, 200)
	a.logOff = 100000 // as holding j leaves it
	a.View()
	// The topmost log line View drew.
	top := ""
	for _, l := range strings.Split(xansi.Strip(a.View()), "\n") {
		if strings.HasPrefix(l, "log line") {
			top = strings.TrimSpace(l)
			break
		}
	}
	x, y := at(t, a, top)
	mouseAt(a, x, y, tea.MouseActionPress)
	_, header := at(t, a, "run logs") // the row above the log pane
	mouseAt(a, x, header, tea.MouseActionMotion)
	_, yAfter := at(t, a, top)
	if yAfter != y+1 {
		t.Fatalf("%s moved from row %d to %d; a drag past the top scrolls up one line", top, y, yAfter)
	}
}

func TestLogDragRestingPastTheBottomKeepsScrolling(t *testing.T) {
	captureCopies(t)
	old := selection.AutoScrollInterval
	selection.AutoScrollInterval = time.Millisecond
	t.Cleanup(func() { selection.AutoScrollInterval = old })
	a := mouseApp(t)
	numberedLog(a, 200)
	x, y := at(t, a, "log line 002")
	mouseAt(a, x, y, tea.MouseActionPress)
	cmd := mouseAt(a, a.width-1, a.height+3, tea.MouseActionMotion)
	if a.logOff != 1 {
		t.Fatalf("setup: logOff = %d after one motion past the bottom", a.logOff)
	}
	for i := range 3 {
		var tick tea.Msg
		for _, m := range tuitest.Drain(cmd, 200*time.Millisecond) {
			if _, ok := m.(selection.AutoScrollMsg); ok {
				tick = m
			}
		}
		if tick == nil {
			t.Fatalf("tick %d: no AutoScrollMsg scheduled", i)
		}
		_, cmd = a.Update(tick)
		if a.logOff != i+2 {
			t.Fatalf("tick %d: logOff = %d, want %d", i, a.logOff, i+2)
		}
	}
}
