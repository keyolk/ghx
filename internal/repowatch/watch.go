// Package repowatch reports when a checkout has been worked in — a commit, a
// checkout, a push, a fetch — without spending a single API request to find out.
//
// The problem it solves is that ghx's queue is refreshed on a timer, and the
// timer knows nothing about what the user is doing. Someone pushes a branch in
// the pane beside ghx and the new PR shows up whenever the next poll happens to
// land, which is up to a poll interval later and, in a window that is not on
// screen, never — polling is suspended there on purpose, because the GraphQL
// budget is per-account and several idle windows share it.
//
// Git writes the answer to disk anyway. Every one of those operations moves a
// file under .git, so watching a handful of paths turns "the user just did
// something to this repo" into a stat() rather than a request. That is what
// makes it safe to refresh a window nobody is looking at: the fetch is caused by
// work that actually happened, so an untouched checkout still costs nothing.
package repowatch

import (
	"os"
	"path/filepath"
	"time"
)

// Which paths, and why each one. Measured against real git, because the
// intuitive choice — the ref files themselves — misses half of what matters:
//
//	commit, checkout, reset, rebase  → logs/HEAD          (the reflog appends)
//	push                             → refs/remotes/<r>/… (the tracking ref moves)
//	fetch, pull                      → FETCH_HEAD + the tracking refs
//
// The remote refs are watched by directory rather than by file. A push writes
// one ref file whose name is the branch, so watching files would mean walking
// every remote branch on every tick — 900 of them in ops-k8s. The parent
// directory's mtime moves when a ref inside it is written, which answers the
// same question in one stat: nested branches (feat/nested/deep) move their own
// leaf directory, so the walk is bounded by depth, not by branch count.
//
// packed-refs is watched too: `git gc` and a fresh clone put refs there instead
// of in individual files, and a repo whose refs are all packed would otherwise
// look permanently quiet.

// Snapshot is the observed state of one repository's git metadata. Two
// snapshots differing means something happened to the checkout.
type Snapshot struct {
	// stamps maps a watched path to its modification time. A path that does not
	// exist is absent rather than zero, so a file appearing (FETCH_HEAD on the
	// first fetch) is a change like any other.
	stamps map[string]time.Time
}

// Changed reports whether s differs from prev.
//
// A zero prev — nothing observed yet — is not a change. The first snapshot is
// the baseline: treating it as an event would fire a fetch for every detected
// repository at startup, on top of the fetches startup already does.
func (s Snapshot) Changed(prev Snapshot) bool {
	if prev.stamps == nil {
		return false
	}
	if len(s.stamps) != len(prev.stamps) {
		return true
	}
	for path, at := range s.stamps {
		was, ok := prev.stamps[path]
		if !ok || !was.Equal(at) {
			return true
		}
	}
	return false
}

// Empty reports whether nothing was observed, which is what a directory that is
// not a git checkout produces.
func (s Snapshot) Empty() bool { return len(s.stamps) == 0 }

// Watcher observes one repository's git directory.
//
// gitDir is the directory git actually writes to, which is not always
// <root>/.git: a linked worktree's .git is a file pointing elsewhere, and its
// HEAD and reflog live under the main repository's worktrees/<name>. Both are
// watched — the worktree's own HEAD for commits made in it, and the common
// directory's remote refs for the pushes those commits turn into.
type Watcher struct {
	gitDir    string // where HEAD and logs/HEAD live for this checkout
	commonDir string // where refs/remotes and packed-refs live; == gitDir outside a worktree
}

// NewWatcher resolves the git directories for a checkout.
//
// It reads the .git entry directly rather than shelling out to `git rev-parse`.
// This runs on the poll path — once per detected repository per tick — and a
// subprocess per repository per tick is exactly the cost this package exists to
// avoid. The formats it has to understand are two: a directory, and a one-line
// "gitdir: <path>" file.
func NewWatcher(root string) *Watcher {
	if root == "" {
		return nil
	}
	dot := filepath.Join(root, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return nil
	}
	gitDir := dot
	if !fi.IsDir() {
		// A linked worktree (or a submodule): ".git" is a file naming the real
		// directory. An unreadable or unrecognized file is not a checkout this
		// package can watch, which is an ordinary outcome, not an error.
		data, readErr := os.ReadFile(dot)
		if readErr != nil {
			return nil
		}
		target := parseGitdirFile(string(data))
		if target == "" {
			return nil
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		gitDir = filepath.Clean(target)
	}
	return &Watcher{gitDir: gitDir, commonDir: commonDirFor(gitDir)}
}

// Snapshot stats the watched paths. It never errors: a repository that has been
// deleted out from under ghx produces an empty snapshot, which reads as "gone"
// rather than as a change to react to.
func (w *Watcher) Snapshot() Snapshot {
	if w == nil {
		return Snapshot{}
	}
	s := Snapshot{stamps: make(map[string]time.Time, 8)}
	// HEAD and its reflog answer "was work done in this checkout": commit,
	// checkout, reset, rebase, merge. The reflog is the more reliable of the
	// two — HEAD itself does not move on a commit to the branch already checked
	// out — but both are cheap and a repo with reflogs disabled still has HEAD.
	s.stat(filepath.Join(w.gitDir, "HEAD"))
	s.stat(filepath.Join(w.gitDir, "logs", "HEAD"))
	// FETCH_HEAD answers "did a fetch or pull just run", including one that
	// brought nothing new — which still means the user asked about this repo.
	s.stat(filepath.Join(w.commonDir, "FETCH_HEAD"))
	s.stat(filepath.Join(w.commonDir, "packed-refs"))
	// The tracking refs answer "was something pushed or fetched into this
	// repo", which is what turns into a new or updated PR.
	for _, dir := range refDirs(filepath.Join(w.commonDir, "refs", "remotes")) {
		s.stat(dir)
	}
	// Local branches are watched for the same reason as HEAD's reflog, and as a
	// backstop for it: `git commit` moves the branch ref whether or not reflogs
	// are enabled, and a repository with core.logAllRefUpdates off would
	// otherwise look untouched through an afternoon of committing.
	for _, dir := range refDirs(filepath.Join(w.commonDir, "refs", "heads")) {
		s.stat(dir)
	}
	return s
}

func (s Snapshot) stat(path string) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	s.stamps[path] = fi.ModTime()
}

// refDirs returns the directories whose mtime moves when a ref beneath root is
// written — root itself and, for branch names containing slashes, the
// directories those names create.
//
// Directories rather than the ref files themselves: a repository has one file
// per branch (900 of them in ops-k8s) and walking those on every sweep would
// make the cost grow with branch count, while a directory's mtime moves when a
// ref inside it is written and answers the same question in one stat.
//
// The walk is depth-bounded because a branch name is a path and nothing stops
// it being deep, while the cost must not grow with how people name branches.
// Four levels covers "origin/feat/nested/deep"; anything deeper still registers
// through its ancestor, because writing the leaf writes the parent directory.
func refDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries)+4)
	out = append(out, root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		out = append(out, dir)
		out = append(out, subdirs(dir, 3)...)
	}
	return out
}

// subdirs returns the directories below dir, at most depth levels deep.
func subdirs(dir string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		out = append(out, sub)
		out = append(out, subdirs(sub, depth-1)...)
	}
	return out
}

// parseGitdirFile extracts the path from a "gitdir: <path>" .git file.
func parseGitdirFile(content string) string {
	const prefix = "gitdir:"
	line := content
	if i := indexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = trimSpace(line)
	if len(line) <= len(prefix) || line[:len(prefix)] != prefix {
		return ""
	}
	return trimSpace(line[len(prefix):])
}

// commonDirFor resolves a worktree git directory to the main repository's.
//
// A linked worktree keeps HEAD, index and its own reflog locally but shares
// refs/remotes with the repository it was created from, and "commondir" is
// where git records that. Outside a worktree the file is absent and the
// directory is its own common directory.
func commonDirFor(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	target := trimSpace(string(data))
	if target == "" {
		return gitDir
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(gitDir, target)
	}
	return filepath.Clean(target)
}

func indexByte(s string, b byte) int {
	for i := range len(s) {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && isSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}
