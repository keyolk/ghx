package gh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// countingGH is fakeGH plus a request log, so a test can assert how many round
// trips an operation costs — not just that it produced the right answer.
func countingGH(t *testing.T, script string) func() []string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	wrapper := fmt.Sprintf(
		"#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\n%s", logPath, script)
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
}

// The REST fallback used to cost two requests per PR: one for `pulls/{n}` and
// one for its reviews. The first was pure duplication — every search path
// already reports state, draft, and merged for the rows it returns — and on a
// real 78-row queue it measured 156 requests for one tab switch, enough to
// exhaust the REST budget in about thirty switches. The whole point of falling
// back is that GraphQL ran out; spending the remaining budget four times faster
// than necessary is not a fallback, it is the same outage one step later.
//
// Reviews genuinely need one request each: no list endpoint embeds them.
func TestRESTStatusFallbackCostsOneRequestPerPR(t *testing.T) {
	calls := countingGH(t, `
case "$*" in
  *graphql*) echo "GraphQL: Resource not accessible by personal access token" >&2; exit 1 ;;
  *reviews*) printf '%s\n' '[{"state":"APPROVED","user":{"login":"alice"}}]' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	rows := make([]pr.Summary, 0, 20)
	for i := 1; i <= 20; i++ {
		rows = append(rows, pr.Summary{
			ID: fmt.Sprintf("PR_%d", i), Number: i,
			Repo: "acme/one", State: "OPEN",
		})
	}
	got, err := NewClient(0).EnrichPRStatuses(context.Background(), rows)
	if err != nil {
		t.Fatalf("the fallback should have succeeded: %v", err)
	}
	for i := range got {
		if got[i].ReviewDecision != "APPROVED" {
			t.Fatalf("row %d lost its review decision: %q", i, got[i].ReviewDecision)
		}
	}

	log := calls()
	var reviews, pulls int
	for _, c := range log {
		switch {
		case strings.Contains(c, "/reviews"):
			reviews++
		case strings.Contains(c, "/pulls/"):
			pulls++
		}
	}
	if reviews != 20 {
		t.Errorf("reviews requests = %d, want one per PR (20)", reviews)
	}
	if pulls != 0 {
		t.Errorf("%d redundant pulls/{n} requests: every row already carried its "+
			"state and draft flag from the search that produced it", pulls)
	}
}

// A row that arrived without a state is the one case the extra request buys
// something: the D and M markers are what this fallback exists to light up, and
// inferring them from an empty string would put a wrong marker on screen.
func TestRESTStatusFallbackStillAsksForAnUnknownState(t *testing.T) {
	calls := countingGH(t, `
case "$*" in
  *graphql*) echo "GraphQL: Resource not accessible by personal access token" >&2; exit 1 ;;
  *reviews*) printf '%s\n' '[]' ;;
  *"pulls/7"*) printf '%s\n' '{"state":"open","draft":true,"merged":false,"merged_at":null}' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	got, err := NewClient(0).EnrichPRStatuses(context.Background(), []pr.Summary{
		{ID: "PR_7", Number: 7, Repo: "acme/one"}, // no State
	})
	if err != nil {
		t.Fatalf("the fallback should have succeeded: %v", err)
	}
	if got[0].State != "OPEN" {
		t.Errorf("state = %q, want it resolved over REST", got[0].State)
	}
	if !got[0].IsDraft {
		t.Error("a row with no state must still get its draft flag")
	}
	var pulls int
	for _, c := range calls() {
		if strings.Contains(c, "/pulls/7") && !strings.Contains(c, "/reviews") {
			pulls++
		}
	}
	if pulls != 1 {
		t.Errorf("pulls/{n} requests = %d, want exactly 1 for a stateless row", pulls)
	}
}
