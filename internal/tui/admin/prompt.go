package admin

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// The admin panel's writes need input, and the two kinds it needs are
// different: a login is free text, a permission level is one of five.
//
// Typing a permission would be the obvious way to ask for both with one
// widget, and it is the wrong one — "pussh" is a rejected request that reads as
// a failed grant, and the set is small enough that nobody should have to
// remember it. So a login is typed and a permission is cycled, and the prompt
// says which of the two it is holding.

// promptKind says what the open prompt is collecting.
type promptKind int

const (
	promptNone promptKind = iota
	// promptLogin collects a GitHub login: adding a collaborator, or adding a
	// member to a team.
	promptLogin
	// promptPermission picks a level from a fixed set, for whatever target the
	// prompt was opened on.
	promptPermission
)

// prompt is the open input, or the zero value when none is.
//
// target names what the collected value will be applied to, resolved when the
// prompt opens rather than when it closes: the list under the cursor can be
// re-fetched by a poll or a refresh while someone is typing, and a cursor
// resolved at commit time would then apply the change to whatever row had
// slid into that position.
type prompt struct {
	kind promptKind
	// value is the typed login, for promptLogin.
	value string
	// choices and choice are the cycled set, for promptPermission.
	choices []string
	choice  int
	// target is the login or team slug the value applies to, or "" when the
	// prompt is collecting the target itself.
	target string
	// teamSlug is set when the prompt concerns a team: either the team being
	// granted a permission, or the team a member is being added to.
	teamSlug string
}

func (p prompt) open() bool { return p.kind != promptNone }

// selected is the currently highlighted permission.
func (p prompt) selected() string {
	if len(p.choices) == 0 {
		return ""
	}
	return p.choices[p.choice%len(p.choices)]
}

// label names the prompt in its own bar, so the row says what it is collecting
// rather than only that something is being typed.
func (p prompt) label() string {
	switch p.kind {
	case promptLogin:
		if p.teamSlug != "" {
			return "Add to " + p.teamSlug
		}
		return "Add collaborator"
	case promptPermission:
		switch {
		case p.teamSlug != "":
			return "Permission for " + p.teamSlug
		case p.target != "":
			return "Permission for " + p.target
		}
		return "Permission"
	}
	return ""
}

// render draws the open prompt in the tab strip's row.
func (p prompt) render(width int) string {
	switch p.kind {
	case promptLogin:
		return tui.RenderPromptBar(p.label(), p.value, width,
			"enter", "next", "esc", "cancel")
	case promptPermission:
		// The whole set is shown with the selection marked, rather than one
		// value that changes as tab is pressed. Five options fit in a line, and
		// seeing them is what makes "which of these is narrower" answerable
		// without leaving the prompt.
		parts := make([]string, 0, len(p.choices))
		for i, c := range p.choices {
			if i == p.choice {
				parts = append(parts, tui.TabActiveStyle.Render("["+c+"]"))
				continue
			}
			parts = append(parts, tui.DimStyle.Render(" "+c+" "))
		}
		line := tui.HelpKeyStyle.Render(p.label()+": ") + strings.Join(parts, "") +
			"  " + tui.FmtHints("tab", "next", "enter", "apply", "esc", "cancel")
		return tui.TruncateFooter(line, width)
	}
	return ""
}

// handlePromptKey edits the open prompt and commits it.
//
// The prompt owns the keyboard while it is open, for the same reason the search
// bar does: a login contains letters that are otherwise navigation keys, and
// "kim" would move the cursor three rows and quit.
func (a *App) handlePromptKey(msg tea.KeyMsg) tea.Cmd {
	switch a.prompt.kind {
	case promptLogin:
		switch msg.Type {
		case tea.KeyEsc:
			a.prompt = prompt{}
		case tea.KeyEnter:
			return a.commitLogin()
		case tea.KeyBackspace:
			if r := []rune(a.prompt.value); len(r) > 0 {
				a.prompt.value = string(r[:len(r)-1])
			}
		case tea.KeyRunes:
			// Spaces and the rest are accepted as typed; GitHub rejects an
			// invalid login with a message, which is more useful than this
			// guessing at the rules.
			a.prompt.value += string(msg.Runes)
		}
		return nil

	case promptPermission:
		switch msg.String() {
		case "esc":
			a.prompt = prompt{}
		case "tab", "j", "down", "right", "l":
			a.prompt.choice = (a.prompt.choice + 1) % len(a.prompt.choices)
		case "shift+tab", "k", "up", "left", "h":
			a.prompt.choice = (a.prompt.choice - 1 + len(a.prompt.choices)) % len(a.prompt.choices)
		case "enter":
			return a.commitPermission()
		}
		return nil
	}
	return nil
}

// commitLogin moves from the typed login to the permission step, or applies a
// team membership directly.
//
// A collaborator needs a level, so the login prompt hands over to the
// permission one rather than committing — a grant with no level silently means
// "pull", which is not something to infer from an empty field. A team
// membership does not: "member" is the answer in almost every case, and the
// role can be changed afterwards from the member row.
func (a *App) commitLogin() tea.Cmd {
	login := strings.TrimSpace(a.prompt.value)
	if login == "" {
		a.prompt = prompt{}
		return nil
	}
	if slug := a.prompt.teamSlug; slug != "" {
		a.prompt = prompt{}
		return a.askConfirm(confirmAction{
			kind: actAddTeamMember, user: login, teamSlug: slug,
		})
	}
	a.prompt = prompt{
		kind:    promptPermission,
		choices: gh.CollaboratorPermissions,
		// Defaults to push rather than to the weakest level: it is what "add a
		// collaborator" almost always means, and the reader can see the whole
		// set in the bar to pick otherwise.
		choice: indexOf(gh.CollaboratorPermissions, "push"),
		target: login,
	}
	return nil
}

// commitPermission applies the picked level to whatever the prompt targets.
func (a *App) commitPermission() tea.Cmd {
	level := a.prompt.selected()
	target, slug := a.prompt.target, a.prompt.teamSlug
	a.prompt = prompt{}
	if level == "" {
		return nil
	}
	if slug != "" {
		return a.askConfirm(confirmAction{
			kind: actSetTeamPermission, teamSlug: slug, permission: level,
		})
	}
	if target == "" {
		return nil
	}
	return a.askConfirm(confirmAction{
		kind: actSetCollaborator, user: target, permission: level,
	})
}

func indexOf(set []string, want string) int {
	for i, s := range set {
		if s == want {
			return i
		}
	}
	return 0
}
