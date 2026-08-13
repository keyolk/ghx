package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/config"
)

func accountDefs(users ...string) []config.AccountDef {
	out := make([]config.AccountDef, len(users))
	for i, u := range users {
		out[i] = config.AccountDef{Name: u, GHUser: u}
	}
	return out
}

// slowVerifier records concurrency: each check sleeps, so a serial
// implementation takes N× as long as a parallel one.
type slowVerifier struct {
	delay time.Duration

	mu       sync.Mutex
	inFlight int
	maxSeen  int
}

func (s *slowVerifier) AuthStatusFor(_ context.Context, _ string) error {
	s.mu.Lock()
	s.inFlight++
	if s.inFlight > s.maxSeen {
		s.maxSeen = s.inFlight
	}
	s.mu.Unlock()

	time.Sleep(s.delay)

	s.mu.Lock()
	s.inFlight--
	s.mu.Unlock()
	return nil
}

func (s *slowVerifier) CredentialToken(_ context.Context, selector string) (string, bool) {
	return "token-for-" + selector, true
}

// Each account costs a ~1.5s `gh auth status` round trip. Serially that was the
// single largest component of startup latency; the checks must overlap.
func TestVerifyAccountsRunsChecksConcurrently(t *testing.T) {
	v := &slowVerifier{delay: 100 * time.Millisecond}

	start := time.Now()
	if err := verifyAccounts(context.Background(), v, accountDefs("a", "b", "c", "d"), &bytes.Buffer{}); err != nil {
		t.Fatalf("verifyAccounts: %v", err)
	}
	elapsed := time.Since(start)

	if v.maxSeen < 2 {
		t.Errorf("max concurrent checks = %d, want the accounts to overlap", v.maxSeen)
	}
	// Serial would be ~400ms; parallel should land near one delay.
	if elapsed > 300*time.Millisecond {
		t.Errorf("elapsed %v suggests the checks ran serially", elapsed)
	}
}

// Concurrency must not scramble which warning belongs to which account.
func TestVerifyAccountsReportsInConfigurationOrder(t *testing.T) {
	v := &selectorVerifier{failing: map[string]bool{
		config.GHUserSelectorPrefix + "second": true,
	}}

	var warn bytes.Buffer
	if err := verifyAccounts(context.Background(), v, accountDefs("first", "second", "third"), &warn); err != nil {
		t.Fatalf("verifyAccounts: %v", err)
	}
	got := warn.String()
	if !strings.Contains(got, "account second") {
		t.Errorf("warning did not name the failing account: %q", got)
	}
	if strings.Contains(got, "account first") || strings.Contains(got, "account third") {
		t.Errorf("a passing account was reported as failing: %q", got)
	}
}

// The duplicate-token warning still fires when the checks run concurrently.
func TestVerifyAccountsStillDetectsSharedTokenConcurrently(t *testing.T) {
	v := &selectorVerifier{sharedToken: "one-token-for-everything"}

	var warn bytes.Buffer
	if err := verifyAccounts(context.Background(), v, accountDefs("one", "two"), &warn); err != nil {
		t.Fatalf("verifyAccounts: %v", err)
	}
	if !strings.Contains(warn.String(), "same token") {
		t.Errorf("collapsed accounts were not reported: %q", warn.String())
	}
}

type selectorVerifier struct {
	failing     map[string]bool
	sharedToken string
}

func (s *selectorVerifier) AuthStatusFor(_ context.Context, selector string) error {
	if s.failing[selector] {
		return fmt.Errorf("HTTP 401: Bad credentials")
	}
	return nil
}

func (s *selectorVerifier) CredentialToken(_ context.Context, selector string) (string, bool) {
	if s.sharedToken != "" {
		return s.sharedToken, true
	}
	return "token-for-" + selector, true
}
