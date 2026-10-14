package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"
)

// SplitPane manages a list + viewport split layout with focus toggling.
// Simplified from ccx: no FoldState (ghx doesn't need block folds).
type SplitPane struct {
	List    *list.Model
	Preview viewport.Model

	Show     bool // preview pane visible
	Focus    bool // true = preview focused, false = list focused
	CacheKey string

	ItemHeight int

	Header       string
	HeaderHeight int

	headerInset int
}

// ListWidth returns the list width given total width and split ratio.
func (sp *SplitPane) ListWidth(totalW, splitRatio int) int {
	if !sp.Show {
		return totalW
	}
	return max(totalW*splitRatio/100, 30)
}

// PreviewWidth returns the preview width (totalW - listW - 1 for border).
func (sp *SplitPane) PreviewWidth(totalW, splitRatio int) int {
	return max(totalW-sp.ListWidth(totalW, splitRatio)-1, 1)
}

// ContentHeight returns the usable content height (totalH - 3 for title+help).
func ContentHeight(totalH int) int {
	return max(totalH-3, 1)
}

// truncateExact truncates s to at most targetW display cells, CJK-aware.
func truncateExact(s string, targetW int) (string, int) {
	if targetW <= 0 {
		return "", 0
	}
	s = ansi.TruncateWc(s, targetW, "")
	w := ansi.StringWidthWc(s)
	for w > targetW && targetW > 0 {
		targetW--
		s = ansi.TruncateWc(s, targetW, "")
		w = ansi.StringWidthWc(s)
	}
	return s, w
}

// renderFixedSplit composes left|border|right with manual ANSI layout, CJK-aware.
//
// The cursor moves (CHA) stay raw: they are layout, not colour. The divider
// is styled through lipgloss so it honours NO_COLOR and the colour profile.
func renderFixedSplit(left, right string, listW, previewW, contentH int, border lipgloss.Style) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	borderCell := border.Render("│")
	const reset = "\x1b[0m"
	borderCHA := fmt.Sprintf("\x1b[%dG", listW+1)
	rightCHA := fmt.Sprintf("\x1b[%dG", listW+2)

	var out strings.Builder
	for i := 0; i < contentH; i++ {
		var l, r string
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		l, lw := truncateExact(l, listW)
		out.WriteString(l)
		out.WriteString(reset)
		if pad := listW - lw; pad > 0 {
			out.WriteString(strings.Repeat(" ", pad))
		}
		out.WriteString(borderCHA)
		out.WriteString(borderCell)
		out.WriteString(rightCHA)
		r, rw := truncateExact(r, previewW)
		out.WriteString(r)
		out.WriteString(reset)
		if pad := previewW - rw; pad > 0 {
			out.WriteString(strings.Repeat(" ", pad))
		}
		if i < contentH-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (sp *SplitPane) listHeaderInset(contentH int) int {
	if sp.Header == "" || sp.HeaderHeight <= 0 {
		sp.headerInset = 0
		return 0
	}
	sp.headerInset = min(sp.HeaderHeight, max(contentH-1, 0))
	return sp.headerInset
}

func (sp *SplitPane) listContentHeight(contentH int) int {
	return max(contentH-sp.listHeaderInset(contentH), 1)
}

func (sp *SplitPane) listView(contentH int) string {
	inset := sp.listHeaderInset(contentH)
	if inset == 0 {
		return sp.List.View()
	}
	headerLines := strings.Split(sp.Header, "\n")
	if len(headerLines) > inset {
		headerLines = headerLines[:inset]
	}
	for len(headerLines) < inset {
		headerLines = append(headerLines, "")
	}
	return strings.Join(headerLines, "\n") + "\n" + sp.List.View()
}

// Render draws the split layout. List-only when narrow (<40w or <10h).
func (sp *SplitPane) Render(totalW, totalH, splitRatio int) string {
	contentH := ContentHeight(totalH)
	if !sp.Show || totalW < 40 || totalH < 10 {
		listW := sp.ListWidth(totalW, splitRatio)
		listH := sp.listContentHeight(contentH)
		if sp.List.Width() > 0 && (sp.List.Width() != listW || sp.List.Height() != listH) {
			sp.List.SetSize(listW, listH)
		}
		return sp.listView(contentH)
	}
	listW := sp.ListWidth(totalW, splitRatio)
	previewW := sp.PreviewWidth(totalW, splitRatio)
	listH := sp.listContentHeight(contentH)
	if sp.List.Width() > 0 && (sp.List.Width() != listW || sp.List.Height() != listH) {
		sp.List.SetSize(listW, listH)
	}
	if sp.Preview.Width != previewW || sp.Preview.Height != contentH {
		sp.Preview.Width = previewW
		sp.Preview.Height = max(contentH, 1)
		sp.CacheKey = ""
	}
	border := paneBorderStyle
	if sp.Focus {
		border = paneBorderFocusedStyle
	}
	left := sp.listView(contentH)
	right := sp.Preview.View()
	return renderFixedSplit(left, right, listW, previewW, contentH, border)
}

// Resize adjusts dimensions after terminal resize, preserving selection.
func (sp *SplitPane) Resize(totalW, totalH, splitRatio int) {
	if sp.List.Width() == 0 {
		return
	}
	idx := sp.List.Index()
	contentH := ContentHeight(totalH)
	sp.List.SetSize(sp.ListWidth(totalW, splitRatio), sp.listContentHeight(contentH))
	sp.List.Select(idx)
}

// SetPreviewContent sets the preview viewport content and sizes it to the
// current split dimensions. Resets scroll to the top.
func (sp *SplitPane) SetPreviewContent(content string, totalW, totalH, splitRatio int) {
	previewW := sp.PreviewWidth(totalW, splitRatio)
	contentH := ContentHeight(totalH)
	sp.Preview = viewport.New(previewW, contentH)
	sp.Preview.SetContent(content)
}
