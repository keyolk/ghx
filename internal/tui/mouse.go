package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
)

// Mouse is an augmentation of the keyboard, not a second command set: every
// gesture below resolves to an action the binding table already names and runs
// through the function that action's key runs, so a click does exactly what
// the key it stands for does.
//
// PR list
//   - tab: switch to that source (the 1-9 jump, selectTab).
//   - row: select it; a second click on the selected row opens the PR (open).
//     The list pane takes focus first, as moving into it with h would.
//   - right click on a row: select it and toggle its multi-select mark (select).
//   - wheel over the list: move the selection three rows (down/up).
//   - click or wheel over the preview: focus it (showpreview). The preview is
//     a short summary rebuilt on every frame and no key scrolls it, so there is
//     nothing for the wheel to scroll.
//
// PR detail
//   - tab: switch to that tab (detailtab).
//   - Files / Comments / Checks rows: select; a second click is that tab's
//     Enter (openfile, expand, logs).
//   - Diff rows: a click selects. The diff is a text pane, so a double click
//     selects a word and a triple click a line (drag_select.go); the thread
//     action stays on its key.
//   - wheel: three steps of the tab's own down/up. Overview, Commits and the
//     check log scroll; the others move their cursor.
//
// Overlays own the mouse while open (overlayMouse): nothing reaches the view
// beneath, a click outside closes the overlay with the action its Esc runs,
// and rows are selected the way the overlay's own keys select them.
//
// Drag, and the release that ends one, belong to the text selection
// (drag_select.go); plain release and motion are ignored.

// handleMouse is the entry point from Update.
func (a *App) handleMouse(msg tea.MouseMsg) tea.Cmd {
	// Below the size View draws a notice instead of the app, so there is
	// nothing under the pointer to act on.
	if a.quitting || a.width < 30 || a.height < 8 {
		return nil
	}
	// The selection sees every event first, release and motion included: a
	// drag is only a drag to it. A press that does not move stays the click
	// below (Consumed is false for it).
	copyCmd, consumed := a.selectMouse(msg)
	if consumed {
		return copyCmd
	}
	d := mouse.Delta(msg)
	if d == 0 && !mouse.IsPress(msg) {
		return copyCmd
	}
	if mouse.IsPress(msg) {
		// A click is the acknowledgement an error toast waits for, as a key is.
		a.toast.Ack()
	}
	// The pointer proves someone is here, as a key does: the poll leaves its
	// idle cadence and a stale list refreshes.
	wake := a.noteActivity()
	return tea.Batch(wake, copyCmd, a.routeMouse(msg, d))
}

func (a *App) routeMouse(msg tea.MouseMsg, d int) tea.Cmd {
	if cmd, owned := a.overlayMouse(msg, d); owned {
		return cmd
	}
	switch a.state {
	case viewPRList:
		return a.listMouse(msg, d)
	case viewPRDetail:
		if a.detail != nil {
			return a.detailMouse(msg, d)
		}
	}
	return nil
}

// Screen rows: the title line is row 0, the view's own tab strip row 1 and its
// body starts on row 2 (View draws title, content, footer; both views put
// their strip first in the content).
const (
	tabStripRow = 1
	bodyTop     = 2
)

// --- PR list ---

func (a *App) listMouse(msg tea.MouseMsg, d int) tea.Cmd {
	m := a.list
	if msg.Y == tabStripRow {
		if !mouse.IsLeftPress(msg) {
			return nil
		}
		a.clicks = mouse.Clicks{}
		// The strip's last cell can be the "stale" marker, which is no tab.
		if i := cellAt(m.tabCells(a.width), msg.X); i >= 0 && i < len(m.sources) {
			return m.selectTab(i)
		}
		return nil
	}
	items := m.list.VisibleItems()
	if len(items) == 0 {
		// The pane shows a loading, error or empty note instead of rows.
		a.clicks = mouse.Clicks{}
		return nil
	}

	g := m.pane.geometry(a.width, a.height-1, m.cfg.DiffSplitRatio)
	listR := offsetRect(g.list, 0, bodyTop)
	prevR := offsetRect(g.preview, 0, bodyTop)

	if d != 0 {
		switch {
		case listR.Contains(msg.X, msg.Y):
			a.focusList()
			act := "down"
			if d < 0 {
				act, d = "up", -d
			}
			for range d {
				m.runAction(keys.Binding{Action: act}, tea.KeyMsg{})
			}
		case g.split && prevR.Contains(msg.X, msg.Y):
			a.focusPreview()
		}
		return nil
	}

	switch {
	case g.split && prevR.Contains(msg.X, msg.Y):
		if mouse.IsLeftPress(msg) {
			a.clicks = mouse.Clicks{}
			a.focusPreview()
		}
		return nil
	case !listR.Contains(msg.X, msg.Y):
		return nil
	}

	left, right := mouse.IsLeftPress(msg), mouse.IsRightPress(msg)
	if !left && !right {
		return nil
	}
	start := m.list.Paginator.Page * m.list.Paginator.PerPage
	hit := mouse.RowHit(listR, start, len(items), msg)
	if !hit.HasItem {
		a.clicks = mouse.Clicks{} // a blank row breaks a double click
		return nil
	}
	row, ok := items[hit.Index].(prListItem)
	if !ok {
		return nil
	}
	id := selectionKey(row.pr)
	was, hadSel := m.selectedItem()
	wasSelected := hadSel && selectionKey(was.pr) == id

	a.focusList()
	m.list.Select(hit.Index)
	if right {
		a.clicks = mouse.Clicks{}
		m.runAction(keys.Binding{Action: "select"}, tea.KeyMsg{})
		return nil
	}
	var double bool
	a.clicks, double = a.clicks.Press(id, a.now())
	if double && wasSelected {
		return a.pressAction(stackList, "open")
	}
	return nil
}

// focusList puts keyboard focus on the list pane: h on a focused preview, the
// key that does the same.
func (a *App) focusList() {
	if a.list.pane.Focus {
		a.list.runAction(keys.Binding{Action: "hidepreview"}, tea.KeyMsg{})
	}
}

// focusPreview focuses the preview pane, which l does.
func (a *App) focusPreview() {
	if !a.list.pane.Focus {
		a.list.runAction(keys.Binding{Action: "showpreview"}, tea.KeyMsg{})
	}
}

// --- PR detail ---

func (a *App) detailMouse(msg tea.MouseMsg, d int) tea.Cmd {
	dm := a.detail
	if msg.Y == tabStripRow {
		if !mouse.IsLeftPress(msg) {
			return nil
		}
		a.clicks = mouse.Clicks{}
		if i := cellAt(dm.tabCells(a.width), msg.X); i >= 0 && i < len(dm.tabs) {
			return dm.setTab(dm.tabs[i])
		}
		return nil
	}
	body := mouse.Rect{X: 0, Y: bodyTop, W: a.width, H: dm.contentHeight()}
	if !body.Contains(msg.X, msg.Y) {
		return nil
	}

	if d != 0 {
		act := "down"
		if d < 0 {
			act, d = "up", -d
		}
		for range d {
			dm.wheelStep(act)
		}
		return nil
	}
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	id, wasSelected, enter := dm.clickRow(body, msg)
	if id == "" {
		a.clicks = mouse.Clicks{}
		return nil
	}
	var double bool
	a.clicks, double = a.clicks.Press(id, a.now())
	if double && wasSelected {
		return enter()
	}
	return nil
}

// wheelStep is one notch's single step on the active tab: its own down or up,
// so the wheel scrolls what j and k scroll.
func (d *prDetailModel) wheelStep(act string) {
	switch d.activeTab {
	case tabOverview:
		d.updateOverview(act)
	case tabFiles:
		d.updateFiles(act)
	case tabDiff:
		d.updateDiff(act)
	case tabComments:
		d.updateComments(act)
	case tabCommits:
		d.updateCommits(act)
	case tabChecks:
		if d.checks.showingLogs() {
			d.updateLogs(act)
		} else {
			d.updateChecks(act)
		}
	}
}

// clickRow selects the row of the active tab under the pointer. id names it
// for double-click detection ("" when the click hit no row), wasSelected says
// it already held the cursor, and enter is what the tab's Enter key does.
func (d *prDetailModel) clickRow(body mouse.Rect, msg tea.MouseMsg) (id string, wasSelected bool, enter func() tea.Cmd) {
	run := func(f func(string) (tea.Cmd, bool), act string) func() tea.Cmd {
		return func() tea.Cmd { cmd, _ := f(act); return cmd }
	}
	switch d.activeTab {
	case tabFiles:
		if d.detail == nil {
			return "", false, nil
		}
		hit := mouse.RowHit(body, d.filesOff, len(d.detail.Files), msg)
		if !hit.HasItem {
			return "", false, nil
		}
		was := d.filesCursor == hit.Index
		d.filesCursor = hit.Index
		return "file:" + d.detail.Files[hit.Index].Path, was, run(d.updateFiles, "openfile")

	case tabDiff:
		i, ok := d.diff.diffRowAt(body, msg)
		if !ok {
			return "", false, nil
		}
		was := d.diff.cursor == i
		d.diff.clickRow(i)
		return "diff:" + itoa(i), was, run(d.updateDiff, "thread")

	case tabComments:
		c := d.comments
		row, ok := c.rowAt(body, msg)
		if !ok {
			return "", false, nil
		}
		was := c.cursor == row
		c.cursor = row
		return "comment:" + c.rowIdentity(row), was, run(d.updateComments, "expand")

	case tabChecks:
		c := d.checks
		if c.showingLogs() {
			return "", false, nil
		}
		hit := mouse.RowHit(body, c.offset, len(c.checks), msg)
		if !hit.HasItem {
			return "", false, nil
		}
		was := c.cursor == hit.Index
		c.cursor = hit.Index
		ck := c.checks[hit.Index]
		return "check:" + ck.Name + "|" + ck.Link, was, run(d.updateChecks, "logs")
	}
	return "", false, nil
}

// rowAt maps a click in the comments body to a row. A row spans its header and,
// when expanded, its comment lines, so the click is measured in screen lines
// against where layoutLines started each row — the same layout render draws.
func (c *commentsView) rowAt(body mouse.Rect, msg tea.MouseMsg) (int, bool) {
	if c.rowCount() == 0 {
		return 0, false
	}
	lines, starts := c.layoutLines(body.W)
	hit := mouse.RowHit(body, c.offset, len(lines), msg)
	if !hit.HasItem {
		return 0, false
	}
	// The blank line and the "conversation" rule between the two kinds of row
	// belong to neither.
	plain := strings.TrimSpace(ansi.Strip(lines[hit.Index]))
	if plain == "" || strings.HasPrefix(plain, "── conversation") {
		return 0, false
	}
	row := 0
	for i, s := range starts {
		if s <= hit.Index {
			row = i
		}
	}
	return row, true
}

// rowIdentity is the stable name of the row at index i of the cursor's
// numbering: threads first, then PR-level conversations.
func (c *commentsView) rowIdentity(i int) string {
	v := c.visible()
	if i < len(v) {
		return threadIdentity(v[i])
	}
	if cv, ok := c.conversationAt(i - len(v)); ok {
		return conversationIdentity(cv)
	}
	return itoa(i)
}

func offsetRect(r mouse.Rect, dx, dy int) mouse.Rect {
	if r == (mouse.Rect{}) {
		return r
	}
	r.X += dx
	r.Y += dy
	return r
}

// pressAction runs action in stack as if its key had been pressed, through
// handleKey, so a click on an overlay takes the path the key takes — including
// whatever the user rebound it to. It does nothing when no key reaches the
// action.
func (a *App) pressAction(stack []keys.Ctx, action string) tea.Cmd {
	key, ok := a.keymap().KeyFor(stack, action, keys.Opts{})
	if !ok {
		return nil
	}
	msg, ok := KeyMsgNamed(key)
	if !ok {
		return nil
	}
	return a.handleKey(msg)
}

// KeyMsgNamed rebuilds the message for a key name from the binding table
// ("esc", "enter", "down", "ctrl+n", "j", " "), for the names the modals bind.
// The subcommand TUIs use it to press an overlay's key for a click.
func KeyMsgNamed(key string) (tea.KeyMsg, bool) {
	switch key {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}, true
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}, true
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}, true
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}, true
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}, true
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}, true
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}, true
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}, true
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}, true
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}, true
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}, true
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, true
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}, true
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}, true
	}
	if r := []rune(key); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r}, true
	}
	return tea.KeyMsg{}, false
}
