// Package repodetect works out which GitHub repositories the user is currently
// working on, so the PR list can lead with them instead of a generic queue.
//
// Two signals are used, in order of confidence: the working directory's git
// remote, and the current tmux window's pane paths. The directory is the
// stronger signal — it is where the user actually is — but ghx is often launched
// from elsewhere while the work sits in other panes of the same window, and a
// task routinely spans several checkouts at once.
package repodetect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Result is a detected repository and where the hint came from.
type Result struct {
	Slug   string // "owner/name"
	Source string // "cwd" | "tmux" | ""
	Path   string // the directory the slug was resolved from
}

// Found reports whether anything was detected.
func (r Result) Found() bool { return r.Slug != "" }

// timeout bounds the whole detection: it runs at startup, and a slow git call on
// a network filesystem must not delay the first frame.
const timeout = 2 * time.Second

// Detect returns the single repository to prioritise, or an empty Result. It is
// the first of DetectAll's results — the working directory when it is a
// checkout, otherwise the most relevant pane of the current tmux window.
func Detect(ctx context.Context, startDir string) Result {
	all := DetectAll(ctx, startDir)
	if len(all) == 0 {
		return Result{}
	}
	return all[0]
}

// DetectAll returns every GitHub repository visible from where the user is
// working, most relevant first: the working directory, then the current tmux
// window's active pane, then its remaining panes in tmux's own order.
//
// A tmux window is one task. The panes in it are the checkouts that task spans,
// so all of them are worth surfacing — not just the one the cursor happens to
// be in. Order is what keeps that predictable: the directory ghx was launched
// from always leads, the active pane comes next, and the rest follow a stable
// tmux ordering rather than whichever git call returned first. Repositories are
// deduplicated, so the same checkout open in three panes still yields one entry.
//
// startDir is normally the process's working directory; it is a parameter so
// tests can drive detection without chdir.
func DetectAll(ctx context.Context, startDir string) []Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Collect the candidate directories first, deduplicated by path. Panes
	// frequently share a directory (a split beside the same checkout), and
	// skipping those before shelling out to git keeps a wide window fast.
	type candidate struct {
		dir    string
		source string
	}
	var candidates []candidate
	seenDir := make(map[string]bool)
	addDir := func(dir, source string) {
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if seenDir[clean] {
			return
		}
		seenDir[clean] = true
		candidates = append(candidates, candidate{dir: dir, source: source})
	}
	addDir(startDir, "cwd")
	for _, dir := range tmuxWindowPanePaths(ctx) {
		addDir(dir, "tmux")
	}

	// Each directory costs two git invocations, so resolve them concurrently.
	// A wide tmux window otherwise serializes a dozen subprocess round trips
	// into the startup path, delaying the first frame.
	type resolved struct {
		slug string
		root string
	}
	found := make([]resolved, len(candidates))
	var wg sync.WaitGroup
	for i, c := range candidates {
		i, c := i, c
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i].slug, found[i].root = slugFromDir(ctx, c.dir)
		}()
	}
	wg.Wait()

	// Rebuild in candidate order so the working directory still leads and the
	// tmux ordering is preserved regardless of which git call finished first.
	var out []Result
	seenSlug := make(map[string]bool)
	for i, c := range candidates {
		slug := found[i].slug
		if slug == "" {
			continue
		}
		key := strings.ToLower(slug)
		if seenSlug[key] {
			continue
		}
		seenSlug[key] = true
		out = append(out, Result{Slug: slug, Source: c.source, Path: found[i].root})
	}
	return out
}

// slugFromDir resolves a directory to "owner/name" via its git remote, and
// returns the repository root it belongs to.
//
// A linked worktree resolves to the same slug as its main checkout, which is
// what makes `.worktree/<branch>` directories behave as the repo they came from.
func slugFromDir(ctx context.Context, dir string) (slug, root string) {
	if dir == "" {
		return "", ""
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", ""
	}
	root = strings.TrimSpace(gitOut(ctx, dir, "rev-parse", "--show-toplevel"))
	if root == "" {
		return "", ""
	}
	// Try the conventional remote names before falling back to whatever exists,
	// so a fork with both `origin` and `upstream` prefers the one being pushed to.
	for _, remote := range []string{"origin", "upstream"} {
		if url := strings.TrimSpace(gitOut(ctx, dir, "remote", "get-url", remote)); url != "" {
			if s := SlugFromURL(url); s != "" {
				return s, root
			}
		}
	}
	for _, remote := range strings.Fields(gitOut(ctx, dir, "remote")) {
		if url := strings.TrimSpace(gitOut(ctx, dir, "remote", "get-url", remote)); url != "" {
			if s := SlugFromURL(url); s != "" {
				return s, root
			}
		}
	}
	return "", ""
}

// SlugFromURL extracts "owner/name" from a git remote URL. It handles the https
// and ssh forms git writes, and returns "" for anything that is not GitHub —
// a GitLab remote must not be offered to the GitHub API.
func SlugFromURL(url string) string {
	u := strings.TrimSpace(url)
	u = strings.TrimSuffix(u, ".git")

	switch {
	case strings.HasPrefix(u, "git@"):
		// git@github.com:owner/name
		host, path, ok := strings.Cut(strings.TrimPrefix(u, "git@"), ":")
		if !ok || !isGitHubHost(host) {
			return ""
		}
		u = path
	case strings.Contains(u, "://"):
		// https://github.com/owner/name, ssh://git@github.com/owner/name
		_, rest, _ := strings.Cut(u, "://")
		// Drop any user@ prefix on the host.
		if _, after, ok := strings.Cut(rest, "@"); ok {
			rest = after
		}
		host, path, ok := strings.Cut(rest, "/")
		if !ok || !isGitHubHost(host) {
			return ""
		}
		u = path
	default:
		return ""
	}

	parts := strings.Split(strings.Trim(u, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	// Deeper paths (GitHub Enterprise subgroups do not exist, but be tolerant of
	// trailing segments) still identify the repo by its first two components.
	return parts[0] + "/" + parts[1]
}

func isGitHubHost(host string) bool {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i] // strip a port
	}
	return host == "github.com" || strings.HasSuffix(host, ".github.com")
}

// tmuxWindowPanePaths returns the current window's pane directories, the active
// pane first, or nil outside tmux.
//
// list-panes defaults to the current window, which is exactly the scope that
// means "this task": panes in other windows belong to other work. The active
// pane is hoisted to the front because it is the strongest single hint about
// what the user is doing right now; the rest keep tmux's ordering so the tab
// order does not shift between launches.
func tmuxWindowPanePaths(ctx context.Context) []string {
	if os.Getenv("TMUX") == "" {
		return nil
	}
	out := run(ctx, "", "tmux", "list-panes", "-F", "#{pane_active}\t#{pane_current_path}")
	var active, rest []string
	for _, line := range strings.Split(out, "\n") {
		flag, dir, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok || dir == "" {
			continue
		}
		if flag == "1" {
			active = append(active, dir)
			continue
		}
		rest = append(rest, dir)
	}
	return append(active, rest...)
}

func gitOut(ctx context.Context, dir string, args ...string) string {
	return run(ctx, dir, "git", args...)
}

// run executes a command and returns its stdout, or "" on any failure. Detection
// is best-effort: a missing git, a directory that is not a repo, and a tmux that
// is not running are all ordinary outcomes, not errors to report.
func run(ctx context.Context, dir, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
