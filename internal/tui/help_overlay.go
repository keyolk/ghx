package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/keys"
)

// The `?` overlay is the discoverability backstop: the footer only has room for
// the handful of keys you need right now, so everything else lives here. It is
// generated from the binding table, so it cannot list a key that does
// something else or miss one that was added.

// helpSections documents the view the overlay was opened over first, then
// every other view, so the keys of a tab that is not open are still findable.
// Each action is listed once, with every key that reaches it.
func (a *App) helpSections() []keys.Section {
	stacks := make([][]keys.Ctx, 0, len(viewStacks)+1)
	if a.helpStack != nil {
		stacks = append(stacks, a.helpStack)
	}
	stacks = append(stacks, viewStacks...)
	return a.keymap().Help(keys.Opts{State: a.helpOpts.State}, stacks...)
}

func helpOverlayStyle() keys.HelpStyle {
	return keys.HelpStyle{
		Title: titleStyle, Key: helpKeyStyle, Desc: helpStyle, Muted: dimStyle,
		Names: keyNames,
	}
}

// helpBox is the overlay's inner size for a body of width x height.
func helpBox(width, height int) (w, h int) {
	return max(width-4, 10), max(height-2, 3)
}

// clampHelp is the offset RenderHelp would draw at, stored back in Update so
// holding j does not grow the offset past the last page.
func (a *App) clampHelp(off int) int {
	w, h := helpBox(a.width, a.contentRows())
	_, off = keys.RenderHelp(a.helpSections(), w, h, off, keys.HelpStyle{Names: keyNames})
	return off
}

// renderHelpOverlay draws the scrollable help box over the body.
func (a *App) renderHelpOverlay(width, height int) string {
	w, h := helpBox(width, height)
	lines, _ := keys.RenderHelp(a.helpSections(), w, h, a.helpOff, helpOverlayStyle())
	body := strings.Join(lines, "\n")
	return decoratedPane("ghx help", body, width, min(lipgloss.Height(body)+2, height), true)
}
