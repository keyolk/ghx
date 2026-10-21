package tui

import (
	"fmt"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/selection"
)

// Drag selection, the way Claude Code's mod engine selects: a press picks the
// pane under it, a drag selects text inside that pane only (never the border,
// the tab strip or the neighbor pane), releasing copies, and the highlight
// stays until the next press or key. Mouse capture turned the terminal's own
// drag-select off, so without this nothing on screen could be copied.
//
// Text panes (Overview, Diff, Commits, a check's log, the preview) take a
// double click as a word and a triple click as a line. List panes (the PR
// list, Files, Comments, Checks, the pickers) keep the second click on the
// selected row as its Enter.

// Pane IDs. The diff's side-by-side layout is two panes, one per column, so a
// drag in the old side never picks up the new side.
const (
	paneList      = "list"
	panePreview   = "preview"
	paneBody      = "body"
	paneDiffLeft  = "diff-left"
	paneDiffRight = "diff-right"
	paneOverlay   = "overlay"
)

// SelectionStyle draws a drag selection, here and in the subcommand TUIs. It
// survives NO_COLOR: reverse video needs no color.
var SelectionStyle = lipgloss.NewStyle().Reverse(true)

// selectionPanes lists what a drag can select, from the numbers View and the
// mouse hit tests draw with: SplitPane.geometry, the detail body rect, and
// boxRect for an overlay. Call it after View has rendered at this size, since
// render settles the scroll offsets it reads.
//
// full asks for the whole content of the diff, which costs a render of every
// row. The mouse path always asks; View asks only while a selection is drawn.
func (a *App) selectionPanes(full bool) []selection.Pane {
	if a.width < 30 || a.height < 8 {
		return nil
	}
	// A modal owns the screen: only its box can be selected, never the view
	// beneath it.
	if box := a.overlayBox(); box != "" {
		r, clipped := a.boxRect(box)
		inner := mouse.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}
		if clipped > 0 {
			// The top border was clipped off; the first drawn line is content.
			inner.Y, inner.H = r.Y, r.H-1
		}
		return []selection.Pane{{ID: paneOverlay, Rect: inner, Kind: a.overlayKind()}}
	}
	switch a.state {
	case viewPRList:
		return a.listPanes()
	case viewPRDetail:
		if a.detail != nil {
			return a.detail.selectionPanes(a.width, full)
		}
	}
	return nil
}

func (a *App) listPanes() []selection.Pane {
	m := a.list
	if m == nil || len(m.list.VisibleItems()) == 0 {
		return nil
	}
	g := m.pane.geometry(a.width, a.height-1, m.cfg.DiffSplitRatio)
	panes := []selection.Pane{{ID: paneList, Rect: offsetRect(g.list, 0, bodyTop), Kind: selection.List}}
	if g.split {
		// The preview is rebuilt every frame and no key scrolls it, so it is
		// read from the frame rather than handed over whole.
		panes = append(panes, selection.Pane{ID: panePreview, Rect: offsetRect(g.preview, 0, bodyTop), Kind: selection.Text})
	}
	return panes
}

// selectionPanes is the active tab's body as one pane, or the diff's two
// columns in the side-by-side layout.
func (d *prDetailModel) selectionPanes(width int, full bool) []selection.Pane {
	body := mouse.Rect{X: 0, Y: bodyTop, W: width, H: d.contentHeight()}
	list := []selection.Pane{{ID: paneBody, Rect: body, Kind: selection.List}}
	switch d.activeTab {
	case tabOverview:
		if d.detail == nil {
			return nil
		}
		return []selection.Pane{{ID: paneBody, Rect: body, Kind: selection.Text,
			Lines: blockLines(d.overviewText(width)), Offset: d.overviewOff}}
	case tabCommits:
		if d.detail == nil || len(d.detail.Commits) == 0 {
			return nil
		}
		return []selection.Pane{{ID: paneBody, Rect: body, Kind: selection.Text,
			Lines: blockLines(d.commitsText()), Offset: d.commitsOff}}
	case tabDiff:
		return d.diff.selectionPanes(body, full)
	case tabChecks:
		c := d.checks
		if c.logBusy {
			return nil
		}
		if c.logCheck == "" {
			return list
		}
		if len(c.logs) == 0 {
			return nil
		}
		// The first body line is the log's header and does not scroll.
		logs := mouse.Rect{X: body.X, Y: body.Y + 1, W: body.W, H: body.H - 1}
		return []selection.Pane{{ID: paneBody, Rect: logs, Kind: selection.Text,
			Lines: c.logs, Offset: c.logOff}}
	case tabFiles, tabComments:
		return list
	}
	return nil
}

func (v *diffView) selectionPanes(body mouse.Rect, full bool) []selection.Pane {
	if len(v.rows) == 0 {
		return nil
	}
	if v.sideBySide && body.W >= 80 {
		half := sideHalfWidth(body.W)
		rightX := half + lipglossWidth(sideDivider)
		left := selection.Pane{ID: paneDiffLeft, Rect: mouse.Rect{X: body.X, Y: body.Y, W: half, H: body.H}, Kind: selection.Text}
		right := selection.Pane{ID: paneDiffRight, Rect: mouse.Rect{X: body.X + rightX, Y: body.Y, W: body.W - rightX, H: body.H}, Kind: selection.Text}
		if full {
			left.Lines, right.Lines, left.Offset = v.sideLines(body.W)
			right.Offset = left.Offset
		}
		return []selection.Pane{left, right}
	}
	p := selection.Pane{ID: paneBody, Rect: body, Kind: selection.Text}
	if full {
		p.Lines, p.Offset = v.unifiedLines(body.W)
	}
	return []selection.Pane{p}
}

// overlayBox is the box of the modal that owns the mouse, in the order
// overlayMouse asks them, or "" when none is open.
func (a *App) overlayBox() string {
	rows := a.contentRows()
	switch {
	case a.mergeFailure != nil:
		return a.renderMergeFailure(a.width, rows)
	case a.composer.active:
		return a.composer.render(a.width, rows, a.modalHints(stackComposer, a.width-4))
	case a.confirm != nil:
		return a.renderConfirm(a.width, rows)
	case a.suggestion != nil:
		return a.renderSuggestionPrompt(a.width, rows)
	case a.statusFilter != nil:
		return a.renderStatusFilter(a.width, rows)
	case a.labels != nil:
		return a.renderLabelPicker(a.width, rows)
	case a.repos != nil:
		return a.renderRepoPicker(a.width, rows)
	case a.mergePrompt != nil:
		return a.renderMergePrompt(a.width, rows)
	case a.search.active:
		return a.search.render(a.width, rows, a.modalHints(stackSearch, a.width-4))
	case a.palette.active:
		return a.palette.render(a.width, rows)
	case a.helpOpen:
		return a.renderHelpOverlay(a.width, rows)
	}
	return ""
}

// overlayKind: pickers keep the second click as their row action, so they are
// lists; every other box is text to read.
func (a *App) overlayKind() selection.Kind {
	switch {
	case a.mergeFailure != nil, a.composer.active, a.confirm != nil, a.suggestion != nil:
		return selection.Text
	case a.statusFilter != nil, a.labels != nil, a.repos != nil, a.mergePrompt != nil:
		return selection.List
	case a.search.active:
		return selection.Text
	case a.palette.active:
		return selection.List
	}
	return selection.Text
}

// selectMouse feeds the event to the selection before any click handling.
// handled means the selection consumed it and the app must not also treat it
// as a click.
func (a *App) selectMouse(msg tea.MouseMsg) (cmd tea.Cmd, handled bool) {
	// view settles the scroll offsets the panes read, and is the frame panes
	// without full content are read from (before Render, so the highlight
	// never reaches a copy). It is pure apart from those clamps, which the next
	// render would apply anyway.
	frame := a.view()
	r := a.sel.Mouse(msg, a.selectionPanes(true), frame, a.now())
	if r.Scroll != 0 {
		a.scrollPane(r.Scroll)
	}
	if r.Copy != "" {
		cmd = copySelection(r.Copy)
	}
	return cmd, r.Consumed
}

// scrollPane scrolls a text pane by n lines through the path its keys take.
//
// Every scrollable pane is the active tab's body (the diff's two columns
// scroll together), so the pane ID does not pick what scrolls.
func (a *App) scrollPane(n int) {
	if a.state != viewPRDetail || a.detail == nil {
		return
	}
	d := a.detail
	act := "down"
	if n < 0 {
		act = "up"
	}
	steps := max(n, -n)
	switch d.activeTab {
	case tabOverview:
		for range steps {
			d.updateOverview(act)
		}
	case tabCommits:
		for range steps {
			d.updateCommits(act)
		}
	case tabChecks:
		d.checks.scrollLogs(n)
	case tabDiff:
		d.diff.scrollLines(n, a.width, d.contentHeight())
	}
}

// Copied reports a finished drag-selection copy: its length, or an error that
// stays until a key (a copy that silently failed would be pasted as whatever
// the clipboard held before). The caller returns Expiry, as after Set.
func (t *Toast) Copied(msg selection.CopiedMsg) {
	if msg.Err != nil {
		t.SetError("copy failed: " + msg.Err.Error())
		return
	}
	t.Set(fmt.Sprintf("copied %d chars", utf8.RuneCountInString(msg.Text)))
}

// copySelection builds the clipboard command for a finished selection. Tests
// swap it to capture the text without ever writing the real clipboard.
var copySelection = selection.Copy
