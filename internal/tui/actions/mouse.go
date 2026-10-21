package actions

import (
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"

	"github.com/keyolk/ghx/internal/tui"
)

// Mouse is an augmentation of the keyboard: every gesture resolves to an
// action the binding table names and runs through run, the function the key
// runs.
//
//   - tab: switch tab (the 1-2 keys).
//   - row: select it; a second click on the selected run is Enter (logs).
//     Workflows have no Enter action, so a second click there does nothing.
//   - wheel over the list: move the selection three rows (down/up).
//   - log viewer: the wheel scrolls it; clicks do nothing, esc goes back.
//   - search bar: owns the mouse while open; a click outside it is its Esc.
//
// A drag selects text (selection.go). Plain release and motion, and the right
// and middle buttons, are ignored: the panel has no mark or toggle action for
// them to stand for.

// Screen rows: title, then the tab strip (or the search bar in its place),
// then the list. The log viewer replaces all of it.
const (
	headerRow = 1
	listTop   = 2
)

func (a *App) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if a.width < 40 || a.height < 8 {
		return nil // View draws a notice instead of the panel
	}
	// The selection sees every event first; a press that does not move stays
	// the click below.
	copyCmd, consumed := a.selectMouse(msg)
	if consumed {
		return copyCmd
	}
	d := mouse.Delta(msg)
	if d == 0 && !mouse.IsPress(msg) {
		return nil
	}
	if mouse.IsPress(msg) {
		a.toast.Ack() // a click acknowledges an error toast, as a key does
	}

	if a.logView != "" {
		// The viewer is the whole screen: nothing behind it to reach.
		a.wheel(d, 0)
		return nil
	}
	if a.searching {
		if mouse.IsLeftPress(msg) && msg.Y != headerRow {
			return a.press(stackSearch, "clear")
		}
		return nil
	}

	if msg.Y == headerRow {
		if mouse.IsLeftPress(msg) {
			a.clicks = mouse.Clicks{}
			if i := tui.TabStripHit(a.tabLabels(), a.width, msg.X); i >= 0 && i < len(tabNames) {
				return a.run("tab", i)
			}
		}
		return nil
	}

	list := mouse.Rect{X: 0, Y: listTop, W: a.width, H: a.listRows()}
	if !a.listShown() || !list.Contains(msg.X, msg.Y) {
		a.clicks = mouse.Clicks{}
		return nil
	}
	if d != 0 {
		a.wheel(d, 0)
		return nil
	}
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	i, ok := a.pane.RowAt(list, a.itemCount(), msg)
	if !ok {
		a.clicks = mouse.Clicks{}
		return nil
	}
	id := a.rowID(i)
	wasSelected := a.pane.Cursor == i
	a.pane.Cursor = i
	var double bool
	a.clicks, double = a.clicks.Press(id, a.clock())
	if double && wasSelected && a.active == tabRuns {
		return a.run("logs", 0)
	}
	return nil
}

// wheel runs |d| steps of down or up.
func (a *App) wheel(d, key int) {
	act := "down"
	if d < 0 {
		act, d = "up", -d
	}
	for range d {
		a.run(act, key)
	}
}

// listShown reports whether the content area is a scrolling list. Loading,
// errors and the empty notes draw text instead.
func (a *App) listShown() bool {
	if a.loading || a.err != nil || a.logBusy {
		return false
	}
	return a.itemCount() > 0
}

// rowID names row i of the visible list, so a second click is recognised by
// the thing clicked and not by its position.
func (a *App) rowID(i int) string {
	if a.active == tabWorkflows {
		return "workflow:" + strconv.FormatInt(int64(a.visibleWorkflows()[i].ID), 10)
	}
	return "run:" + strconv.FormatInt(int64(a.visibleRuns()[i].DatabaseID), 10)
}

// press runs action in stack as if its key had been pressed, through
// handleKey, so a click takes the path the key takes — including a rebound
// key. It does nothing when no key reaches the action.
func (a *App) press(stack []keys.Ctx, action string) tea.Cmd {
	key, ok := a.keymap().KeyFor(stack, action, keys.Opts{})
	if !ok {
		return nil
	}
	msg, ok := tui.KeyMsgNamed(key)
	if !ok {
		return nil
	}
	return a.handleKey(msg)
}

// clock is time.Now unless a test has replaced it, so a double click can be
// staged without sleeping.
func (a *App) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}
