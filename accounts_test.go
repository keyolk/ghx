package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/config"
)

// fakeVerifier stands in for the gh client: tokens[selector] is what the
// credential layer would resolve, and failing[selector] marks an account whose
// `gh auth status` fails.
type fakeVerifier struct {
	tokens  map[string]string
	failing map[string]bool
}

func (f fakeVerifier) AuthStatusFor(_ context.Context, selector string) error {
	if f.failing[selector] {
		return fmt.Errorf("HTTP 401: Bad credentials")
	}
	if _, ok := f.tokens[selector]; !ok {
		return fmt.Errorf("no Git credential found for %s", selector)
	}
	return nil
}

func (f fakeVerifier) CredentialToken(_ context.Context, selector string) (string, bool) {
	token, ok := f.tokens[selector]
	return token, ok
}

func TestVerifyAccountsWarnsWhenTwoAccountsShareOneToken(t *testing.T) {
	// A credential helper that ignores the repository path — the exact shape of a
	// global [credential "https://github.com"] override — hands every selector the
	// active account's token.
	client := fakeVerifier{tokens: map[string]string{
		"keyolk/ghx":              "one-token-for-everything",
		"sendbird/platform-tools": "one-token-for-everything",
	}}

	var warn bytes.Buffer
	err := verifyAccounts(context.Background(), client, []config.AccountDef{
		{Name: "personal", CredentialRepo: "keyolk/ghx"},
		{Name: "work", CredentialRepo: "sendbird/platform-tools"},
	}, &warn)
	if err != nil {
		t.Fatalf("verifyAccounts: %v", err)
	}
	if !strings.Contains(warn.String(), "same token") {
		t.Errorf("collapsed accounts were not reported: %q", warn.String())
	}
}

func TestVerifyAccountsStaysQuietForDistinctTokens(t *testing.T) {
	client := fakeVerifier{tokens: map[string]string{
		config.GHUserSelectorPrefix + "keyolk":      "gho_keyolk",
		config.GHUserSelectorPrefix + "gavin-jeong": "gho_gavin",
	}}

	var warn bytes.Buffer
	err := verifyAccounts(context.Background(), client, []config.AccountDef{
		{Name: "personal", GHUser: "keyolk"},
		{Name: "work", GHUser: "gavin-jeong"},
	}, &warn)
	if err != nil {
		t.Fatalf("verifyAccounts: %v", err)
	}
	if warn.String() != "" {
		t.Errorf("distinct accounts produced warnings: %q", warn.String())
	}
}

// One broken account must not withhold the other's review queue.
func TestVerifyAccountsSurvivesOneFailingAccount(t *testing.T) {
	client := fakeVerifier{
		tokens: map[string]string{
			config.GHUserSelectorPrefix + "keyolk": "gho_keyolk",
			config.GHUserSelectorPrefix + "broken": "gho_stale",
		},
		failing: map[string]bool{config.GHUserSelectorPrefix + "broken": true},
	}

	var warn bytes.Buffer
	err := verifyAccounts(context.Background(), client, []config.AccountDef{
		{Name: "good", GHUser: "keyolk"},
		{Name: "bad", GHUser: "broken"},
	}, &warn)
	if err != nil {
		t.Fatalf("one working account should still start ghx: %v", err)
	}
	if !strings.Contains(warn.String(), "account bad") {
		t.Errorf("failing account was not reported: %q", warn.String())
	}
}

func TestVerifyAccountsFailsWhenNoAccountWorks(t *testing.T) {
	client := fakeVerifier{
		tokens: map[string]string{
			config.GHUserSelectorPrefix + "a": "gho_a",
			config.GHUserSelectorPrefix + "b": "gho_b",
		},
		failing: map[string]bool{
			config.GHUserSelectorPrefix + "a": true,
			config.GHUserSelectorPrefix + "b": true,
		},
	}

	err := verifyAccounts(context.Background(), client, []config.AccountDef{
		{Name: "one", GHUser: "a"},
		{Name: "two", GHUser: "b"},
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no configured GitHub account is authenticated") {
		t.Fatalf("error = %v, want no-account-authenticated", err)
	}
}

func TestVerifyAccountsRejectsAccountWithoutSelector(t *testing.T) {
	client := fakeVerifier{tokens: map[string]string{
		config.GHUserSelectorPrefix + "keyolk": "gho_keyolk",
	}}

	var warn bytes.Buffer
	err := verifyAccounts(context.Background(), client, []config.AccountDef{
		{Name: "good", GHUser: "keyolk"},
		{Name: "empty"},
	}, &warn)
	if err != nil {
		t.Fatalf("verifyAccounts: %v", err)
	}
	if !strings.Contains(warn.String(), "gh_user or credential_repo is required") {
		t.Errorf("selector-less account was not reported: %q", warn.String())
	}
}

// gh_user must win over credential_repo: it addresses the account directly,
// while credential_repo depends on Git credential routing that can collapse.
func TestAccountSelectorPrefersGHUser(t *testing.T) {
	a := config.AccountDef{Name: "work", GHUser: "gavin-jeong", CredentialRepo: "sendbird/platform-tools"}
	if got, want := a.Selector(), config.GHUserSelectorPrefix+"gavin-jeong"; got != want {
		t.Errorf("Selector() = %q, want %q", got, want)
	}
	repoOnly := config.AccountDef{Name: "personal", CredentialRepo: "keyolk/ghx"}
	if got := repoOnly.Selector(); got != "keyolk/ghx" {
		t.Errorf("Selector() = %q, want keyolk/ghx", got)
	}
	if got := (config.AccountDef{Name: "empty"}).Selector(); got != "" {
		t.Errorf("Selector() = %q, want empty", got)
	}
}

// Account verification runs after the TUI owns the screen, so nothing it has to
// say may be written to the terminal: bubbletea does not know about a direct
// write and never redraws over it. A shared-token warning went to os.Stderr and
// sat on top of the PR list for the rest of the session.
func TestVerifyAccountsForTUIReturnsWarningsInsteadOfPrinting(t *testing.T) {
	client := fakeVerifier{tokens: map[string]string{
		"ghuser:one": "same-token",
		"ghuser:two": "same-token",
	}}
	accounts := []config.AccountDef{
		{Name: "one", GHUser: "one"},
		{Name: "two", GHUser: "two"},
	}

	err := verifyAccountsForTUI(context.Background(), client, accounts)
	if err == nil {
		t.Fatal("the shared-token warning was dropped instead of being returned")
	}
	var warning accountWarning
	if !errors.As(err, &warning) {
		t.Fatalf("got %T, want an accountWarning the TUI can toast", err)
	}
	if !strings.Contains(warning.Error(), "same token") {
		t.Errorf("warning = %q, want the shared-token explanation", warning)
	}
}

// A clean run says nothing at all — a toast on every startup would be noise.
func TestVerifyAccountsForTUIStaysQuietWhenEverythingWorks(t *testing.T) {
	client := fakeVerifier{tokens: map[string]string{
		"ghuser:one": "token-one",
		"ghuser:two": "token-two",
	}}
	accounts := []config.AccountDef{
		{Name: "one", GHUser: "one"},
		{Name: "two", GHUser: "two"},
	}

	if err := verifyAccountsForTUI(context.Background(), client, accounts); err != nil {
		t.Errorf("a healthy setup produced %q", err)
	}
}

// A failing account is still a warning, not a shutdown: the other account's
// review queue is usable and withholding it helps nobody.
func TestVerifyAccountsForTUIWarnsAboutOneBrokenAccount(t *testing.T) {
	client := fakeVerifier{
		tokens:  map[string]string{"ghuser:one": "token-one", "ghuser:two": "token-two"},
		failing: map[string]bool{"ghuser:two": true},
	}
	accounts := []config.AccountDef{
		{Name: "one", GHUser: "one"},
		{Name: "two", GHUser: "two"},
	}

	err := verifyAccountsForTUI(context.Background(), client, accounts)
	var warning accountWarning
	if !errors.As(err, &warning) {
		t.Fatalf("got %T, want a warning that names the broken account", err)
	}
	if !strings.Contains(warning.Error(), "two") {
		t.Errorf("warning = %q, want it to name the failing account", warning)
	}
}
