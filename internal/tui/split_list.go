package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/split"

	"github.com/keyolk/ghx/internal/config"
)

// The PR list | preview boundary is a tuikit split.Model. Before it, ghx kept
// a percentage in cfg.DiffSplitRatio, moved it 5 at a time with [ and ],
// clamped it to 25-75 and saved it as ui.json split_ratio. The numbers below
// keep that look and range; what the model adds is a divider drag, a reset,
// and a clamp that also respects the 30-column list minimum at the current
// width, so a key press always moves the divider.

// newListSplit builds the list|preview split. diff_split_ratio (despite its
// name, the list's share of the PR list screen) stays the initial ratio and
// what `=` and a double click on the divider reset to.
func newListSplit(cfg *config.Config) split.Model {
	var def float64
	if cfg != nil && cfg.DiffSplitRatio > 0 {
		def = float64(cfg.DiffSplitRatio) / 100
	}
	return split.Model{
		Divider:  1,
		MinA:     30, // the list was never drawn narrower
		MinB:     1,
		Collapse: 40, // under 40 columns the list is drawn alone
		Lo:       0.25,
		Hi:       0.75,
		Step:     0.05,
		Default:  def,
	}
}

// splitBindings are the split's keys in the list context. `[ ]` were ghx's
// resize keys before `< > =` existed and stay as aliases, so muscle memory
// keeps working. The pair sits low in the footer: resizing is occasional.
func splitBindings() []keys.Binding {
	bs := split.Bindings(ctxList)
	for i := range bs {
		bs[i].Group = grpLayout
		switch bs[i].Action {
		case split.ActionShrink:
			bs[i].Keys = append(bs[i].Keys, "[")
			bs[i].Help = "shrink the list pane"
			bs[i].Footer, bs[i].FooterGroup = 40, "resize"
		case split.ActionGrow:
			bs[i].Keys = append(bs[i].Keys, "]")
			bs[i].Help = "grow the list pane"
			bs[i].Footer, bs[i].FooterGroup = 40, "resize"
		case split.ActionReset:
			bs[i].Help = "reset the list|preview split"
		}
	}
	return bs
}

// handleSplit runs a split key against the rect the split is drawn in, so the
// clamp knows the minimums at this width.
func (m *prListModel) handleSplit(action string) {
	var r split.Result
	m.pane.Split, r = m.pane.Split.Handle(action, m.pane.splitBody(m.width, m.height-1))
	if r.Changed {
		m.pane.Resize(m.width, m.height-1)
	}
}

// splitMouse gives a mouse event to the split before the text selection and
// the list's clicks, as the kit's contract asks: a press on the divider starts
// a drag and is the split's alone, so it never starts a selection or lands on
// a row. consumed means nothing else may see the event.
func (a *App) splitMouse(msg tea.MouseMsg) (cmd tea.Cmd, consumed bool) {
	m := a.list
	if a.state != viewPRList || m == nil {
		return nil, false
	}
	// Only a left press or a drag in progress can be the split's; checking that
	// first keeps every other event from rendering the overlay boxes below.
	if !m.pane.Split.Dragging() && !mouse.IsLeftPress(msg) {
		return nil, false
	}
	// An open overlay owns the mouse, and an empty list draws a note instead
	// of the split.
	if a.overlayBox() != "" || len(m.list.VisibleItems()) == 0 {
		return nil, false
	}
	area := offsetRect(m.pane.splitBody(a.width, a.height-1), 0, bodyTop)
	var r split.Result
	m.pane.Split, r = m.pane.Split.UpdateAt(msg, area, a.now())
	if r.Changed {
		m.pane.Resize(a.width, a.height-1)
	}
	if !r.Consumed {
		return nil, false
	}
	if mouse.IsLeftPress(msg) {
		// A divider press is a click somewhere else than the last row: the
		// next row click must not complete a double click.
		a.clicks = mouse.Clicks{}
		a.toast.Ack()
	}
	return a.noteActivity(), true
}
