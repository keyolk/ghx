package gh

import (
	"strings"
	"testing"
)

// gh subcommands are multi-word, so --repo has to land after the subcommand
// words, not after the first one: `gh pr --repo X list` is rejected outright.
func TestAppendWithRepoPlacement(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			"pr list with flags",
			[]string{"pr", "list", "--state", "open", "--limit", "50"},
			"pr list --repo o/r --state open --limit 50",
		},
		{
			"pr view with a number",
			[]string{"pr", "view", "830", "--json", "number"},
			"pr view 830 --repo o/r --json number",
		},
		{
			"pr diff, no flags",
			[]string{"pr", "diff", "830"},
			"pr diff 830 --repo o/r",
		},
		{
			"single word subcommand",
			[]string{"browse"},
			"browse --repo o/r",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(appendWithRepo(c.args, "o/r"), " ")
			if got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestAppendWithRepoEmptyArgs(t *testing.T) {
	if got := appendWithRepo(nil, "o/r"); len(got) != 0 {
		t.Errorf("empty args should stay empty, got %v", got)
	}
}

// Qualifiers gh models as flags must be lifted out of the query so the listing
// stays on REST; the rest falls through to --search (GraphQL, scarcer budget).
func TestListQueryArgsSplitsFlagsFromSearch(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		wantFlags    string
		wantLeftover string
	}{
		{"state only", "state:open", "--state open", ""},
		{"is: alias", "is:open", "--state open", ""},
		{"draft", "state:draft", "--draft", ""},
		{"author and state", "author:@me state:open", "--author @me --state open", ""},
		{
			"review-requested has no flag",
			"review-requested:@me state:open",
			"--state open",
			"review-requested:@me",
		},
		{"free text passes through", "state:open flaky test", "--state open", "flaky test"},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			flags, leftover := listQueryArgs(c.query)
			if got := strings.Join(flags, " "); got != c.wantFlags {
				t.Errorf("flags = %q, want %q", got, c.wantFlags)
			}
			if leftover != c.wantLeftover {
				t.Errorf("leftover = %q, want %q", leftover, c.wantLeftover)
			}
		})
	}
}

// gh search prs rejects some qualifiers as free text, so the same lifting has to
// happen for the cross-repo path.
func TestSearchQueryArgs(t *testing.T) {
	got := strings.Join(searchQueryArgs("review-requested:@me state:open"), " ")
	// Free text must precede flags for gh to read it as the query.
	if !strings.Contains(got, "--review-requested @me") {
		t.Errorf("review-requested should become a flag: %q", got)
	}
	if !strings.Contains(got, "--state open") {
		t.Errorf("state should become a flag: %q", got)
	}
}

func TestSplitSlug(t *testing.T) {
	owner, repo, ok := splitSlug("keyolk/ghx")
	if !ok || owner != "keyolk" || repo != "ghx" {
		t.Errorf("got (%q, %q, %v)", owner, repo, ok)
	}
	if _, _, ok := splitSlug("no-slash"); ok {
		t.Error("a slug without a slash must not parse")
	}
	if _, _, ok := splitSlug("/empty"); ok {
		t.Error("an empty owner must not parse")
	}
}

func TestParseGitHubTime(t *testing.T) {
	if _, err := parseGitHubTime("2026-07-25T15:06:44Z"); err != nil {
		t.Errorf("RFC3339 should parse: %v", err)
	}
	if _, err := parseGitHubTime(""); err == nil {
		t.Error("empty timestamp should error rather than yield the zero time silently")
	}
}

// An archived repository is read-only, so its PRs can be listed but not acted
// on. They belong out of the cross-repo queues entirely.
func TestWithoutArchived(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"plain queue", "review-requested:@me state:open", "review-requested:@me state:open archived:false"},
		{"empty", "", "archived:false"},
		// An explicit archived: is the user asking for something specific.
		{"explicit false kept once", "author:@me archived:false", "author:@me archived:false"},
		{"explicit true honored", "author:@me archived:true", "author:@me archived:true"},
		{"case insensitive", "author:@me Archived:true", "author:@me Archived:true"},
		// A repo-scoped query is the REST fallback for a pinned source: the tab
		// names that repository, so it must list its PRs archived or not.
		{"repo scoped untouched", "repo:o/r state:open", "repo:o/r state:open"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withoutArchived(c.query); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// --archived takes {true|false} and only parses when the value is joined with
// an =; passed as a separate argument gh reads it as the free-text query and
// silently searches for the word "false".
func TestSearchQueryArgsArchived(t *testing.T) {
	got := strings.Join(searchQueryArgs("author:@me archived:false"), " ")
	if !strings.Contains(got, "--archived=false") {
		t.Errorf("archived should become a joined flag: %q", got)
	}
	// A value gh cannot parse must stay free text rather than become
	// `--archived=maybe`, which gh rejects and which would empty the tab.
	got = strings.Join(searchQueryArgs("archived:maybe"), " ")
	if got != "archived:maybe" {
		t.Errorf("unparseable value should pass through as text: %q", got)
	}
}
