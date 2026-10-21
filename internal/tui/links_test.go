package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/link"
	"github.com/keyolk/tuikit/tuitest"

	"github.com/keyolk/ghx/internal/pr"
)

// Links (E11): labels that stand for a URL are OSC 8 hyperlinks, and `o`
// opens the selected row's URL. Every position comes from the rendered View.

// linked is label as an OSC 8 hyperlink to url, exactly as link.Wrap emits it.
func linked(url, label string) string {
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

// stubLauncher records the URLs link.Open hands to the browser, so no test
// ever opens a real one.
func stubLauncher(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := link.Launcher
	link.Launcher = func(u string) error {
		got = append(got, u)
		return nil
	}
	t.Cleanup(func() { link.Launcher = old })
	return &got
}

// pressKey sends a key through Update and runs what it returned, feeding the
// messages back, as the Bubble Tea loop would.
func pressKey(t *testing.T, a *App, k string) []tea.Msg {
	t.Helper()
	_, cmd := a.Update(keyMsg(k))
	msgs := tuitest.Drain(cmd, 0)
	for _, m := range msgs {
		a.Update(m)
	}
	return msgs
}

func linkedListApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	rows := rowsFor(1, 2, 3)
	rows[0].URL = "https://github.com/acme/one/pull/1"
	rows[1].URL = "javascript:alert(1)"
	a := listApp(t, rows)
	a.width, a.height = 120, 30
	a.list.resize(120, 30)
	a.View()
	return a
}

func TestPRTitleIsAHyperlinkInTheListAndThePreview(t *testing.T) {
	a := linkedListApp(t)
	want := linked("https://github.com/acme/one/pull/1", "pull request 1")
	if !strings.Contains(a.View(), want) {
		t.Fatalf("the PR title should link in its list row:\n%q", a.View())
	}
	a.list.pane.Show = true
	a.View()
	if !strings.Contains(a.View(), want) {
		t.Fatalf("the preview's title should link to the PR:\n%q", a.View())
	}
	a.list.pane.Show = false
	// Moving the cursor off the row draws it unselected; it still links.
	pressKey(t, a, "j")
	if !strings.Contains(a.View(), want) {
		t.Fatal("an unselected row lost its link")
	}
	for i, line := range strings.Split(a.View(), "\n") {
		if w := ansi.StringWidth(line); w > a.width {
			t.Fatalf("line %d is %d cells wide, pane is %d: OSC 8 must take no width", i, w, a.width)
		}
	}
}

func TestANonHTTPURLIsNeitherLinkedNorOpened(t *testing.T) {
	capture := fakeOpener(t)
	a := linkedListApp(t)
	if strings.Contains(a.View(), "javascript:") {
		t.Fatal("a javascript: URL from the data was linked")
	}
	if !onScreen(a, "pull request 2") {
		t.Fatal("the label of a bad URL must still show")
	}
	pressKey(t, a, "j") // onto #2, whose URL is javascript:
	_, cmd := a.Update(keyMsg("o"))
	runCmds(t, a, cmd)
	if got := openedURLs(t, capture); len(got) != 0 {
		t.Fatalf("the browser action opened %q", got)
	}
}

func TestCheckNameIsAHyperlinkAndOOpensTheSelectedCheck(t *testing.T) {
	opened := stubLauncher(t)
	a := detailApp(t)
	a.detail.setTab(tabChecks)
	lint := "https://github.com/acme/one/actions/runs/33/job/44"
	if !strings.Contains(a.View(), linked(lint, "lint")) {
		t.Fatalf("the check name should link to its page:\n%q", a.View())
	}
	clickText(t, a, "lint")
	a.View()
	msgs := pressKey(t, a, "o")
	if len(*opened) != 1 || (*opened)[0] != lint {
		t.Fatalf("o opened %q, want exactly %q", *opened, lint)
	}
	if len(msgs) != 1 {
		t.Fatalf("o returned %d messages, want one OpenedMsg: %#v", len(msgs), msgs)
	}
	if m, ok := msgs[0].(link.OpenedMsg); !ok || m.URL != lint || m.Err != nil {
		t.Fatalf("o reported %#v, want OpenedMsg for %s", msgs[0], lint)
	}
	if !onScreen(a, "opened "+lint) {
		t.Fatal("the result of o should show as a toast")
	}
}

func TestOOnACheckWithoutALinkOpensNothingAndSaysSo(t *testing.T) {
	opened := stubLauncher(t)
	a := detailApp(t)
	a.detail.setTab(tabChecks)
	clickText(t, a, "unit")
	a.View()
	pressKey(t, a, "o")
	if len(*opened) != 0 {
		t.Fatalf("o on a check without a URL opened %q", *opened)
	}
	if !onScreen(a, "no link for this check") {
		t.Fatal("o on a check without a URL should say so")
	}
}

func TestPRCommentLinksAndOOpensItButAThreadSaysItHasNone(t *testing.T) {
	opened := stubLauncher(t)
	a := detailApp(t)
	u := "https://github.com/acme/one/pull/7#issuecomment-1"
	a.detail.comments.setConversations([]pr.Conversation{
		{ID: "C1", Kind: "comment", Body: "a pull request level remark", Author: pr.User{Login: "bot"}, URL: u},
	})
	a.detail.setTab(tabComments)
	a.View()
	label := conversationKindLabel("comment")
	if !strings.Contains(a.View(), linked(u, label)) {
		t.Fatalf("the PR comment's label should link to it:\n%q", a.View())
	}
	clickText(t, a, "a pull request level remark")
	a.View()
	pressKey(t, a, "o")
	if len(*opened) != 1 || (*opened)[0] != u {
		t.Fatalf("o opened %q, want %q", *opened, u)
	}

	clickText(t, a, "app.go")
	a.View()
	pressKey(t, a, "o")
	if len(*opened) != 1 {
		t.Fatalf("o on a review thread (no URL) opened %q", (*opened)[1:])
	}
	if !onScreen(a, "no link for this review thread") {
		t.Fatal("o on a review thread should say it has no link")
	}
}

func TestOpenLinkIsInHelpForChecksAndComments(t *testing.T) {
	for _, tab := range []detailTabKind{tabChecks, tabComments} {
		a := detailApp(t)
		a.detail.setTab(tab)
		a.handleKey(keyMsg("?"))
		// Every line of the help the overlay scrolls through, not only the
		// first page of it.
		help := ansi.Strip(strings.Join(keys.HelpLines(a.helpSections(), 100, keys.HelpStyle{Names: keyNames}), "\n"))
		if !strings.Contains(help, "open link") {
			t.Fatalf("tab %v: help does not list o (open link):\n%s", tab, help)
		}
	}
}

func TestOverviewTitleIsAHyperlink(t *testing.T) {
	a := detailApp(t)
	u := "https://github.com/acme/one/pull/7"
	a.detail.detail.URL = u
	a.detail.setTab(tabOverview)
	if !strings.Contains(a.View(), linked(u, "a pull request")) {
		t.Fatalf("the Overview title should link to the PR:\n%q", a.View())
	}
}
