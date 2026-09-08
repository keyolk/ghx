package repodetect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Slug parsing is the part that decides whether a detected directory maps to a
// GitHub repository at all, so it is worth pinning down precisely: a wrong slug
// sends every later gh call to the wrong place, and a non-GitHub remote must not
// be offered to the GitHub API at all.
func TestSlugFromURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"https", "https://github.com/keyolk/ghx.git", "keyolk/ghx"},
		{"https without .git", "https://github.com/acme/widget", "acme/widget"},
		{"ssh scp form", "git@github.com:acme/soda.git", "acme/soda"},
		{"ssh url form", "ssh://git@github.com/acme/ops.git", "acme/ops"},
		{"https with credentials", "https://user@github.com/o/n.git", "o/n"},
		{"trailing slash", "https://github.com/o/n/", "o/n"},
		{"whitespace", "  https://github.com/o/n.git\n", "o/n"},

		// Anything not GitHub must be rejected rather than guessed at.
		{"gitlab", "git@gitlab.com:o/n.git", ""},
		{"bitbucket", "https://bitbucket.org/o/n.git", ""},
		{"local path", "/srv/git/repo.git", ""},
		{"file url", "file:///srv/git/repo", ""},
		{"empty", "", ""},
		{"host only", "https://github.com/", ""},
		{"owner only", "https://github.com/owner", ""},
		// A lookalike host must not pass: evil-github.com is not GitHub.
		{"lookalike host", "https://evil-github.com/o/n.git", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SlugFromURL(c.url); got != c.want {
				t.Errorf("SlugFromURL(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

// A subdomain of github.com (an Enterprise host) is still GitHub.
func TestSlugFromURLAcceptsEnterpriseSubdomain(t *testing.T) {
	if got := SlugFromURL("https://code.github.com/o/n.git"); got != "o/n" {
		t.Errorf("got %q, want o/n", got)
	}
}

// initRepo makes a real git repository so detection is exercised against git
// itself rather than a stub — the parsing is only useful if it matches what git
// actually prints.
func initRepo(t *testing.T, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	if remote != "" {
		runGit(t, dir, "remote", "add", "origin", remote)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDetectFromWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir, "https://github.com/keyolk/ghx.git")

	got := Detect(context.Background(), dir)
	if !got.Found() {
		t.Fatal("nothing detected in a repository with a GitHub remote")
	}
	if got.Slug != "keyolk/ghx" {
		t.Errorf("slug = %q, want keyolk/ghx", got.Slug)
	}
	if got.Source != "cwd" {
		t.Errorf("source = %q, want cwd", got.Source)
	}
}

// A subdirectory belongs to the same repository as its root.
func TestDetectFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root, "git@github.com:acme/widget.git")
	sub := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Detect(context.Background(), sub)
	if got.Slug != "acme/widget" {
		t.Errorf("slug = %q, want acme/widget", got.Slug)
	}
}

// A linked worktree must resolve to the repository it came from: those live under
// paths like `<repo>/.worktree/<branch>`, and treating them as unknown would drop
// the detection exactly when the user is deep in a task.
func TestDetectFromLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root, "https://github.com/acme/soda.git")
	// A worktree needs at least one commit to branch from.
	writeFile(t, filepath.Join(root, "f.txt"), "x")
	runGit(t, root, "add", "f.txt")
	runGit(t, root, "-c", "user.email=t@e", "-c", "user.name=t", "commit", "-qm", "init")

	wt := filepath.Join(root, ".worktree", "feature")
	runGit(t, root, "worktree", "add", "-q", "-b", "feature", wt)

	got := Detect(context.Background(), wt)
	if got.Slug != "acme/soda" {
		t.Errorf("slug = %q, want acme/soda (a worktree is still the repo)", got.Slug)
	}
}

// Outside a repository nothing is detected — and specifically no guess is made,
// because a wrong leading tab is worse than none.
func TestDetectOutsideRepositoryFindsNothing(t *testing.T) {
	// TMUX is cleared so the fallback cannot reach into a real session and make
	// the result depend on the developer's terminal.
	t.Setenv("TMUX", "")

	got := Detect(context.Background(), t.TempDir())
	if got.Found() {
		t.Errorf("detected %q outside any repository", got.Slug)
	}
}

// A repository whose remote is not GitHub yields nothing: its PRs do not live
// anywhere gh can reach.
func TestDetectIgnoresNonGitHubRemote(t *testing.T) {
	t.Setenv("TMUX", "")
	dir := t.TempDir()
	initRepo(t, dir, "git@gitlab.com:group/project.git")

	if got := Detect(context.Background(), dir); got.Found() {
		t.Errorf("detected %q from a GitLab remote", got.Slug)
	}
}

// A repository with no remote at all is not an error, just not detectable.
func TestDetectRepositoryWithoutRemote(t *testing.T) {
	t.Setenv("TMUX", "")
	dir := t.TempDir()
	initRepo(t, dir, "")

	if got := Detect(context.Background(), dir); got.Found() {
		t.Errorf("detected %q with no remote configured", got.Slug)
	}
}

// origin wins over other remotes: on a fork it is the one being pushed to, so it
// is the repo whose PRs the user cares about.
func TestDetectPrefersOrigin(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir, "https://github.com/me/fork.git")
	runGit(t, dir, "remote", "add", "upstream", "https://github.com/acme/original.git")

	got := Detect(context.Background(), dir)
	if got.Slug != "me/fork" {
		t.Errorf("slug = %q, want me/fork (origin takes precedence)", got.Slug)
	}
}

// With only an upstream remote, that one is used rather than giving up.
func TestDetectFallsBackToUpstream(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir, "")
	runGit(t, dir, "remote", "add", "upstream", "https://github.com/acme/original.git")

	got := Detect(context.Background(), dir)
	if got.Slug != "acme/original" {
		t.Errorf("slug = %q, want acme/original", got.Slug)
	}
}

// A remote under some other name still identifies the repo.
func TestDetectFallsBackToAnyRemote(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir, "")
	runGit(t, dir, "remote", "add", "fork", "https://github.com/o/n.git")

	if got := Detect(context.Background(), dir); got.Slug != "o/n" {
		t.Errorf("slug = %q, want o/n", got.Slug)
	}
}

// A nonexistent or non-directory path must be handled quietly: detection runs at
// startup and may be handed anything.
func TestDetectHandlesBadPaths(t *testing.T) {
	t.Setenv("TMUX", "")
	file := filepath.Join(t.TempDir(), "a-file")
	writeFile(t, file, "not a directory")

	for _, p := range []string{"", "/definitely/not/here", file} {
		if got := Detect(context.Background(), p); got.Found() {
			t.Errorf("Detect(%q) returned %q", p, got.Slug)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Remote lookup went from one git process per remote to one for all of them,
// because a process is the expense and the detection deadline is what runs out.
// The parsing that replaced `remote get-url` has to answer identically.
func TestParseRemoteURLs(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want map[string]string
	}{
		{"empty", "", map[string]string{}},
		{"one", "remote.origin.url https://github.com/o/n.git\n",
			map[string]string{"origin": "https://github.com/o/n.git"}},
		{"several", "remote.origin.url git@github.com:o/n.git\nremote.upstream.url https://github.com/u/n.git\n",
			map[string]string{"origin": "git@github.com:o/n.git", "upstream": "https://github.com/u/n.git"}},
		// A remote name may contain dots. Splitting the key on every dot would
		// drop such a remote, and with it the only URL some repos have.
		{"dotted name", "remote.origin.old.url https://github.com/o/n.git\n",
			map[string]string{"origin.old": "https://github.com/o/n.git"}},
		// Other remote.* keys share the prefix and must not be read as URLs.
		{"non-url keys ignored", "remote.origin.fetch +refs/heads/*:refs/remotes/origin/*\nremote.origin.url https://github.com/o/n.git\n",
			map[string]string{"origin": "https://github.com/o/n.git"}},
		{"malformed lines skipped", "garbage\nremote.origin.url https://github.com/o/n.git\n\n",
			map[string]string{"origin": "https://github.com/o/n.git"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseRemoteURLs(c.out)
			if len(got) != len(c.want) {
				t.Fatalf("parseRemoteURLs = %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("remote %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// A repository whose only remote is neither origin nor upstream is still
// resolved, and always to the same one — map iteration order would otherwise
// make a two-remote repo pick differently between runs.
func TestSlugFromUnconventionalRemoteIsStable(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir, "")
	runGit(t, dir, "remote", "add", "zeta", "https://github.com/z/last.git")
	runGit(t, dir, "remote", "add", "alpha", "https://github.com/a/first.git")

	for i := 0; i < 3; i++ {
		got := Detect(context.Background(), dir)
		if got.Slug != "a/first" {
			t.Fatalf("run %d: slug = %q, want a/first (the stable first remote)", i, got.Slug)
		}
	}
}
