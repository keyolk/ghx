package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// prListModel is the PR list: source tabs across the top, the list on the left,
// a summary preview on the right.
type prListModel struct {
	cfg    *config.Config
	client *gh.Client
	km     *Keymap

	sources []config.SourceDef
	curTab  int

	// detectedRepos are the repositories the launch directory and the current
	// tmux window belong to. Kept so the tab strip can mark which tabs came from
	// where the user is, rather than looking like arbitrary leading entries.
	detectedRepos map[string]bool

	list *list.Model
	pane *SplitPane

	// per-source cache so switching tabs is instant after the first load
	caches      [][]pr.Summary
	loadings    []bool
	generations []uint64
	// errs keeps the last failure per source so an empty pane can say why it is
	// empty instead of implying the source has no PRs.
	errs     []error
	inFlight bool

	// query and statusFilters narrow the current source client-side. Statuses
	// combine with OR, then intersect with the text query.
	query         string
	statusFilters map[prStatus]bool

	// selected is keyed by owner/repo#number so cross-repo queues cannot collide.
	// The summary value lets bulk actions retain their targets across tabs and
	// filters; syncListItems refreshes it whenever newer row data arrives.
	selected map[string]pr.Summary

	// fileCache persists each source's PR list to ~/.config/ghx/cache/ so a
	// restart shows the previous results immediately while a fresh fetch runs.
	fileCache *prFileCache

	missStreak int
	stale      bool

	spinner int

	width  int
	height int
}

// prListItem wraps a PR for bubbles/list.
type prListItem struct{ pr pr.Summary }

func (i prListItem) FilterValue() string {
	return fmt.Sprintf("#%d %s %s", i.pr.Number, i.pr.Title, i.pr.Author.Login)
}

func newPRListModel(cfg *config.Config, client *gh.Client, km *Keymap) *prListModel {
	return newPRListModelWithRepo(cfg, client, km, nil)
}

// newPRListModelWithRepo builds the list with detectedRepos leading the tabs, so
// launching ghx inside a checkout — or beside one in the same tmux window —
// opens on those repositories' PRs.
func newPRListModelWithRepo(cfg *config.Config, client *gh.Client, km *Keymap, detectedRepos []string) *prListModel {
	sources := cfg.EffectiveSources(detectedRepos...)
	if len(sources) == 0 {
		sources = []config.SourceDef{
			{Name: "My reviews", Query: "review-requested:@me state:open"},
		}
	}
	detected := make(map[string]bool, len(detectedRepos))
	for _, repo := range detectedRepos {
		if repo != "" {
			detected[strings.ToLower(repo)] = true
		}
	}
	m := &prListModel{
		cfg:           cfg,
		client:        client,
		km:            km,
		sources:       sources,
		detectedRepos: detected,
		caches:        make([][]pr.Summary, len(sources)),
		loadings:      make([]bool, len(sources)),
		generations:   make([]uint64, len(sources)),
		errs:          make([]error, len(sources)),
		selected:      make(map[string]pr.Summary),
		statusFilters: make(map[prStatus]bool),
		fileCache:     newPRFileCache(),
	}
	// Seed each tab from its on-disk cache so the first frame shows the previous
	// session's results while the fetches run. The data may be stale; the
	// background reload overwrites it the moment a fresh response arrives.
	for i, s := range sources {
		if cached := m.fileCache.load(s, 0); cached != nil {
			m.caches[i] = cached
		}
	}
	l := list.New(nil, prListDelegate{isSelected: m.isSelected}, 80, 20)
	initListBase(&l)
	configureListSearch(&l)
	m.list = &l
	m.pane = &SplitPane{List: m.list, ItemHeight: 1}
	// Push the seeded rows (if any) into the list so the first frame shows them
	// rather than a blank pane waiting for the fetch.
	m.syncListItems()
	return m
}

func (m *prListModel) init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.sources))
	for i := range m.sources {
		m.loadings[i] = true
		cmds = append(cmds, m.fetchSource(i))
	}
	return tea.Batch(cmds...)
}

// fetchSource loads one source off the UI goroutine.
//
// A source pinned to one repo goes through `gh pr list`, which is REST and has
// its own generous rate limit. Only an unpinned source needs `gh search prs`,
// which spans repositories but spends the much scarcer GraphQL search budget.
func (m *prListModel) fetchSource(i int) tea.Cmd {
	if i < 0 || i >= len(m.sources) {
		return nil
	}
	src := m.sources[i]
	client := m.client
	m.generations[i]++
	generation := m.generations[i]
	if src.Repo != "" {
		scoped := client.WithRepo(src.Repo)
		query, repo := src.Query, src.Repo
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			prs, err := scoped.ListPRs(c, query, 50)
			// ListPRs answers about one repo, so it never echoes the slug back;
			// fill it in or every later call would have nothing to scope to.
			for j := range prs {
				if prs[j].Repo == "" {
					prs[j].Repo = repo
				}
			}
			var warning error
			if err == nil {
				prs, warning = scoped.EnrichPRStatuses(c, prs)
			}
			return prListMsg{sourceIdx: i, generation: generation, prs: prs, warning: warning, err: err}
		}
	}
	query := src.Query
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		prs, warning, err := searchPRsAcrossAccounts(c, client, m.cfg.Accounts, query, 50)
		if err == nil {
			var statusWarning error
			prs, statusWarning = client.EnrichPRStatuses(c, prs)
			warning = errors.Join(warning, statusWarning)
		}
		return prListMsg{sourceIdx: i, generation: generation, prs: prs, warning: warning, err: err}
	}
}

// refreshCurrent reloads the visible source.
func (m *prListModel) refreshCurrent() tea.Cmd {
	m.loadings[m.curTab] = true
	return m.fetchSource(m.curTab)
}

// invalidateCachesAndRefresh drops every source after a bulk action because one
// PR can appear in several queues. Only the visible source reloads immediately;
// the others reload on their next visit.
func (m *prListModel) invalidateCachesAndRefresh() tea.Cmd {
	for i := range m.sources {
		m.generations[i]++
		m.caches[i] = nil
		m.loadings[i] = false
		m.errs[i] = nil
	}
	m.syncListItems()
	return m.refreshCurrent()
}

func (m *prListModel) loading() bool {
	for _, l := range m.loadings {
		if l {
			return true
		}
	}
	return false
}

func (m *prListModel) resize(w, h int) {
	m.width, m.height = w, h
	// One row goes to the tab strip.
	m.pane.Resize(w, h-1, m.cfg.DiffSplitRatio)
}

func (m *prListModel) setSpinnerFrame(f int) { m.spinner = f }

func (m *prListModel) handlePRListMsg(msg prListMsg) tea.Cmd {
	if msg.sourceIdx < 0 || msg.sourceIdx >= len(m.sources) {
		return nil
	}
	// A bulk action invalidates every source. Responses started before that point
	// must not restore stale rows after the changed PRs have been refreshed.
	if msg.generation != m.generations[msg.sourceIdx] {
		return nil
	}
	m.loadings[msg.sourceIdx] = false
	m.inFlight = false
	if msg.err != nil {
		m.missStreak++
		// Only claim staleness after repeated failures — one blip isn't news.
		if m.missStreak >= 5 {
			m.stale = true
		}
		m.errs[msg.sourceIdx] = msg.err
		// Surface the first failure: an empty list that is really an error must
		// not read as "no PRs here".
		if m.missStreak == 1 {
			return errCmd(fmt.Errorf("%s: %w", m.sources[msg.sourceIdx].Name, msg.err))
		}
		return nil
	}
	m.missStreak = 0
	m.stale = false
	m.errs[msg.sourceIdx] = nil
	m.caches[msg.sourceIdx] = msg.prs
	// Persist so the next session opens on these rows instead of re-fetching.
	if m.fileCache != nil {
		m.fileCache.save(m.sources[msg.sourceIdx], msg.prs)
	}
	if msg.sourceIdx == m.curTab {
		m.syncListItems()
	}
	if msg.warning != nil {
		return func() tea.Msg {
			return toastMsg{text: "PR list warning: " + msg.warning.Error()}
		}
	}
	return nil
}

// handlePollTick refreshes the current source, skipping if one is in flight.
func (m *prListModel) handlePollTick() tea.Cmd {
	if m.inFlight {
		return nil
	}
	m.inFlight = true
	return m.fetchSource(m.curTab)
}

// applyQuery sets the client-side filter and rebuilds the visible rows.
func (m *prListModel) applyQuery(q string) {
	m.query = q
	m.syncListItems()
}

// selectSourceByName switches tabs by (case-insensitive, prefix) name.
func (m *prListModel) selectSourceByName(name string) bool {
	if name == "" {
		return false
	}
	want := strings.ToLower(name)
	for i, s := range m.sources {
		ls := strings.ToLower(s.Name)
		if ls == want || strings.HasPrefix(ls, want) {
			m.selectTab(i)
			return true
		}
	}
	return false
}

func (m *prListModel) selectTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.sources) || i == m.curTab {
		return nil
	}
	m.curTab = i
	m.syncListItems()
	// Load on first visit; cached sources render immediately.
	if m.caches[i] == nil && !m.loadings[i] {
		m.loadings[i] = true
		return m.fetchSource(i)
	}
	return nil
}

// selectionKey is stable across sources and unique in cross-repo queues.
func selectionKey(p pr.Summary) string {
	return fmt.Sprintf("%s#%d", strings.ToLower(p.Repo), p.Number)
}

func (m *prListModel) isSelected(p pr.Summary) bool {
	_, ok := m.selected[selectionKey(p)]
	return ok
}

func (m *prListModel) toggleSelected() bool {
	item, ok := m.selectedItem()
	if !ok {
		return false
	}
	key := selectionKey(item.pr)
	if _, exists := m.selected[key]; exists {
		delete(m.selected, key)
		return false
	}
	m.selected[key] = item.pr
	return true
}

func (m *prListModel) toggleAllVisible() {
	items := m.list.VisibleItems()
	allSelected := len(items) > 0
	for _, item := range items {
		row, ok := item.(prListItem)
		if ok && !m.isSelected(row.pr) {
			allSelected = false
			break
		}
	}
	for _, item := range items {
		row, ok := item.(prListItem)
		if !ok {
			continue
		}
		key := selectionKey(row.pr)
		if allSelected {
			delete(m.selected, key)
		} else {
			m.selected[key] = row.pr
		}
	}
}

func (m *prListModel) clearSelected() { clear(m.selected) }

func (m *prListModel) selectedSummaries() []pr.Summary {
	out := make([]pr.Summary, 0, len(m.selected))
	for _, summary := range m.selected {
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Number < out[j].Number
	})
	return out
}

// syncListItems pushes the current source's rows (filtered) into the list.
func (m *prListModel) syncListItems() {
	src := m.caches[m.curTab]
	items := make([]list.Item, 0, len(src))
	for _, p := range src {
		if _, ok := m.selected[selectionKey(p)]; ok {
			m.selected[selectionKey(p)] = p
		}
		if !matchesQuery(p, m.query) || !matchesStatusFilters(p, m.statusFilters) {
			continue
		}
		items = append(items, prListItem{pr: p})
	}
	setListItemsPreservingFilter(m.list, items)
}

func (m *prListModel) update(msg tea.KeyMsg) tea.Cmd {
	key, isNav := translateNav(msg)
	if isNav {
		switch key {
		case "up", "down", "pgup", "pgdown":
			// Let the list move the cursor; it handles bounds and paging.
			newL, cmd := m.list.Update(navKeyMsg(key))
			*m.list = newL
			return cmd
		case "home":
			m.list.Select(0)
			return nil
		case "end":
			if n := len(m.list.VisibleItems()); n > 0 {
				m.list.Select(n - 1)
			}
			return nil
		case "right":
			m.pane.Show = true
			m.pane.Focus = true
			m.pane.Resize(m.width, m.height-1, m.cfg.DiffSplitRatio)
			return nil
		case "left":
			if m.pane.Focus {
				m.pane.Focus = false
			} else {
				m.pane.Show = false
			}
			m.pane.Resize(m.width, m.height-1, m.cfg.DiffSplitRatio)
			return nil
		}
		return nil
	}

	switch key {
	case "enter":
		return func() tea.Msg { return openDetailMsg{} }
	case " ":
		m.toggleSelected()
		return nil
	case "A":
		m.toggleAllVisible()
		return nil
	case "R":
		return m.refreshCurrent()
	case "tab":
		return m.selectTab((m.curTab + 1) % len(m.sources))
	case "shift+tab":
		return m.selectTab((m.curTab - 1 + len(m.sources)) % len(m.sources))
	case "[":
		m.adjustRatio(-5)
		return nil
	case "]":
		m.adjustRatio(5)
		return nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.selectTab(int(key[0] - '1'))
	case "esc":
		// Clear one narrowing layer at a time before dropping multi-selection.
		if m.query != "" {
			m.applyQuery("")
		} else if len(m.statusFilters) > 0 {
			clear(m.statusFilters)
			m.syncListItems()
		} else if len(m.selected) > 0 {
			m.clearSelected()
		}
		return nil
	}
	return nil
}

// navKeyMsg rebuilds a canonical key message for the list to consume.
func navKeyMsg(key string) tea.KeyMsg {
	switch key {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	}
	return tea.KeyMsg{}
}

func (m *prListModel) adjustRatio(delta int) {
	r := m.cfg.DiffSplitRatio + delta
	// Keep both panes usable; a 10% pane shows nothing worth reading.
	m.cfg.DiffSplitRatio = clamp(r, 25, 75)
	m.pane.Resize(m.width, m.height-1, m.cfg.DiffSplitRatio)
}

func (m *prListModel) selectedItem() (prListItem, bool) {
	if m.list == nil {
		return prListItem{}, false
	}
	it, ok := m.list.SelectedItem().(prListItem)
	return it, ok
}

func (m *prListModel) title() string {
	if m.curTab >= len(m.sources) {
		return ""
	}
	s := m.sources[m.curTab].Name
	n := len(m.list.VisibleItems())
	total := len(m.caches[m.curTab])
	selected := ""
	if count := len(m.selected); count > 0 {
		selected = fmt.Sprintf(" · %d selected", count)
	}
	filtered := m.query != "" || len(m.statusFilters) > 0
	if filtered {
		parts := make([]string, 0, 2)
		if m.query != "" {
			parts = append(parts, fmt.Sprintf("text %q", m.query))
		}
		if labels := activeStatusLabels(m.statusFilters); len(labels) > 0 {
			parts = append(parts, "status "+strings.Join(labels, " | "))
		}
		return fmt.Sprintf("%s · %d/%d · %s%s", s, n, total, strings.Join(parts, " · "), selected)
	}
	return fmt.Sprintf("%s · %d%s", s, n, selected)
}

func (m *prListModel) view(w, h int) string {
	tabs := m.renderTabs(w)
	if m.pane.Show {
		m.renderPreview()
	}
	// An empty pane must distinguish loading, a failed query, an over-narrow
	// filter, and a genuinely empty source — they call for different actions.
	if len(m.list.VisibleItems()) == 0 {
		var body string
		switch {
		case m.loadings[m.curTab]:
			body = renderSpinner(m.spinner, "Loading "+m.sources[m.curTab].Name+"…")
		case m.errs[m.curTab] != nil:
			body = errorStyle.Render("Could not load this source:") + "\n  " +
				dimStyle.Render(m.errs[m.curTab].Error()) + "\n\n" +
				dimStyle.Render("R retries.")
		case m.query != "" || len(m.statusFilters) > 0:
			var filters []string
			if m.query != "" {
				filters = append(filters, fmt.Sprintf("text %q", m.query))
			}
			if labels := activeStatusLabels(m.statusFilters); len(labels) > 0 {
				filters = append(filters, "status "+strings.Join(labels, " | "))
			}
			body = dimStyle.Render("No PRs match " + strings.Join(filters, " and ") + " — esc clears one filter.")
		default:
			body = dimStyle.Render("No PRs in this source.")
		}
		return joinVertical(tabs, body)
	}
	return joinVertical(tabs, m.pane.Render(w, h-1, m.cfg.DiffSplitRatio))
}

// renderTabs draws the source strip, keeping every tab's index visible even
// when the terminal narrows. Rather than truncate the joined strip from the
// right — which drops the highest-numbered tabs entirely — each tab shrinks
// toward its index, so the 1-9 jump keys always have something to land on.
func (m *prListModel) renderTabs(w int) string {
	tabs := m.tabLabels()
	if len(tabs) == 0 {
		return ""
	}

	widths := make([]int, len(tabs))
	total := 0
	for i, t := range tabs {
		widths[i] = lipglossWidth(t)
		total += widths[i]
	}

	stale := ""
	if m.stale {
		stale = checkFailStyle.Render(" stale")
		total += lipglossWidth(stale)
	}

	if total <= w {
		var b strings.Builder
		for _, t := range tabs {
			b.WriteString(t)
		}
		b.WriteString(stale)
		return b.String()
	}

	// Drop per-tab decorations (counts, spinner) first; the index stays.
	for i := range tabs {
		if widths[i] <= 4 {
			continue
		}
		tabs[i] = m.renderTab(i, m.sources[i], false)
		widths[i] = lipglossWidth(tabs[i])
	}
	total = sumWidths(widths) + lipglossWidth(stale)
	if total <= w {
		var b strings.Builder
		for _, t := range tabs {
			b.WriteString(t)
		}
		b.WriteString(stale)
		return b.String()
	}

	// Still too wide: cap each tab at an equal share. This is what keeps tabs
	// 6-8 visible instead of being truncated off the right edge. A truncated
	// inactive tab keeps its trailing space so it doesn't run into the next one.
	per := w / len(tabs)
	if per < 3 {
		per = 3
	}
	var b strings.Builder
	for i, t := range tabs {
		if widths[i] <= per {
			b.WriteString(t)
			continue
		}
		trunc, _ := truncateExact(t, per)
		// Re-attach the inactive padding the truncator may have consumed, so
		// adjacent tabs stay separated at the cost of a column of content.
		if i != m.curTab && per > 3 && lipglossWidth(trunc) < per {
			trunc += " "
		}
		b.WriteString(trunc)
	}
	b.WriteString(stale)
	return b.String()
}

func sumWidths(ws []int) int {
	n := 0
	for _, w := range ws {
		n += w
	}
	return n
}

// tabLabels builds the fully styled label for every tab, with count and
// detection markers.
func (m *prListModel) tabLabels() []string {
	tabs := make([]string, len(m.sources))
	for i, s := range m.sources {
		tabs[i] = m.renderTab(i, s, true)
	}
	return tabs
}

// renderTab produces one tab's styled string. withCount controls the (N) and
// reload-spinner decorations; the index and detection marker are always shown.
func (m *prListModel) renderTab(i int, s config.SourceDef, withCount bool) string {
	label := fmt.Sprintf("%d %s", i+1, s.Name)
	if m.detectedRepos[strings.ToLower(s.Repo)] {
		label += tabCountStyle.Render("*")
	}
	if withCount {
		if n := len(m.caches[i]); n > 0 {
			label += tabCountStyle.Render(fmt.Sprintf("(%d)", n))
		}
		if m.loadings[i] && len(m.caches[i]) > 0 {
			label += tabCountStyle.Render(spinnerFrames[m.spinner%len(spinnerFrames)])
		}
	}
	if i == m.curTab {
		return tabActiveStyle.Render("[" + label + "]")
	}
	return tabDimStyle.Render(" " + label + " ")
}

// renderPreview fills the right pane with the selected PR's summary.
func (m *prListModel) renderPreview() {
	it, ok := m.selectedItem()
	if !ok {
		m.pane.SetPreviewContent(dimStyle.Render("No selection."),
			m.width, m.height-1, m.cfg.DiffSplitRatio)
		return
	}
	p := it.pr
	var b strings.Builder
	b.WriteString(prTitleStyle.Render(p.Title) + "\n\n")
	b.WriteString(field("PR", prNumberStyle.Render(fmt.Sprintf("#%d", p.Number))))
	if p.Repo != "" {
		b.WriteString(field("Repo", p.Repo))
	}
	b.WriteString(field("Author", prAuthorStyle.Render(p.Author.Login)))
	state := p.State
	if p.IsDraft {
		state += prDraftStyle.Render(" (draft)")
	}
	b.WriteString(field("State", state))
	b.WriteString(field("Review", reviewDecisionLabel(p.ReviewDecision)))
	b.WriteString(field("Conversations", conversationStatusLabel(p)))
	b.WriteString(field("Branch", p.HeadRefName))
	b.WriteString(field("Updated", relTime(p.UpdatedAt)+" ago"))
	b.WriteString("\n" + dimStyle.Render("enter opens the full PR"))
	m.pane.SetPreviewContent(b.String(), m.width, m.height-1, m.cfg.DiffSplitRatio)
}

func conversationStatusLabel(p prSummary) string {
	if !p.ConversationsKnown {
		return dimStyle.Render("unknown")
	}
	if p.UnresolvedConversations == 0 {
		return checkPassStyle.Render("resolved")
	}
	return prUnresolvedStyle.Render(fmt.Sprintf("%d unresolved", p.UnresolvedConversations))
}

func reviewDecisionLabel(d string) string {
	switch d {
	case "APPROVED":
		return prApprovedStyle.Render("approved")
	case "CHANGES_REQUESTED":
		return prChangesStyle.Render("changes requested")
	case "REVIEW_REQUIRED":
		return prRequiredStyle.Render("review required")
	case "":
		return dimStyle.Render("—")
	}
	return d
}

func (m *prListModel) helpLine() string {
	return fmtHints(
		" ", "select",
		"A", "all",
		"enter", "open",
		"a", "approve",
		"x", "close",
		"M", "merge",
		"L", "labels",
		"d", "ready/draft",
		"r", "request",
		"o", "browser",
		"f", "status",
		"/", "search",
		"R", "refresh",
		":", "palette",
		"?", "help",
	)
}
