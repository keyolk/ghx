package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
)

// The `e` picker: type part of a repository's name, press enter, and its PRs
// open as a tab. Ranked by use, so the repos you actually work in are the first
// three rows and typing is optional.

type repoPicker struct {
	all    []repoCandidate
	query  string
	cursor int
	offset int
}

// openRepoPicker builds the picker from the visit history plus every repository
// visible in the loaded queues.
func (a *App) openRepoPicker() tea.Cmd {
	if a.list == nil {
		return nil
	}
	a.repos = &repoPicker{all: a.repoStore.rankRepos(a.list.seenRepos(), a.now())}
	return nil
}

// visible is the candidate list narrowed by the query. Matching is the same
// substring-of-all-terms rule the other filters use, over the slug, so "ops k8"
// finds sendbird/ops-k8s.
func (p *repoPicker) visible() []repoCandidate {
	idx := filterRows(p.query, len(p.all), func(i int) string { return p.all[i].Slug })
	out := make([]repoCandidate, 0, len(idx))
	for _, i := range idx {
		out = append(out, p.all[i])
	}
	return out
}

// typed is the query read as a repository slug, for the case the picker cannot
// answer from its list: a repo with no PRs in any queue and no history is not a
// row, and refusing to open it would make the picker useless for exactly the
// repository someone had to type out in full.
func (p *repoPicker) typed() (string, bool) {
	q := strings.TrimSpace(p.query)
	if !validRepoSlug(q) {
		return "", false
	}
	for _, c := range p.all {
		if strings.EqualFold(c.Slug, q) {
			// Already a row; enter takes that path and keeps its spelling.
			return "", false
		}
	}
	return q, true
}

func (a *App) handleRepoPickerKey(msg tea.KeyMsg) tea.Cmd {
	p := a.repos
	if p == nil {
		return nil
	}
	switch msg.String() {
	case "esc":
		a.repos = nil
		return nil
	case "enter":
		vis := p.visible()
		if p.cursor < len(vis) {
			slug := vis[p.cursor].Slug
			a.repos = nil
			return a.openRepo(slug)
		}
		// Nothing matched, but the query names a repository outright. Opening it
		// is the only way to reach a repo ghx has never seen.
		if slug, ok := p.typed(); ok {
			a.repos = nil
			return a.openRepo(slug)
		}
		return nil
	case "up", "ctrl+p":
		if n := len(p.visible()); n > 0 {
			p.cursor = clamp(p.cursor-1, 0, n-1)
		}
		return nil
	case "down", "ctrl+n":
		if n := len(p.visible()); n > 0 {
			p.cursor = clamp(p.cursor+1, 0, n-1)
		}
		return nil
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.cursor = 0
		}
		return nil
	}
	// Everything else types. Unlike the label picker there is no j/k navigation
	// to reserve: a slug can contain any letter, and taking two of them for
	// movement would make "sendbird/kite" untypeable one keystroke in. Arrows
	// and ^n/^p move instead.
	if msg.Type == tea.KeyRunes {
		p.query += string(msg.Runes)
		p.cursor = 0
	}
	if msg.Type == tea.KeySpace {
		// A space separates filter terms, so "ops k8s" narrows the way the other
		// filters do. It cannot be part of a slug, so nothing is lost.
		p.query += " "
		p.cursor = 0
	}
	return nil
}

// openRepo focuses the tab for slug, adding one if the repo has none.
//
// The tab is added rather than replacing the current one: someone who opens a
// repo to check one PR still has the queue they came from, in the position they
// left it. That the tab persists for the session is the point — the second
// visit is a number key.
func (a *App) openRepo(slug string) tea.Cmd {
	if a.list == nil || !validRepoSlug(slug) {
		return errCmd(fmt.Errorf("not a repository: %q", slug))
	}
	a.repoStore.record(slug, a.now())
	if i, ok := a.list.sourceForRepo(slug); ok {
		return a.list.selectTab(i)
	}
	a.list.appendSource(config.SourceDef{
		Name:  config.ShortRepoName(slug),
		Query: "state:open",
		Repo:  slug,
	}, nil)
	return a.list.selectTab(len(a.list.sources) - 1)
}

func (a *App) renderRepoPicker(width, height int) string {
	p := a.repos
	if p == nil {
		return ""
	}
	boxW := min(width-4, 64)
	boxH := min(max(height/2, 9), 18)

	var b strings.Builder
	b.WriteString(helpKeyStyle.Render("repo: ") + p.query + blockCursor() + "\n")

	vis := p.visible()
	rows := max(boxH-5, 1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	p.offset = clamp(p.offset, 0, max(len(vis)-rows, 0))

	if len(vis) == 0 {
		if slug, ok := p.typed(); ok {
			b.WriteString(dimStyle.Render("open "+slug+" — not seen before") + "\n")
		} else {
			b.WriteString(dimStyle.Render("no repository matches; type owner/name") + "\n")
		}
	}
	end := min(p.offset+rows, len(vis))
	for i := p.offset; i < end; i++ {
		c := vis[i]
		// The two numbers say different things and a row needs both: visits is
		// why this row is near the top, open PRs is whether it is worth opening
		// right now. Collapsing them into one figure would hide which is which.
		var meta []string
		if c.Visits > 0 {
			meta = append(meta, fmt.Sprintf("%d visits", c.Visits))
		}
		if c.OpenPRs > 0 {
			meta = append(meta, fmt.Sprintf("%d open", c.OpenPRs))
		}
		line := c.Slug
		if len(meta) > 0 {
			line += "  " + strings.Join(meta, " · ")
		}
		if i == p.cursor {
			// The selection band overwrites every style inside the row, so the
			// meta text is padded into the band rather than dimmed — dimming
			// would simply vanish under it, on the one row being read.
			b.WriteString(selectedRowStyle.Render(padCell(line, boxW-2)) + "\n")
			continue
		}
		if len(meta) > 0 {
			line = c.Slug + "  " + dimStyle.Render(strings.Join(meta, " · "))
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n" + fmtHints("↑↓", "move", "enter", "open", "esc", "cancel"))
	return decoratedPane("open repository", b.String(), boxW, boxH, true)
}

// seenRepos counts the loaded rows per repository, across every source — not
// just the visible tab. A repo is worth offering because its PRs are in some
// queue, and which tab that queue happens to be is not something the person
// typing knows or cares about.
func (m *prListModel) seenRepos() map[string]int {
	out := make(map[string]int)
	for _, rows := range m.caches {
		for _, row := range rows {
			if row.Repo != "" {
				out[row.Repo]++
			}
		}
	}
	return out
}

// sourceForRepo finds the tab already scoped to slug.
func (m *prListModel) sourceForRepo(slug string) (int, bool) {
	for i, s := range m.sources {
		if strings.EqualFold(s.Repo, slug) {
			return i, true
		}
	}
	return 0, false
}
