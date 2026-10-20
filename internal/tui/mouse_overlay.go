package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
)

// overlayMouse gives the mouse to the overlay that owns the keyboard, in the
// order handleKey asks them. owned is true while any overlay is open: nothing
// reaches the view beneath, as with keys.
//
// Every overlay is measured from the string View draws it from (overlayRect),
// not from numbers recomputed here.
func (a *App) overlayMouse(msg tea.MouseMsg, d int) (cmd tea.Cmd, owned bool) {
	rows := a.contentRows()
	switch {
	case a.mergeFailure != nil:
		return a.closeOutside(msg, a.renderMergeFailure(a.width, rows), stackFailure, "dismiss"), true

	case a.composer.active:
		// Esc discards the draft, and a stray click is not worth a half-written
		// review: the box closes on a click outside only while it is empty.
		if a.composer.ta.Value() != "" || a.composer.busy {
			return nil, true
		}
		box := a.composer.render(a.width, rows, a.modalHints(stackComposer, a.width-4))
		return a.closeOutside(msg, box, stackComposer, "cancel"), true

	case a.confirm != nil:
		return a.closeOutside(msg, a.renderConfirm(a.width, rows), stackConfirm, "no"), true

	case a.suggestion != nil:
		return a.closeOutside(msg, a.renderSuggestionPrompt(a.width, rows), stackSuggest, "no"), true

	case a.statusFilter != nil:
		return a.statusFilterMouse(msg, d), true

	case a.labels != nil:
		return a.labelsMouse(msg, d), true

	case a.repos != nil:
		return a.reposMouse(msg, d), true

	case a.mergePrompt != nil:
		return a.mergePromptMouse(msg), true

	case a.search.active:
		box := a.search.render(a.width, rows, a.modalHints(stackSearch, a.width-4))
		return a.closeOutside(msg, box, stackSearch, "clear"), true

	case a.palette.active:
		return a.paletteMouse(msg, d), true

	case a.helpOpen:
		return a.helpMouse(msg, d), true
	}
	return nil, false
}

// boxRect is where View composites box, in screen coordinates, and how many of
// its top lines the screen clipped.
func (a *App) boxRect(box string) (mouse.Rect, int) {
	r, clipped := overlayRect(box, a.contentRows())
	r.Y += tabStripRow // the title line sits above the body
	return r, clipped
}

// closeOutside runs the overlay's Esc action on a click outside its box and
// does nothing for any other gesture, so a click inside, the wheel and the
// right button are swallowed rather than passed to the view beneath.
func (a *App) closeOutside(msg tea.MouseMsg, box string, stack []keys.Ctx, action string) tea.Cmd {
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	if r, _ := a.boxRect(box); r.Contains(msg.X, msg.Y) {
		return nil
	}
	return a.pressAction(stack, action)
}

// wheelKeys turns a wheel delta into that many presses of the overlay's down
// or up, so the wheel moves a picker the way the arrow keys do.
func (a *App) wheelKeys(stack []keys.Ctx, d int) {
	act := "down"
	if d < 0 {
		act, d = "up", -d
	}
	for range d {
		a.pressAction(stack, act)
	}
}

// stepTo moves a picker's cursor from cur to target with its own down and up,
// so a click lands where the keys would, clamping and all.
func (a *App) stepTo(stack []keys.Ctx, cur, target int) {
	act, n := "down", target-cur
	if n < 0 {
		act, n = "up", -n
	}
	for range n {
		a.pressAction(stack, act)
	}
}

// optionArea is the rect of a picker's option rows. first is the box line the
// first option is drawn on (0 is the top border), clipped the lines the screen
// cut off the top of the box, and n the options drawn. skipped is how many of
// those options the clip hid, for the caller to add to its first-row offset.
func optionArea(r mouse.Rect, clipped, first, n int) (area mouse.Rect, skipped int) {
	y := first - clipped
	if y < 0 {
		skipped = -y
		n += y
		y = 0
	}
	return mouse.Rect{X: r.X + 1, Y: r.Y + y, W: max(r.W-2, 0), H: max(n, 0)}, skipped
}

func (a *App) statusFilterMouse(msg tea.MouseMsg, d int) tea.Cmd {
	p := a.statusFilter
	box := a.renderStatusFilter(a.width, a.contentRows())
	r, clipped := a.boxRect(box)
	if d != 0 {
		a.wheelKeys(stackStatus, d)
		return nil
	}
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	if !r.Contains(msg.X, msg.Y) {
		return a.pressAction(stackStatus, "cancel")
	}
	// Note line, blank line, then one row per status.
	area, skipped := optionArea(r, clipped, 3, len(prStatusOptions))
	hit := mouse.RowHit(area, skipped, len(prStatusOptions), msg)
	if !hit.HasItem {
		a.clicks = mouse.Clicks{}
		return nil
	}
	was := p.cursor == hit.Index
	a.stepTo(stackStatus, p.cursor, hit.Index)
	// Enter applies and closes the picker, so the row's own key — the toggle —
	// is what a second click runs.
	return a.secondClick("status:"+string(prStatusOptions[hit.Index]), was, stackStatus, "toggle")
}

func (a *App) labelsMouse(msg tea.MouseMsg, d int) tea.Cmd {
	p := a.labels
	box := a.renderLabelPicker(a.width, a.contentRows())
	r, clipped := a.boxRect(box)
	if d != 0 {
		if !p.loading && p.err == nil {
			a.wheelKeys(stackLabels, d)
		}
		return nil
	}
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	if !r.Contains(msg.X, msg.Y) {
		return a.pressAction(stackLabels, "cancel")
	}
	if p.loading || p.err != nil {
		return nil
	}
	vis := p.visible()
	rows := pickerRows(r.H + clipped)
	// Filter prompt, then the labels.
	area, skipped := optionArea(r, clipped, 2, rows)
	hit := mouse.RowHit(area, p.offset+skipped, len(vis), msg)
	if !hit.HasItem {
		a.clicks = mouse.Clicks{}
		return nil
	}
	was := p.cursor == hit.Index
	a.stepTo(stackLabels, p.cursor, hit.Index)
	// Enter submits the whole edit; toggling the row is what a second click on
	// it means.
	return a.secondClick("label:"+vis[hit.Index].Name, was, stackLabels, "toggle")
}

func (a *App) reposMouse(msg tea.MouseMsg, d int) tea.Cmd {
	p := a.repos
	box := a.renderRepoPicker(a.width, a.contentRows())
	r, clipped := a.boxRect(box)
	if d != 0 {
		a.wheelKeys(stackRepos, d)
		return nil
	}
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	if !r.Contains(msg.X, msg.Y) {
		return a.pressAction(stackRepos, "cancel")
	}
	vis := p.visible()
	rows := pickerRows(r.H + clipped)
	// Query prompt, then the repositories.
	area, skipped := optionArea(r, clipped, 2, rows)
	hit := mouse.RowHit(area, p.offset+skipped, len(vis), msg)
	if !hit.HasItem {
		a.clicks = mouse.Clicks{}
		return nil
	}
	was := p.cursor == hit.Index
	a.stepTo(stackRepos, p.cursor, hit.Index)
	return a.secondClick("repo:"+vis[hit.Index].Slug, was, stackRepos, "open")
}

// mergePromptStrategyLine is the box line the strategy choices are drawn on,
// counting the top border as line 0 (renderMergePrompt: question, warning,
// blank, choices).
const mergePromptStrategyLine = 4

func (a *App) mergePromptMouse(msg tea.MouseMsg) tea.Cmd {
	p := a.mergePrompt
	box := a.renderMergePrompt(a.width, a.contentRows())
	r, clipped := a.boxRect(box)
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	if !r.Contains(msg.X, msg.Y) {
		return a.pressAction(stackMerge, "no")
	}
	if msg.Y != r.Y+mergePromptStrategyLine-clipped {
		return nil
	}
	// The choices follow the "Strategy: " label inside the box's left border,
	// one cell apart. Picking one only sets the strategy, as s, m and b do;
	// merging stays on y.
	x := r.X + 1 + lipglossWidth(strategyLabel)
	for i, part := range strategyParts(p.strategy) {
		w := lipglossWidth(part)
		if msg.X >= x && msg.X < x+w {
			return a.pressAction(stackMerge, [...]string{"squash", "mergecommit", "rebase"}[i])
		}
		x += w + 1
	}
	return nil
}

func (a *App) paletteMouse(msg tea.MouseMsg, d int) tea.Cmd {
	p := a.palette
	box := p.render(a.width, a.contentRows())
	r, clipped := a.boxRect(box)
	if d != 0 {
		a.wheelKeys(stackPalette, d)
		return nil
	}
	if !mouse.IsLeftPress(msg) {
		return nil
	}
	if !r.Contains(msg.X, msg.Y) {
		return a.pressAction(stackPalette, "cancel")
	}
	ms := p.matches()
	n := min(len(ms), paletteMaxMatches)
	// The command line, then the best matches.
	area, skipped := optionArea(r, clipped, 2, n)
	hit := mouse.RowHit(area, skipped, n, msg)
	if !hit.HasItem {
		a.clicks = mouse.Clicks{}
		return nil
	}
	was := p.cursor == hit.Index
	a.stepTo(stackPalette, p.cursor, hit.Index)
	return a.secondClick("palette:"+ms[hit.Index].Action, was, stackPalette, "run")
}

func (a *App) helpMouse(msg tea.MouseMsg, d int) tea.Cmd {
	if d != 0 {
		a.wheelKeys(stackHelp, d)
		return nil
	}
	return a.closeOutside(msg, a.renderHelpOverlay(a.width, a.contentRows()), stackHelp, "close")
}

// secondClick records a click on the overlay row id and, when it is the second
// on a row that already held the cursor, runs the row's action.
func (a *App) secondClick(id string, wasSelected bool, stack []keys.Ctx, action string) tea.Cmd {
	var double bool
	a.clicks, double = a.clicks.Press(id, a.now())
	if double && wasSelected {
		return a.pressAction(stack, action)
	}
	return nil
}
