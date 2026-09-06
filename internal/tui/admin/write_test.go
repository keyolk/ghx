package admin

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/gh"
)

// The admin panel was read-only, and not by design: internal/gh already had
// nine write operations and the TUI reached none of them. RemoveCollaborator
// even had its confirmation plumbing, with no key bound to open it — code that
// could not run.
//
// These fix the two properties that make the writes safe to have: nothing
// destructive happens without a y, and an action that leaves the repository
// says so.

func collabApp(t *testing.T, rows []gh.Collaborator) *App {
	t.Helper()
	a := testApp(t, 120, 24)
	deliver(a, dataMsg{cat: catCollaborators, collaborators: rows})
	return a
}

func teamApp(t *testing.T, rows []gh.Team) *App {
	t.Helper()
	a := testApp(t, 120, 24)
	a.active = catTeams
	deliver(a, dataMsg{cat: catTeams, teams: rows})
	return a
}

// Adding a collaborator asks for a login, then a level. A grant with no level
// silently means "pull", which is not something to infer from an empty field.
func TestAddingACollaboratorAsksForALoginThenAPermission(t *testing.T) {
	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "admin", Direct: true}})

	a.handleKey(keyMsg("a"))
	if a.prompt.kind != promptLogin {
		t.Fatalf("a did not open the login prompt: %+v", a.prompt)
	}
	typeKeys(a, "bob")
	a.handleKey(keyMsg("enter"))
	if a.prompt.kind != promptPermission {
		t.Fatalf("the login did not hand over to the permission prompt: %+v", a.prompt)
	}
	if a.prompt.target != "bob" {
		t.Errorf("permission prompt targets %q, want bob", a.prompt.target)
	}
	if a.confirm != nil {
		t.Error("a grant was confirmed before its level was picked")
	}

	a.handleKey(keyMsg("enter"))
	if a.confirm == nil {
		t.Fatal("picking a level did not open the confirmation")
	}
	if a.confirm.kind != actSetCollaborator || a.confirm.user != "bob" {
		t.Errorf("confirmation is %+v, want a grant for bob", *a.confirm)
	}
	if a.confirm.permission == "" {
		t.Error("the confirmed grant carries no permission level")
	}
}

// The prompt owns the keyboard while it is open. A login contains letters that
// are otherwise navigation keys, so typing "kim" would move the cursor and quit.
func TestTheLoginPromptOwnsTheKeyboard(t *testing.T) {
	a := collabApp(t, manyCollaborators(20))
	a.handleKey(keyMsg("a"))
	typeKeys(a, "kim")
	if a.prompt.value != "kim" {
		t.Errorf("prompt holds %q, want kim — the navigation keys ate it", a.prompt.value)
	}
	if a.pane.Cursor != 0 {
		t.Errorf("cursor moved to %d while a login was being typed", a.pane.Cursor)
	}
}

// Nothing destructive happens without an explicit y. Anything else is ignored
// rather than treated as consent.
func TestOnlyYConfirmsAWrite(t *testing.T) {
	for _, key := range []string{"n", "esc", "j", "d", "enter", "a"} {
		a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}})
		a.handleKey(keyMsg("d"))
		if a.confirm == nil {
			t.Fatal("d did not open a confirmation")
		}
		a.handleKey(keyMsg(key))
		if a.busy {
			t.Errorf("%q ran the write", key)
		}
	}

	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}})
	a.handleKey(keyMsg("d"))
	a.handleKey(keyMsg("y"))
	if !a.busy {
		t.Error("y did not run the write")
	}
}

// A team-derived row has nothing on the repository to change: the access comes
// from the team's grant, so both endpoints would answer about a collaborator
// who was never added here. Measured on sendbird/ops-k8s, that is 149 of 150
// rows — refusing with the reason is what keeps the row's "via team" marker
// actionable rather than decorative.
func TestATeamDerivedCollaboratorCannotBeChangedHere(t *testing.T) {
	for _, key := range []string{"d", "p"} {
		a := collabApp(t, []gh.Collaborator{{Login: "carol", RoleName: "write", Direct: false}})
		cmd, handled := a.handleWriteKey(key)
		if !handled {
			t.Fatalf("%q was not handled", key)
		}
		if a.confirm != nil {
			t.Errorf("%q opened a confirmation for a team-derived row", key)
		}
		if a.prompt.open() {
			t.Errorf("%q opened a prompt for a team-derived row", key)
		}
		if cmd == nil {
			t.Fatalf("%q said nothing about why it did nothing", key)
		}
		msg, ok := cmd().(toastMsg)
		if !ok {
			t.Fatalf("%q produced %T, want a toast explaining the refusal", key, cmd())
		}
		if !strings.Contains(msg.text, "through a team") {
			t.Errorf("the refusal does not name the reason: %q", msg.text)
		}
	}
}

// Two of these writes leave the repository. Adding or removing a team member is
// an organization change that alters access to every repository the team can
// reach, and the reader is looking at a panel titled with one repo — so the
// prompt names the org, says what it reaches, and is marked in the footer
// rather than only worded differently.
func TestTeamMembershipConfirmationsSayTheyAreOrgWide(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "platform", Permission: "push"}})
	a.handleKey(keyMsg("enter"))
	a.Update(membersMsg{slug: "platform", members: []gh.TeamMember{{Login: "dave"}}})

	a.handleKey(keyMsg("d"))
	if a.confirm == nil {
		t.Fatal("d did not open a confirmation on a team member")
	}
	if !a.confirm.orgWide() {
		t.Error("removing a team member is not marked org-wide")
	}
	text := a.confirm.prompt(a.repo, a.org())
	for _, want := range []string{"ORG", "acme/platform", "EVERY repo"} {
		if !strings.Contains(text, want) {
			t.Errorf("the prompt does not say %q:\n%s", want, text)
		}
	}
	if foot := plain(a.footer()); !strings.Contains(foot, "ORG change") {
		t.Errorf("the footer does not mark an org-wide action:\n%s", foot)
	}

	// The warning has to survive the width it will actually be read at. The
	// footer truncates, and an earlier draft lost "EVERY repo that team
	// reaches" at 100 columns — leaving a prompt that named a team and then
	// stopped, which reads as ordinary. That is why the warning leads the
	// sentence rather than following the description of the change.
	for _, w := range []int{80, 100} {
		a.width = w
		foot := plain(a.footer())
		for _, want := range []string{"ORG change", "EVERY repo"} {
			if !strings.Contains(foot, want) {
				t.Errorf("at %d columns the warning lost %q:\n%s", w, want, foot)
			}
		}
	}
}

// A repo-scoped write must not carry the org warning: a marker on everything
// marks nothing.
func TestRepoScopedWritesAreNotMarkedOrgWide(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "platform", Permission: "push"}})
	a.handleKey(keyMsg("d"))
	if a.confirm == nil {
		t.Fatal("d did not open a confirmation on a team row")
	}
	if a.confirm.orgWide() {
		t.Error("revoking a team's access to this repo was marked org-wide")
	}
	if foot := plain(a.footer()); strings.Contains(foot, "ORG change") {
		t.Errorf("a repo-scoped action carries the org warning:\n%s", foot)
	}
}

// The cursor indexes the filtered rows. Indexing the source would revoke
// whoever happened to sit at that position before the filter — the same
// hazard the PR list's selection has.
func TestAWriteActsOnTheFilteredRowUnderTheCursor(t *testing.T) {
	a := collabApp(t, []gh.Collaborator{
		{Login: "alice", RoleName: "write", Direct: true},
		{Login: "bob", RoleName: "write", Direct: true},
		{Login: "carol", RoleName: "write", Direct: true},
	})
	a.handleKey(keyMsg("/"))
	typeKeys(a, "carol")
	a.handleKey(keyMsg("enter"))
	a.handleKey(keyMsg("d"))
	if a.confirm == nil {
		t.Fatal("d did not open a confirmation")
	}
	if a.confirm.user != "carol" {
		t.Errorf("the write targets %q, want carol — the cursor indexed the "+
			"unfiltered list", a.confirm.user)
	}
}

// A write whose row does not change is indistinguishable from one that
// silently failed: the toast is four seconds of text over a list still showing
// the old state. So reloading is part of the action.
func TestASuccessfulWriteReloadsTheList(t *testing.T) {
	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}})
	a.busy = true
	_, cmd := a.Update(writeDoneMsg{text: "removed alice", reload: catCollaborators})
	if a.busy {
		t.Error("busy stayed set after the write settled")
	}
	if cmd == nil {
		t.Error("a successful write did not re-fetch, so the row it changed " +
			"stays on screen as it was")
	}
}

// A team-membership write invalidates the cached member list, which is what the
// drill-down renders from. Without dropping it the re-fetch is served from the
// map and the removed member stays on screen.
func TestATeamWriteDropsTheCachedMemberList(t *testing.T) {
	a := teamApp(t, []gh.Team{{Slug: "platform", Permission: "push"}})
	a.teamSlug = "platform"
	a.teamMembers["platform"] = []gh.TeamMember{{Login: "dave"}}

	a.Update(writeDoneMsg{text: "removed dave", reloadTeam: "platform"})
	if _, cached := a.teamMembers["platform"]; cached {
		t.Error("the stale member list survived the write")
	}
}

// A failed write keeps the list on screen. Replacing it with an error page
// hides exactly the state needed to decide what to do next.
func TestAFailedWriteKeepsTheRosterOnScreen(t *testing.T) {
	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}})
	a.busy = true
	a.Update(writeDoneMsg{err: errFake})
	if a.err != nil {
		t.Error("a failed write replaced the roster with an error page")
	}
	if !strings.Contains(plain(a.footer()), "error") {
		t.Errorf("the failure is not reported anywhere:\n%s", plain(a.footer()))
	}
	if len(a.collaborators) != 1 {
		t.Error("the roster was emptied by a failed write")
	}
}

// The write keys are listed only where they do something. A key advertised on a
// read-only category is a key someone presses and gets nothing from, which
// reads as a broken binding rather than an inapplicable one.
func TestTheFooterOnlyOffersKeysThatApply(t *testing.T) {
	a := testApp(t, 160, 24)
	a.active = catBranches
	deliver(a, dataMsg{cat: catBranches, branches: []gh.Branch{{Name: "main"}}})
	foot := plain(a.footer())
	for _, key := range []string{"add", "remove", "permission"} {
		if strings.Contains(foot, key) {
			t.Errorf("a read-only category offers %q:\n%s", key, foot)
		}
	}

	b := collabApp(t, []gh.Collaborator{{Login: "alice", Direct: true}})
	b.width = 160
	foot = plain(b.footer())
	for _, key := range []string{"add", "remove", "permission"} {
		if !strings.Contains(foot, key) {
			t.Errorf("People does not offer %q:\n%s", key, foot)
		}
	}
}

// An error is not a list. Measured against sendbird/ops-k8s without admin
// rights, which answers the roster with HTTP 403: the pane said "Must have push
// access" and the footer went on offering a/p/d — keys with no row to act on
// and no permission to act with.
func TestTheFooterOffersNoWritesOverAnError(t *testing.T) {
	a := testApp(t, 160, 24)
	deliver(a, dataMsg{cat: catCollaborators, err: errFake})
	foot := plain(a.footer())
	for _, key := range []string{"add", "remove", "permission"} {
		if strings.Contains(foot, key) {
			t.Errorf("an error pane offers %q:\n%s", key, foot)
		}
	}
	if !strings.Contains(foot, "category") {
		t.Errorf("navigation was dropped along with the writes:\n%s", foot)
	}
}

// An empty list is the opposite case, and conflating the two is the fix's own
// trap: a repository with no collaborators is exactly where `a` is wanted. Only
// the keys that need a row under the cursor go away.
func TestAnEmptyListStillOffersAdd(t *testing.T) {
	a := testApp(t, 160, 24)
	deliver(a, dataMsg{cat: catCollaborators, collaborators: nil})
	foot := plain(a.footer())
	if !strings.Contains(foot, "add") {
		t.Errorf("an empty roster cannot be added to:\n%s", foot)
	}
	for _, key := range []string{"remove", "permission"} {
		if strings.Contains(foot, key) {
			t.Errorf("an empty roster offers %q, which needs a row:\n%s", key, foot)
		}
	}
}

// The permission prompt shows the whole set with the selection marked, rather
// than one value that changes as tab is pressed: seeing them is what makes
// "which of these is narrower" answerable without leaving the prompt.
func TestThePermissionPromptShowsEveryLevel(t *testing.T) {
	a := collabApp(t, []gh.Collaborator{{Login: "alice", RoleName: "write", Direct: true}})
	a.handleKey(keyMsg("p"))
	if a.prompt.kind != promptPermission {
		t.Fatalf("p did not open the permission prompt: %+v", a.prompt)
	}
	bar := plain(a.renderHeader())
	for _, level := range gh.CollaboratorPermissions {
		if !strings.Contains(bar, level) {
			t.Errorf("the prompt does not offer %q:\n%s", level, bar)
		}
	}
	first := a.prompt.selected()
	a.handleKey(keyMsg("tab"))
	if a.prompt.selected() == first {
		t.Error("tab did not move the selection")
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "http 403: forbidden" }

var errFake = fakeErr{}

// The footer is a description, not the gate. Hiding a key while leaving it
// bound is the worse of the two failures: the panel would look read-only and
// act otherwise, so pressing d over an error pane must also do nothing.
func TestWriteKeysDoNothingOverAnErrorPane(t *testing.T) {
	for _, key := range []string{"a", "d", "p"} {
		a := testApp(t, 120, 24)
		deliver(a, dataMsg{cat: catCollaborators, err: errFake})
		a.handleKey(keyMsg(key))
		if a.confirm != nil {
			t.Errorf("%q opened a confirmation over an error pane: %+v", key, *a.confirm)
		}
		if a.prompt.open() {
			t.Errorf("%q opened a prompt over an error pane: %+v", key, a.prompt)
		}
	}
}
