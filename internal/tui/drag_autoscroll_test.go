package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/selection"
	"github.com/keyolk/tuikit/tuitest"
)

var descLine = regexp.MustCompile(`description line (\d\d)`)

// firstDescription is the number of the topmost "description line NN" View
// drew, and its row; -1 when none is on screen.
func firstDescription(a *App) (n, y int) {
	for i, l := range strings.Split(ansi.Strip(a.View()), "\n") {
		if m := descLine.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n, i
		}
	}
	return -1, -1
}

// The keys leave overviewOff unclamped past the end (render clamps it on the
// next frame). The selection's Offset is set, not added, so a drag past the
// top of such a pane still scrolls it up one line.
func TestDragPastTheTopOfAnOverscrolledOverviewScrollsUp(t *testing.T) {
	captureCopies(t)
	a := overviewApp(t)
	a.detail.overviewOff = 100000 // as many j presses past the end leave it
	a.View()
	before, y := firstDescription(a)
	if before < 0 {
		t.Fatal("setup: the overview's end should be on screen")
	}
	x, _ := at(t, a, "description line "+strconv.Itoa(before))
	_, strip := at(t, a, "1 Overview")
	press(a, x, y)
	a.View()
	motion(a, x, strip) // above the pane: the tab strip's row
	// Scrolling up moves every line down a row.
	_, yAfter := at(t, a, "description line "+strconv.Itoa(before))
	if yAfter != y+1 {
		t.Fatalf("a drag past the top should scroll the overview up one line: line %02d moved from row %d to %d", before, y, yAfter)
	}
}

// A drag resting past the bottom keeps scrolling by AutoScrollMsg ticks: the
// terminal sends no motion for a pointer that does not move.
func TestDragRestingPastTheBottomKeepsScrolling(t *testing.T) {
	captureCopies(t)
	old := selection.AutoScrollInterval
	selection.AutoScrollInterval = time.Millisecond
	t.Cleanup(func() { selection.AutoScrollInterval = old })

	a := overviewApp(t)
	x0, y0 := at(t, a, "Author:")
	_, footer := at(t, a, "q quit")
	press(a, x0, y0)
	a.View()
	cmd := motion(a, a.width-1, footer)
	a.View()
	off := a.detail.overviewOff
	if off == 0 {
		t.Fatal("setup: the motion past the bottom scrolls once")
	}
	for i := range 3 {
		var tick tea.Msg
		for _, m := range tuitest.Drain(cmd, 200*time.Millisecond) {
			if _, ok := m.(selection.AutoScrollMsg); ok {
				tick = m
			}
		}
		if tick == nil {
			t.Fatalf("tick %d: a drag resting past the edge should schedule an AutoScrollMsg", i)
		}
		_, cmd = a.Update(tick)
		a.View()
		if a.detail.overviewOff != off+1 {
			t.Fatalf("tick %d: overviewOff = %d, want %d", i, a.detail.overviewOff, off+1)
		}
		off = a.detail.overviewOff
	}
	release(a, a.width-1, footer)
	if msgs := tuitest.Drain(cmd, 50*time.Millisecond); len(msgs) > 0 {
		if _, cmd2 := a.Update(msgs[0]); cmd2 != nil || a.detail.overviewOff != off {
			t.Fatal("a tick after the release must not scroll")
		}
	}
}

// NO_COLOR asks for no color, and reverse is not one: the drag highlight
// must still be drawn.
func TestNoColorKeepsTheDragHighlight(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	captureCopies(t)
	a := overviewApp(t)
	x0, y0 := at(t, a, "Author:")
	drag(a, x0, y0, x0+5, y0)
	got := tuitest.Reversed(a.View())
	if len(got) != 1 || got[0] != "Author" {
		t.Fatalf("highlighted %q, want [Author]", got)
	}
}
