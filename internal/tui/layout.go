package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// layout.go holds the layout primitives shared across views: paneBox,
// decoratedPane, overlayBody, and the responsive floor helpers. Mirrors
// ccproxy's paneBox/decoratedPane/overlayBody — never hand-count cells.

// paneBox clamps a string to exact width x height, padding with spaces.
// Every renderer produces a plain string and hands sizing off to here.
func paneBox(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	// Truncate or pad each line to exactly w cells.
	for i := range lines {
		lines[i], _ = truncateExact(lines[i], w)
		if pad := w - lipgloss.Width(lines[i]); pad > 0 {
			lines[i] += strings.Repeat(" ", pad)
		}
	}
	// Pad or trim to exactly h rows.
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// decoratedPane draws a bordered box with a label, bright border when focused.
func decoratedPane(label, body string, w, h int, focused bool) string {
	border := paneBorderStyle
	if focused {
		border = paneBorderFocusedStyle
	}
	return borderedPane(label, body, w, h, border)
}

// borderedPane is decoratedPane with the border style chosen by the caller, for
// a box whose frame has to say something before its text is read (a failure).
//
// The frame is painted through lipgloss styles from styles.go, not hand-built
// escape sequences: raw truecolor codes ignore the terminal's colour profile
// and survive SetNoColor, so NO_COLOR still got coloured boxes.
func borderedPane(label, body string, w, h int, border lipgloss.Style) string {
	if w < 6 || h < 3 {
		return paneBox(body, w, h)
	}
	// Top border with label: ╭─ label ─╮
	labelStr := " " + label + " "
	labelW := lipgloss.Width(labelStr)
	innerW := w - 2 // two corner chars
	if labelW+2 > innerW {
		labelStr, _ = truncateExact(labelStr, innerW-2)
		labelW = lipgloss.Width(labelStr)
	}
	dashBefore := 1
	dashAfter := innerW - labelW - dashBefore
	if dashAfter < 0 {
		dashAfter = 0
	}
	col := border.Render
	top := col("╭"+strings.Repeat("─", dashBefore)) + paneLabelStyle.Render(labelStr) +
		col(strings.Repeat("─", dashAfter)+"╮")
	bottom := col("╰" + strings.Repeat("─", innerW) + "╯")
	side := col("│")
	// Body lines: wrap with side borders.
	bodyLines := strings.Split(body, "\n")
	for len(bodyLines) < h-2 {
		bodyLines = append(bodyLines, "")
	}
	bodyLines = bodyLines[:h-2]
	var out strings.Builder
	out.WriteString(top)
	out.WriteByte('\n')
	for _, bl := range bodyLines {
		bl, _ = truncateExact(bl, w-2)
		pad := (w - 2) - lipgloss.Width(bl)
		if pad < 0 {
			pad = 0
		}
		out.WriteString(side)
		out.WriteString(bl)
		out.WriteString(strings.Repeat(" ", pad))
		out.WriteString(side)
		out.WriteByte('\n')
	}
	out.WriteString(bottom)
	return out.String()
}

// overlayBody floats a modal over the base content, anchored to the bottom of
// the available height.
//
// The base is padded out to the full height first: a short base (a 3-row list,
// say) otherwise leaves no rows for the overlay to land on and the modal simply
// would not appear.
func overlayBody(base, overlay string, w, h int) string {
	if overlay == "" || h <= 0 {
		return base
	}
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// The overlay reserves the bottom rows; anything taller than the screen is
	// clipped from the top so its last line stays visible.
	if len(overlayLines) > h {
		overlayLines = overlayLines[len(overlayLines)-h:]
	}
	for len(baseLines) < h {
		baseLines = append(baseLines, "")
	}
	startY := len(baseLines) - len(overlayLines)

	for i, ov := range overlayLines {
		// Blank overlay rows stay transparent so the box's interior padding
		// doesn't erase content beside it.
		if strings.TrimSpace(ov) == "" {
			continue
		}
		baseLines[startY+i] = ov
	}
	return strings.Join(baseLines, "\n")
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// lipglossWidth is a thin alias so call sites read clearly.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

// tabStop is the column interval a terminal advances a tab to. Code is full of
// tabs, and a terminal expands each one to the next multiple of this — while
// width calculations count it as a single cell. Left as-is, a tabbed line
// occupies more columns than the layout budgeted and pushes everything after it
// out of alignment, which is what makes two-column diffs drift apart.
const tabStop = 8

// expandTabs replaces tabs with the spaces the terminal would have drawn, so a
// line's measured width matches what it actually occupies.
//
// startCol is the column the string begins at, needed because a tab's width
// depends on where it lands, not on how many precede it.
func expandTabs(s string, startCol int) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + tabStop)
	col := startCol
	for _, r := range s {
		if r == '\t' {
			n := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		// Measuring per rune keeps CJK and emoji correct: they advance two cells.
		col += lipgloss.Width(string(r))
	}
	return b.String()
}
