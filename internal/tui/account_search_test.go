package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
)

func TestSearchPRsAcrossAccountsMergesAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	writeTestExecutable(t, filepath.Join(dir, "git"), `#!/bin/sh
input=$(cat)
case "$input" in
  *personal/selector.git*) token=personal-token ;;
  *work/selector.git*) token=work-token ;;
  *) exit 1 ;;
esac
printf 'password=%s\n\n' "$token"
`)
	writeTestExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
case "${GH_TOKEN:-}" in
  personal-token)
    printf '%s\n' '[{"id":"PR_personal","number":1,"title":"personal","state":"open","updatedAt":"2026-08-10T00:00:00Z","author":{"login":"personal"},"repository":{"name":"one","nameWithOwner":"personal/one"}},{"id":"PR_shared","number":3,"title":"shared","state":"open","updatedAt":"2026-08-08T00:00:00Z","author":{"login":"shared"},"repository":{"name":"shared","nameWithOwner":"acme/shared"}}]'
    ;;
  work-token)
    printf '%s\n' '[{"id":"PR_work","number":2,"title":"work","state":"open","updatedAt":"2026-08-11T00:00:00Z","author":{"login":"work"},"repository":{"name":"two","nameWithOwner":"work/two"}},{"id":"PR_shared","number":3,"title":"shared","state":"open","updatedAt":"2026-08-08T00:00:00Z","author":{"login":"shared"},"repository":{"name":"shared","nameWithOwner":"acme/shared"}}]'
    ;;
  *) exit 1 ;;
esac
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "global-token")

	accounts := []config.AccountDef{
		{Name: "personal", CredentialRepo: "personal/selector"},
		{Name: "work", CredentialRepo: "work/selector"},
	}
	got, warning, err := searchPRsAcrossAccounts(
		context.Background(), gh.NewClient(0), accounts, "author:@me state:open", 50,
	)
	if err != nil {
		t.Fatalf("searchPRsAcrossAccounts: %v", err)
	}
	if warning != nil {
		t.Fatalf("unexpected warning: %v", warning)
	}
	if len(got) != 3 {
		t.Fatalf("got %d PRs, want 3 after deduplication: %#v", len(got), got)
	}
	if got[0].ID != "PR_work" || got[1].ID != "PR_personal" || got[2].ID != "PR_shared" {
		t.Errorf("PR order = %s,%s,%s, want work,personal,shared", got[0].ID, got[1].ID, got[2].ID)
	}
	if got[0].CredentialRepo != "work/selector" {
		t.Errorf("work selector = %q, want work/selector", got[0].CredentialRepo)
	}
	// The same PR is visible to both accounts. Configuration order deliberately
	// chooses which credential owns its later detail and action requests.
	if got[2].CredentialRepo != "personal/selector" {
		t.Errorf("shared selector = %q, want first account personal/selector", got[2].CredentialRepo)
	}
}

func TestSearchPRsAcrossAccountsKeepsSuccessfulAccount(t *testing.T) {
	dir := t.TempDir()
	writeTestExecutable(t, filepath.Join(dir, "git"), `#!/bin/sh
input=$(cat)
case "$input" in
  *broken/selector.git*) token=broken-token ;;
  *work/selector.git*) token=work-token ;;
  *) exit 1 ;;
esac
printf 'password=%s\n\n' "$token"
`)
	writeTestExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
if [ "${GH_TOKEN:-}" = broken-token ]; then
  printf '%s\n' 'HTTP 403: unavailable account' >&2
  exit 1
fi
printf '%s\n' '[{"id":"PR_work","number":2,"title":"work","state":"open","updatedAt":"2026-08-11T00:00:00Z","author":{"login":"work"},"repository":{"name":"two","nameWithOwner":"work/two"}}]'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	accounts := []config.AccountDef{
		{Name: "broken", CredentialRepo: "broken/selector"},
		{Name: "work", CredentialRepo: "work/selector"},
	}
	got, warning, err := searchPRsAcrossAccounts(
		context.Background(), gh.NewClient(0), accounts, "state:open", 50,
	)
	if err != nil {
		t.Fatalf("one successful account should keep the source usable: %v", err)
	}
	if len(got) != 1 || got[0].ID != "PR_work" {
		t.Fatalf("results = %#v, want the successful account's PR", got)
	}
	if warning == nil || !strings.Contains(warning.Error(), "account broken") {
		t.Errorf("warning = %v, want failed account name", warning)
	}
}
