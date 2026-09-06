package admin

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// The writes, and what each one is asked to confirm.
//
// Access changes are not undoable from here in any useful sense — re-adding
// someone is a different event from never having removed them, and a revoked
// team permission takes everyone behind it with it — so every one of these goes
// through a confirmation. What the confirmation has to say varies more than the
// old string-prefixed version could express, which is why this is a struct.
//
// The distinction that matters most: two of these leave the repository. Adding
// or removing a team member is an *organization* change, and it alters that
// person's access to every repository the team can reach, not the one ghx was
// launched in. That is a different blast radius from everything else in this
// panel, and the prompt says so outright rather than leaving the reader to
// infer it from the fact that they are looking at a team.

// actionKind identifies a write.
type actionKind int

const (
	actSetCollaborator actionKind = iota
	actRemoveCollaborator
	actSetTeamPermission
	actRemoveTeamRepo
	actAddTeamMember
	actRemoveTeamMember
)

// confirmAction is a pending write plus everything needed to describe and run
// it. Resolved when the key is pressed, not when y is: the row under the cursor
// can move in between.
type confirmAction struct {
	kind       actionKind
	user       string
	teamSlug   string
	permission string
}

// orgWide reports whether this action reaches beyond the current repository.
func (a confirmAction) orgWide() bool {
	return a.kind == actAddTeamMember || a.kind == actRemoveTeamMember
}

// prompt is the question, without the y/n the footer adds.
func (a confirmAction) prompt(repo, org string) string {
	switch a.kind {
	case actSetCollaborator:
		return fmt.Sprintf("Give %s %s access to %s?", a.user, a.permission, repo)
	case actRemoveCollaborator:
		return fmt.Sprintf("Remove %s from %s?", a.user, repo)
	case actSetTeamPermission:
		return fmt.Sprintf("Give team %s %s access to %s?", a.teamSlug, a.permission, repo)
	case actRemoveTeamRepo:
		return fmt.Sprintf("Revoke team %s's access to %s? Its members lose it too.",
			a.teamSlug, repo)
	case actAddTeamMember:
		return fmt.Sprintf("ORG change — add %s to %s/%s, granting access to "+
			"EVERY repo that team reaches, not just %s?",
			a.user, org, a.teamSlug, repo)
	case actRemoveTeamMember:
		return fmt.Sprintf("ORG change — remove %s from %s/%s, revoking access "+
			"to EVERY repo that team reaches, not just %s?",
			a.user, org, a.teamSlug, repo)
	}
	return ""
}

// run performs the write and reports what happened.
//
// Every outcome is a toast rather than a.err: the list on screen is still the
// truth about who has access, and replacing it with an error page would hide
// exactly the state the reader needs to decide what to do next. A failure that
// is only a toast can be missed, which is why the successful path re-fetches —
// the row changing is the durable confirmation.
func (a *App) run(act confirmAction) tea.Cmd {
	ctx := context.Background()
	client, org := a.client, a.org()
	switch act.kind {
	case actSetCollaborator:
		return func() tea.Msg {
			if err := client.AddCollaborator(ctx, act.user, act.permission); err != nil {
				return writeDoneMsg{err: err}
			}
			// GitHub answers a grant to a non-member with an invitation rather
			// than access, and the roster will not show them until it is
			// accepted. Saying "added" would have the reader looking for a row
			// that is not going to appear.
			return writeDoneMsg{
				text: fmt.Sprintf("granted %s %s (pending their acceptance if "+
					"they were not already a collaborator)", act.user, act.permission),
				reload: catCollaborators,
			}
		}
	case actRemoveCollaborator:
		return func() tea.Msg {
			if err := client.RemoveCollaborator(ctx, act.user); err != nil {
				return writeDoneMsg{err: err}
			}
			return writeDoneMsg{
				text: "removed " + act.user, reload: catCollaborators,
			}
		}
	case actSetTeamPermission:
		return func() tea.Msg {
			if err := client.SetTeamRepoPermission(ctx, org, act.teamSlug, act.permission); err != nil {
				return writeDoneMsg{err: err}
			}
			return writeDoneMsg{
				text:   fmt.Sprintf("team %s now has %s", act.teamSlug, act.permission),
				reload: catTeams,
			}
		}
	case actRemoveTeamRepo:
		return func() tea.Msg {
			if err := client.RemoveTeamRepo(ctx, org, act.teamSlug); err != nil {
				return writeDoneMsg{err: err}
			}
			return writeDoneMsg{
				text: "revoked team " + act.teamSlug, reload: catTeams,
			}
		}
	case actAddTeamMember:
		slug := act.teamSlug
		return func() tea.Msg {
			if err := client.SetTeamMembership(ctx, org, slug, act.user, "member"); err != nil {
				return writeDoneMsg{err: err}
			}
			return writeDoneMsg{
				text: fmt.Sprintf("invited %s to %s (pending their acceptance)",
					act.user, slug),
				reloadTeam: slug,
			}
		}
	case actRemoveTeamMember:
		slug := act.teamSlug
		return func() tea.Msg {
			if err := client.RemoveTeamMembership(ctx, org, slug, act.user); err != nil {
				return writeDoneMsg{err: err}
			}
			return writeDoneMsg{
				text:       fmt.Sprintf("removed %s from %s", act.user, slug),
				reloadTeam: slug,
			}
		}
	}
	return nil
}

// askConfirm opens the confirmation for an action.
func (a *App) askConfirm(act confirmAction) tea.Cmd {
	a.confirm = &act
	return nil
}

// handleWriteKey dispatches the keys that start a write, for the category and
// row currently in view. It reports whether it consumed the key, so navigation
// keeps working on the categories that have no writes.
//
// Read-only categories deliberately do nothing rather than saying "not
// supported": the footer lists the keys that apply to what is on screen, so a
// key that is absent from it was never offered.
func (a *App) handleWriteKey(key string) (tea.Cmd, bool) {
	// Nothing is written over a pane that is not showing its data. The footer
	// already stops advertising the keys there, and that alone would be the
	// worse half of the fix: a panel that looks read-only and acts otherwise.
	// The 403 that surfaced this is also the case where a write cannot succeed
	// anyway — no permission to read the roster is no permission to change it.
	if a.loading || a.err != nil || a.memberBusy {
		return nil, false
	}
	switch a.active {
	case catCollaborators:
		return a.collaboratorWriteKey(key)
	case catTeams:
		if a.teamSlug != "" {
			return a.memberWriteKey(key)
		}
		return a.teamWriteKey(key)
	}
	return nil, false
}

func (a *App) collaboratorWriteKey(key string) (tea.Cmd, bool) {
	switch key {
	case "a":
		a.prompt = prompt{kind: promptLogin}
		return nil, true
	case "d", "p":
		rows := a.visibleCollaborators()
		if a.pane.Cursor >= len(rows) {
			return nil, true
		}
		row := rows[a.pane.Cursor]
		// A team-derived row has nothing on the repository to change: the
		// access comes from the team's grant, and both endpoints would answer
		// about a collaborator who was never added here. Refusing with the
		// reason is what keeps the row's own "via team" marker actionable
		// rather than decorative.
		if !row.Direct {
			return toast(fmt.Sprintf(
				"%s has access through a team — change it on the Teams tab (2)",
				row.Login)), true
		}
		if key == "d" {
			return a.askConfirm(confirmAction{
				kind: actRemoveCollaborator, user: row.Login,
			}), true
		}
		a.prompt = prompt{
			kind:    promptPermission,
			choices: permissionChoices(),
			choice:  indexOf(permissionChoices(), row.RoleName),
			target:  row.Login,
		}
		return nil, true
	}
	return nil, false
}

func (a *App) teamWriteKey(key string) (tea.Cmd, bool) {
	rows := a.visibleTeams()
	switch key {
	case "p":
		if a.pane.Cursor >= len(rows) {
			return nil, true
		}
		row := rows[a.pane.Cursor]
		a.prompt = prompt{
			kind:     promptPermission,
			choices:  permissionChoices(),
			choice:   indexOf(permissionChoices(), row.Permission),
			teamSlug: row.Slug,
		}
		return nil, true
	case "d":
		if a.pane.Cursor >= len(rows) {
			return nil, true
		}
		return a.askConfirm(confirmAction{
			kind: actRemoveTeamRepo, teamSlug: rows[a.pane.Cursor].Slug,
		}), true
	}
	return nil, false
}

func (a *App) memberWriteKey(key string) (tea.Cmd, bool) {
	switch key {
	case "a":
		a.prompt = prompt{kind: promptLogin, teamSlug: a.teamSlug}
		return nil, true
	case "d":
		rows := a.visibleMembers()
		if a.pane.Cursor >= len(rows) {
			return nil, true
		}
		return a.askConfirm(confirmAction{
			kind: actRemoveTeamMember, user: rows[a.pane.Cursor].Login,
			teamSlug: a.teamSlug,
		}), true
	}
	return nil, false
}

// permissionChoices is the set offered in the permission prompt. A function
// rather than the package var directly so a caller cannot reorder the shared
// slice by sorting the one it was handed.
func permissionChoices() []string {
	return append([]string(nil), permissionSet...)
}

func toast(text string) tea.Cmd {
	return func() tea.Msg { return toastMsg{text: text} }
}
