package tui

import "github.com/charmbracelet/lipgloss"

// Exported style values for sub-TUIs (admin, actions). The underlying styles
// are defined in styles.go as unexported vars; these are copies so the
// sub-packages do not need to redefine a color system.
//
// They are values, not pointers: SetNoColor mutates the unexported originals,
// so these must be read after SetNoColor has run (which it has, at startup,
// before any sub-TUI is constructed).

// TitleStyle is the top-bar title style.
var TitleStyle = titleStyle

// DimStyle is the secondary-text style.
var DimStyle = dimStyle

// ErrorStyle is the error-text style.
var ErrorStyle = errorStyle

// TabActiveStyle is the active-tab marker style.
var TabActiveStyle = tabActiveStyle

// TabDimStyle is the inactive-tab style.
var TabDimStyle = tabDimStyle

// SelectedRowStyle is the selected-row background style.
var SelectedRowStyle = selectedRowStyle

// DiffCursorStyle is the diff cursor style (slightly brighter than selection).
var DiffCursorStyle = diffCursorStyle

// DiffAddStyle, DiffDelStyle, DiffCtxStyle are diff line coloring.
var (
	DiffAddStyle = diffAddStyle
	DiffDelStyle = diffDelStyle
	DiffCtxStyle = diffCtxStyle
)

// CheckPassStyle, CheckFailStyle, CheckPendingStyle, CheckSkipStyle are
// check-status coloring.
var (
	CheckPassStyle    = checkPassStyle
	CheckFailStyle    = checkFailStyle
	CheckPendingStyle = checkPendingStyle
	CheckSkipStyle    = checkSkipStyle
)

// HelpKeyStyle and HelpStyle are the footer hint styles.
var (
	HelpKeyStyle = helpKeyStyle
	HelpStyle    = helpStyle
)

// FmtHints is exported for sub-TUIs to build footer hint lines.
func FmtHints(pairs ...string) string { return fmtHints(pairs...) }

// TruncateExact is exported for sub-TUIs.
func TruncateExact(s string, w int) (string, int) { return truncateExact(s, w) }

// TruncateFooter is exported for sub-TUIs.
func TruncateFooter(s string, w int) string { return truncateFooter(s, w) }

// LipglossWidth is exported for sub-TUIs that need cell-accurate width.
func LipglossWidth(s string) int { return lipgloss.Width(s) }

// --- shared list scaffolding for the subcommand TUIs ---

// FitRows is exported so the subcommand TUIs size their frame the way the PR
// app does. Without it an overflowing frame loses its top rows — the title and
// the tab strip — because bubbletea keeps the last `height` lines.
func FitRows(s string, h int) string { return fitRows(s, h) }

// FilterRows keeps the rows whose haystack matches every term in the query.
func FilterRows(query string, n int, haystack func(int) string) []int {
	return filterRows(query, n, haystack)
}

// RenderSearchBar draws the one-line query prompt the subcommands show while
// filtering.
func RenderSearchBar(query string, w int) string { return renderSearchBar(query, w) }

// RenderPromptBar draws a labelled one-line prompt in the same row the search
// bar uses, for the subcommand TUIs' own inputs (a login to add, a permission
// to grant). hints are key/description pairs, as FmtHints takes them.
func RenderPromptBar(label, value string, w int, hints ...string) string {
	return renderPromptBar(label, value, w, hints...)
}
