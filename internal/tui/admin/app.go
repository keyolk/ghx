// Package admin is the repo-administration TUI, launched via `ghx admin`.
// It is a separate tea.Model from the PR review app: the concerns are different
// (managing a repository vs reviewing its pull requests) and the data sources
// are the REST admin endpoints rather than the PR/graphql ones.
package admin

import (
	"context"
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

	// prompt is the open input for a write — a login to add, a permission to
	// grant — or the zero value when none is open. See prompt.go.
	prompt prompt

	// confirm is the pending write awaiting y/n, or nil. It carries the whole
	// action rather than a string to parse: the two team-membership writes are
	// organization-wide and the prompt has to say so, which a prefix-matched
	// string could not express. See actions.go.
	confirm *confirmAction

	// busy marks a write in flight. The list stays on screen while it runs —
	// it is still the truth about who has access — so this only gates the
	// footer and a second keypress.
	busy bool
}

// permissionSet is the levels offered in the permission prompt, weakest first.
// Aliased from the gh package so the TUI does not restate a set the API owns.
var permissionSet = gh.CollaboratorPermissions

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

// writeDoneMsg reports the outcome of a write.
//
// It carries what to re-fetch rather than leaving the caller to guess. A write
// whose row does not change is indistinguishable from one that silently failed
// — the toast is four seconds of text over a list that still shows the old
// state — so the arriving row is the durable confirmation, and reloading is
// part of the action rather than something the user has to press R for.
type writeDoneMsg struct {
	text string
	err  error
	// reload is the category to re-fetch, or the zero value (People) only when
	// reloadTeam is set instead. See needsReload.
	reload     category
	reloadTeam string
}

// needsReload distinguishes "re-fetch People" from "re-fetch nothing", which
// the zero value of a category cannot do on its own.
func (m writeDoneMsg) needsReload() bool {
	return m.err == nil && m.reloadTeam == ""
}

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

	case writeDoneMsg:
		a.busy = false
		if msg.err != nil {
			// A toast, not a.err: the list on screen is still the truth about
			// who has access, and replacing it with an error page would hide
			// exactly the state needed to decide what to do next.
			a.toast = tui.ErrorStyle.Render("error: ") + msg.err.Error()
			a.toastAt = time.Now()
			return a, nil
		}
		a.toast = msg.text
		a.toastAt = time.Now()
		if msg.reloadTeam != "" {
			// The cached member list is now wrong, and it is what the drill-down
			// renders from. Dropping it is what makes the re-fetch actually go
			// out rather than being served from the map.
			delete(a.teamMembers, msg.reloadTeam)
			return a, a.loadTeamMembers(msg.reloadTeam)
		}
		if msg.needsReload() {
			return a, a.loadCategory(msg.reload)
		}
		return a, nil
	}
	return a, nil
}

// isInTextInput reports whether a text field currently owns the keyboard. Used
// to gate CJK normalization: there the jamo IS the intended input.
//
// The write prompt is split by kind: promptLogin is a text field, while
// promptPermission is a shortcut menu (tab/j/k/l/h), so the prompt being open
// does not by itself mean text is being typed.
func (a *App) isInTextInput() bool {
	return a.searching || a.prompt.kind == promptLogin
}

func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	// ctrl+c quits from anywhere, ahead of the confirmation, the write prompt,
	// and the search box. Nothing is written until Enter, so aborting is safe.
	if msg.Type == tea.KeyCtrlC {
		return tea.Quit
	}

	// Under a Korean input source the shortcut keys arrive as jamo (`q` -> `ㅂ`).
	// Rewrite them to the Latin key at the same physical position so shortcuts
	// fire without switching the input source back.
	if !a.isInTextInput() {
		msg = tui.NormalizeCJKKey(msg)
	}

	// confirmation prompt owns the keyboard
	if a.confirm != nil {
		switch msg.String() {
		case "y", "Y":
			act := *a.confirm
			a.confirm = nil
			a.busy = true
			return a.run(act)
		case "n", "N", "esc":
			a.confirm = nil
		}
		// Anything else is ignored rather than treated as consent.
		return nil
	}

	// The write prompt owns the keyboard for the same reason the search one
	// does: a login contains letters that are otherwise navigation keys, so
	// typing "kim" would move the cursor and quit.
	if a.prompt.open() {
		return a.handlePromptKey(msg)
	}

	// The search prompt owns the keyboard while it is open, so a query can
	// contain j, k, q, and the digits without triggering navigation.
	if a.searching {
		return a.handleSearchKey(msg)
	}

	switch msg.String() {
	case "q":
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
	// The write keys come last so they can never shadow navigation: a and d are
	// not bound above, but a future navigation binding must win over a write
	// rather than the reverse — pressing a key that used to move the cursor and
	// having it revoke someone's access is the failure worth designing out.
	if cmd, handled := a.handleWriteKey(msg.String()); handled {
		return cmd
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
