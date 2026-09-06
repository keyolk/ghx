package admin

import (
	"fmt"
	"strings"
	"time"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// Rendering: the frame, its rows of chrome, one list per category, and the
// filtered views those lists are built from.
//
// Split from app.go when the writes pushed it past the 500-line mark. Two
// invariants live here: the frame is never taller than the terminal (see
// contentRows), and every row the cursor can reach comes from a visible*()
// helper (see the filtered views).

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
	// The write prompt takes the same row as the search bar, for the same
	// reason: an extra line would resize the list under the cursor while
	// someone is typing into it.
	if a.prompt.open() {
		return a.prompt.render(a.width)
	}
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
	if a.confirm != nil {
		// The org-wide warning is the sentence's own first words rather than a
		// marker prepended here. Both would say the same thing twice, and the
		// footer truncates — so what has to lead is the part that must not be
		// cut off, and that is the warning itself. See confirmAction.prompt.
		return tui.TruncateFooter(
			tui.ErrorStyle.Render(a.confirm.prompt(a.repo, a.org())+" y/n"),
			a.width)
	}
	if a.busy {
		return tui.TruncateFooter(tui.DimStyle.Render("  working…"), a.width)
	}

	hints := []string{"1-7", "category", "j/k", "move", "/", "search"}
	// The write keys are listed only where they do something. A key advertised
	// on a read-only category is a key someone presses and gets nothing from,
	// which reads as a broken binding rather than an inapplicable one.
	//
	// Nor while the pane is showing an error or nothing at all — measured
	// against sendbird/ops-k8s without admin rights, which answers the roster
	// with HTTP 403: the pane said "Must have push access" and the footer went
	// on offering `a:add · p:permission · d:remove`, keys that had no row to
	// act on and no permission to act with. The footer describes what is on
	// screen, and an error is not a list.
	switch {
	case a.loading || a.err != nil || a.memberBusy:
		// Nothing added: navigation and quit still apply, and they are the only
		// things that do. An *empty* list is not this case — a repository with
		// no collaborators is exactly where `a` is wanted, and the row-based
		// keys below are filtered by whether there is a row.
	case a.teamSlug != "":
		hints = append(hints, "a", "add member")
		if a.itemCount() > 0 {
			hints = append(hints, "d", "remove")
		}
		hints = append(hints, "esc", "back to teams")
	case a.active == catTeams:
		if a.itemCount() > 0 {
			hints = append(hints,
				"enter", "members", "p", "permission", "d", "revoke")
		}
	case a.active == catCollaborators:
		hints = append(hints, "a", "add")
		if a.itemCount() > 0 {
			hints = append(hints, "p", "permission", "d", "remove")
		}
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
