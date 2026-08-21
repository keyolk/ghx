package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// Row rendering for the PR list. Cells are padded as plain text and styled
// afterwards: styling first would make width math count ANSI escapes and
// misalign every column.

// prListDelegate renders one row per PR straight to the writer.
type prListDelegate struct {
	isSelected func(prSummary) bool
}

func (d prListDelegate) Height() int                         { return 1 }
func (d prListDelegate) Spacing() int                        { return 0 }
func (d prListDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d prListDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(prListItem)
	if !ok {
		return
	}
	p := it.pr
	width := m.Width()

	// Build the row from padded plain-text cells, then style each cell. Styling
	// first would make %-*s pad against ANSI escapes and misalign every column.
	const (
		numW    = 7 // "#11380" and beyond
		repoW   = 18
		authorW = 14
		ageW    = 5 // "12mo" plus a digit of headroom
		statusW = 9 // fixed "D M A C U" status cell
		// Past this a title is just whitespace; the leftover width is better
		// spent as a gap than as a title column stretched across the terminal.
		titleMaxW = 90
	)
	// The queue spans repositories, so the row has to say which one; without it
	// two same-numbered PRs are indistinguishable.
	// Keep fixed cells for the multi-select mark and status flags so columns stay
	// aligned as selections and PR states change.
	fixed := 2 + numW + 1 + statusW + 1 + repoW + 1 + authorW + 1 + ageW
	titleW := clamp(width-fixed, 10, titleMaxW)

	mark := " "
	if d.isSelected != nil && d.isSelected(p) {
		mark = iconCheck
	}
	numCell := padCell("#"+itoa(p.Number), numW)
	repoCell := padCell(fitCell(shortRepo(p.Repo), repoW), repoW)
	titleCell := padCell(fitCell(p.Title, titleW), titleW)
	authorCell := padCell(fitCell(p.Author.Login, authorW), authorW)
	ageCell := leftPadCell(relTime(p.UpdatedAt), ageW)

	// The selected row is themed as a whole rather than cell by cell. Wrapping a
	// background around already-styled cells stops at the first reset sequence
	// inside them, which is what made the selection nearly invisible: only the
	// gaps between cells picked up the highlight.
	if index == m.Index() {
		plain := mark + " " + numCell + " " + prStatusMark(p) + " " +
			repoCell + " " + titleCell + " " + authorCell + " " + ageCell
		plain, _ = truncateExact(plain, width)
		if pad := width - lipglossWidth(plain); pad > 0 {
			plain += strings.Repeat(" ", pad)
		}
		fmt.Fprint(w, selectedRowStyle.Render(plain))
		return
	}

	titleStyle := prTitleStyle
	if p.IsDraft {
		titleStyle = prDraftStyle
	}

	markCell := "  "
	if mark != " " {
		markCell = checkPassStyle.Render(mark) + " "
	}
	row := markCell + prNumberStyle.Render(numCell) + " " +
		prStatusCell(p) + " " +
		dimStyle.Render(repoCell) + " " +
		titleStyle.Render(titleCell) + " " +
		prAuthorStyle.Render(authorCell) + " " +
		dimStyle.Render(ageCell)

	row, _ = truncateExact(row, width)
	if pad := width - lipglossWidth(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	fmt.Fprint(w, row)
}

// prStatusMark is the uncoloured fixed-width state cell used inside the cursor
// row, where one theme is applied over the entire line.
func prStatusMark(p prSummary) string {
	flags := []struct {
		status prStatus
		mark   string
	}{
		{statusDraft, "D"},
		{statusMerged, "M"},
		{statusApproved, "A"},
		{statusChangesRequested, "C"},
		{statusUnresolved, "U"},
	}
	parts := make([]string, 0, len(flags))
	for _, flag := range flags {
		if prHasStatus(p, flag.status) {
			parts = append(parts, flag.mark)
		} else {
			parts = append(parts, "·")
		}
	}
	return strings.Join(parts, " ")
}

// prStatusCell styles each state independently while preserving the same nine
// display cells as prStatusMark: Draft, Merged, Approved, Changes requested,
// Unresolved.
func prStatusCell(p prSummary) string {
	cell := []string{
		dimStyle.Render("·"),
		dimStyle.Render("·"),
		dimStyle.Render("·"),
		dimStyle.Render("·"),
		dimStyle.Render("·"),
	}
	if prHasStatus(p, statusDraft) {
		cell[0] = prDraftStyle.Render("D")
	}
	if prHasStatus(p, statusMerged) {
		cell[1] = prMergedStyle.Render("M")
	}
	if prHasStatus(p, statusApproved) {
		cell[2] = prApprovedStyle.Render("A")
	}
	if prHasStatus(p, statusChangesRequested) {
		cell[3] = prChangesStyle.Render("C")
	}
	if prHasStatus(p, statusUnresolved) {
		cell[4] = prUnresolvedStyle.Render("U")
	}
	return strings.Join(cell, " ")
}

// shortRepo drops the owner prefix: within one org the owner repeats on every
// row and only costs width. A name without a slash is returned as-is.
func shortRepo(slug string) string {
	if _, name, ok := strings.Cut(slug, "/"); ok && name != "" {
		return name
	}
	return slug
}

// fitCell truncates to width cells so a long value cannot push later columns
// out of alignment (%-*s pads but never trims).
func fitCell(s string, width int) string {
	if lipglossWidth(s) <= width {
		return s
	}
	out, _ := truncateExact(s, width)
	return out
}

// padCell right-pads to exactly width display cells. Padding is measured in
// cells, not bytes, so CJK titles line up like ASCII ones.
func padCell(s string, width int) string {
	if pad := width - lipglossWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// leftPadCell right-aligns within width cells.
func leftPadCell(s string, width int) string {
	if pad := width - lipglossWidth(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// reviewDot encodes review state as a single glyph so the row stays scannable.
func reviewDot(decision string) string {
	switch decision {
	case "APPROVED":
		return prApprovedStyle.Render(iconCheck)
	case "CHANGES_REQUESTED":
		return prChangesStyle.Render(iconFail)
	case "REVIEW_REQUIRED", "":
		return prRequiredStyle.Render(iconPending)
	}
	return dimStyle.Render("·")
}

// relTime renders a compact "how long ago" for the updated column.
func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
}
