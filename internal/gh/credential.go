package gh

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
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
		resolve: resolveGitCredential,
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

func (c *Client) command(ctx context.Context, args ...string) (*exec.Cmd, bool) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	if c.credentials == nil {
		return cmd, false
	}
	if token, ok := c.credentials.token(ctx, c.repo); ok {
		// A repo credential must win over the process's globally active gh account.
		// Keep it subprocess-local and never include it in argv or errors.
		cmd.Env = append(withoutEnv(os.Environ(), "GH_TOKEN", "GITHUB_TOKEN"), "GH_TOKEN="+token)
		return cmd, true
	}
	return cmd, false
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
