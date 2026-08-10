package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Color tokens — named semantic vars, never raw hex in render paths. ghx adds
// a NO_COLOR gate (SetNoColor) that ccx lacks: when stdout is non-tty or
// NO_COLOR is set, all composed styles drop color/bold.

const (
	iconFoldClosed = "▸"
	iconFoldOpen   = "▾"
	iconPR         = "⎇"
	iconCheck      = "✓"
	iconFail       = "✗"
	iconPending    = "●"
	iconSkip       = "⊘"
	iconComment    = "💬"
	iconResolved   = "✓"
	iconDraft      = "○"
)

var (
	colorPrimary = lipgloss.Color("#7C3AED")
	colorTitleBg = lipgloss.Color("#1E293B")
	// Selection needs to read at a glance against a black terminal. #1E293B is
	// only a few shades off pure black, so a row wearing it looked unselected;
	// these are bright enough to spot without shouting over the content.
	colorSelectedBg    = lipgloss.Color("#2D4B73")
	colorSelectedText  = lipgloss.Color("#F8FAFC")
	colorCursorBg      = lipgloss.Color("#3E6DA8")
	colorDim           = lipgloss.Color("#6B7280")
	colorAccent        = lipgloss.Color("#10B981")
	colorUser          = lipgloss.Color("#3B82F6")
	colorAssistant     = lipgloss.Color("#F59E0B")
	colorError         = lipgloss.Color("#EF4444")
	colorSuccess       = lipgloss.Color("#22C55E")
	colorWarn          = lipgloss.Color("#FBBF24")
	colorPurple        = lipgloss.Color("#A78BFA")
	colorCyan          = lipgloss.Color("#22D3EE")
	colorSky           = lipgloss.Color("#7DD3FC")
	colorBorderFocused = lipgloss.Color("#38BDF8")
	colorBorderDim     = lipgloss.Color("#374151")
	colorHelp          = lipgloss.Color("#9CA3AF")
	colorMatchPink     = lipgloss.Color("#F9A8D4")
	colorAddition      = lipgloss.Color("#4ADE80")
	colorDeletion      = lipgloss.Color("#F87171")
	colorSelectedFg    = lipgloss.Color("#D1D5DB")
)

var (
	titleStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(colorDim)
	errorStyle = lipgloss.NewStyle().Foreground(colorError)
	// The selected row is themed whole — background plus a bright foreground —
	// so it reads as one band instead of a slightly darker gap between cells.
	selectedRowStyle = lipgloss.NewStyle().
				Background(colorSelectedBg).
				Foreground(colorSelectedText).
				Bold(true)
	helpStyle      = lipgloss.NewStyle().Foreground(colorHelp)
	helpKeyStyle   = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	matchHighlight = lipgloss.NewStyle().Foreground(colorMatchPink).Bold(true)

	// PR list row styles
	prNumberStyle     = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	prTitleStyle      = lipgloss.NewStyle().Foreground(colorSelectedFg)
	prAuthorStyle     = lipgloss.NewStyle().Foreground(colorUser)
	prDraftStyle      = lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	prMergedStyle     = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	prApprovedStyle   = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	prChangesStyle    = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	prRequiredStyle   = lipgloss.NewStyle().Foreground(colorAssistant).Bold(true)
	prUnresolvedStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)

	// Detail tab strip
	tabActiveStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	tabDimStyle    = lipgloss.NewStyle().Foreground(colorDim)
	tabCountStyle  = lipgloss.NewStyle().Foreground(colorHelp)

	// Diff
	diffAddStyle  = lipgloss.NewStyle().Foreground(colorAddition)
	diffDelStyle  = lipgloss.NewStyle().Foreground(colorDeletion)
	diffCtxStyle  = lipgloss.NewStyle().Foreground(colorDim)
	diffHunkStyle = lipgloss.NewStyle().Foreground(colorCyan)
	diffFileStyle = lipgloss.NewStyle().Foreground(colorSky).Bold(true)
	// The diff cursor sits inside a range that may also be highlighted, so it is
	// a step brighter than the selection to stay distinguishable from it.
	diffCursorStyle = lipgloss.NewStyle().
			Background(colorCursorBg).
			Foreground(colorSelectedText).
			Bold(true)

	// Checks
	checkPassStyle    = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	checkFailStyle    = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	checkPendingStyle = lipgloss.NewStyle().Foreground(colorAssistant).Bold(true)
	checkSkipStyle    = lipgloss.NewStyle().Foreground(colorDim)

	// Review threads
	threadStyle    = lipgloss.NewStyle().Foreground(colorCyan)
	threadResolved = lipgloss.NewStyle().Foreground(colorDim).Strikethrough(true)

	// Spinner
	spinnerColors = []lipgloss.Color{colorSuccess, colorUser, colorAssistant, colorPrimary, colorPurple}
)

// SetNoColor strips color/bold from all composed styles when the terminal
// cannot render color (NO_COLOR env or non-tty stdout). Call once at startup.
// SetNoColor strips colour from every composed style, for NO_COLOR or a non-tty
// stdout. Call once at startup.
//
// Selection is the exception: reverse video carries no colour, and without it
// there is no way to tell which row the keys will act on. lipgloss suppresses
// even reverse under the Ascii profile, so the profile is pinned to ANSI —
// enough for the attribute, still free of colour.
func SetNoColor() {
	lipgloss.SetColorProfile(termenv.ANSI)

	strip := func(s *lipgloss.Style) {
		*s = s.UnsetForeground().UnsetBackground().UnsetBold().UnsetItalic().UnsetStrikethrough()
	}
	invert := func(s *lipgloss.Style) {
		*s = lipgloss.NewStyle().Reverse(true)
	}
	invert(&selectedRowStyle)
	invert(&diffCursorStyle)

	strip(&titleStyle)
	strip(&dimStyle)
	strip(&errorStyle)
	strip(&helpStyle)
	strip(&helpKeyStyle)
	strip(&matchHighlight)
	strip(&prNumberStyle)
	strip(&prTitleStyle)
	strip(&prAuthorStyle)
	strip(&prDraftStyle)
	strip(&prMergedStyle)
	strip(&prApprovedStyle)
	strip(&prChangesStyle)
	strip(&prRequiredStyle)
	strip(&prUnresolvedStyle)
	strip(&tabActiveStyle)
	strip(&tabDimStyle)
	strip(&tabCountStyle)
	strip(&diffAddStyle)
	strip(&diffDelStyle)
	strip(&diffCtxStyle)
	strip(&diffHunkStyle)
	strip(&diffFileStyle)
	// diffCursorStyle is set to reverse video above; stripping it here would undo
	// that and leave the cursor invisible without colour.
	strip(&checkPassStyle)
	strip(&checkFailStyle)
	strip(&checkPendingStyle)
	strip(&checkSkipStyle)
	strip(&threadStyle)
	strip(&threadResolved)
}

// fmtKey renders a "key:desc" token for the help line.
func fmtKey(key, desc string) string {
	return helpKeyStyle.Render(key) + helpStyle.Render(":"+desc)
}
