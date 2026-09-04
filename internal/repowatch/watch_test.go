package repowatch

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These tests drive real git rather than stubbing the file layout. The whole
// package is a claim about where git writes, and a stub would assert that claim
// against itself — which is how the wrong paths get watched and the feature
// silently never fires.

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		// A developer's ~/.gitconfig can set init.templateDir, hooks, or a
		// default branch; the test asserts about git's own layout, not theirs.
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", name)
}

// initRepo makes a checkout with a bare "remote" it can push to, which is what
// a tracking ref needs to exist at all.
func initRepo(t *testing.T) (work string) {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	git(t, base, "init", "-q", "--bare", "bare.git")
	git(t, base, "clone", "-q", bare, "work")
	work = filepath.Join(base, "work")
	commit(t, work, "first")
	git(t, work, "push", "-q", "-u", "origin", "HEAD")
	return work
}

// settle keeps a filesystem with one-second mtime granularity from reporting
// two distinct writes as the same instant. APFS is finer than that, but the
// test must not depend on which filesystem TMPDIR is on.
func settle() { time.Sleep(1100 * time.Millisecond) }

func TestCommitIsSeen(t *testing.T) {
	work := initRepo(t)
	w := NewWatcher(work)
	if w == nil {
		t.Fatal("no watcher for a real checkout")
	}
	before := w.Snapshot()
	if before.Empty() {
		t.Fatal("snapshot of a real checkout is empty")
	}

	settle()
	commit(t, work, "second")

	if !w.Snapshot().Changed(before) {
		t.Error("a commit did not register as a change")
	}
}

// The reflog is the primary signal for a commit, but it is optional: a
// repository with core.logAllRefUpdates off has no logs/HEAD to append to, and
// HEAD itself does not move when committing to the branch already checked out.
// The branch ref does, which is what keeps such a repo from looking untouched
// through an afternoon of work.
func TestCommitIsSeenWithoutReflogs(t *testing.T) {
	work := initRepo(t)
	git(t, work, "config", "core.logAllRefUpdates", "false")
	if err := os.RemoveAll(filepath.Join(work, ".git", "logs")); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(work)
	before := w.Snapshot()

	settle()
	commit(t, work, "second")

	if !w.Snapshot().Changed(before) {
		t.Error("a commit in a repo without reflogs was not seen")
	}
}

func TestBranchCheckoutIsSeen(t *testing.T) {
	work := initRepo(t)
	w := NewWatcher(work)
	before := w.Snapshot()

	settle()
	git(t, work, "checkout", "-qb", "feature")

	if !w.Snapshot().Changed(before) {
		t.Error("switching branches did not register as a change")
	}
}

// The event this whole feature is for: a branch pushed in the pane beside ghx
// is about to become a PR, and the queue should not wait for the next tick.
func TestPushIsSeen(t *testing.T) {
	work := initRepo(t)
	git(t, work, "checkout", "-qb", "feature")
	commit(t, work, "second")

	w := NewWatcher(work)
	before := w.Snapshot()

	settle()
	git(t, work, "push", "-q", "origin", "HEAD")

	if !w.Snapshot().Changed(before) {
		t.Error("a push did not register as a change")
	}
}

// A slash in the branch name puts the tracking ref in a subdirectory, so a
// watcher that only stats refs/remotes/<remote> would miss the update. This is
// the ordinary case, not an edge one — every ticket-prefixed branch has one.
func TestPushToNestedBranchIsSeen(t *testing.T) {
	work := initRepo(t)
	git(t, work, "checkout", "-qb", "feat/nested/deep")
	commit(t, work, "second")
	git(t, work, "push", "-q", "-u", "origin", "HEAD")

	w := NewWatcher(work)
	before := w.Snapshot()

	settle()
	commit(t, work, "third")
	git(t, work, "push", "-q", "origin", "HEAD")

	if !w.Snapshot().Changed(before) {
		t.Error("a push updating an existing nested tracking ref was not seen")
	}
}

func TestFetchIsSeen(t *testing.T) {
	work := initRepo(t)
	w := NewWatcher(work)
	before := w.Snapshot()

	settle()
	git(t, work, "fetch", "-q", "origin")

	if !w.Snapshot().Changed(before) {
		t.Error("a fetch did not register as a change")
	}
}

// The counterpart that makes this safe to run on an unfocused window: a
// checkout nobody touched must produce no event, or the budget saving is gone.
func TestQuietRepoReportsNoChange(t *testing.T) {
	work := initRepo(t)
	w := NewWatcher(work)
	before := w.Snapshot()

	settle()

	if w.Snapshot().Changed(before) {
		t.Error("an untouched checkout reported a change")
	}
}

// The first observation is a baseline, not an event. Treating it as one would
// fire a fetch per detected repository at startup, on top of the fetches
// startup already does.
func TestFirstSnapshotIsNotAChange(t *testing.T) {
	work := initRepo(t)
	w := NewWatcher(work)
	if w.Snapshot().Changed(Snapshot{}) {
		t.Error("the first snapshot reported a change against nothing")
	}
}

// A linked worktree commits into its own HEAD under worktrees/<name> while
// sharing refs/remotes with the main repository. Watching only <root>/.git
// would find a file, not a directory, and see nothing at all.
func TestWorktreeCommitIsSeen(t *testing.T) {
	work := initRepo(t)
	tree := filepath.Join(filepath.Dir(work), "wt")
	git(t, work, "worktree", "add", "-q", tree, "-b", "wt")

	w := NewWatcher(tree)
	if w == nil {
		t.Fatal("no watcher for a linked worktree")
	}
	before := w.Snapshot()
	if before.Empty() {
		t.Fatal("worktree snapshot is empty — the gitdir file was not followed")
	}

	settle()
	commit(t, tree, "in-worktree")

	if !w.Snapshot().Changed(before) {
		t.Error("a commit in a linked worktree was not seen")
	}
}

// A push from a worktree updates the shared tracking refs, which live in the
// main repository's directory rather than the worktree's.
func TestWorktreePushIsSeen(t *testing.T) {
	work := initRepo(t)
	tree := filepath.Join(filepath.Dir(work), "wt")
	git(t, work, "worktree", "add", "-q", tree, "-b", "wt")
	commit(t, tree, "in-worktree")

	w := NewWatcher(tree)
	before := w.Snapshot()

	settle()
	git(t, tree, "push", "-q", "-u", "origin", "HEAD")

	if !w.Snapshot().Changed(before) {
		t.Error("a push from a linked worktree was not seen")
	}
}

func TestNonRepoHasNoWatcher(t *testing.T) {
	if w := NewWatcher(t.TempDir()); w != nil {
		t.Error("a directory that is not a checkout produced a watcher")
	}
	if w := NewWatcher(""); w != nil {
		t.Error("an empty path produced a watcher")
	}
}

// A repository deleted out from under ghx must read as gone, not as an event
// that triggers a fetch on every tick forever.
func TestDeletedRepoGoesEmpty(t *testing.T) {
	work := initRepo(t)
	w := NewWatcher(work)
	w.Snapshot()

	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	if !w.Snapshot().Empty() {
		t.Error("a deleted checkout still reports watched paths")
	}
}

func TestParseGitdirFile(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"gitdir: /a/b/.git/worktrees/wt\n", "/a/b/.git/worktrees/wt"},
		{"gitdir: ../.git\n", "../.git"},
		{"gitdir:/no/space", "/no/space"},
		{"not a gitdir file", ""},
		{"", ""},
		{"gitdir:", ""},
	} {
		if got := parseGitdirFile(c.in); got != c.want {
			t.Errorf("parseGitdirFile(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
