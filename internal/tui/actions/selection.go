package actions

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/selection"
)

// Drag selection, as in the main TUI (tui/drag_select.go): a drag selects text
// inside the pane under the press, releasing copies, a key clears it.
//
//   - log viewer: a text pane over the whole log, so a drag past its bottom
//     scrolls and the copy keeps the lines that left the screen.
//   - runs / workflows: a list pane; the second click stays Enter.
//   - loading, error and empty notes: a text pane read from the frame.
//   - search bar: owns the screen while open, so only the bar is selectable.
//     A drag there selects; it never moves the query's cursor or edits it.

// panes lists what a drag can select, from the rects handleMouse and View use.
func (a *App) panes() []selection.Pane {
	if a.width < 40 || a.height < 8 {
		return nil
	}
	if a.logView != "" {
		// renderLogs: a header row, then the log from row 1.
		return []selection.Pane{{ID: "logs", Kind: selection.Text,
			Rect:  mouse.Rect{X: 0, Y: 1, W: a.width, H: max(a.height-1, 1)},
			Lines: a.logLines(), Offset: a.logOff}}
	}
	header := selection.Pane{ID: "header", Kind: selection.Text, Rect: mouse.Rect{X: 0, Y: headerRow, W: a.width, H: 1}}
	if a.searching {
		return []selection.Pane{header}
	}
	content := mouse.Rect{X: 0, Y: listTop, W: a.width, H: a.listRows()}
	kind := selection.Text
	if a.listShown() {
		kind = selection.List
	}
	return []selection.Pane{{ID: "content", Kind: kind, Rect: content}}
}

// logLines is the log as renderLogs splits it.
func (a *App) logLines() []string {
	return strings.Split(strings.TrimRight(a.logView, "\n"), "\n")
}

// selectMouse feeds a mouse event or an auto-scroll tick to the selection
// before click handling; handled means it was a drag (or a word pick) and is
// not also a click.
func (a *App) selectMouse(msg tea.Msg) (cmd tea.Cmd, handled bool) {
	// view clamps logOff the way the panes need it read, and is the frame
	// before Render, so the highlight never reaches a copy. Built at most once
	// per event, and only when the selection asks.
	var frame string
	built := false
	view := func() string {
		if !built {
			frame, built = a.view(), true
		}
		return frame
	}
	panes := func() []selection.Pane {
		view()
		return a.panes()
	}
	r, cmd := a.sel.Update(msg, panes, view, a.clock())
	if r.Scroll != 0 {
		// The only scrollable pane is the log. The kit's offset is set, not
		// added: the keys leave logOff unclamped past the end (render clamps
		// it), and adding -1 to such a value would not move the log.
		a.logOff = r.Offset
	}
	return cmd, r.Consumed
}
