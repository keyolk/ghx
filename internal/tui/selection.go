package tui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// initListBase applies standard styling: hide title/status/filter/pagination/help,
// disable quit keybindings. Mirrors ccx selection.go.
func initListBase(l *list.Model) {
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()
}

// configureListSearch sets the filter prompt and arrow-only cursor.
func configureListSearch(l *list.Model) {
	l.FilterInput.Prompt = "Search: "
	l.KeyMap.AcceptWhileFiltering = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "apply"),
	)
	l.KeyMap.CursorUp = key.NewBinding(
		key.WithKeys("up"),
		key.WithHelp("↑", "up"),
	)
	l.KeyMap.CursorDown = key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "down"),
	)
}

// setListItemsPreservingFilter replaces items and re-runs any active filter.
// bubbles' SetItems clears filteredItems and returns the re-filter as a cmd
// callers drop — re-applying SetFilterText synchronously keeps the visible
// set correct and restores the cursor.
//
// identity, when non-nil, names each item so the cursor can be restored to the
// same row rather than the same index. A background poll re-sorts by updatedAt,
// so a PR that moved — or one that arrived above the cursor — silently changes
// what the index points at. Passing nil keeps the index-based behaviour.
func setListItemsPreservingFilter(l *list.Model, items []list.Item, identity func(list.Item) string) {
	state := l.FilterState()
	filter := l.FilterInput.Value()
	focused := currentItemKey(l, identity)

	if state == list.Unfiltered || filter == "" {
		l.SetItems(items)
		restoreCursor(l, focused, identity, l.Index())
		return
	}
	savedIndex := l.Index()
	l.SetItems(items)
	l.SetFilterText(filter)
	if state == list.Filtering {
		l.SetFilterState(list.Filtering)
	}
	restoreCursor(l, focused, identity, savedIndex)
}

// currentItemKey names the row under the cursor, or "" when there is none.
func currentItemKey(l *list.Model, identity func(list.Item) string) string {
	if identity == nil {
		return ""
	}
	it := l.SelectedItem()
	if it == nil {
		return ""
	}
	return identity(it)
}

// restoreCursor puts the cursor back on the row it was on. Falling back to the
// clamped index covers the row genuinely leaving the list — merged, filtered
// out — where staying near where the user was reading is the best available
// answer.
func restoreCursor(l *list.Model, focused string, identity func(list.Item) string, fallbackIndex int) {
	vis := l.VisibleItems()
	if len(vis) == 0 {
		return
	}
	if focused != "" && identity != nil {
		for i, it := range vis {
			if identity(it) == focused {
				l.Select(i)
				return
			}
		}
	}
	if fallbackIndex < 0 {
		fallbackIndex = 0
	}
	if fallbackIndex >= len(vis) {
		fallbackIndex = len(vis) - 1
	}
	l.Select(fallbackIndex)
}

// listFilterTerm returns the active filter term, or "" if not filtering.
func listFilterTerm(m list.Model) string {
	if m.FilterState() == list.Filtering || m.FilterState() == list.FilterApplied {
		return m.FilterValue()
	}
	return ""
}

// startListSearch activates the filter input. Placeholder — Phase 6 fills in.
func startListSearch(l *list.Model) tea.Cmd {
	if l.Width() == 0 {
		return nil
	}
	if v := l.FilterInput.Value(); v != "" {
		l.SetFilterText(v)
	}
	openMsg := tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'/'}})
	newL, cmd := l.Update(openMsg)
	*l = newL
	return cmd
}
