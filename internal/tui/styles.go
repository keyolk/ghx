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

	// Review-thread state glyphs. They lead the row rather than trailing it as a
	// tag, so they line up in a column the eye can scan, and they survive the
	// cursor: a selected row is themed as one band and any colour or strikethrough
	// inside it is overwritten, which left the row under the cursor — the one
	// being looked at — as the only row with no state signal at all.
	iconThreadOpen     = "○"
	iconThreadResolved = "✓"
	iconThreadUnknown  = "?"

	// Marks where a `\n` escape inside a string was drawn as a line break, so an
	// unfolded value still reads as one scalar rather than as lines the file
	// actually has.
	iconEscapedBreak = "↵"
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

	// Comment bodies. A review comment is markdown, and the blocks inside it —
	// a fenced plan, a table, an inline span — carry meaning the surrounding
	// prose does not. Code is left uncoloured but distinct from prose, so a
	// terraform plan's own +/-/~ colours are the only signal in the block.
	mdCodeStyle     = lipgloss.NewStyle().Foreground(colorSelectedFg)
	mdCodeSpanStyle = lipgloss.NewStyle().Foreground(colorSky)
	// terraform's in-place update (~) and replace (!) markers. Neither is an
	// addition or a deletion, and borrowing green or red for them would report
	// a change the plan is not making.
	diffChangeStyle = lipgloss.NewStyle().Foreground(colorAssistant)

	// Review threads
	threadStyle    = lipgloss.NewStyle().Foreground(colorCyan)
	threadResolved = lipgloss.NewStyle().Foreground(colorDim).Strikethrough(true)
	// The state glyph carries the meaning on its own, so it is coloured but never
	// struck through — a strikethrough glyph is harder to tell from its neighbours
	// at a glance, which is the one job it has.
	threadOpenGlyph     = lipgloss.NewStyle().Foreground(colorWarn)
	threadResolvedGlyph = lipgloss.NewStyle().Foreground(colorSuccess)
	threadUnknownGlyph  = lipgloss.NewStyle().Foreground(colorDim)

	// The in-flight action marker. Warn rather than success: the row is mid
	// action, and green would read as the action having finished.
	pendingMarkStyle = lipgloss.NewStyle().Foreground(colorWarn)

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
	strip(&mdCodeStyle)
	strip(&mdCodeSpanStyle)
	strip(&diffChangeStyle)
	strip(&threadStyle)
	strip(&threadResolved)
	strip(&threadOpenGlyph)
	strip(&threadResolvedGlyph)
	strip(&threadUnknownGlyph)
}

// fmtKey renders a "key:desc" token for the help line.
func fmtKey(key, desc string) string {
	return helpKeyStyle.Render(key) + helpStyle.Render(":"+desc)
}
