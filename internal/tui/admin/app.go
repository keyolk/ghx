// Package admin is the repo-administration TUI, launched via `ghx admin`.
// It is a separate tea.Model from the PR review app: the concerns are different
// (managing a repository vs reviewing its pull requests) and the data sources
// are the REST admin endpoints rather than the PR/graphql ones.
package admin

import (
	"context"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"

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
	toast   tui.Toast

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
		a.toast.Ack()
		return a, a.handleKey(msg)

	case tui.ToastExpiredMsg:
		a.toast.Expire(msg)
		return a, nil

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
		a.toast.Set(msg.text)
		return a, a.toast.Expiry()

	case writeDoneMsg:
		a.busy = false
		if msg.err != nil {
			// A toast, not a.err: the list on screen is still the truth about
			// who has access, and replacing it with an error page would hide
			// exactly the state needed to decide what to do next.
			a.toast.SetError(tui.ErrorStyle.Render("error: ") + msg.err.Error())
			return a, nil
		}
		a.toast.Set(msg.text)
		expiry := a.toast.Expiry()
		if msg.reloadTeam != "" {
			// The cached member list is now wrong, and it is what the drill-down
			// renders from. Dropping it is what makes the re-fetch actually go
			// out rather than being served from the map.
			delete(a.teamMembers, msg.reloadTeam)
			return a, tea.Batch(expiry, a.loadTeamMembers(msg.reloadTeam))
		}
		if msg.needsReload() {
			return a, tea.Batch(expiry, a.loadCategory(msg.reload))
		}
		return a, expiry
	}
	return a, nil
}

func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	// ctrl+c quits from anywhere, ahead of the confirmation, the write prompt,
	// and the search box. Nothing is written until Enter, so aborting is safe.
	if msg.Type == tea.KeyCtrlC {
		return tea.Quit
	}

	// confirmation prompt owns the keyboard
	if a.confirm != nil {
		b, _ := keymap.Resolve(stackConfirm, msg, keys.Opts{})
		switch b.Action {
		case "yes":
			act := *a.confirm
			a.confirm = nil
			a.busy = true
			return a.run(act)
		case "no":
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
	// contain j, k, q, and the digits without triggering navigation. Raw: a
	// jamo there is the query, not a shortcut.
	if a.searching {
		b, _ := keymap.ResolveRaw(stackSearch, msg, keys.Opts{})
		return a.handleSearchKey(b.Action, msg)
	}

	// Resolve rewrites a Korean jamo to the Latin key at the same physical
	// position, so shortcuts fire without switching the input source back.
	//
	// The write keys sit in contexts in front of the list, so an inner write
	// would win over a list key on the same letter. The list binds none of a,
	// d, p, and TestWriteKeysShadowNoListKey keeps it so: a key that used to
	// move the cursor and now revokes someone's access is the failure worth
	// designing out.
	b, ok := keymap.Resolve(a.stack(), msg, a.keyOpts())
	if !ok {
		return nil
	}
	switch b.Action {
	case "quit":
		return tea.Quit
	case "search":
		a.searching = true
	case "clear":
		if a.query != "" {
			a.query = ""
			a.pane.Reset()
			return nil
		}
		if a.teamSlug != "" {
			a.teamSlug = ""
			a.pane.Reset()
		}
	case "down":
		a.pane.Move(1, a.itemCount())
	case "up":
		a.pane.Move(-1, a.itemCount())
	case "pagedown":
		a.pane.Move(a.listRows()/2, a.itemCount())
	case "pageup":
		a.pane.Move(-a.listRows()/2, a.itemCount())
	case "top":
		a.pane.Top()
	case "bottom":
		a.pane.Bottom(a.itemCount())
	case "category":
		// The position of the pressed key picks the category, so a rebound
		// key still means "the third".
		idx := slices.Index(b.Keys, keys.NormalizeCJK(msg).String())
		if idx >= 0 && idx < len(categoryNames) {
			a.active = category(idx)
			a.teamSlug = ""
			a.query = ""
			a.pane.Reset()
			return a.loadCategory(a.active)
		}
	case "members":
		return a.onEnter()
	default:
		cmd, _ := a.handleWriteKey(b.Action)
		return cmd
	}
	return nil
}

// handleSearchKey edits the query. The filter applies as it is typed so the
// list narrows under the cursor rather than after a commit.
func (a *App) handleSearchKey(action string, msg tea.KeyMsg) tea.Cmd {
	switch action {
	case "clear":
		a.searching = false
		a.query = ""
		a.pane.Reset()
		return nil
	case "apply":
		a.searching = false
		return nil
	case "erase":
		if r := []rune(a.query); len(r) > 0 {
			a.query = string(r[:len(r)-1])
			a.pane.Reset()
		}
		return nil
	}
	switch msg.Type {
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
