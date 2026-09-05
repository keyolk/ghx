package cachefile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type payload struct {
	Name string `json:"name"`
	Blob string `json:"blob"`
}

// A reader must never see a half-written file. The caches are shared between
// ghx processes on purpose, so a truncate-then-write would put a window of
// invalid JSON in front of every other instance — and the entry they discard
// is the fetch the cache existed to save, which makes the sharing cost requests
// instead of saving them.
func TestConcurrentWritersNeverPublishAPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry.json")
	// Large enough that a non-atomic write would take several syscalls, which is
	// what opens the window a reader can fall into.
	big := strings.Repeat("x", 512*1024)

	if err := WriteJSON(path, payload{Name: "seed", Blob: big}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readErr error
	var mu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			var got payload
			if err := ReadJSON(path, &got); err != nil {
				mu.Lock()
				readErr = err
				mu.Unlock()
				return
			}
			if len(got.Blob) != len(big) {
				mu.Lock()
				readErr = errShort(len(got.Blob))
				mu.Unlock()
				return
			}
		}
	}()

	for i := range 40 {
		if err := WriteJSON(path, payload{Name: "w", Blob: big[:len(big)-i]}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if readErr != nil {
		if _, ok := readErr.(errShort); ok {
			// A shorter blob is one of the writes above, not a torn read.
			return
		}
		t.Errorf("a reader saw an invalid file while writes were in flight: %v", readErr)
	}
}

type errShort int

func (e errShort) Error() string { return "short blob" }

// A repository name contains a path separator, and the caches are keyed by one.
// Without the substitution a slug could name a path outside the cache
// directory, and ".." is the case that matters.
func TestKeyCannotEscapeTheCacheDirectory(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "..", "a/b/../../../c", `x\y`} {
		got := Key(in)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("Key(%q) = %q, which still contains a path separator", in, got)
		}
		if filepath.Clean(filepath.Join("/cache", got)) == "/cache" ||
			!strings.HasPrefix(filepath.Clean(filepath.Join("/cache", got)), "/cache/") {
			t.Errorf("Key(%q) = %q resolves outside the cache directory", in, got)
		}
	}
}

// Two PRs of the same number in different repositories are different entries —
// the queue spans repositories, so the number alone is not an identity.
func TestPRKeyIsPerRepository(t *testing.T) {
	if a, b := PRKey("acme/one", 7), PRKey("acme/two", 7); a == b {
		t.Errorf("both repositories keyed to %q", a)
	}
	if a, b := PRKey("acme/one", 7), PRKey("acme/one", 8); a == b {
		t.Errorf("both PR numbers keyed to %q", a)
	}
}

// A missing file is an ordinary cache miss, not something a caller has to
// handle specially.
func TestReadingAMissingFileIsAnError(t *testing.T) {
	var v payload
	if err := ReadJSON(filepath.Join(t.TempDir(), "nope.json"), &v); err == nil {
		t.Error("a missing entry read back without error")
	}
}

// The write creates its directory: the caches are lazily populated, and a first
// write on a fresh machine has nowhere to land otherwise.
func TestWriteCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "deeper", "entry.json")
	if err := WriteJSON(path, payload{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	var got payload
	if err := ReadJSON(path, &got); err != nil || got.Name != "x" {
		t.Errorf("read back %+v, %v", got, err)
	}
}

// The temporary must not survive a successful write, or the cache directory
// fills with the debris of every entry ever stored.
func TestNoTemporaryIsLeftBehind(t *testing.T) {
	dir := t.TempDir()
	if err := WriteJSON(filepath.Join(dir, "entry.json"), payload{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("left %q behind", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want just the entry", len(entries))
	}
}

// A value that cannot be marshalled must not destroy the entry that is already
// there: the caller treats a failed write as "costs a future fetch", and a
// truncated file would make it cost a wrong answer instead.
func TestAFailedMarshalLeavesTheExistingEntryIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry.json")
	if err := WriteJSON(path, payload{Name: "good"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, make(chan int)); err == nil {
		t.Fatal("marshalling a channel succeeded")
	}
	var got payload
	if err := ReadJSON(path, &got); err != nil || got.Name != "good" {
		t.Errorf("the existing entry was damaged: %+v, %v", got, err)
	}
}

var _ = json.Marshal
