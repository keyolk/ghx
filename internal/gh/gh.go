// Package gh wraps the gh CLI subprocess. Each method shells out to `gh` with
// exec.CommandContext + a timeout and decodes --json output into typed structs.
// Callers must invoke these inside tea.Cmd closures (func() tea.Msg{...}) so
// I/O never blocks the Bubble Tea UI goroutine.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Client wraps gh CLI invocation with a default timeout.
type Client struct {
	timeout            time.Duration
	repo               string // "owner/repo" or empty for auto-detect
	credentialRepo     string // repo URL used only to select a Git credential
	credentialExplicit bool   // configured accounts must not fall back to another identity
	credentials        *credentialCache
}

// NewClient returns a gh wrapper with the given per-call timeout (default 30s).
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{timeout: timeout, credentials: newCredentialCache()}
}

// WithRepo scopes subsequent calls to a specific "owner/repo". Unless an
// account credential was selected explicitly, the repository also selects the
// Git credential, preserving the existing per-repository behavior.
func (c *Client) WithRepo(repo string) *Client {
	cp := *c
	cp.repo = repo
	if !cp.credentialExplicit {
		cp.credentialRepo = repo
	}
	return &cp
}

// WithCredentialRepo selects an account through the Git credential associated
// with repo without scoping the GitHub operation itself. This is used for
// cross-repository searches where --repo would change the query semantics.
func (c *Client) WithCredentialRepo(repo string) *Client {
	cp := *c
	cp.credentialRepo = repo
	cp.credentialExplicit = true
	return &cp
}

// exec runs `gh <args...>` with a timeout and returns stdout.
func (c *Client) exec(ctx context.Context, args ...string) ([]byte, error) {
	if c.repo != "" {
		// Insert --repo right after the pr/run/api subcommand. For simplicity,
		// prepend --repo to the args that follow the subcommand. Callers that
		// already pass --repo should use WithRepo("") to avoid double scoping.
		args = appendWithRepo(args, c.repo)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out, err := c.output(ctx, args...)
	if err != nil {
		// gh explains refusals on stderr ("can not approve your own pull
		// request"). Dropping it leaves the user with a bare exit status and no
		// idea what to do next.
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh %s: %s",
				strings.Join(subcommandWords(args), " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %v: %w", args, err)
	}
	return out, nil
}

func (c *Client) output(ctx context.Context, args ...string) ([]byte, error) {
	if c.credentialExplicit {
		selector := c.credentialSelector()
		if selector == "" {
			return nil, fmt.Errorf("configured GitHub account has no credential repository")
		}
		if _, ok := c.credentials.token(ctx, selector); !ok {
			return nil, fmt.Errorf("no Git credential found for configured account repository %s", selector)
		}
	}
	cmd, credentialUsed := c.command(ctx, args...)
	out, err := cmd.Output()
	if err == nil || !credentialUsed || !isCredentialAuthFailure(err) {
		return out, err
	}

	c.credentials.reject(c.credentialSelector())
	if c.credentialExplicit {
		// A configured account is an identity boundary. Falling back to the active
		// gh login here would silently return another account's PRs.
		return out, err
	}

	// A stale repository credential must not make an ordinary repo-scoped call
	// unusable when gh already has a valid active login.
	fallback := exec.CommandContext(ctx, "gh", args...)
	return fallback.Output()
}

func isCredentialAuthFailure(err error) bool {
	ee, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	message := strings.ToLower(string(ee.Stderr))
	return strings.Contains(message, "bad credentials") ||
		strings.Contains(message, "http 401") ||
		strings.Contains(message, "status 401") ||
		strings.Contains(message, "requires authentication") ||
		strings.Contains(message, "token in gh_token is invalid") ||
		strings.Contains(message, "failed to log in")
}

// subcommandWords returns the leading non-flag words of an argv, for error text
// that names the operation without echoing every flag back at the user.
func subcommandWords(args []string) []string {
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			return args[:i]
		}
	}
	return args
}

// execJSON runs gh with --json and unmarshals into target.
func (c *Client) execJSON(ctx context.Context, target interface{}, args ...string) error {
	out, err := c.exec(ctx, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, target)
}

// execRaw runs `gh <args...>` WITHOUT injecting --repo. Used by `gh api` calls
// where the repo is already part of the endpoint path or a GraphQL variable —
// passing --repo there is either rejected or silently ignored.
func (c *Client) execRaw(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out, err := c.output(ctx, args...)
	if err != nil {
		// gh writes API errors to stderr; surface them so the TUI can show why.
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh %s: %s",
				strings.Join(subcommandWords(args), " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %v: %w", args, err)
	}
	return out, nil
}

// appendWithRepo inserts `--repo owner/repo` after the leading subcommand
// words. gh subcommands are multi-word ("pr list", "pr view"), so the flag has
// to go after the last bare word rather than after args[0] — `gh pr --repo X
// list` is not valid.
func appendWithRepo(args []string, repo string) []string {
	if len(args) == 0 {
		return args
	}
	// Find the first flag; everything before it is subcommand words.
	split := len(args)
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			split = i
			break
		}
	}
	// A positional argument (a PR number) may sit among those words; keep the
	// flag after it, which gh accepts either way.
	out := make([]string, 0, len(args)+2)
	out = append(out, args[:split]...)
	out = append(out, "--repo", repo)
	out = append(out, args[split:]...)
	return out
}

// AuthStatus returns nil if `gh` is authenticated, an error otherwise.
func (c *Client) AuthStatus(ctx context.Context) error {
	// auth status has no --repo flag, but a scoped client still supplies the
	// credential selected for that repository through GH_TOKEN.
	_, err := c.execRaw(ctx, "auth", "status")
	return err
}

// AuthStatusFor reports whether the account named by selector is usable. It is
// the whole-account check that pairs with CredentialToken, so callers verifying
// a configured account list need not build scoped clients themselves.
func (c *Client) AuthStatusFor(ctx context.Context, selector string) error {
	return c.WithCredentialRepo(selector).AuthStatus(ctx)
}
