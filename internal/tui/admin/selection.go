package admin

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/selection"
)

// Drag selection, as in the main TUI (tui/drag_select.go): a drag selects text
// inside the pane under the press, releasing copies, a key clears it.
//
//   - the lists (collaborators, teams, members, releases, ...): a list pane;
//     the second click stays Enter.
//   - branch protection, loading and error notes: a text pane read from the
//     frame (no key scrolls them, so a drag cannot scroll them either).
//   - the confirmation (its question is the footer) and the write prompt or
//     search bar (the header row) own the screen while open: only they are
//     selectable. A drag in the prompt selects; it never moves its cursor or
//     edits what was typed.

// panes lists what a drag can select, from the rects handleMouse and View use.
func (a *App) panes() []selection.Pane {
	if a.width < 40 || a.height < 8 {
		return nil
	}
	switch {
	case a.confirm != nil:
		return []selection.Pane{{ID: "confirm", Kind: selection.Text, Rect: mouse.Rect{X: 0, Y: a.height - 1, W: a.width, H: 1}}}
	case a.prompt.open(), a.searching:
		return []selection.Pane{{ID: "header", Kind: selection.Text, Rect: mouse.Rect{X: 0, Y: headerRow, W: a.width, H: 1}}}
	}
	kind := selection.Text
	if a.listShown() {
		kind = selection.List
	}
	return []selection.Pane{{ID: "content", Kind: kind, Rect: mouse.Rect{X: 0, Y: listTop, W: a.width, H: a.listRows()}}}
}

// selectMouse feeds a mouse event to the selection before click handling;
// handled means it was a drag (or a word pick) and is not also a click. No
// admin pane hands over Lines, so nothing here scrolls and no auto-scroll
// tick is ever scheduled. The frame is View before Render, so the highlight
// never reaches a copy.
func (a *App) selectMouse(msg tea.Msg) (cmd tea.Cmd, handled bool) {
	r, cmd := a.sel.Update(msg, a.panes, a.view, a.clock())
	return cmd, r.Consumed
}
