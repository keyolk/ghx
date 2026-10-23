package admin

import (
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// Mouse is an augmentation of the keyboard: every gesture resolves to an
// action the binding table names and runs through the function its key runs.
//
//   - tab: switch category (the 1-7 keys).
//   - row: select it; a second click on the selected row is Enter (members,
//     which drills into a team and does nothing on the other lists).
//   - wheel over the list: move the selection three rows (down/up).
//   - confirmation, write prompt and search bar own the mouse while open:
//     nothing reaches the list, and a click outside the bar is the bar's Esc.
//     The permission prompt's choices are clickable the way tab cycles them.
//
// A drag selects text (selection.go). Plain release and motion, and the right
// and middle buttons, are ignored: the panel has no mark or toggle action for
// them to stand for.

// Screen rows: title, then the tab strip (or the search/prompt bar in its
// place), then the list. The footer is the last row.
const (
	headerRow = 1
	listTop   = 2
)

func (a *App) handleMouse(in tea.Msg) tea.Cmd {
	if a.width < 40 || a.height < 8 {
		return nil // View draws a notice instead of the panel
	}
	// The selection sees every event first; a press that does not move stays
	// the click below.
	selCmd, consumed := a.selectMouse(in)
	msg, isMouse := in.(tea.MouseMsg)
	if consumed || !isMouse {
		return selCmd
	}
	d := mouse.Delta(msg)
	if d == 0 && !mouse.IsPress(msg) {
		return nil
	}
	if mouse.IsPress(msg) {
		a.toast.Ack() // a click acknowledges an error toast, as a key does
	}

	switch {
	case a.confirm != nil:
		// The question is the footer line; a click anywhere else declines.
		if mouse.IsLeftPress(msg) && msg.Y != a.height-1 {
			return a.press(stackConfirm, "no")
		}
		return nil
	case a.prompt.open():
		return a.promptMouse(msg)
	case a.searching:
		if mouse.IsLeftPress(msg) && msg.Y != headerRow {
			return a.press(stackSearch, "clear")
		}
		return nil
	}

	if msg.Y == headerRow {
		if mouse.IsLeftPress(msg) {
			a.clicks = mouse.Clicks{}
			if i := tui.TabStripHit(a.tabLabels(), a.width, msg.X); i >= 0 && i < len(categoryNames) {
				return a.dispatch("category", i)
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
		act := "down"
		if d < 0 {
			act, d = "up", -d
		}
		for range d {
			a.dispatch(act, 0)
		}
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
	// Enter is "members", and like the key it only applies where the panel
	// offers it (not while loading or after an error).
	if double && wasSelected && a.keyOpts().Enabled("members") {
		return a.dispatch("members", 0)
	}
	return nil
}

// listShown reports whether the content area is a scrolling list. Loading,
// errors and the branch-protection summary draw text instead.
func (a *App) listShown() bool {
	if a.loading || a.err != nil || a.memberBusy || a.active == catBranchProtection {
		return false
	}
	return a.itemCount() > 0
}

// rowID names row i of the visible list, so a second click is recognised by
// the thing clicked and not by its position.
func (a *App) rowID(i int) string {
	switch a.active {
	case catCollaborators:
		return "collab:" + a.visibleCollaborators()[i].Login
	case catTeams:
		if a.teamSlug != "" {
			return "member:" + a.teamSlug + "/" + a.visibleMembers()[i].Login
		}
		return "team:" + a.visibleTeams()[i].Slug
	case catReleases:
		return "release:" + a.visibleReleases()[i].TagName
	case catBranches:
		return "branch:" + a.visibleBranches()[i].Name
	case catTags:
		return "tag:" + a.visibleTags()[i].Name
	case catWebhooks:
		return "hook:" + hookID(a.visibleWebhooks()[i])
	}
	return ""
}

func hookID(w gh.Webhook) string { return w.Config.URL + "#" + strconv.Itoa(w.ID) }

func (a *App) promptMouse(msg tea.MouseMsg) tea.Cmd {
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	switch a.prompt.kind {
	case promptLogin:
		// Esc drops what was typed, and a stray click is not worth a login: the
		// prompt closes on a click away from its bar only while it is empty.
		if a.prompt.value == "" && msg.Y != headerRow {
			return a.press(stackLogin, "cancel")
		}
	case promptPermission:
		if msg.Y != headerRow {
			return a.press(stackPermission, "cancel")
		}
		// The choices follow the label, side by side: clicking one cycles to it
		// with tab, which is how the keys reach it.
		x := tui.LipglossWidth(a.prompt.permissionPrefix())
		for i, part := range a.prompt.permissionParts() {
			w := tui.LipglossWidth(part)
			if msg.X >= x && msg.X < x+w {
				n := len(a.prompt.choices)
				for range ((i-a.prompt.choice)%n + n) % n {
					a.press(stackPermission, "next")
				}
				return nil
			}
			x += w
		}
	}
	return nil
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
