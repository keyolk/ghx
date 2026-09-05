// Package cachefile is the shared plumbing under ghx's on-disk caches.
//
// The caches are not private to one process. A ghx parked in every tmux window
// is the normal way this tool is used, and all of them read and write the same
// files under ~/.config/ghx/cache. That is the point — what one instance paid
// for, the others get for free — but it makes two things load-bearing that a
// single-process cache could ignore.
//
// Writes are atomic. os.WriteFile truncates before it writes, so a reader that
// opens the file inside that window gets a valid path to invalid JSON, and two
// writers interleave into something neither of them wrote. Either way the entry
// is discarded as corrupt and the instance pays for the fetch it was trying to
// avoid — the exact failure the cache exists to prevent, made *more* likely by
// the sharing that makes the cache worth having. A rename is atomic within one
// filesystem, so a reader sees either the whole old entry or the whole new one.
//
// Names are derived here rather than in each cache, so the one rule that keeps
// a repository name from climbing out of the cache directory is written once.
package cachefile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir returns ~/.config/ghx/cache joined with parts, or "" when there is no
// home directory to put it in. A caller that gets "" disables its cache: a
// process with no home is a process whose cache nobody else can find either.
func Dir(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{home, ".config", "ghx", "cache"}, parts...)...)
}

// Key builds a filesystem-safe name from arbitrary identifiers.
//
// The path separator is replaced first, which is what keeps a repository name
// from escaping the cache directory: with no "/" left, ".." is an ordinary
// two-character filename component rather than a parent.
var keyReplacer = strings.NewReplacer("/", "_", ":", "_", " ", "_", `\`, "_")

// Key joins the parts into one safe filename component.
//
// Stripping the separators is not sufficient on its own: "." and ".." survive
// it unchanged and still name a directory rather than a file in it, so a
// repository called ".." would have its entry written to the cache directory's
// parent. Callers today all append a suffix, which happens to defuse it — but
// that is a property of the callers, not of this function, and the next one
// will not have it.
func Key(parts ...string) string {
	out := keyReplacer.Replace(strings.Join(parts, "|"))
	if out == "" || out == "." || out == ".." {
		return "_" + out
	}
	return out
}

// PRKey names a per-PR entry. The queue spans repositories, so the number
// alone is not unique.
func PRKey(repo string, number int) string {
	return fmt.Sprintf("%s_%d.json", Key(repo), number)
}

// WriteJSON marshals v and writes it atomically to path, creating the
// directory. Every failure is returned rather than surfaced: a cache write is
// an optimization, and the callers all treat an error as "this costs a future
// fetch", never as something the user has to see.
func WriteJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return Write(path, data)
}

// Write puts data at path atomically.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The temporary lands in the destination directory so the rename never
	// crosses a filesystem, which is the one case where it is not atomic.
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ReadJSON decodes path into v. A missing or malformed file is an ordinary
// cache miss, reported as an error the caller ignores.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
