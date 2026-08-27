package admin

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// The admin panel had three faults that compound on a real org repository: a
// roster of 150 people with no indication that 149 of them are there through a
// team, a frame that pushed its own title and tab strip off the top once the
// list outgrew the terminal, and no way to search either.

func testApp(t *testing.T, w, h int) *App {
	t.Helper()
	a := NewApp(gh.NewClient(0), "acme/ops")
	a.width, a.height = w, h
	return a
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// deliver hands the model a fetch result the way the real command would, so
// the loading state clears and the category renders.
func deliver(a *App, msg dataMsg) {
	a.Update(msg)
}

// typeKeys sends each rune as its own key message, the way a terminal does.
func typeKeys(a *App, s string) {
	for _, r := range s {
		a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func manyCollaborators(n int) []gh.Collaborator {
	out := make([]gh.Collaborator, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, gh.Collaborator{
			Login:    fmt.Sprintf("user%03d", i),
			RoleName: "write",
		})
	}
	return out
}

// The frame must never be taller than the terminal. bubbletea keeps the LAST
// height lines of an overflowing frame, so an extra row costs the title and the
// tab strip — the two rows that say where you are and how to leave.
func TestFrameFitsTerminalWithLongList(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 20}, {160, 30}, {250, 62}, {80, 10}} {
		a := testApp(t, size.w, size.h)
		a.collaborators = manyCollaborators(150)

		frame := a.View()
		if got := len(strings.Split(frame, "\n")); got != size.h {
			t.Errorf("%dx%d: frame is %d rows, want exactly %d",
				size.w, size.h, got, size.h)
		}

		rows := strings.Split(frame, "\n")
		if !strings.Contains(plain(rows[0]), "ghx admin") {
			t.Errorf("%dx%d: first row is %q, want the title",
				size.w, size.h, plain(rows[0]))
		}
		if !strings.Contains(plain(rows[1]), "People") {
			t.Errorf("%dx%d: second row is %q, want the tab strip",
				size.w, size.h, plain(rows[1]))
		}
	}
}

// A list longer than the pane has to scroll inside its own rows, which means
// the cursor stays visible as it moves past the bottom.
func TestCursorStaysVisibleWhileScrolling(t *testing.T) {
	a := testApp(t, 120, 20)
	a.collaborators = manyCollaborators(150)

	for i := 0; i < 40; i++ {
		a.handleKey(keyMsg("j"))
	}
	if a.pane.Cursor != 40 {
		t.Fatalf("cursor = %d after 40 j, want 40", a.pane.Cursor)
	}
	if !strings.Contains(plain(a.View()), "user040") {
		t.Error("the cursor row is not in the frame after scrolling to it")
	}

	// G lands on the last row, which must also be on screen.
	a.handleKey(keyMsg("G"))
	if !strings.Contains(plain(a.View()), "user149") {
		t.Error("G left the last row off screen")
	}
	if len(strings.Split(a.View(), "\n")) != 20 {
		t.Error("scrolling to the end grew the frame")
	}
}

// Nothing distinguished a real collaborator from someone who is only here
// through a team, so a repo whose access is all team-based read as 150
// individual grants — none of which can be revoked from this panel.
func TestCollaboratorRowsSayWhereAccessComesFrom(t *testing.T) {
	a := testApp(t, 120, 20)
	a.collaborators = []gh.Collaborator{
		{Login: "direct-user", RoleName: "admin", Direct: true},
		{Login: "team-user", RoleName: "write"},
	}

	got := plain(a.View())
	if !strings.Contains(got, "direct-user") || !strings.Contains(got, "direct") {
		t.Errorf("a directly-granted user is not marked as such:\n%s", got)
	}
	if !strings.Contains(got, "via team") {
		t.Errorf("a team-derived user is not marked as such:\n%s", got)
	}
}

// Teams are a category of their own, since the collaborators endpoint discards
// the structure entirely.
func TestTeamsTabListsTeamsAndDrillsIntoMembers(t *testing.T) {
	a := testApp(t, 120, 20)
	a.handleKey(keyMsg("2"))
	deliver(a, dataMsg{cat: catTeams, teams: []gh.Team{
		{Slug: "team_core_platform", Permission: "admin"},
		{Slug: "team_seceng", Permission: "push"},
	}})

	got := plain(a.View())
	if !strings.Contains(got, "team_core_platform") || !strings.Contains(got, "admin") {
		t.Errorf("the teams tab does not list teams with their permission:\n%s", got)
	}

	// Enter drills in. The members are seeded so no fetch is needed.
	a.teamMembers["team_core_platform"] = []gh.TeamMember{{Login: "gavin-jeong"}}
	a.handleKey(keyMsg("enter"))
	if a.teamSlug != "team_core_platform" {
		t.Fatalf("enter drilled into %q, want team_core_platform", a.teamSlug)
	}
	if !strings.Contains(plain(a.View()), "gavin-jeong") {
		t.Error("the member list is not shown after drilling in")
	}

	// Esc steps back out to the team list rather than quitting the category.
	a.handleKey(keyMsg("esc"))
	if a.teamSlug != "" {
		t.Errorf("esc left the drill-down at %q", a.teamSlug)
	}
	if !strings.Contains(plain(a.View()), "team_seceng") {
		t.Error("esc did not return to the team list")
	}
}

// Search is the only way through a list this long, and the query has to own the
// keyboard while it is open — a name containing j or q must not navigate.
func TestSearchFiltersAndOwnsTheKeyboard(t *testing.T) {
	a := testApp(t, 120, 20)
	a.collaborators = []gh.Collaborator{
		{Login: "jinsekim", RoleName: "write"},
		{Login: "gavin-jeong", RoleName: "admin"},
		{Login: "unrelated", RoleName: "read"},
	}

	a.handleKey(keyMsg("/"))
	if !a.searching {
		t.Fatal("/ did not open the search prompt")
	}
	typeKeys(a, "jeong")

	if a.pane.Cursor != 0 {
		t.Errorf("typing moved the cursor to %d; j should be a query character", a.pane.Cursor)
	}
	got := plain(a.View())
	if !strings.Contains(got, "gavin-jeong") {
		t.Errorf("the matching row is missing:\n%s", got)
	}
	if strings.Contains(got, "unrelated") {
		t.Errorf("a non-matching row survived the filter:\n%s", got)
	}
	if !strings.Contains(got, "Search: jeong") {
		t.Errorf("the prompt does not show the query:\n%s", got)
	}

	// Enter commits: the filter stays, the keyboard comes back.
	a.handleKey(keyMsg("enter"))
	if a.searching {
		t.Error("enter left the prompt open")
	}
	if a.query != "jeong" {
		t.Errorf("query = %q after enter, want it kept", a.query)
	}

	// Esc clears the filter and restores the full list.
	a.handleKey(keyMsg("esc"))
	if a.query != "" {
		t.Errorf("esc left query %q", a.query)
	}
	if !strings.Contains(plain(a.View()), "unrelated") {
		t.Error("esc did not restore the unfiltered list")
	}
}

// The cursor indexes the filtered rows. Indexing the source instead would act
// on whichever row sat at that position before the filter — which for the
// destructive actions means the wrong person.
func TestActionsTargetTheFilteredRow(t *testing.T) {
	a := testApp(t, 120, 20)
	a.teams = []gh.Team{
		{Slug: "aaa-first", Permission: "pull"},
		{Slug: "target-team", Permission: "admin"},
		{Slug: "zzz-last", Permission: "push"},
	}
	a.handleKey(keyMsg("2"))
	deliver(a, dataMsg{cat: catTeams, teams: a.teams})

	a.handleKey(keyMsg("/"))
	typeKeys(a, "target")
	a.handleKey(keyMsg("enter"))

	// The cursor is at 0 of the filtered list, which is target-team — not
	// aaa-first, which is row 0 of the source.
	a.teamMembers["target-team"] = []gh.TeamMember{{Login: "someone"}}
	a.handleKey(keyMsg("enter"))
	if a.teamSlug != "target-team" {
		t.Errorf("enter drilled into %q, want target-team", a.teamSlug)
	}
}

// An empty result means two different things, and they call for opposite
// responses: a repository with no teams is a fact, an over-narrow query is one
// keystroke from being right.
func TestEmptyFilterIsDistinguishedFromEmptySource(t *testing.T) {
	a := testApp(t, 120, 20)
	a.handleKey(keyMsg("2"))
	deliver(a, dataMsg{cat: catTeams})

	if got := plain(a.View()); !strings.Contains(got, "No teams") {
		t.Errorf("an empty source should say so:\n%s", got)
	}

	deliver(a, dataMsg{cat: catTeams, teams: []gh.Team{
		{Slug: "team_core_platform", Permission: "admin"},
	}})
	a.handleKey(keyMsg("/"))
	typeKeys(a, "nomatch")
	if got := plain(a.View()); !strings.Contains(got, "Nothing matches") {
		t.Errorf("an over-narrow filter should say so:\n%s", got)
	}
}

// A filter that empties the list must not leave the offset past the end, or the
// pane renders blank for rows that exist once the filter widens again.
func TestOffsetRecoversWhenTheListShrinks(t *testing.T) {
	a := testApp(t, 120, 20)
	a.collaborators = manyCollaborators(150)
	a.handleKey(keyMsg("G")) // cursor and offset far down

	a.handleKey(keyMsg("/"))
	typeKeys(a, "user001")
	got := plain(a.View())
	if !strings.Contains(got, "user001") {
		t.Errorf("the single match is not rendered — offset stranded past the end:\n%s", got)
	}
}

// ListPane's own contract, exercised directly: the window must contain the
// cursor and be exactly the requested height.
func TestListPaneWindowsAroundTheCursor(t *testing.T) {
	rows := make([]string, 100)
	for i := range rows {
		rows[i] = fmt.Sprintf("row%03d", i)
	}
	var p tui.ListPane
	p.Cursor = 80

	out := p.RenderList(rows, 40, 10)
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("rendered %d rows, want 10", len(lines))
	}
	if !strings.Contains(plain(out), "row080") {
		t.Errorf("the cursor row is outside the window:\n%s", plain(out))
	}

	// A short list pads rather than letting the caller's footer float up.
	out = p.RenderList(rows[:3], 40, 10)
	if got := len(strings.Split(out, "\n")); got != 10 {
		t.Errorf("a 3-row list rendered %d rows, want 10 padded", got)
	}
}
