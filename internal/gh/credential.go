package gh

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/keyolk/ghx/internal/pr"
)

type credentialEntry struct {
	token string
	found bool
}

// credentialCache shares repo credential lookups across WithRepo clones. Misses
// are cached too, so a missing helper does not spawn git for every list action.
type credentialCache struct {
	mu      sync.Mutex
	entries map[string]credentialEntry
	resolve func(context.Context, string) (string, bool)
}

func newCredentialCache() *credentialCache {
	return &credentialCache{
		entries: make(map[string]credentialEntry),
		resolve: resolveCredential,
	}
}

func (c *credentialCache) token(ctx context.Context, repo string) (string, bool) {
	repo = strings.ToLower(strings.TrimSpace(repo))
	if repo == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[repo]; ok {
		return entry.token, entry.found
	}
	token, found := c.resolve(ctx, repo)
	c.entries[repo] = credentialEntry{token: token, found: found}
	return token, found
}

func (c *credentialCache) reject(repo string) {
	repo = strings.ToLower(strings.TrimSpace(repo))
	if repo == "" {
		return
	}
	c.mu.Lock()
	c.entries[repo] = credentialEntry{}
	c.mu.Unlock()
}

// resolveCredential turns a selector into a token. Two selector kinds exist:
// "ghuser:<login>" addresses a gh CLI login directly, anything else is treated
// as an "owner/repo" whose Git credential identifies the account.
func resolveCredential(ctx context.Context, selector string) (string, bool) {
	if user, ok := strings.CutPrefix(selector, pr.GHUserSelectorPrefix); ok {
		return resolveGHUserToken(ctx, user)
	}
	return resolveGitCredential(ctx, selector)
}

// resolveGHUserToken reads the token gh already holds for a named login. gh
// keeps one token per account in its keyring, so this reaches a second account
// without depending on the Git credential helper — which a global
// `[credential "https://github.com"]` helper override can collapse onto a
// single token for every repository path.
func resolveGHUserToken(ctx context.Context, user string) (string, bool) {
	if user == "" {
		return "", false
	}
	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--user", user)
	// The ambient token must not stand in for the requested account.
	cmd.Env = withoutEnv(os.Environ(), "GH_TOKEN", "GITHUB_TOKEN")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(string(out))
	return token, token != ""
}

// resolveGitCredential delegates account selection to ~/.gitconfig. In
// particular, credential.useHttpPath and helper/includeIf rules can return a
// different password/token for each owner/repository URL.
func resolveGitCredential(ctx context.Context, repo string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", "credential", "fill")
	cmd.Env = append(withoutEnv(os.Environ(), "GIT_TERMINAL_PROMPT"), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = strings.NewReader("url=https://github.com/" + repo + ".git\n\n")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && key == "password" && value != "" {
			return value, true
		}
	}
	return "", false
}

// CredentialToken resolves the token for a selector, reporting whether one was
// found. Callers use it to compare identities — never to log or display a token.
func (c *Client) CredentialToken(ctx context.Context, selector string) (string, bool) {
	if c.credentials == nil {
		return "", false
	}
	return c.credentials.token(ctx, selector)
}

func (c *Client) command(ctx context.Context, args ...string) (*exec.Cmd, bool) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	if c.credentials == nil {
		return cmd, false
	}
	if token, ok := c.credentials.token(ctx, c.credentialSelector()); ok {
		// A selected Git credential must win over the process's globally active gh
		// account. Keep it subprocess-local and never include it in argv or errors.
		cmd.Env = append(withoutEnv(os.Environ(), "GH_TOKEN", "GITHUB_TOKEN"), "GH_TOKEN="+token)
		return cmd, true
	}
	return cmd, false
}

func (c *Client) credentialSelector() string {
	if c.credentialRepo != "" {
		return c.credentialRepo
	}
	return c.repo
}

func withoutEnv(env []string, keys ...string) []string {
	blocked := make(map[string]bool, len(keys))
	for _, key := range keys {
		blocked[key] = true
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			out = append(out, entry)
		}
	}
	return out
}
