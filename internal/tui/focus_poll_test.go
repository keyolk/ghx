package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// lastKeyAt cannot tell a window being read from one left behind in another
// tmux window: both have "pressed a key recently". Focus is the signal that
// can, and the cost of not having it is an account-wide budget spent on frames
// nobody sees.

func TestBlurSuspendsThePoll(t *testing.T) {
	a, _ := idleApp(t)

	if a.armPoll() == nil {
		t.Fatal("setup: a focused app should arm a poll")
	}

	_, cmd := a.Update(tea.BlurMsg{})
	if cmd != nil {
		t.Error("blurring scheduled work instead of standing down")
	}
	if a.armPoll() != nil {
		t.Error("an unfocused app armed a poll timer")
	}
}

// The timer already pending when focus is lost cannot be cancelled — bubbletea
// has no way to. Retiring it by generation is what actually stops the fetch.
func TestPendingTickIsDroppedWhileUnfocused(t *testing.T) {
	a, _ := idleApp(t)
	armedGen := a.pollGen

	a.Update(tea.BlurMsg{})

	_, cmd := a.Update(prListTickMsg{at: time.Now(), gen: armedGen})
	if cmd != nil {
		t.Error("a tick armed before blurring still fetched")
	}
}

// Coming back refreshes: the rows are as old as the time spent away.
func TestFocusResumesAndRefreshes(t *testing.T) {
	a, now := idleApp(t)
	a.Update(tea.BlurMsg{})

	*now = now.Add(2 * time.Hour)

	_, cmd := a.Update(tea.FocusMsg{})
	if cmd == nil {
		t.Fatal("regaining focus did not refresh the stale rows")
	}
	if a.unfocused {
		t.Error("app is still marked unfocused after a focus event")
	}
	// Returning to a window is attention, so the cadence is the active one
	// rather than an idle stretch that merely spanned the absence.
	if got := a.pollInterval(); got != a.cfg.PollDuration() {
		t.Errorf("poll interval after refocus is %s, want the active %s",
			got, a.cfg.PollDuration())
	}
}

// Some terminals never report focus returning. A keypress proves someone is
// here, and without this the poll would stay suspended for a window in use.
func TestKeypressResumesAPollSuspendedByBlur(t *testing.T) {
	a, _ := idleApp(t)
	a.Update(tea.BlurMsg{})

	if cmd := a.noteActivity(); cmd == nil {
		t.Fatal("a keypress while unfocused did not resume the poll")
	}
	if a.unfocused {
		t.Error("a keypress left the app marked unfocused")
	}
	if a.armPoll() == nil {
		t.Error("the poll chain did not restart after a keypress")
	}
}

// A terminal that never reports focus must behave exactly as before.
func TestPollUnchangedWithoutFocusReporting(t *testing.T) {
	a, _ := idleApp(t)
	if a.unfocused {
		t.Fatal("an app starts focused until told otherwise")
	}
	if a.armPoll() == nil {
		t.Error("poll did not arm without any focus event")
	}
}

// Stopping silently reads as ghx being broken. The title line says why.
func TestUnfocusedPollIsVisibleInTheTitle(t *testing.T) {
	a := listApp(t, rowsFor(1, 2, 3))
	a.Update(tea.BlurMsg{})

	if got := a.titleLine(); !strings.Contains(got, "paused") {
		t.Errorf("title line does not say the poll is paused: %q", got)
	}
}
