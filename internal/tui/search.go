package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Search filters the PR list by substring across number, title, author, and
// branch. Substring beats fuzzy here: PR titles carry ticket prefixes people
// type exactly ("CPLAT-10365"), and fuzzy matching would bury those under
// incidental matches.

// search holds the query modal's state.
type search struct {
	active bool
	query  string
}

func (s *search) open(initial string) {
	s.active = true
	s.query = initial
}

func (s *search) close() {
	s.active = false
}

// clear closes the modal and drops the query, so the caller knows to unfilter.
func (s *search) clear() {
	s.active = false
	s.query = ""
}

// update handles search keys. handled=false means the key wasn't consumed.
func (s *search) update(action string, msg tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	if !s.active {
		return nil, false
	}
	switch action {
	case "clear":
		// Esc abandons the edit and the filter, restoring the full list.
		q := s.query
		s.clear()
		if q == "" {
			return nil, true
		}
		return func() tea.Msg { return searchSubmitMsg{query: ""} }, true
	case "apply":
		s.close()
		q := s.query
		return func() tea.Msg { return searchSubmitMsg{query: q} }, true
	case "erase":
		if r := []rune(s.query); len(r) > 0 {
			s.query = string(r[:len(r)-1])
		}
		return s.liveCmd(), true
	}
	switch msg.Type {
	case tea.KeyRunes:
		s.query += string(msg.Runes)
		return s.liveCmd(), true
	case tea.KeySpace:
		s.query += " "
		return s.liveCmd(), true
	}
	return nil, true
}

// liveCmd applies the filter as the user types so results narrow immediately.
func (s *search) liveCmd() tea.Cmd {
	q := s.query
	return func() tea.Msg { return searchSubmitMsg{query: q} }
}

// render draws the search prompt as a one-line overlay.
func (s *search) render(width, height int, hint string) string {
	if !s.active {
		return ""
	}
	prompt := helpKeyStyle.Render("Search: ") + s.query + blockCursor()
	return decoratedPane("filter", prompt+"\n"+hint, width, 4, true)
}

// matches reports whether a PR satisfies the query. Empty query matches all.
func (s *search) matches(p prSummary) bool {
	return matchesQuery(p, s.query)
}

func matchesQuery(p prSummary, query string) bool {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return true
	}
	// Every whitespace-separated term must appear somewhere, so adding a term
	// narrows rather than widens the result set.
	haystack := strings.ToLower(strings.Join([]string{
		"#" + itoa(p.Number),
		p.Title,
		p.Author.Login,
		p.Author.Name,
		p.HeadRefName,
		p.State,
		p.ReviewDecision,
	}, " "))
	for _, term := range strings.Fields(q) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func itoa(n int) string { return intToStr(n) }
