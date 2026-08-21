package gh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// fakeGH installs a stub `gh` on PATH. The script receives the full argv, so a
// case pattern can distinguish the GraphQL call from the REST endpoint that
// stands in for it.
func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Deciding whether to retry over REST is the whole fallback: retrying a
// genuinely broken request just makes the real error take twice as long, and
// not retrying a GraphQL refusal is the outage this exists to prevent.
func TestIsGraphQLUnavailableSeparatesRefusalsFromRealErrors(t *testing.T) {
	retry := []string{
		`gh api graphql: {"errors":[{"type":"RATE_LIMITED"}],"message":"API rate limit exceeded"}`,
		`gh search prs: GraphQL: API rate limit exceeded (graphql_rate_limit)`,
		"gh api graphql: GraphQL: Resource not accessible by personal access token",
		"gh api graphql: Resource not accessible by integration",
		"gh pr list: GraphQL: rate limit exceeded",
		"gh api graphql: GraphQL: Forbidden",
		"gh search prs: You have exceeded a secondary rate limit and your request was submitted too quickly",
	}
	for _, msg := range retry {
		if !isGraphQLUnavailable(errors.New(msg)) {
			t.Errorf("should fall back to REST: %q", msg)
		}
	}

	keep := []string{
		"gh pr view: GraphQL: Could not resolve to a PullRequest with the number 999",
		"gh pr list: could not determine base repository",
		"gh api graphql: dial tcp: lookup api.github.com: no such host",
		"gh pr review: GraphQL: Can not approve your own pull request",
		"gh api: HTTP 404: Not Found",
		"gh pr merge: GraphQL: Pull request is not mergeable",
	}
	for _, msg := range keep {
		if isGraphQLUnavailable(errors.New(msg)) {
			t.Errorf("REST would fail the same way; should not retry: %q", msg)
		}
	}
	if isGraphQLUnavailable(nil) {
		t.Error("a nil error is not a GraphQL refusal")
	}
}

// The cross-repo tabs are the first thing to run out of GraphQL search budget,
// so they are the first thing that has to survive it.
func TestSearchPRsFallsBackToRESTSearch(t *testing.T) {
	fakeGH(t, `
case "$1 $2" in
  "search prs")
    echo "GraphQL: API rate limit exceeded (graphql_rate_limit)" >&2
    exit 1
    ;;
  "api search/issues"*)
    printf '%s\n' '{"items":[{"node_id":"PR_x","number":7,"title":"fallback","state":"open","draft":true,"html_url":"https://github.com/acme/one/pull/7","updated_at":"2026-08-05T00:00:00Z","user":{"login":"someone"},"repository_url":"https://api.github.com/repos/acme/one","pull_request":{"merged_at":null}}]}'
    ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	got, err := NewClient(0).SearchPRs(context.Background(), "review-requested:@me state:open", 50)
	if err != nil {
		t.Fatalf("SearchPRs should have fallen back: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d PRs, want 1", len(got))
	}
	if got[0].Number != 7 || got[0].Title != "fallback" {
		t.Errorf("got %#v", got[0])
	}
	// Without the repository every later call for this PR has nothing to scope
	// to, and search/issues carries it only inside repository_url.
	if got[0].Repo != "acme/one" {
		t.Errorf("repo = %q, want acme/one — parsed from repository_url", got[0].Repo)
	}
	if !got[0].IsDraft {
		t.Error("the REST path dropped the draft flag")
	}
	if got[0].ID != "PR_x" {
		t.Errorf("node id = %q; without it the status enrichment cannot batch", got[0].ID)
	}
}

// An error REST cannot fix must surface as itself, not as a second failure.
func TestSearchPRsDoesNotFallBackOnOrdinaryErrors(t *testing.T) {
	fakeGH(t, `
case "$1 $2" in
  "search prs") echo "could not determine base repository" >&2; exit 1 ;;
  *) echo "REST must not be reached" >&2; exit 9 ;;
esac
`)
	_, err := NewClient(0).SearchPRs(context.Background(), "state:open", 50)
	if err == nil {
		t.Fatal("expected the original error")
	}
	if !strings.Contains(err.Error(), "base repository") {
		t.Errorf("the original error was replaced: %v", err)
	}
}

// A merged PR reports state "closed" over REST. Taking that at face value would
// blank the M marker on every merged row.
func TestRESTSearchMapsMergedState(t *testing.T) {
	fakeGH(t, `
case "$1 $2" in
  "search prs") echo "GraphQL: API rate limit exceeded (graphql_rate_limit)" >&2; exit 1 ;;
  "api search/issues"*)
    printf '%s\n' '{"items":[{"node_id":"PR_m","number":9,"title":"merged","state":"closed","html_url":"u","updated_at":"2026-08-05T00:00:00Z","user":{"login":"a"},"repository_url":"https://api.github.com/repos/acme/one","pull_request":{"merged_at":"2026-08-05T01:00:00Z"}}]}'
    ;;
esac
`)
	got, err := NewClient(0).SearchPRs(context.Background(), "state:merged", 50)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].State != "MERGED" {
		t.Errorf("state = %q, want MERGED", got[0].State)
	}
}

// The markers are the reason the list is scannable; losing GraphQL must not
// leave every row blank.
func TestEnrichPRStatusesFallsBackToREST(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *graphql*)
    echo "GraphQL: Resource not accessible by personal access token" >&2
    exit 1
    ;;
  *"pulls/101/reviews"*)
    printf '%s\n' '[{"state":"APPROVED","user":{"login":"alice"}},{"state":"COMMENTED","user":{"login":"alice"}}]'
    ;;
  *"pulls/101"*)
    printf '%s\n' '{"state":"open","draft":true,"merged":false,"merged_at":null}'
    ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	// State and IsDraft as both search paths deliver them — the row reaching
	// enrichment already knows them, and the enrichment must preserve rather
	// than re-fetch them.
	got, err := NewClient(0).EnrichPRStatuses(context.Background(), []pr.Summary{
		{ID: "PR_1", Number: 101, Repo: "acme/one", State: "OPEN", IsDraft: true},
	})
	if err != nil {
		t.Fatalf("the fallback should have succeeded: %v", err)
	}
	if !got[0].IsDraft {
		t.Error("the D marker lost its draft flag")
	}
	if got[0].State != "OPEN" {
		t.Errorf("state = %q, want the row's own OPEN preserved", got[0].State)
	}
	// A later COMMENTED review must not erase the approval: COMMENTED is not a
	// verdict, and letting it win drops the A marker off an approved PR.
	if got[0].ReviewDecision != "APPROVED" {
		t.Errorf("review decision = %q, want APPROVED", got[0].ReviewDecision)
	}
	// REST has no thread-resolution bit anywhere. Reporting zero unresolved
	// conversations would be a claim nothing verified.
	if got[0].ConversationsKnown {
		t.Error("REST cannot know the conversation state; it must stay unknown")
	}
}

// One block outranks any number of approvals, which is how GitHub itself
// reports the decision.
func TestRESTReviewDecisionPrefersLatestVerdictPerReviewer(t *testing.T) {
	cases := []struct {
		name    string
		reviews []restReview
		want    string
	}{
		{"none", nil, ""},
		{"comment only", []restReview{{State: "COMMENTED"}}, ""},
		{"approved", []restReview{rev("alice", "APPROVED")}, "APPROVED"},
		{"blocked wins", []restReview{
			rev("alice", "APPROVED"), rev("bob", "CHANGES_REQUESTED"),
		}, "CHANGES_REQUESTED"},
		{"reviewer changed their mind", []restReview{
			rev("alice", "CHANGES_REQUESTED"), rev("alice", "APPROVED"),
		}, "APPROVED"},
		{"comment after approval keeps it", []restReview{
			rev("alice", "APPROVED"), rev("alice", "COMMENTED"),
		}, "APPROVED"},
		{"dismissed approval no longer counts", []restReview{
			rev("alice", "APPROVED"), rev("alice", "DISMISSED"),
		}, ""},
	}
	for _, tc := range cases {
		if got := restReviewDecision(tc.reviews); got != tc.want {
			t.Errorf("%s: decision = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func rev(user, state string) restReview {
	r := restReview{State: state}
	r.User.Login = user
	return r
}

// Inline threads are the substance of a review; losing GraphQL must not empty
// the Comments tab.
func TestReviewThreadsFallsBackToREST(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *graphql*) echo "GraphQL: API rate limit exceeded (graphql_rate_limit)" >&2; exit 1 ;;
  *comments*)
    printf '%s\n' '[
      {"id":1,"node_id":"PRRC_1","body":"first","path":"a.go","line":10,"original_line":10,"side":"RIGHT","in_reply_to_id":null,"created_at":"2026-08-05T00:00:00Z","user":{"login":"alice"}},
      {"id":2,"node_id":"PRRC_2","body":"reply","path":"a.go","line":10,"original_line":10,"side":"RIGHT","in_reply_to_id":1,"created_at":"2026-08-05T00:01:00Z","user":{"login":"bob"}},
      {"id":3,"node_id":"PRRC_3","body":"other","path":"b.go","line":4,"original_line":4,"side":"LEFT","in_reply_to_id":null,"created_at":"2026-08-05T00:02:00Z","user":{"login":"carol"}}
    ]'
    ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	got, err := NewClient(0).ReviewThreads(context.Background(), "acme", "one", 5)
	if err != nil {
		t.Fatalf("the fallback should have succeeded: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d threads, want 2 (a reply belongs to its root)", len(got))
	}
	if len(got[0].Comments) != 2 {
		t.Errorf("thread 1 has %d comments, want 2", len(got[0].Comments))
	}
	if got[0].Comments[1].Body != "reply" {
		t.Errorf("the reply was not attached to its root: %#v", got[0].Comments)
	}
	// Replying goes through REST, which wants the numeric id — so a fallback
	// thread must still be answerable.
	if got[0].Comments[0].DatabaseID != 1 {
		t.Errorf("database id = %d; without it a reply is rejected", got[0].Comments[0].DatabaseID)
	}
	if got[1].DiffSide != "LEFT" {
		t.Errorf("side = %q, want LEFT", got[1].DiffSide)
	}
	for i, th := range got {
		if th.ResolutionKnown {
			t.Errorf("thread %d claims to know its resolution, which REST cannot answer", i)
		}
		if th.ID != "" {
			t.Errorf("thread %d carries an ID %q — a comment node id is not a thread "+
				"node id, and resolveReviewThread would reject it", i, th.ID)
		}
	}
}

// A reply whose root is not in the page still has to surface: a dropped comment
// is review feedback the author never sees.
func TestRESTThreadsKeepOrphanedReplies(t *testing.T) {
	got := buildRESTThreads([]restReviewComment{
		{ID: 2, Body: "reply to something older", Path: "a.go", Line: 3, InReplyToID: 99},
	})
	if len(got) != 1 {
		t.Fatalf("got %d threads, want the orphaned reply kept", len(got))
	}
	if got[0].Comments[0].Body != "reply to something older" {
		t.Errorf("got %#v", got[0].Comments)
	}
}

// A `gh pr list` refusal empties a repo-scoped tab; REST search answers the
// same question, scoped to that repository.
func TestListPRsFallsBackToRESTSearch(t *testing.T) {
	fakeGH(t, fmt.Sprintf(`
case "$1 $2" in
  "pr list") echo "GraphQL: API rate limit exceeded (graphql_rate_limit)" >&2; exit 1 ;;
  "api search/issues"*)
    case "$*" in
      *%s*) ;;
      *) echo "the fallback did not scope to the repository: $*" >&2; exit 1 ;;
    esac
    printf '%%s\n' '{"items":[{"node_id":"PR_l","number":3,"title":"scoped","state":"open","html_url":"u","updated_at":"2026-08-05T00:00:00Z","user":{"login":"a"},"repository_url":"https://api.github.com/repos/acme/one"}]}'
    ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`, "repo:acme/one"))
	got, err := NewClient(0).WithRepo("acme/one").ListPRs(context.Background(), "state:open", 30)
	if err != nil {
		t.Fatalf("ListPRs should have fallen back: %v", err)
	}
	if len(got) != 1 || got[0].Number != 3 {
		t.Fatalf("got %#v", got)
	}
}

func TestRepoFromAPIURL(t *testing.T) {
	cases := map[string]string{
		"https://api.github.com/repos/acme/one":       "acme/one",
		"https://api.github.com/repos/acme/one/":      "acme/one",
		"https://github.example.com/api/v3/repos/o/n": "o/n",
		"https://api.github.com/users/acme":           "",
		"":                                            "",
	}
	for in, want := range cases {
		if got := repoFromAPIURL(in); got != want {
			t.Errorf("repoFromAPIURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The enrichment pass is what everything else on the row is refreshed from, so
// a PR marked ready (or sent back to draft) after the list was cached kept
// showing the stale marker — the batch query never asked for isDraft.
func TestEnrichPRStatusesRefreshesDraft(t *testing.T) {
	fakeGH(t, `
printf '%s\n' '{"data":{"nodes":[{"id":"PR_1","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}]}}'
`)
	got, err := NewClient(0).EnrichPRStatuses(context.Background(), []pr.Summary{
		// The cached row still says draft; GitHub says it was marked ready.
		{ID: "PR_1", Number: 1, Repo: "acme/one", State: "OPEN", IsDraft: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].IsDraft {
		t.Error("the draft marker stayed stale after the PR was marked ready")
	}

	fakeGH(t, `
printf '%s\n' '{"data":{"nodes":[{"id":"PR_1","state":"OPEN","isDraft":true,"reviewDecision":"","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}]}}'
`)
	got, err = NewClient(0).EnrichPRStatuses(context.Background(), []pr.Summary{
		{ID: "PR_1", Number: 1, Repo: "acme/one", State: "OPEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].IsDraft {
		t.Error("a PR converted back to draft was not picked up")
	}
}
