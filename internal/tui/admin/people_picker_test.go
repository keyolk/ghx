package admin

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
)

var roster = []gh.OrgPerson{
	{Login: "alice", Name: "Alice Kim"},
	{Login: "crescent-bird", Name: "moon.Park", Email: "moon.park@acme.com"},
	{Login: "moon", Name: "Someone Else"},
	{Login: "seungtae62", Email: "seungtae.moon@acme.com"},
}

func pickerApp(t *testing.T) *App {
	t.Helper()
	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}})
	if cmd := a.handleKey(keyMsg("a")); cmd == nil {
		t.Fatal("opening the login prompt did not fetch the roster")
	}
	a.Update(peopleMsg{people: roster})
	return a
}

// The case that motivated this: the reader knows the company email, not the
// GitHub account.
func TestThePickerFindsALoginByCompanyEmail(t *testing.T) {
	a := pickerApp(t)
	typeKeys(a, "moon.park")
	if body := plain(a.View()); !strings.Contains(body, "crescent-bird") {
		t.Fatalf("searching the email did not list crescent-bird:\n%s", body)
	}
	a.handleKey(keyMsg("enter"))
	if a.prompt.target != "crescent-bird" {
		t.Fatalf("enter picked %q, want crescent-bird", a.prompt.target)
	}
	a.handleKey(keyMsg("enter"))
	if a.confirm == nil {
		t.Fatal("no confirmation")
	}
	q := a.confirm.prompt(a.repo, a.org())
	if !strings.Contains(q, "moon.Park") || !strings.Contains(q, "moon.park@acme.com") {
		t.Errorf("confirmation %q does not name the person, only the login", q)
	}
	if a.confirm.user != "crescent-bird" {
		t.Errorf("the write targets %q, want the login", a.confirm.user)
	}
}

// A login typed exactly wins over looser matches that sort ahead of it —
// "moon" must not become seungtae62 because his email contains it.
func TestAnExactLoginRanksFirst(t *testing.T) {
	a := pickerApp(t)
	typeKeys(a, "moon")
	a.handleKey(keyMsg("enter"))
	if a.prompt.target != "moon" {
		t.Errorf("enter picked %q, want the exact login moon", a.prompt.target)
	}
}

func TestArrowKeysChooseAmongCandidates(t *testing.T) {
	a := pickerApp(t)
	typeKeys(a, "moon")
	a.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	a.handleKey(keyMsg("enter"))
	if a.prompt.target == "moon" || a.prompt.target == "" {
		t.Errorf("down did not move off the first candidate: picked %q", a.prompt.target)
	}
}

// An outside collaborator is not an org member; the picker must not lock them
// out. With no match, enter sends the text as typed.
func TestALoginTheRosterDoesNotListIsSentAsTyped(t *testing.T) {
	a := pickerApp(t)
	typeKeys(a, "outsider-42")
	if body := plain(a.View()); !strings.Contains(body, `enter sends "outsider-42"`) {
		t.Errorf("the body does not say the text will be sent as typed:\n%s", body)
	}
	a.handleKey(keyMsg("enter"))
	if a.prompt.target != "outsider-42" {
		t.Errorf("picked %q, want the typed login", a.prompt.target)
	}
}

// Enter on an empty prompt cancels; it must not pick whoever sorts first.
func TestAnEmptyPickerPicksNobody(t *testing.T) {
	a := pickerApp(t)
	a.handleKey(keyMsg("enter"))
	if a.prompt.open() || a.confirm != nil {
		t.Errorf("enter on an empty prompt went on: prompt %+v confirm %+v", a.prompt, a.confirm)
	}
}

// A roster that cannot be fetched (a user-owned repo has no org) leaves the
// prompt as it was: a login typed by hand.
func TestARosterErrorStillAcceptsATypedLogin(t *testing.T) {
	a := collabApp(t, nil)
	a.handleKey(keyMsg("a"))
	a.Update(peopleMsg{err: fakeErr{}})
	if body := plain(a.View()); !strings.Contains(body, "Type the GitHub login") {
		t.Errorf("the error body does not say how to proceed:\n%s", body)
	}
	typeKeys(a, "bob")
	a.handleKey(keyMsg("enter"))
	if a.prompt.target != "bob" {
		t.Errorf("picked %q, want bob", a.prompt.target)
	}
}

// Someone already on the team is marked, not hidden: the reader looking for
// them should see they were found.
func TestTeamMembersAreMarkedInThePicker(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "platform", Permission: "push"}})
	a.handleKey(keyMsg("enter"))
	a.Update(membersMsg{slug: "platform", members: []gh.TeamMember{{Login: "crescent-bird"}}})
	a.handleKey(keyMsg("a"))
	a.Update(peopleMsg{people: roster})
	typeKeys(a, "moon.park")
	if body := plain(a.View()); !strings.Contains(body, "in team") {
		t.Errorf("an existing member is not marked:\n%s", body)
	}
	a.handleKey(keyMsg("enter"))
	if a.confirm == nil || !strings.Contains(a.confirm.prompt(a.repo, a.org()), "ORG change") {
		t.Fatalf("team add lost its org-wide warning: %+v", a.confirm)
	}
	if !strings.HasPrefix(a.confirm.prompt(a.repo, a.org()), "ORG change") {
		t.Error("the person label pushed the org-wide warning off the front")
	}
}

// The roster is fetched once per panel, not once per prompt.
func TestTheRosterIsFetchedOncePerPanel(t *testing.T) {
	a := pickerApp(t)
	a.handleKey(keyMsg("esc"))
	if cmd := a.handleKey(keyMsg("a")); cmd != nil {
		t.Error("reopening the prompt fetched the roster again")
	}
}
