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
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// category identifies a section of the admin panel.
type category int

const (
	catCollaborators category = iota
	catBranchProtection
	catReleases
	catBranches
	catTags
	catWebhooks
)

var categoryNames = []string{
	"Collaborators", "Branch Protection", "Releases",
	"Branches", "Tags", "Webhooks",
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
	branches      []gh.Branch
	tags          []gh.Tag
	releases      []gh.Release
	webhooks      []gh.Webhook
	protection    *gh.BranchProtection

	// loading and error state
	loading bool
	err     error
	toast   string
	toastAt time.Time

	// cursor and offset for the active list
	cursor int
	offset int

	// confirm prompt for destructive actions
	confirm string

	// log viewer state (for run logs in actions, reused pattern)
	logView string
	logOff  int
}

// NewApp constructs the admin TUI.
func NewApp(client *gh.Client, repo string) *App {
	return &App{client: client, repo: repo, active: catCollaborators}
}

// Init loads the first category's data.
func (a *App) Init() tea.Cmd {
	return a.loadCategory(catCollaborators)
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

// dataMsg carries the result of an admin data fetch.
type dataMsg struct {
	cat           category
	err           error
	collaborators []gh.Collaborator
	branches      []gh.Branch
	tags          []gh.Tag
	releases      []gh.Release
	webhooks      []gh.Webhook
	protection    *gh.BranchProtection
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
		a.cursor = 0
		a.offset = 0
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

	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "j", "down":
		a.cursor = min(a.cursor+1, a.itemCount()-1)
		return nil
	case "k", "up":
		a.cursor = max(a.cursor-1, 0)
		return nil
	case "1", "2", "3", "4", "5", "6":
		idx := int(msg.String()[0] - '1')
		if idx < len(categoryNames) {
			a.active = category(idx)
			a.cursor = 0
			a.offset = 0
			return a.loadCategory(a.active)
		}
	case "g":
		a.cursor = 0
		return nil
	case "G":
		a.cursor = max(a.itemCount()-1, 0)
		return nil
	case "?":
		// TODO: help overlay
		return nil
	}
	return nil
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

// itemCount returns how many items the active category has.
func (a *App) itemCount() int {
	switch a.active {
	case catCollaborators:
		return len(a.collaborators)
	case catBranches:
		return len(a.branches)
	case catTags:
		return len(a.tags)
	case catReleases:
		return len(a.releases)
	case catWebhooks:
		return len(a.webhooks)
	case catBranchProtection:
		if a.protection != nil {
			return 1
		}
		return 0
	}
	return 0
}

func (a *App) View() string {
	if a.width == 0 || a.height == 0 {
		return "Loading ghx admin…"
	}
	if a.width < 40 || a.height < 8 {
		return fmt.Sprintf("Terminal too small (%dx%d).\nghx admin needs at least 40x8.", a.width, a.height)
	}

	title := tui.TitleStyle.Render(" ghx admin ") + " " + tui.DimStyle.Render(a.repo)
	cats := a.renderCategories()
	content := a.renderContent()
	footer := a.footer()

	return strings.Join([]string{title, cats, content, footer}, "\n")
}

func (a *App) renderCategories() string {
	tabs := make([]tui.TabLabel, len(categoryNames))
	for i, name := range categoryNames {
		tabs[i] = tui.TabLabel{Name: name, Active: category(i) == a.active}
	}
	return tui.RenderTabStrip(tabs, a.width)
}

func (a *App) renderContent() string {
	h := a.height - 4 // title, cats, footer, padding
	if h < 1 {
		h = 1
	}
	if a.loading {
		return "  Loading…"
	}
	if a.err != nil {
		return tui.ErrorStyle.Render("  error: "+a.err.Error()) + "\n\n  Press 1-6 to retry."
	}

	switch a.active {
	case catCollaborators:
		return a.renderCollaborators(h)
	case catBranchProtection:
		return a.renderProtection(h)
	case catReleases:
		return a.renderReleases(h)
	case catBranches:
		return a.renderBranches(h)
	case catTags:
		return a.renderTags(h)
	case catWebhooks:
		return a.renderWebhooks(h)
	}
	return ""
}

func (a *App) renderCollaborators(h int) string {
	if len(a.collaborators) == 0 {
		return tui.DimStyle.Render("  No collaborators.")
	}
	var b strings.Builder
	for i, c := range a.collaborators {
		line := fmt.Sprintf("  %-20s %s", c.Login, c.RoleName)
		if i == a.cursor {
			line = tui.SelectedRowStyle.Render(padLine(line, a.width))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (a *App) renderProtection(h int) string {
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

func (a *App) renderReleases(h int) string {
	if len(a.releases) == 0 {
		return tui.DimStyle.Render("  No releases.")
	}
	var b strings.Builder
	for i, r := range a.releases {
		marker := " "
		if r.IsDraft {
			marker = "○"
		} else if r.IsPrerelease {
			marker = "●"
		}
		line := fmt.Sprintf("  %s %-20s %s", marker, r.TagName, r.Name)
		if i == a.cursor {
			line = tui.SelectedRowStyle.Render(padLine(line, a.width))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (a *App) renderBranches(h int) string {
	if len(a.branches) == 0 {
		return tui.DimStyle.Render("  No branches.")
	}
	var b strings.Builder
	for i, br := range a.branches {
		marker := " "
		if br.Protected {
			marker = "🔒"
		}
		line := fmt.Sprintf("  %s %s", marker, br.Name)
		if i == a.cursor {
			line = tui.SelectedRowStyle.Render(padLine(line, a.width))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (a *App) renderTags(h int) string {
	if len(a.tags) == 0 {
		return tui.DimStyle.Render("  No tags.")
	}
	var b strings.Builder
	for i, t := range a.tags {
		line := fmt.Sprintf("  %-30s %s", t.Name, t.Commit.SHA[:min(len(t.Commit.SHA), 7)])
		if i == a.cursor {
			line = tui.SelectedRowStyle.Render(padLine(line, a.width))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (a *App) renderWebhooks(h int) string {
	if len(a.webhooks) == 0 {
		return tui.DimStyle.Render("  No webhooks.")
	}
	var b strings.Builder
	for i, w := range a.webhooks {
		active := ""
		if !w.Active {
			active = tui.DimStyle.Render(" (disabled)")
		}
		line := fmt.Sprintf("  %-40s %s%s", w.Config.URL, strings.Join(w.Events, ", "), active)
		if i == a.cursor {
			line = tui.SelectedRowStyle.Render(padLine(line, a.width))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (a *App) footer() string {
	if a.toast != "" && time.Since(a.toastAt) < 4*time.Second {
		return tui.TruncateFooter(a.toast, a.width)
	}
	if a.confirm != "" {
		return tui.TruncateFooter(
			tui.ErrorStyle.Render(a.confirm+" y/n"), a.width)
	}
	return tui.TruncateFooter(
		tui.FmtHints("1-6", "category", "j/k", "move", "d", "delete", "q", "quit"),
		a.width)
}

// padLine pads a string to width using cell-accurate measurement.
func padLine(s string, w int) string {
	width := lipgloss.Width(s)
	if width >= w {
		return s
	}
	return s + strings.Repeat(" ", w-width)
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
