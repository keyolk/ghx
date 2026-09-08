package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Rendering a comment body — a review reply, a bot report, a PR description —
// as terminal lines.
//
// The shape this exists for is the one ghx was worst at. An Atlantis or
// Codelight report is a terraform plan inside a ```diff fence inside a
// <details> block, and the old path word-wrapped the whole thing as prose: the
// fence and the HTML were printed verbatim, and the plan's leading +/-/~
// markers — the only part carrying meaning — were reflowed into the middle of
// wrapped lines. A plan is the comment a reviewer has to read most closely and
// it was the one rendered least readably.
//
// This is deliberately not a full CommonMark implementation. It recognizes the
// block kinds that appear in review comments and leaves everything else as
// prose, because a wrong guess here silently rewrites what someone said.

// renderCommentBody lays out a comment body to width cells. The caller adds its
// own indent; width is what is left after it.
func renderCommentBody(body string, width int) []string {
	r := &mdRenderer{width: max(width, 8)}
	r.run(strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n"))
	if len(r.out) == 0 {
		return nil
	}
	return r.out
}

type mdRenderer struct {
	width int
	out   []string
}

func (r *mdRenderer) push(lines ...string) { r.out = append(r.out, lines...) }

func (r *mdRenderer) run(lines []string) {
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if fence, info, ok := codeFenceOpen(line); ok {
			body, end := codeFenceBody(lines, i+1, fence)
			r.code(body, info)
			i = end
			continue
		}

		// <details>/<summary> is a fold in the web UI. Here the comment is
		// already expanded — the reader asked for it — so the summary becomes a
		// section header and the contents follow, rather than being hidden
		// behind a second fold the tab has no cursor to open.
		if head, consumed, ok := detailsHeader(lines, i); ok {
			if head != "" {
				r.push(threadStyle.Render(iconFoldOpen + " " + head))
			}
			i = consumed
			continue
		}
		if stripped, changed := stripDetailsTags(line); changed {
			if strings.TrimSpace(stripped) == "" {
				continue
			}
			line = stripped
		}

		r.block(line)
	}
}

// block renders one non-fenced line.
func (r *mdRenderer) block(line string) {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		r.push("")
		return
	case hrRe.MatchString(trimmed):
		r.push(dimStyle.Render(strings.Repeat("─", min(r.width, 40))))
		return
	}

	// A table keeps its columns only if it is not reflowed, so it is chunked
	// like code rather than wrapped like prose.
	if strings.HasPrefix(trimmed, "|") {
		for _, seg := range chunkCells(inlinePlain(trimmed), r.width) {
			r.push(dimStyle.Render(seg))
		}
		return
	}

	if m := headingRe.FindStringSubmatch(trimmed); m != nil {
		for _, seg := range wrapText(inlinePlain(m[2]), r.width) {
			r.push(titleStyle.Render(seg))
		}
		return
	}

	if strings.HasPrefix(trimmed, ">") {
		quoted := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		for _, seg := range wrapText(inlinePlain(quoted), max(r.width-2, 8)) {
			r.push(dimStyle.Render("│ " + seg))
		}
		return
	}

	// A list item hangs its continuation under the text rather than under the
	// marker, so a wrapped bullet still reads as one item.
	if m := listRe.FindStringSubmatch(line); m != nil {
		marker := m[1] + m[2] + " "
		hang := strings.Repeat(" ", lipglossWidth(marker))
		segs := wrapText(inlinePlain(m[3]), max(r.width-lipglossWidth(marker), 8))
		for i, seg := range segs {
			if i == 0 {
				r.push(styleInline(marker + seg))
				continue
			}
			r.push(hang + styleInline(seg))
		}
		return
	}

	for _, seg := range wrapText(inlinePlain(line), r.width) {
		r.push(styleInline(seg))
	}
}

// code renders a fenced block verbatim, coloured by what the fence holds.
func (r *mdRenderer) code(lines []string, info string) {
	lang, _, _ := strings.Cut(strings.TrimSpace(info), " ")
	lang = strings.ToLower(lang)
	diffish := diffLangs[lang] || (lang == "" && looksLikeDiff(lines))

	for _, l := range lines {
		l = expandTabs(l, 0)
		style := mdCodeStyle
		if diffish {
			style = diffMarkerStyle(l)
		}
		for _, seg := range chunkCells(l, r.width) {
			if seg == "" {
				r.push("")
				continue
			}
			r.push(style.Render(seg))
		}
	}
}

// diffLangs are the fence languages whose leading character is a change marker.
// `terraform`/`hcl` are here because Atlantis tags its plan output that way as
// often as it tags it `diff`, and the body is the same +/-/~ plan either way.
var diffLangs = map[string]bool{
	"diff": true, "patch": true, "suggestion": true,
	"terraform": true, "tf": true, "hcl": true, "plan": true,
}

// diffMarkerStyle colours one line of a diff or a terraform plan by its leading
// marker. The marker may be indented — a plan nests its resource blocks — so the
// prefix is looked for after leading whitespace, not at column zero.
func diffMarkerStyle(line string) lipgloss.Style {
	t := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(t, "@@"):
		return diffHunkStyle
	case strings.HasPrefix(t, "+++"), strings.HasPrefix(t, "---"):
		// A file header, not an addition or a deletion of three lines.
		return diffFileStyle
	case strings.HasPrefix(t, "+"):
		return diffAddStyle
	case strings.HasPrefix(t, "-"):
		return diffDelStyle
	case strings.HasPrefix(t, "~"), strings.HasPrefix(t, "!"):
		// terraform's in-place update and replace markers. Neither is an add or
		// a delete, and colouring them as one misreports what the plan will do.
		return diffChangeStyle
	case strings.HasPrefix(t, "#"):
		// In a plan this names the resource the following block belongs to,
		// which is the line that makes the rest of the block readable.
		return diffHunkStyle
	}
	return mdCodeStyle
}

// looksLikeDiff decides whether an untagged fence is a diff. Bots often omit
// the language on plan output, and prose fenced as a code block would be
// misleadingly coloured if the test were loose — so it wants several marker
// lines and a real share of the block, not one bullet that starts with "-".
func looksLikeDiff(lines []string) bool {
	markers, content := 0, 0
	for _, l := range lines {
		t := strings.TrimLeft(l, " \t")
		if t == "" {
			continue
		}
		content++
		if len(t) > 1 && strings.ContainsRune("+-~!", rune(t[0])) && (t[1] == ' ' || t[1] == t[0]) {
			markers++
		}
	}
	return markers >= 2 && content > 0 && markers*4 >= content
}

// codeFenceOpen recognizes any opening fence and returns the run of characters
// that must close it, per the CommonMark rule that a closing fence is at least
// as long as its opener.
func codeFenceOpen(line string) (fence, info string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	marker := ""
	switch {
	case strings.HasPrefix(trimmed, "```"):
		marker = "`"
	case strings.HasPrefix(trimmed, "~~~"):
		marker = "~"
	default:
		return "", "", false
	}
	n := 0
	for n < len(trimmed) && string(trimmed[n]) == marker {
		n++
	}
	info = strings.TrimSpace(trimmed[n:])
	// A backtick fence's info string cannot contain a backtick; a line like
	// ```` `x` ```` is an inline span, not a fence.
	if marker == "`" && strings.Contains(info, "`") {
		return "", "", false
	}
	return strings.Repeat(marker, n), info, true
}

// codeFenceBody collects a fence's contents and returns the index to resume at.
//
// An unterminated fence is read to the end of the body rather than abandoned.
// That is what GitHub renders, and unlike applying a suggestion — where the
// same input is refused because guessing writes to someone's branch — showing
// the rest as code costs nothing if the guess is wrong.
func codeFenceBody(lines []string, from int, fence string) (body []string, end int) {
	for i := from; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if strings.HasPrefix(trimmed, fence) &&
			strings.TrimSpace(strings.TrimLeft(trimmed, string(fence[0]))) == "" {
			return lines[from:i], i
		}
	}
	return lines[from:], len(lines) - 1
}

// detailsHeader pulls the <summary> text out of a <details> block, reporting
// the last line it consumed. The tags are usually written on one line, but a
// summary spanning lines is legal and dropping half of it would leave the
// section unlabelled.
func detailsHeader(lines []string, i int) (head string, end int, ok bool) {
	line := lines[i]
	if !summaryOpenRe.MatchString(line) {
		return "", i, false
	}
	if m := summaryRe.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(inlinePlain(m[1])), i, true
	}
	// Opened here, closed later.
	collected := []string{summaryOpenRe.ReplaceAllString(line, "")}
	for j := i + 1; j < len(lines); j++ {
		if idx := strings.Index(strings.ToLower(lines[j]), "</summary>"); idx >= 0 {
			collected = append(collected, lines[j][:idx])
			return strings.TrimSpace(inlinePlain(strings.Join(collected, " "))), j, true
		}
		collected = append(collected, lines[j])
	}
	return strings.TrimSpace(inlinePlain(strings.Join(collected, " "))), len(lines) - 1, true
}

// stripDetailsTags removes the <details> wrapper itself, which carries no text.
func stripDetailsTags(line string) (string, bool) {
	out := detailsTagRe.ReplaceAllString(line, "")
	return out, out != line
}

// inlinePlain flattens inline markup to text: images out, links down to their
// label, emphasis markers gone, HTML dropped. Backticks are kept — they are one
// cell each, they measure correctly, and they are the only inline-code signal
// left once colour is stripped for NO_COLOR.
func inlinePlain(s string) string {
	s = markdownImageRe.ReplaceAllString(s, "")
	s = markdownLinkRe.ReplaceAllString(s, "$1")
	s = brRe.ReplaceAllString(s, " ")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("**", "", "__", "", "~~", "").Replace(s)
	return strings.TrimRight(s, " \t")
}

// styleInline colours the code spans within an already-wrapped line. A span
// broken across the wrap is left as literal backticks rather than styled from
// its opening half, which would run the colour to the end of the line.
func styleInline(s string) string {
	if !strings.Contains(s, "`") {
		return s
	}
	return codeSpanRe.ReplaceAllStringFunc(s, func(m string) string {
		return mdCodeSpanStyle.Render(m)
	})
}

// chunkCells hard-breaks a line that must not be reflowed — code, a table — at
// the display width, indenting every segment after the first so a wrapped line
// is not read as a line the source actually has.
func chunkCells(s string, width int) []string {
	if width <= 4 || lipglossWidth(s) <= width {
		return []string{s}
	}
	const cont = "  "
	var out []string
	var cur strings.Builder
	limit, w := width, 0
	flush := func() {
		seg := cur.String()
		if len(out) > 0 {
			seg = cont + seg
		}
		out = append(out, seg)
		cur.Reset()
		w = 0
		limit = width - len(cont)
	}
	for _, r := range s {
		rw := lipglossWidth(string(r))
		if w+rw > limit && cur.Len() > 0 {
			flush()
		}
		cur.WriteRune(r)
		w += rw
	}
	if cur.Len() > 0 {
		flush()
	}
	return out
}

var (
	headingRe     = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	hrRe          = regexp.MustCompile(`^(?:-{3,}|\*{3,}|_{3,})$`)
	listRe        = regexp.MustCompile(`^(\s*)([-*+]|\d+\.)\s+(.*)$`)
	codeSpanRe    = regexp.MustCompile("`[^`]+`")
	brRe          = regexp.MustCompile(`(?i)<br\s*/?>`)
	detailsTagRe  = regexp.MustCompile(`(?i)</?details[^>]*>`)
	summaryRe     = regexp.MustCompile(`(?i)<summary[^>]*>(.*?)</summary>`)
	summaryOpenRe = regexp.MustCompile(`(?i)<summary[^>]*>`)

	markdownImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	htmlTagRe       = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	whitespaceRe    = regexp.MustCompile(`\s+`)
)

// commentPreview flattens a comment body into one scannable line. Review bots
// lead with markdown badges and HTML, which would otherwise fill the preview
// with `<sub>` and shields.io URLs instead of the actual finding.
//
// Fenced blocks are summarized rather than flattened. A collapsed Atlantis row
// used to read "Show Output diff Terraform used the selected providers to
// generate the following exec…" — the fence marker and the plan's boilerplate
// preamble, ending before the first line that says what the plan does. A plan
// is unreadable at one line wide either way; naming it and leaving the prose
// is what makes the row worth scanning.
func commentPreview(body string) string {
	prose, blocks := previewParts(body)
	s := strings.Join(prose, " ")
	// Drop markdown images (badges) and collapse links to their text.
	s = markdownImageRe.ReplaceAllString(s, "")
	s = markdownLinkRe.ReplaceAllString(s, "$1")
	s = htmlTagRe.ReplaceAllString(s, "")
	// Strip emphasis and heading markers that add noise without structure here.
	s = strings.NewReplacer("**", "", "`", "", "#", "").Replace(s)
	s = strings.TrimSpace(whitespaceRe.ReplaceAllString(s, " "))
	// The tag leads rather than trails. A collapsed row is truncated at the
	// pane width, and what it drops is whatever sits at the end — which is
	// exactly where a trailing tag is, so the one part of the row that is not
	// guessable from the prose would be the one part never shown. It is the
	// same reason the thread state is a leading glyph rather than a tag.
	if blocks != "" {
		if s == "" {
			return blocks
		}
		return blocks + " " + s
	}
	return s
}

// previewParts splits a body into its prose lines and a tag naming the fenced
// blocks it carries, so a preview can mention a block without quoting it.
func previewParts(body string) (prose []string, blocks string) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	kinds := map[string]int{}
	var order []string
	for i := 0; i < len(lines); i++ {
		fence, info, ok := codeFenceOpen(lines[i])
		if !ok {
			prose = append(prose, lines[i])
			continue
		}
		body, end := codeFenceBody(lines, i+1, fence)
		i = end
		kind, _, _ := strings.Cut(strings.TrimSpace(info), " ")
		kind = strings.ToLower(kind)
		if kind == "" {
			kind = "code"
			if looksLikeDiff(body) {
				kind = "diff"
			}
		}
		if kinds[kind] == 0 {
			order = append(order, kind)
		}
		kinds[kind]++
	}
	var parts []string
	for _, k := range order {
		if n := kinds[k]; n > 1 {
			parts = append(parts, fmt.Sprintf("%d %s blocks", n, k))
			continue
		}
		parts = append(parts, k+" block")
	}
	if len(parts) > 0 {
		blocks = "[" + strings.Join(parts, ", ") + "]"
	}
	return prose, blocks
}
