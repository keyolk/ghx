package admin

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// The login prompt is also a people picker. What someone knows about a
// colleague is their name or company email — "moon.park" — not the GitHub
// account, "crescent-bird", and before this the only way across was the org's
// people page in a browser. So while a login is being typed, the body lists the
// org members it matches by login, profile name or SSO email
// (gh.ListOrgPeople), and enter takes the highlighted one.
//
// Typing a login the roster does not list still works: with no match, enter
// sends the text as typed. That case is real — an outside collaborator is not
// an org member, and neither is yesterday's hire in a day-old roster — and a
// picker that could only pick would lock both out.
//
// The risk a picker adds is taking a different person than the one typed. Two
// things bound it: a login typed exactly ranks first, and the confirmation
// names the person (login, name, email) rather than only the login, so a wrong
// pick is read before it is written.

// peopleMsg carries the org roster.
type peopleMsg struct {
	people []gh.OrgPerson
	err    error
}

// openLoginPrompt opens the login prompt and fetches the roster the first time.
func (a *App) openLoginPrompt(teamSlug string) tea.Cmd {
	a.prompt = prompt{kind: promptLogin, teamSlug: teamSlug}
	a.pickPane.Reset()
	if a.people != nil || a.peopleLoading {
		return nil
	}
	return a.loadPeople(false)
}

func (a *App) loadPeople(refresh bool) tea.Cmd {
	a.peopleLoading = true
	a.peopleErr = nil
	client, org := a.client, a.org()
	return func() tea.Msg {
		v, err := client.ListOrgPeople(context.Background(), org, refresh)
		return peopleMsg{people: v, err: err}
	}
}

func (a *App) onPeople(msg peopleMsg) {
	a.peopleLoading = false
	if msg.err != nil {
		a.peopleErr = msg.err
		return
	}
	// Non-nil even when empty, so "fetched, nobody there" is not retried as
	// "never fetched" on every prompt.
	a.people = append([]gh.OrgPerson{}, msg.people...)
	a.pickPane.Reset()
}

// candidates is the roster rows the typed text matches, exact login first.
// Empty text matches nobody: enter on an empty prompt cancels, and it must not
// instead pick whoever sorts first.
func (a *App) candidates() []gh.OrgPerson {
	q := strings.TrimSpace(a.prompt.value)
	if q == "" {
		return nil
	}
	idx := tui.FilterRows(q, len(a.people), func(i int) string {
		p := a.people[i]
		return p.Login + " " + p.Name + " " + p.Email
	})
	out := make([]gh.OrgPerson, 0, len(idx))
	for _, i := range idx {
		p := a.people[i]
		if strings.EqualFold(p.Login, q) {
			out = append([]gh.OrgPerson{p}, out...)
			continue
		}
		out = append(out, p)
	}
	return out
}

// pickedLogin is what enter commits: the highlighted candidate, or the typed
// text when nothing matches.
func (a *App) pickedLogin() string {
	if rows := a.candidates(); len(rows) > 0 {
		return rows[min(a.pickPane.Cursor, len(rows)-1)].Login
	}
	return strings.TrimSpace(a.prompt.value)
}

// personLabel names a login the way the confirmation should say it: with the
// name and email when the roster knows them, so the reader can tell the right
// person from the one a stray match picked.
func (a *App) personLabel(login string) string {
	for _, p := range a.people {
		if !strings.EqualFold(p.Login, login) {
			continue
		}
		var who []string
		if p.Name != "" {
			who = append(who, p.Name)
		}
		if p.Email != "" {
			who = append(who, p.Email)
		}
		if len(who) > 0 {
			return fmt.Sprintf("%s (%s)", p.Login, strings.Join(who, ", "))
		}
	}
	return login
}

// alreadyHere is the set of logins the add would be a no-op for: the team's
// members, or the repository's collaborators. Marked rather than hidden — the
// reader looking for someone should see that they are found, and where.
func (a *App) alreadyHere() (map[string]bool, string) {
	set := map[string]bool{}
	if slug := a.prompt.teamSlug; slug != "" {
		for _, m := range a.teamMembers[slug] {
			set[strings.ToLower(m.Login)] = true
		}
		return set, "in team"
	}
	for _, c := range a.collaborators {
		set[strings.ToLower(c.Login)] = true
	}
	return set, "has access"
}

// renderPicker is the body while a login is being typed.
func (a *App) renderPicker() string {
	switch {
	case a.peopleLoading && a.people == nil:
		return tui.DimStyle.Render("  Loading " + a.org() + " members…")
	case a.peopleErr != nil && a.people == nil:
		return tui.DimStyle.Render("  Member search unavailable: "+a.peopleErr.Error()) +
			"\n" + tui.DimStyle.Render("  Type the GitHub login; enter sends it as typed.")
	}
	if strings.TrimSpace(a.prompt.value) == "" {
		return tui.DimStyle.Render(fmt.Sprintf(
			"  Type a name, email or login to search %d %s members.", len(a.people), a.org()))
	}
	rows := a.candidates()
	if len(rows) == 0 {
		return tui.DimStyle.Render(fmt.Sprintf(
			"  No %s member matches — enter sends %q as the login.",
			a.org(), strings.TrimSpace(a.prompt.value)))
	}
	here, mark := a.alreadyHere()
	lines := make([]string, 0, len(rows))
	for _, p := range rows {
		line := "  " + padCells(p.Login, 24) + " " + padCells(p.Name, 24) + " " +
			tui.DimStyle.Render(p.Email)
		if here[strings.ToLower(p.Login)] {
			line += "  " + tui.CheckPassStyle.Render(mark)
		}
		lines = append(lines, line)
	}
	return a.pickPane.RenderList(lines, a.width, a.listRows())
}

// pickerStatus is the footer while the picker is open: how many members match,
// and the position when they do not all fit.
func (a *App) pickerStatus() string {
	if a.people == nil {
		return ""
	}
	n := len(a.candidates())
	line := tui.DimStyle.Render(fmt.Sprintf("  %d of %d %s members match", n, len(a.people), a.org()))
	if a.peopleLoading {
		line += tui.DimStyle.Render(" · refreshing…")
	}
	if pos := a.pickPane.ScrollHint(n, a.listRows()); pos != "" {
		line += "  " + tui.DimStyle.Render(pos)
	}
	return line
}

// padCells pads s to w display cells; names are often CJK, which %-24s would
// count by bytes and misalign.
func padCells(s string, w int) string {
	s, _ = tui.TruncateExact(s, w)
	if pad := w - tui.LipglossWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}
