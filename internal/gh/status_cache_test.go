package gh

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/pr"
)

// enrichScript answers the batch query for whatever node IDs it is given, so a
// test can count requests without pinning the page's contents.
const enrichScript = `
case "$*" in
  *graphql*)
    ids=$(printf '%s\n' "$*" | tr ' ' '\n' | sed -n 's/^ids\[\]=//p')
    nodes=""
    for id in $ids; do
      nodes="$nodes{\"id\":\"$id\",\"state\":\"OPEN\",\"isDraft\":false,\"reviewDecision\":\"APPROVED\",\"reviewThreads\":{\"nodes\":[{\"isResolved\":false}],\"pageInfo\":{\"hasNextPage\":false,\"endCursor\":\"\"}}},"
    done
    nodes=$(printf '%s' "$nodes" | sed 's/,$//')
    printf '{"data":{"rateLimit":{"remaining":4000,"limit":5000,"resetAt":"2099-01-01T00:00:00Z"},"nodes":[%s]}}\n' "$nodes"
    ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`

func rowsForEnrich(n int, updatedAt time.Time) []pr.Summary {
	rows := make([]pr.Summary, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, pr.Summary{
			ID: fmt.Sprintf("PR_%d", i), Number: i,
			Repo: "acme/one", State: "OPEN", UpdatedAt: updatedAt,
		})
	}
	return rows
}

func graphQLCalls(log []string) int {
	n := 0
	for _, c := range log {
		if strings.Contains(c, "graphql") {
			n++
		}
	}
	return n
}

// Enrichment is the expensive half of a poll: the list request is one point,
// the reviewThreads connection on every node is what actually drains the
// account's 5,000/hour. Re-sending it for rows that have not moved is the bulk
// of what six parked windows spend, and updatedAt is proof they have not moved.
func TestEnrichmentSkipsRowsItAlreadyKnows(t *testing.T) {
	calls := countingGH(t, enrichScript)
	stamp := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	rows := rowsForEnrich(20, stamp)

	c := NewClient(0)
	if _, err := c.EnrichPRStatuses(context.Background(), rows); err != nil {
		t.Fatalf("first enrichment: %v", err)
	}
	if n := graphQLCalls(calls()); n != 1 {
		t.Fatalf("first enrichment sent %d GraphQL requests, want 1", n)
	}

	got, err := c.EnrichPRStatuses(context.Background(), rows)
	if err != nil {
		t.Fatalf("second enrichment: %v", err)
	}
	if n := graphQLCalls(calls()); n != 1 {
		t.Errorf("an unchanged page cost %d more GraphQL requests, want 0", n-1)
	}
	for i := range got {
		if got[i].ReviewDecision != "APPROVED" || !got[i].ConversationsKnown ||
			got[i].UnresolvedConversations != 1 {
			t.Fatalf("row %d came back from cache incomplete: %+v", i, got[i])
		}
	}
}

// A separate Client is another ghx window. The saving only matters if it
// crosses the process boundary — an in-memory map would help one window
// scrolling its own tabs and do nothing about the case that empties the pool.
func TestASecondInstanceReusesTheFirstsEnrichment(t *testing.T) {
	calls := countingGH(t, enrichScript)
	stamp := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	rows := rowsForEnrich(10, stamp)

	if _, err := NewClient(0).EnrichPRStatuses(context.Background(), rows); err != nil {
		t.Fatalf("first instance: %v", err)
	}
	before := graphQLCalls(calls())

	got, err := NewClient(0).EnrichPRStatuses(context.Background(), rows)
	if err != nil {
		t.Fatalf("second instance: %v", err)
	}
	if n := graphQLCalls(calls()); n != before {
		t.Errorf("the second instance sent %d requests for a page the first "+
			"had already paid for", n-before)
	}
	if got[0].ReviewDecision != "APPROVED" {
		t.Errorf("the shared entry did not carry the review decision: %+v", got[0])
	}
}

// The cache must not outlive the thing it describes. A push moves updatedAt,
// and a row that reads back as approved after new commits landed is worse than
// no marker at all.
func TestAChangedRowIsEnrichedAgain(t *testing.T) {
	calls := countingGH(t, enrichScript)
	stamp := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	c := NewClient(0)
	if _, err := c.EnrichPRStatuses(context.Background(), rowsForEnrich(3, stamp)); err != nil {
		t.Fatalf("first enrichment: %v", err)
	}
	before := graphQLCalls(calls())

	pushed := rowsForEnrich(3, stamp.Add(time.Minute))
	if _, err := c.EnrichPRStatuses(context.Background(), pushed); err != nil {
		t.Fatalf("second enrichment: %v", err)
	}
	if n := graphQLCalls(calls()); n != before+1 {
		t.Errorf("a page whose rows were all pushed to sent %d requests, want 1",
			n-before)
	}
}

// A row with no updatedAt cannot be validated against anything, so it is asked
// for rather than guessed at — the same rule the PR detail cache follows.
func TestAnUnstampedRowIsNeverServedFromCache(t *testing.T) {
	calls := countingGH(t, enrichScript)
	rows := []pr.Summary{{ID: "PR_1", Number: 1, Repo: "acme/one", State: "OPEN"}}

	c := NewClient(0)
	for i := 0; i < 2; i++ {
		if _, err := c.EnrichPRStatuses(context.Background(), rows); err != nil {
			t.Fatalf("enrichment %d: %v", i, err)
		}
	}
	if n := graphQLCalls(calls()); n != 2 {
		t.Errorf("unstamped rows cost %d requests over two passes, want 2 — "+
			"an entry nothing can validate must not be trusted", n)
	}
}

// An action taken inside ghx changes the PR before its updatedAt catches up, so
// the entry still validates against a state that just stopped being true. The
// entry is shared, so leaving it would push this window's stale markers out to
// every other one.
func TestEvictingAPRForcesTheNextEnrichment(t *testing.T) {
	calls := countingGH(t, enrichScript)
	stamp := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	rows := rowsForEnrich(2, stamp)

	c := NewClient(0)
	if _, err := c.EnrichPRStatuses(context.Background(), rows); err != nil {
		t.Fatalf("first enrichment: %v", err)
	}
	before := graphQLCalls(calls())

	c.EvictStatus("acme/one", 1)
	if _, err := c.EnrichPRStatuses(context.Background(), rows); err != nil {
		t.Fatalf("second enrichment: %v", err)
	}
	if n := graphQLCalls(calls()); n != before+1 {
		t.Errorf("the evicted PR was not re-enriched (%d requests)", n-before)
	}
}

// REST cannot see thread resolution. Caching that as a zero count would turn
// "we could not find out" into "there is nothing outstanding", on a PR that may
// be blocked — and would then hand that reading to every other window.
func TestARESTEnrichedRowStaysHonestAboutWhatItDoesNotKnow(t *testing.T) {
	countingGH(t, `
case "$*" in
  *graphql*) echo "GraphQL: Resource not accessible by personal access token" >&2; exit 1 ;;
  *reviews*) printf '%s\n' '[{"state":"APPROVED","user":{"login":"alice"}}]' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	stamp := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	rows := rowsForEnrich(1, stamp)

	if _, err := NewClient(0).EnrichPRStatuses(context.Background(), rows); err != nil {
		t.Fatalf("first enrichment: %v", err)
	}
	got, err := NewClient(0).EnrichPRStatuses(context.Background(), rows)
	if err != nil {
		t.Fatalf("second enrichment: %v", err)
	}
	if got[0].ConversationsKnown {
		t.Error("a REST-derived entry came back claiming it knew the thread count")
	}
	if got[0].ReviewDecision != "APPROVED" {
		t.Errorf("the entry lost what REST could answer: %+v", got[0])
	}
}
