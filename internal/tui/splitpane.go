package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	ansi "github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/split"
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

	// Split is the list|preview boundary (split_list.go): its ratio, the
	// divider drag and the keys that move it.
	Split split.Model
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

// paneGeom is where Render draws the split, in cells relative to the pane's
// top-left corner. Render and the mouse hit test both read it, so a click can
// only land on what was drawn.
type paneGeom struct {
	split   bool // the preview is drawn beside the list
	list    mouse.Rect
	divider mouse.Rect // zero when the list is drawn alone
	preview mouse.Rect // zero when the list is drawn alone
	// contentH is the pane height, listH the list's share of it below any
	// header rows.
	contentH, listH int
}

// splitBody is the rect the list|preview split is laid out in, relative to
// the pane's top-left corner, or the zero Rect while the preview is hidden or
// the pane is too short for it (under 10 rows). The zero Rect makes the split
// draw nothing and take no press, so the mouse and the keys see the same
// geometry as Render.
func (sp *SplitPane) splitBody(totalW, totalH int) mouse.Rect {
	if !sp.Show || totalH < 10 || totalW <= 0 {
		return mouse.Rect{}
	}
	return mouse.Rect{W: totalW, H: ContentHeight(totalH)}
}

func (sp *SplitPane) geometry(totalW, totalH int) paneGeom {
	contentH := ContentHeight(totalH)
	listH := sp.listContentHeight(contentH)
	g := paneGeom{
		list:     mouse.Rect{X: 0, Y: sp.headerInset, W: totalW, H: listH},
		contentH: contentH, listH: listH,
	}
	body := sp.splitBody(totalW, totalH)
	if body.W == 0 {
		return g
	}
	a, div, b := sp.Split.Layout(body)
	if b.W <= 0 {
		// Collapsed (under 40 columns): the list alone, full width.
		return g
	}
	g.split = true
	g.list.W = a.W
	g.divider, g.preview = div, b
	return g
}

// Render draws the split layout. List-only when narrow (<40w or <10h).
func (sp *SplitPane) Render(totalW, totalH int) string {
	g := sp.geometry(totalW, totalH)
	if sp.List.Width() > 0 && (sp.List.Width() != g.list.W || sp.List.Height() != g.listH) {
		sp.List.SetSize(g.list.W, g.listH)
	}
	if !g.split {
		return sp.listView(g.contentH)
	}
	if sp.Preview.Width != g.preview.W || sp.Preview.Height != g.contentH {
		sp.Preview.Width = g.preview.W
		sp.Preview.Height = max(g.contentH, 1)
		sp.CacheKey = ""
	}
	border := paneBorderStyle
	if sp.Focus {
		border = paneBorderFocusedStyle
	}
	body := sp.splitBody(totalW, totalH)
	return sp.Split.Join(body, sp.listView(g.contentH), sp.Split.DrawDivider(g.divider, border), sp.Preview.View())
}

// Resize adjusts dimensions after terminal resize, preserving selection.
func (sp *SplitPane) Resize(totalW, totalH int) {
	if sp.List.Width() == 0 {
		return
	}
	idx := sp.List.Index()
	g := sp.geometry(totalW, totalH)
	sp.List.SetSize(g.list.W, g.listH)
	sp.List.Select(idx)
}

// SetPreviewContent sets the preview viewport content and sizes it to the
// current split dimensions. Resets scroll to the top.
func (sp *SplitPane) SetPreviewContent(content string, totalW, totalH int) {
	g := sp.geometry(totalW, totalH)
	sp.Preview = viewport.New(max(g.preview.W, 1), g.contentH)
	sp.Preview.SetContent(content)
}
