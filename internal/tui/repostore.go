package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/cachefile"
)

// A queue is not the only way people navigate. "Open sendbird/ops-k8s" is a
// thing a reviewer wants to do directly, and neither the tab strip nor the
// text filter can do it: the strip holds the repos ghx guessed at startup, and
// the filter only narrows rows that were already fetched. A repo whose PRs are
// in none of the queues is unreachable without editing the config.
//
// So repositories are ranked and offered on their own. What makes the ranking
// worth keeping rather than a plain alphabetical list is that people revisit a
// handful of repos constantly and the rest almost never — the top three are
// usually the whole answer.

// repoVisit is one repository's usage record.
type repoVisit struct {
	Visits int       `json:"visits"`
	LastAt time.Time `json:"last_at"`
}

// repoStore records which repositories the user actually works in, persisted to
// ~/.config/ghx/repos.json.
//
// An unreadable file is an empty history — the picker still lists everything
// visible in the loaded queues. A failed write is reported, though: it means
// every later visit will be lost too, and silence would leave the ranking
// mysteriously frozen.
type repoStore struct {
	path   string
	visits map[string]repoVisit
}

// repoStoreFile is the on-disk shape. Keyed by lowercased slug so a repo typed
// with different capitalization does not split into two entries.
type repoStoreFile struct {
	Repos map[string]repoVisit `json:"repos"`
}

func newRepoStore() *repoStore {
	home, err := os.UserHomeDir()
	if err != nil {
		return &repoStore{visits: map[string]repoVisit{}}
	}
	return newRepoStoreAt(filepath.Join(home, ".config", "ghx", "repos.json"))
}

func newRepoStoreAt(path string) *repoStore {
	s := &repoStore{path: path, visits: map[string]repoVisit{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var file repoStoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		return s
	}
	for slug, v := range file.Repos {
		s.visits[strings.ToLower(slug)] = v
	}
	return s
}

// record counts a visit to slug and returns the command that persists it.
//
// Writing on every visit rather than at exit is deliberate: ghx is a TUI people
// close with ^C and terminal windows people close outright, so there is no
// shutdown path reliable enough to hold the only copy of this.
//
// The in-memory count moves now, so the picker reflects the visit at once; the
// disk write runs as a tea.Cmd because record is called from Update, and file
// IO there stalls every frame behind the filesystem.
func (s *repoStore) record(slug string, at time.Time) tea.Cmd {
	if s == nil {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(slug))
	if !validRepoSlug(key) {
		return nil
	}
	v := s.visits[key]
	v.Visits++
	v.LastAt = at
	s.visits[key] = v
	if s.path == "" {
		return nil
	}
	path := s.path
	return func() tea.Msg {
		if err := persistVisit(path, key, at); err != nil {
			return errMsg{err: fmt.Errorf("save repo history: %w", err)}
		}
		return nil
	}
}

// persistVisit adds one visit to the file at path.
//
// The file is shared by every ghx window, so it re-reads and merges under a
// cross-process lock rather than writing this process's map: a snapshot write
// would drop whatever visits the other windows recorded since this one
// started. The write itself goes through cachefile so a reader never sees a
// half-written file — a torn file reads as an empty history.
func persistVisit(path, key string, at time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), repoStoreLockWait)
	defer cancel()
	release := cachefile.Lock(ctx, path+".lock")
	defer release()

	var file repoStoreFile
	if err := cachefile.ReadJSON(path, &file); err != nil {
		// A missing or corrupt file is an empty history, as on load. Reset
		// rather than keep whatever a partial decode left behind.
		file = repoStoreFile{}
	}
	if file.Repos == nil {
		file.Repos = map[string]repoVisit{}
	}
	v := file.Repos[key]
	v.Visits++
	if at.After(v.LastAt) {
		v.LastAt = at
	}
	file.Repos[key] = v
	return cachefile.WriteJSON(path, file)
}

// repoStoreLockWait bounds how long a visit waits for another window's write.
// The holder only reads and rewrites a small JSON file, so anything longer is a
// wedged process, and proceeding unlocked beats never recording the visit.
const repoStoreLockWait = 2 * time.Second

// score ranks a repository by how much it is used and how recently.
//
// Visits alone would freeze last quarter's repo at the top forever; recency
// alone would put a repo opened once by accident above the one opened daily for
// a year. Multiplying is what lets a genuinely current repo overtake a stale
// heavy hitter within about a week without ever losing the history.
//
// The half-life is a week because that is roughly the span of "what I am
// working on right now" — a repo untouched for a month should be findable but
// not offered first.
func (s *repoStore) score(slug string, now time.Time) float64 {
	v, ok := s.visits[strings.ToLower(slug)]
	if !ok || v.Visits == 0 {
		return 0
	}
	days := now.Sub(v.LastAt).Hours() / 24
	if days < 0 {
		// A clock that moved backwards (or a record written on another machine)
		// must not score above a visit made a moment ago.
		days = 0
	}
	// 1 / 2^(days/7): full weight today, half after a week, ~1/8 after three.
	recency := 1.0
	for range int(days / 7) {
		recency /= 2
	}
	if rem := days - float64(int(days/7))*7; rem > 0 {
		recency *= 1 - rem/14 // linear inside the current half-life step
	}
	return float64(v.Visits) * recency
}

// repoCandidate is one row of the picker.
type repoCandidate struct {
	Slug string
	// Visits is 0 for a repository only seen in a queue. The picker shows the
	// difference so a row nobody has opened does not look like a favourite.
	Visits int
	// OpenPRs is how many rows of the loaded queues belong to this repo, which
	// is what makes a never-visited repo worth offering at all.
	OpenPRs int
	score   float64
}

// rankRepos merges the visit history with whatever the loaded queues contain
// and orders the result: used repositories by score, then the rest by how many
// PRs of theirs are on screen, then alphabetically.
//
// Queue repos are included because the history is empty on first run, and a
// picker that opens empty teaches people it is broken before it has had a
// chance to learn anything.
func (s *repoStore) rankRepos(seen map[string]int, now time.Time) []repoCandidate {
	byKey := make(map[string]*repoCandidate)
	add := func(slug string) *repoCandidate {
		key := strings.ToLower(slug)
		if c, ok := byKey[key]; ok {
			return c
		}
		c := &repoCandidate{Slug: slug}
		byKey[key] = c
		return c
	}
	if s != nil {
		for key, v := range s.visits {
			c := add(key)
			c.Visits = v.Visits
			c.score = s.score(key, now)
		}
	}
	for slug, n := range seen {
		if !validRepoSlug(slug) {
			continue
		}
		c := add(slug)
		// The queue's spelling wins: it came from the API, while the stored key
		// was lowercased for matching.
		c.Slug = slug
		c.OpenPRs = n
	}

	out := make([]repoCandidate, 0, len(byKey))
	for _, c := range byKey {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.score > 0) != (b.score > 0) {
			return a.score > 0
		}
		if a.score != b.score {
			return a.score > b.score
		}
		if a.OpenPRs != b.OpenPRs {
			return a.OpenPRs > b.OpenPRs
		}
		return strings.ToLower(a.Slug) < strings.ToLower(b.Slug)
	})
	return out
}

// validRepoSlug reports whether s is "owner/name" with both halves present and
// no extra path segments. It is what stops a typo from becoming a tab that
// every later gh call fails against.
func validRepoSlug(s string) bool {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || name == "" {
		return false
	}
	return !strings.Contains(name, "/")
}
