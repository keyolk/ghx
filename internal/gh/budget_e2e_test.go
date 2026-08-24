//go:build e2e

package gh

import (
	"os"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/pr"
)

// The rateLimit block is free only if it is actually in the response. A typo in
// the query, or a shape the decoder does not match, leaves the budget unknown
// forever — and an unknown budget is treated as a full one, so the guard would
// silently never engage. That failure is invisible in a unit test built from a
// fixture, because the fixture would contain whatever was believed.
//
//	GHX_E2E_REPO=keyolk/ghx go test -tags e2e -run BudgetIsObserved ./internal/gh/
func TestE2EBudgetIsObservedFromEnrichment(t *testing.T) {
	repo := os.Getenv("GHX_E2E_REPO")
	if repo == "" {
		t.Skip("set GHX_E2E_REPO to run the budget observation check")
	}
	c := NewClient(60 * time.Second).WithRepo(repo)

	if b := c.GraphQLBudget(); b.Known {
		t.Fatal("the budget is known before any request was made")
	}

	rows, err := c.SearchPRs(t.Context(), "repo:"+repo+" is:pr", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("no PRs to enrich in " + repo)
	}
	if _, err := c.EnrichPRStatuses(t.Context(), rows); err != nil {
		t.Fatalf("enrich: %v", err)
	}

	b := c.GraphQLBudget()
	if !b.Known {
		t.Fatal("enrichment did not report a rateLimit block — the guard would never engage")
	}
	if b.Limit <= 0 || b.Remaining < 0 || b.Remaining > b.Limit {
		t.Errorf("implausible budget: %d/%d", b.Remaining, b.Limit)
	}
	if b.ResetAt.IsZero() {
		t.Error("resetAt did not decode")
	}
	if f := b.Fraction(); f <= 0 || f > 1 {
		t.Errorf("fraction %v out of range", f)
	}
	t.Logf("observed %d/%d remaining (%.0f%%), resets %s",
		b.Remaining, b.Limit, b.Fraction()*100, b.ResetAt.Format(time.RFC3339))
}

// A derived client must see the same pool: WithRepo and WithCredentialRepo copy
// the struct, but the budget they spend is one account-wide allowance.
func TestE2EBudgetIsSharedAcrossDerivedClients(t *testing.T) {
	repo := os.Getenv("GHX_E2E_REPO")
	if repo == "" {
		t.Skip("set GHX_E2E_REPO to run the budget sharing check")
	}
	root := NewClient(60 * time.Second)
	scoped := root.WithRepo(repo)

	rows, err := scoped.SearchPRs(t.Context(), "repo:"+repo+" is:pr", 5)
	if err != nil || len(rows) == 0 {
		t.Skipf("no rows to enrich (err=%v)", err)
	}
	if _, err := scoped.EnrichPRStatuses(t.Context(), rows); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if !root.GraphQLBudget().Known {
		t.Error("an observation through a derived client is invisible to its parent")
	}
	var _ []pr.Summary = rows
}
