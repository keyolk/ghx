package repodetect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/keyolk/tuikit/tmux"
)

// outside is a Detector with no tmux: its Client runs nothing and answers
// tmux.ErrNotInTmux, so only the launch directory is consulted.
var outside = New(func(string) string { return "" })

// ownPane is the pane the fake detector reports as ghx's own; its window is the
// one listed.
const ownPane = "%7"

// fakeTmux returns a Detector whose tmux answers list-panes with the given
// panes, one "<active>\t<dir>" per line, and the Fake it records calls on.
//
// A tmux.Fake rather than a `tmux` script on PATH: nothing is spawned, so the
// test cannot reach the developer's real tmux and pays no exec cost — the
// script version spent ~250ms per new path on macOS's first-exec signature
// check, which under a parallel `go test ./...` was enough to blow the
// detection deadline and fail as an empty result.
func fakeTmux(t *testing.T, panes string) (Detector, *tmux.Fake) {
	t.Helper()
	f := tmux.NewFake().On("list-panes", paneRows(panes), nil)
	return Detector{Tmux: tmux.Client{Runner: f}, Pane: ownPane}, f
}

// paneRows renders "<active>\t<dir>" lines with paneRow.
func paneRows(panes string) string {
	var b strings.Builder
	for i, line := range strings.Split(panes, "\n") {
		if active, dir, ok := strings.Cut(line, "\t"); ok {
			b.WriteString(paneRow(i, active, dir))
		}
	}
	return b.String()
}

// paneRow renders one pane in the format tmux.Client asks list-panes for: ten
// tab-separated fixed fields, then each free-text field as "<bytes>:<value>".
func paneRow(index int, active, dir string) string {
	var b strings.Builder
	fixed := []string{fmt.Sprintf("%%%d", index), "@1", "$1", "0", strconv.Itoa(index), "100",
		active, "1", "80", "24"}
	b.WriteString(strings.Join(fixed, "\t"))
	for _, v := range []string{"main", "work", dir, "zsh", "title"} {
		fmt.Fprintf(&b, "\t%d:%s", len(v), v)
	}
	b.WriteByte('\n')
	return b.String()
}

func slugs(results []Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Slug)
	}
	return out
}

// A tmux window is one task spanning several checkouts, so every pane repo is
// surfaced — not only the one the cursor is in.
func TestDetectAllReturnsEveryPaneRepository(t *testing.T) {
	launch := t.TempDir()
	initRepo(t, launch, "https://github.com/keyolk/ghx.git")
	other := t.TempDir()
	initRepo(t, other, "https://github.com/sendbird/platform-tools.git")
	third := t.TempDir()
	initRepo(t, third, "git@github.com:keyolk/kmd.git")

	d, _ := fakeTmux(t, "0\t"+other+"\n1\t"+third)

	got := slugs(d.DetectAll(context.Background(), launch))
	// cwd leads, then the active pane, then the remaining panes in tmux order.
	want := []string{"keyolk/ghx", "keyolk/kmd", "sendbird/platform-tools"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("DetectAll = %v, want %v", got, want)
	}
}

// The same checkout split across panes must not produce duplicate tabs.
func TestDetectAllDeduplicatesRepeatedPanes(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/keyolk/ghx.git")
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Same path twice, plus a subdirectory of the same repository.
	d, _ := fakeTmux(t, "1\t"+repo+"\n0\t"+repo+"\n0\t"+sub)

	got := d.DetectAll(context.Background(), repo)
	if len(got) != 1 || got[0].Slug != "keyolk/ghx" {
		t.Errorf("DetectAll = %v, want one keyolk/ghx entry", slugs(got))
	}
}

// The launch directory wins over a pane showing the same repository, so the
// leading tab keeps the stronger cwd source.
func TestDetectAllPrefersWorkingDirectoryOverPane(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/keyolk/ghx.git")
	d, _ := fakeTmux(t, "1\t"+repo)

	got := d.DetectAll(context.Background(), repo)
	if len(got) != 1 {
		t.Fatalf("DetectAll = %v, want one entry", slugs(got))
	}
	if got[0].Source != "cwd" {
		t.Errorf("source = %q, want cwd", got[0].Source)
	}
}

// Panes holding logs, shells, or non-GitHub checkouts are skipped rather than
// producing a tab that cannot be queried.
func TestDetectAllSkipsPanesWithoutGitHubRepository(t *testing.T) {
	launch := t.TempDir()
	initRepo(t, launch, "https://github.com/keyolk/ghx.git")
	plain := t.TempDir()  // not a repository at all
	gitlab := t.TempDir() // a repository, but not on GitHub
	initRepo(t, gitlab, "git@gitlab.com:group/project.git")
	noRemote := t.TempDir()
	initRepo(t, noRemote, "")

	d, _ := fakeTmux(t, "1\t"+plain+"\n0\t"+gitlab+"\n0\t"+noRemote+"\n0\t/definitely/not/here")

	got := slugs(d.DetectAll(context.Background(), launch))
	if len(got) != 1 || got[0] != "keyolk/ghx" {
		t.Errorf("DetectAll = %v, want only keyolk/ghx", got)
	}
}

// ghx is often launched from a scratch directory beside the code; the window's
// panes are then the only signal.
func TestDetectAllFindsPaneReposFromNonRepoLaunchDirectory(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/acme/widget.git")
	d, _ := fakeTmux(t, "1\t"+repo)

	got := d.DetectAll(context.Background(), t.TempDir())
	if len(got) != 1 || got[0].Slug != "acme/widget" {
		t.Fatalf("DetectAll = %v, want acme/widget", slugs(got))
	}
	if got[0].Source != "tmux" {
		t.Errorf("source = %q, want tmux", got[0].Source)
	}
}

// Outside tmux only the working directory is consulted.
func TestDetectAllWithoutTmuxUsesOnlyWorkingDirectory(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/keyolk/ghx.git")

	got := outside.DetectAll(context.Background(), repo)
	if len(got) != 1 || got[0].Slug != "keyolk/ghx" {
		t.Errorf("DetectAll = %v, want one keyolk/ghx entry", slugs(got))
	}
}

// Detect stays the single-repository entry point that existing callers use.
func TestDetectReturnsFirstOfDetectAll(t *testing.T) {
	launch := t.TempDir()
	initRepo(t, launch, "https://github.com/keyolk/ghx.git")
	other := t.TempDir()
	initRepo(t, other, "https://github.com/sendbird/platform-tools.git")
	d, _ := fakeTmux(t, "1\t"+other)

	if got := d.Detect(context.Background(), launch); got.Slug != "keyolk/ghx" {
		t.Errorf("Detect = %q, want keyolk/ghx", got.Slug)
	}
}

// A window with nothing checked out anywhere yields nothing, rather than a
// guess.
func TestDetectAllEmptyWhenNothingIsARepository(t *testing.T) {
	d, _ := fakeTmux(t, "1\t"+t.TempDir())
	if got := d.DetectAll(context.Background(), t.TempDir()); len(got) != 0 {
		t.Errorf("DetectAll = %v, want none", slugs(got))
	}
}

// The window listed is ghx's own pane's window, named by ID: a bare
// `list-panes` resolved "current" inside tmux, and an index would move when the
// window is renumbered.
func TestDetectAllListsItsOwnPanesWindowByID(t *testing.T) {
	d, f := fakeTmux(t, "1\t"+t.TempDir())
	d.DetectAll(context.Background(), t.TempDir())

	want := []string{"list-panes", "-t", ownPane, "-F", tmux.PaneFormat(nil)}
	calls := f.Calls()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Errorf("tmux calls = %q, want exactly %q", calls, want)
	}
}

// Without $TMUX_PANE the window the user is looking at is asked for first and
// then listed by its ID — never by an empty target.
func TestDetectAllWithoutOwnPaneListsTheCurrentWindow(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/acme/widget.git")
	f := tmux.NewFake().
		On("display-message", "$1\t@4\t%9\n", nil).
		On("list-panes", paneRows("1\t"+repo), nil)
	d := Detector{Tmux: tmux.Client{Runner: f}}

	got := d.DetectAll(context.Background(), t.TempDir())
	if len(got) != 1 || got[0].Slug != "acme/widget" {
		t.Fatalf("DetectAll = %v, want acme/widget", slugs(got))
	}
	if calls := f.CallLines(); len(calls) != 2 || !strings.HasPrefix(calls[1], "list-panes -t @4 ") {
		t.Errorf("tmux calls = %q, want display-message then list-panes -t @4", calls)
	}
}

// A directory name holding a newline is still one pane. tmux prints the
// newline raw, and the old "<active>\t<path>" per line split read it as two
// rows, neither of them a real directory, so that checkout was never detected.
func TestDetectAllReadsAPanePathHoldingANewline(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "odd\nname")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, repo, "https://github.com/acme/newline.git")
	f := tmux.NewFake().On("list-panes", paneRow(0, "1", repo), nil)
	d := Detector{Tmux: tmux.Client{Runner: f}, Pane: ownPane}

	got := d.DetectAll(context.Background(), t.TempDir())
	if len(got) != 1 || got[0].Slug != "acme/newline" {
		t.Errorf("DetectAll = %v, want acme/newline from the pane whose path holds a newline", slugs(got))
	}
}
