// Package admin is the repo-administration TUI, launched via `ghx admin`.
// It is a separate tea.Model from the PR review app: the concerns are different
// (managing a repository vs reviewing its pull requests) and the data sources
// are the REST admin endpoints rather than the PR/graphql ones.
package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// category identifies a section of the admin panel.
type category int

const (
	catCollaborators category = iota
	catTeams
	catBranchProtection
	catReleases
	catBranches
	catTags
	catWebhooks
)

var categoryNames = []string{
	"People", "Teams", "Branch Protection",
	"Releases", "Branches", "Tags", "Webhooks",
}

// App is the admin TUI root model.
type App struct {
	client *gh.Client
	repo   string

	active category
	width  int
	height int

	// data caches per category
	collaborators []gh.Collaborator
	teams         []gh.Team
	branches      []gh.Branch
	tags          []gh.Tag
	releases      []gh.Release
	webhooks      []gh.Webhook
	protection    *gh.BranchProtection

	// teamMembers is the drilled-into team, or "" when showing the team list.
	// Members are cached per slug so stepping back and forth costs one fetch.
	teamSlug    string
	teamMembers map[string][]gh.TeamMember
	memberBusy  bool

	// loading and error state
	loading bool
	err     error
	toast   string
	toastAt time.Time

	// list scrolls inside its own rows; see tui.ListPane.
	pane tui.ListPane

	// query filters the active list; searching is true while it is being typed.
	query     string
	searching bool

	// confirm prompt for destructive actions
	confirm string
}

// NewApp constructs the admin TUI.
func NewApp(client *gh.Client, repo string) *App {
	return &App{
		client: client, repo: repo, active: catCollaborators,
		teamMembers: make(map[string][]gh.TeamMember),
	}
}

// Init loads the first category's data.
func (a *App) Init() tea.Cmd {
	return a.loadCategory(catCollaborators)
}

// org is the owner half of the repository slug, which is the organization a
// team belongs to.
func (a *App) org() string {
	owner, _, _ := strings.Cut(a.repo, "/")
	return owner
}

// loadCategory fires an async fetch for the given category.
func (a *App) loadCategory(cat category) tea.Cmd {
	a.loading = true
	a.err = nil
	client, ctx := a.client, context.Background()
	switch cat {
	case catCollaborators:
		return func() tea.Msg {
			v, err := client.ListCollaborators(ctx)
			return dataMsg{cat: cat, err: err, collaborators: v}
		}
	case catTeams:
		return func() tea.Msg {
			v, err := client.ListTeams(ctx)
			return dataMsg{cat: cat, err: err, teams: v}
		}
	case catBranchProtection:
		return func() tea.Msg {
			// branch protection needs a branch name; use "main" for now
			v, err := client.GetBranchProtection(ctx, "main")
			return dataMsg{cat: cat, err: err, protection: v}
		}
	case catReleases:
		return func() tea.Msg {
			v, err := client.ListReleases(ctx)
			return dataMsg{cat: cat, err: err, releases: v}
		}
	case catBranches:
		return func() tea.Msg {
			v, err := client.ListBranches(ctx)
			return dataMsg{cat: cat, err: err, branches: v}
		}
	case catTags:
		return func() tea.Msg {
			v, err := client.ListTags(ctx)
			return dataMsg{cat: cat, err: err, tags: v}
		}
	case catWebhooks:
		return func() tea.Msg {
			v, err := client.ListWebhooks(ctx)
			return dataMsg{cat: cat, err: err, webhooks: v}
		}
	}
	return nil
}

// loadTeamMembers drills into one team. Cached results render immediately, so
// stepping in and out of teams while comparing them costs one fetch each.
func (a *App) loadTeamMembers(slug string) tea.Cmd {
	a.teamSlug = slug
	a.pane.Reset()
	a.query = ""
	if _, ok := a.teamMembers[slug]; ok {
		return nil
	}
	a.memberBusy = true
	client, ctx, org := a.client, context.Background(), a.org()
	return func() tea.Msg {
		v, err := client.ListTeamMembers(ctx, org, slug)
		return membersMsg{slug: slug, members: v, err: err}
	}
}

// dataMsg carries the result of an admin data fetch.
type dataMsg struct {
	cat           category
	err           error
	collaborators []gh.Collaborator
	teams         []gh.Team
	branches      []gh.Branch
	tags          []gh.Tag
	releases      []gh.Release
	webhooks      []gh.Webhook
	protection    *gh.BranchProtection
}

// membersMsg carries one team's members.
type membersMsg struct {
	slug    string
	members []gh.TeamMember
	err     error
}

// toastMsg surfaces a transient message.
type toastMsg struct{ text string }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tea.KeyMsg:
		return a, a.handleKey(msg)

	case dataMsg:
		a.loading = false
		if msg.err != nil {
			a.err = msg.err
			return a, nil
		}
		a.err = nil
		switch msg.cat {
		case catCollaborators:
			a.collaborators = msg.collaborators
		case catTeams:
			a.teams = msg.teams
		case catBranchProtection:
			a.protection = msg.protection
		case catReleases:
			a.releases = msg.releases
		case catBranches:
			a.branches = msg.branches
		case catTags:
			a.tags = msg.tags
		case catWebhooks:
			a.webhooks = msg.webhooks
		}
		a.pane.Reset()
		return a, nil

	case membersMsg:
		a.memberBusy = false
		if msg.err != nil {
			a.err = msg.err
			return a, nil
		}
		a.teamMembers[msg.slug] = msg.members
		a.pane.Reset()
		return a, nil

	case toastMsg:
		a.toast = msg.text
		a.toastAt = time.Now()
		return a, nil
	}
	return a, nil
}

func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	// confirmation prompt owns the keyboard
	if a.confirm != "" {
		switch msg.String() {
		case "y", "Y":
			action := a.confirm
			a.confirm = ""
			return a.runConfirm(action)
		case "n", "N", "esc":
			a.confirm = ""
		}
		return nil
	}

	// The search prompt owns the keyboard while it is open, so a query can
	// contain j, k, q, and the digits without triggering navigation.
	if a.searching {
		return a.handleSearchKey(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "/":
		a.searching = true
		return nil
	case "esc":
		// One narrowing layer at a time: the filter first, then the drill-down.
		if a.query != "" {
			a.query = ""
			a.pane.Reset()
			return nil
		}
		if a.teamSlug != "" {
			a.teamSlug = ""
			a.pane.Reset()
		}
		return nil
	case "j", "down":
		a.pane.Move(1, a.itemCount())
		return nil
	case "k", "up":
		a.pane.Move(-1, a.itemCount())
		return nil
	case "ctrl+d", "pgdown":
		a.pane.Move(a.listRows()/2, a.itemCount())
		return nil
	case "ctrl+u", "pgup":
		a.pane.Move(-a.listRows()/2, a.itemCount())
		return nil
	case "enter":
		return a.onEnter()
	case "1", "2", "3", "4", "5", "6", "7":
		idx := int(msg.String()[0] - '1')
		if idx < len(categoryNames) {
			a.active = category(idx)
			a.teamSlug = ""
			a.query = ""
			a.pane.Reset()
			return a.loadCategory(a.active)
		}
	case "g":
		a.pane.Top()
		return nil
	case "G":
		a.pane.Bottom(a.itemCount())
		return nil
	case "?":
		// TODO: help overlay
		return nil
	}
	return nil
}

// handleSearchKey edits the query. The filter applies as it is typed so the
// list narrows under the cursor rather than after a commit.
func (a *App) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		a.searching = false
		a.query = ""
		a.pane.Reset()
	case tea.KeyEnter:
		a.searching = false
	case tea.KeyBackspace:
		if r := []rune(a.query); len(r) > 0 {
			a.query = string(r[:len(r)-1])
			a.pane.Reset()
		}
	case tea.KeyRunes:
		a.query += string(msg.Runes)
		a.pane.Reset()
	case tea.KeySpace:
		a.query += " "
		a.pane.Reset()
	}
	return nil
}

// onEnter drills into a team, which is the only category with a second level.
func (a *App) onEnter() tea.Cmd {
	if a.active != catTeams || a.teamSlug != "" {
		return nil
	}
	rows := a.visibleTeams()
	if a.pane.Cursor >= len(rows) {
		return nil
	}
	return a.loadTeamMembers(rows[a.pane.Cursor].Slug)
}

// runConfirm executes a confirmed destructive action.
func (a *App) runConfirm(action string) tea.Cmd {
	ctx := context.Background()
	client := a.client
	switch {
	case strings.HasPrefix(action, "remove-collaborator:"):
		user := strings.TrimPrefix(action, "remove-collaborator:")
		return func() tea.Msg {
			err := client.RemoveCollaborator(ctx, user)
			if err != nil {
				return toastMsg{text: "error: " + err.Error()}
			}
			return toastMsg{text: "removed " + user}
		}
	}
	return nil
}

// --- filtered views ---
//
// Each returns the rows the query keeps, in display order. The cursor indexes
// these, never the unfiltered slice: indexing the source would act on whichever
// row happened to sit at that position before the filter.

func (a *App) visibleCollaborators() []gh.Collaborator {
	idx := tui.FilterRows(a.query, len(a.collaborators), func(i int) string {
		c := a.collaborators[i]
		return c.Login + " " + c.RoleName
	})
	out := make([]gh.Collaborator, 0, len(idx))
	for _, i := range idx {
		out = append(out, a.collaborators[i])
	}
	return out
}

func (a *App) visibleTeams() []gh.Team {
	idx := tui.FilterRows(a.query, len(a.teams), func(i int) string {
		t := a.teams[i]
		parent := ""
		if t.Parent != nil {
			parent = t.Parent.Slug
		}
		return t.Slug + " " + t.Name + " " + t.Permission + " " + t.Description + " " + parent
	})
	out := make([]gh.Team, 0, len(idx))
	for _, i := range idx {
		out = append(out, a.teams[i])
	}
	return out
}

func (a *App) visibleMembers() []gh.TeamMember {
	members := a.teamMembers[a.teamSlug]
	idx := tui.FilterRows(a.query, len(members), func(i int) string {
		return members[i].Login
	})
	out := make([]gh.TeamMember, 0, len(idx))
	for _, i := range idx {
		out = append(out, members[i])
	}
	return out
}

func (a *App) visibleBranches() []gh.Branch {
	idx := tui.FilterRows(a.query, len(a.branches), func(i int) string {
		return a.branches[i].Name
	})
	out := make([]gh.Branch, 0, len(idx))
	for _, i := range idx {
		out = append(out, a.branches[i])
	}
	return out
}

func (a *App) visibleTags() []gh.Tag {
	idx := tui.FilterRows(a.query, len(a.tags), func(i int) string {
		return a.tags[i].Name
	})
	out := make([]gh.Tag, 0, len(idx))
	for _, i := range idx {
		out = append(out, a.tags[i])
	}
	return out
}

func (a *App) visibleReleases() []gh.Release {
	idx := tui.FilterRows(a.query, len(a.releases), func(i int) string {
		return a.releases[i].TagName + " " + a.releases[i].Name
	})
	out := make([]gh.Release, 0, len(idx))
	for _, i := range idx {
		out = append(out, a.releases[i])
	}
	return out
}

func (a *App) visibleWebhooks() []gh.Webhook {
	idx := tui.FilterRows(a.query, len(a.webhooks), func(i int) string {
		w := a.webhooks[i]
		return w.Config.URL + " " + strings.Join(w.Events, " ")
	})
	out := make([]gh.Webhook, 0, len(idx))
	for _, i := range idx {
		out = append(out, a.webhooks[i])
	}
	return out
}

// itemCount returns how many rows the active view shows, after filtering.
func (a *App) itemCount() int {
	switch a.active {
	case catCollaborators:
		return len(a.visibleCollaborators())
	case catTeams:
		if a.teamSlug != "" {
			return len(a.visibleMembers())
		}
		return len(a.visibleTeams())
	case catBranches:
		return len(a.visibleBranches())
	case catTags:
		return len(a.visibleTags())
	case catReleases:
		return len(a.visibleReleases())
	case catWebhooks:
		return len(a.visibleWebhooks())
	case catBranchProtection:
		if a.protection != nil {
			return 1
		}
		return 0
	}
	return 0
}

// contentRows is how many rows the body may occupy: the terminal minus the
// title, the tab strip, and the footer. Sizing to a.height instead is what
// pushed the title and the tab strip off the top of an overflowing frame —
// bubbletea keeps the LAST height lines, so the rows it drops are the ones the
// user needs to navigate with.
func (a *App) contentRows() int { return max(a.height-3, 1) }

// listRows is the height available to list rows themselves.
func (a *App) listRows() int { return a.contentRows() }

func (a *App) View() string {
	if a.width == 0 || a.height == 0 {
		return "Loading ghx admin…"
	}
	if a.width < 40 || a.height < 8 {
		return fmt.Sprintf("Terminal too small (%dx%d).\nghx admin needs at least 40x8.", a.width, a.height)
	}

	title := tui.TitleStyle.Render(" ghx admin ") + " " + tui.DimStyle.Render(a.repo)
	header := a.renderHeader()
	content := tui.FitRows(a.renderContent(), a.contentRows())
	footer := a.footer()

	return strings.Join([]string{title, header, content, footer}, "\n")
}

// renderHeader is the tab strip, or the search prompt while one is being typed.
// The prompt replaces the strip rather than adding a row: an extra row would
// resize the list mid-search, and the tabs are unreachable while the query owns
// the keyboard anyway.
func (a *App) renderHeader() string {
	if a.searching {
		return tui.RenderSearchBar(a.query, a.width)
	}
	tabs := make([]tui.TabLabel, len(categoryNames))
	for i, name := range categoryNames {
		tabs[i] = tui.TabLabel{Name: name, Active: category(i) == a.active}
	}
	strip := tui.RenderTabStrip(tabs, a.width)
	if a.query != "" {
		strip += " " + tui.TabActiveStyle.Render("["+a.query+"]")
	}
	return strip
}

func (a *App) renderContent() string {
	if a.loading {
		return "  Loading…"
	}
	if a.err != nil {
		return tui.ErrorStyle.Render("  error: "+a.err.Error()) + "\n\n  Press 1-7 to retry."
	}
	if a.memberBusy {
		return "  Loading members…"
	}

	switch a.active {
	case catCollaborators:
		return a.renderCollaborators()
	case catTeams:
		if a.teamSlug != "" {
			return a.renderMembers()
		}
		return a.renderTeams()
	case catBranchProtection:
		return a.renderProtection()
	case catReleases:
		return a.renderReleases()
	case catBranches:
		return a.renderBranches()
	case catTags:
		return a.renderTags()
	case catWebhooks:
		return a.renderWebhooks()
	}
	return ""
}

// emptyOr renders the list, or a note when the filter or the source left it
// with nothing. The two are worth distinguishing: an empty source is a fact
// about the repository, an over-narrow filter is one keystroke from being
// wrong, and they call for opposite responses.
func (a *App) emptyOr(rows []string, empty string) string {
	if len(rows) == 0 {
		if a.query != "" {
			return tui.DimStyle.Render(fmt.Sprintf("  Nothing matches %q.", a.query))
		}
		return tui.DimStyle.Render("  " + empty)
	}
	return a.pane.RenderList(rows, a.width, a.listRows())
}

func (a *App) renderCollaborators() string {
	items := a.visibleCollaborators()
	rows := make([]string, 0, len(items))
	for _, c := range items {
		// Say where the access comes from. Without it a repo whose permissions
		// are entirely team-based reads as a list of individual grants, and
		// pressing d on one of those cannot work: there is nothing on the
		// repository to revoke.
		via := tui.DimStyle.Render("via team")
		if c.Direct {
			via = tui.CheckPassStyle.Render("direct")
		}
		rows = append(rows, fmt.Sprintf("  %-24s %-10s %s", c.Login, c.RoleName, via))
	}
	return a.emptyOr(rows, "No collaborators.")
}

func (a *App) renderTeams() string {
	items := a.visibleTeams()
	rows := make([]string, 0, len(items))
	for _, t := range items {
		nested := ""
		if t.Parent != nil {
			nested = tui.DimStyle.Render(" ⤷ " + t.Parent.Slug)
		}
		count := ""
		if members, ok := a.teamMembers[t.Slug]; ok {
			count = tui.DimStyle.Render(fmt.Sprintf(" %d members", len(members)))
		}
		rows = append(rows, fmt.Sprintf("  %-32s %-8s%s%s",
			t.Slug, t.Permission, count, nested))
	}
	return a.emptyOr(rows, "No teams have access to this repository.")
}

func (a *App) renderMembers() string {
	items := a.visibleMembers()
	rows := make([]string, 0, len(items))
	for _, m := range items {
		rows = append(rows, "  "+m.Login)
	}
	return a.emptyOr(rows, "This team has no members.")
}

func (a *App) renderProtection() string {
	if a.protection == nil {
		return tui.DimStyle.Render("  Branch 'main' is not protected.")
	}
	var b strings.Builder
	if a.protection.RequiredReviews != nil {
		b.WriteString(fmt.Sprintf("  Required reviews: %d\n", a.protection.RequiredReviews.RequiredCount))
		b.WriteString(fmt.Sprintf("  Dismiss stale: %t\n", a.protection.RequiredReviews.DismissStale))
	}
	if a.protection.RequiredStatusChecks != nil {
		b.WriteString(fmt.Sprintf("  Status checks: %v\n", a.protection.RequiredStatusChecks.Contexts))
	}
	b.WriteString(fmt.Sprintf("  Enforce admins: %t\n", a.protection.EnforceAdmins.Enabled))
	return b.String()
}

func (a *App) renderReleases() string {
	items := a.visibleReleases()
	rows := make([]string, 0, len(items))
	for _, r := range items {
		marker := " "
		if r.IsDraft {
			marker = "○"
		} else if r.IsPrerelease {
			marker = "●"
		}
		rows = append(rows, fmt.Sprintf("  %s %-24s %s", marker, r.TagName, r.Name))
	}
	return a.emptyOr(rows, "No releases.")
}

func (a *App) renderBranches() string {
	items := a.visibleBranches()
	rows := make([]string, 0, len(items))
	for _, br := range items {
		marker := " "
		if br.Protected {
			marker = "🔒"
		}
		rows = append(rows, fmt.Sprintf("  %s %s", marker, br.Name))
	}
	return a.emptyOr(rows, "No branches.")
}

func (a *App) renderTags() string {
	items := a.visibleTags()
	rows := make([]string, 0, len(items))
	for _, t := range items {
		rows = append(rows, fmt.Sprintf("  %-32s %s",
			t.Name, t.Commit.SHA[:min(len(t.Commit.SHA), 7)]))
	}
	return a.emptyOr(rows, "No tags.")
}

func (a *App) renderWebhooks() string {
	items := a.visibleWebhooks()
	rows := make([]string, 0, len(items))
	for _, w := range items {
		active := ""
		if !w.Active {
			active = tui.DimStyle.Render(" (disabled)")
		}
		rows = append(rows, fmt.Sprintf("  %-40s %s%s",
			w.Config.URL, strings.Join(w.Events, ", "), active))
	}
	return a.emptyOr(rows, "No webhooks.")
}

func (a *App) footer() string {
	if a.toast != "" && time.Since(a.toastAt) < 4*time.Second {
		return tui.TruncateFooter(a.toast, a.width)
	}
	if a.confirm != "" {
		return tui.TruncateFooter(
			tui.ErrorStyle.Render(a.confirm+" y/n"), a.width)
	}

	hints := []string{"1-7", "category", "j/k", "move", "/", "search"}
	switch {
	case a.teamSlug != "":
		hints = append(hints, "esc", "back to teams")
	case a.active == catTeams:
		hints = append(hints, "enter", "members")
	}
	hints = append(hints, "q", "quit")

	line := tui.FmtHints(hints...)
	// The position is only worth a footer slot when the list does not fit; a
	// counter that always reads 1/1 is noise.
	if pos := a.pane.ScrollHint(a.itemCount(), a.listRows()); pos != "" {
		line += "  " + tui.DimStyle.Render(pos)
	}
	return tui.TruncateFooter(line, a.width)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
