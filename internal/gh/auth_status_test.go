package gh

import (
	"context"
	"strings"
	"testing"
)

// `gh auth status` reports on every logged-in account, so its stderr is an
// inventory: a dozen lines about the accounts that are fine, around the one
// that is not. Carried whole, that error reached a one-line footer as a
// fragment of a list — and, written to a terminal the TUI had already taken,
// painted itself over the PR list.
func TestAuthStatusReportsOnlyTheFailingLine(t *testing.T) {
	fakeGH(t, `
cat >&2 <<'OUT'
github.com
  X Failed to log in to github.com using token (GH_TOKEN)
  - Active account: true
  - The token in GH_TOKEN is invalid.

  ✓ Logged in to github.com account keyolk (keyring)
  - Active account: false
  - Token scopes: 'gist', 'read:org', 'repo', 'workflow'
OUT
exit 1
`)
	err := NewClient(0).AuthStatus(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("error is multi-line, so a footer shows a fragment: %q", err)
	}
	if !strings.Contains(err.Error(), "Failed to log in") {
		t.Errorf("error = %q, want the line that says what failed", err)
	}
	// The accounts that are fine are not the problem and must not be reported
	// as though they were.
	if strings.Contains(err.Error(), "keyolk") {
		t.Errorf("error names a working account: %q", err)
	}
}

// A hard failure has no inventory to pick from; the first line is the reason.
func TestAuthStatusKeepsAPlainFailure(t *testing.T) {
	fakeGH(t, "echo 'could not connect to github.com' >&2; exit 1\n")

	err := NewClient(0).AuthStatus(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "could not connect") {
		t.Errorf("error = %q, want the reason gh gave", err)
	}
}
