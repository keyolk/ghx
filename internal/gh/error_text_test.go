package gh

import (
	"context"
	"strings"
	"testing"
)

// A stderr-less failure — a timeout, a killed subprocess — must not put the
// whole argv in the error. `gh api graphql -f query=...` carries a multi-line
// GraphQL document, and the TUI shows this text in its one-line footer.
func TestExecErrorNamesTheSubcommandNotTheWholeArgv(t *testing.T) {
	fakeGH(t, "exit 1\n")

	_, err := NewClient(0).exec(context.Background(), "api", "graphql", "-f",
		"query="+prStatusBatchQuery)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "rateLimit") || strings.Contains(err.Error(), "\n") {
		t.Errorf("error echoed the GraphQL document: %q", err)
	}
	if !strings.HasPrefix(err.Error(), "gh api graphql:") {
		t.Errorf("error did not name the subcommand: %q", err)
	}
}

// execRaw is the path the PR status enrichment actually takes.
func TestExecRawErrorNamesTheSubcommandNotTheWholeArgv(t *testing.T) {
	fakeGH(t, "exit 1\n")

	_, err := NewClient(0).execRaw(context.Background(), "api", "graphql", "-f",
		"query="+prStatusBatchQuery)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "rateLimit") || strings.Contains(err.Error(), "\n") {
		t.Errorf("error echoed the GraphQL document: %q", err)
	}
}
