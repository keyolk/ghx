package gh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoCredentialIsUsedForMerge(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	writeExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
printf '%s|%s\n' "${GH_TOKEN:-}" "$*" >> "$CAPTURE"
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GH_TOKEN", "global-token")

	client := NewClient(0)
	client.credentials.resolve = func(_ context.Context, repo string) (string, bool) {
		if repo != "keyolk/ghx" {
			t.Fatalf("credential lookup repo = %q, want keyolk/ghx", repo)
		}
		return "keyolk-token", true
	}
	if err := client.WithRepo("keyolk/ghx").Merge(context.Background(), 42, "squash"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	got := readFile(t, capture)
	want := "keyolk-token|pr merge 42 --repo keyolk/ghx --squash\n"
	if got != want {
		t.Errorf("captured call = %q, want %q", got, want)
	}
}

func TestExplicitAccountCredentialIsUsedForTargetRepository(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	writeExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
printf '%s|%s\n' "${GH_TOKEN:-}" "$*" >> "$CAPTURE"
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GH_TOKEN", "global-token")

	client := NewClient(0)
	client.credentials.resolve = func(_ context.Context, repo string) (string, bool) {
		if repo != "work/selector" {
			t.Fatalf("credential lookup repo = %q, want work/selector", repo)
		}
		return "work-token", true
	}
	client = client.WithCredentialRepo("work/selector").WithRepo("acme/target")
	if err := client.Merge(context.Background(), 42, "squash"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	got := readFile(t, capture)
	want := "work-token|pr merge 42 --repo acme/target --squash\n"
	if got != want {
		t.Errorf("captured call = %q, want %q", got, want)
	}
}

func TestExplicitAccountWithoutCredentialDoesNotUseActiveGHAuth(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	writeExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
printf '%s|%s\n' "${GH_TOKEN:-}" "$*" >> "$CAPTURE"
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GH_TOKEN", "other-account-token")

	client := NewClient(0)
	client.credentials.resolve = func(context.Context, string) (string, bool) {
		return "", false
	}
	err := client.WithCredentialRepo("work/selector").AuthStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no Git credential found") {
		t.Fatalf("AuthStatus error = %v, want missing configured credential", err)
	}
	if _, statErr := os.Stat(capture); !os.IsNotExist(statErr) {
		t.Errorf("gh ran despite missing account credential: %v", statErr)
	}
}

func TestInvalidRepoCredentialFallsBackToActiveGHAuth(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	writeExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
printf '%s|%s\n' "${GH_TOKEN:-}" "$*" >> "$CAPTURE"
if [ "${GH_TOKEN:-}" = "stale-token" ]; then
  printf '%s\n' 'HTTP 401: Bad credentials' >&2
  exit 1
fi
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GH_TOKEN", "active-gh-token")

	client := NewClient(0)
	client.credentials.resolve = func(context.Context, string) (string, bool) {
		return "stale-token", true
	}
	if err := client.WithRepo("gavin-jeong/ghx").Merge(context.Background(), 7, "merge"); err != nil {
		t.Fatalf("Merge with fallback: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(readFile(t, capture)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d calls, want credential attempt plus one fallback: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "stale-token|") {
		t.Errorf("first call did not use repo credential: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "active-gh-token|") {
		t.Errorf("fallback did not use active gh auth environment: %q", lines[1])
	}
}

func TestPermissionFailureDoesNotChangeAccounts(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	writeExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
printf '%s|%s\n' "${GH_TOKEN:-}" "$*" >> "$CAPTURE"
printf '%s\n' 'HTTP 403: Resource not accessible by personal access token' >&2
exit 1
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GH_TOKEN", "other-account-token")

	client := NewClient(0)
	client.credentials.resolve = func(context.Context, string) (string, bool) {
		return "repo-token", true
	}
	err := client.WithRepo("keyolk/private").Merge(context.Background(), 9, "squash")
	if err == nil {
		t.Fatal("permission failure should be returned")
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, capture)), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "repo-token|") {
		t.Errorf("403 must not retry as another account: %q", lines)
	}
}

func TestWithRepoSwitchesOnlyImplicitCredentialSelector(t *testing.T) {
	implicit := NewClient(0).WithRepo("acme/one").WithRepo("acme/two")
	if got := implicit.credentialSelector(); got != "acme/two" {
		t.Errorf("implicit selector = %q, want acme/two", got)
	}

	explicit := NewClient(0).WithCredentialRepo("work/selector").WithRepo("acme/two")
	if got := explicit.credentialSelector(); got != "work/selector" {
		t.Errorf("explicit selector = %q, want work/selector", got)
	}
}

func TestCredentialLookupIsCachedAcrossWithRepoClones(t *testing.T) {
	client := NewClient(0)
	calls := 0
	client.credentials.resolve = func(context.Context, string) (string, bool) {
		calls++
		return "token", true
	}
	if _, ok := client.WithRepo("keyolk/ghx").credentials.token(context.Background(), "keyolk/ghx"); !ok {
		t.Fatal("first credential lookup missed")
	}
	if _, ok := client.WithRepo("KEYOLK/GHX").credentials.token(context.Background(), "KEYOLK/GHX"); !ok {
		t.Fatal("cached credential lookup missed")
	}
	if calls != 1 {
		t.Errorf("resolver called %d times, want 1", calls)
	}
}

func TestResolveGitCredentialUsesRepositoryURL(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "credential-input")
	writeExecutable(t, filepath.Join(dir, "git"), `#!/bin/sh
cat > "$CAPTURE"
printf '%s\n' 'protocol=https' 'host=github.com' 'path=keyolk/ghx.git' 'username=keyolk' 'password=keyolk-token' ''
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)

	token, ok := resolveGitCredential(context.Background(), "keyolk/ghx")
	if !ok || token != "keyolk-token" {
		t.Fatalf("credential = (%q, %v), want keyolk-token", token, ok)
	}
	if got, want := readFile(t, capture), "url=https://github.com/keyolk/ghx.git\n\n"; got != want {
		t.Errorf("credential input = %q, want %q", got, want)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
