package repodetect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmux puts a `tmux` on PATH that prints the given list-panes output, so
// pane scanning is exercised without a real tmux session — which a test cannot
// rely on and must not reach into.
func fakeTmux(t *testing.T, panes string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'PANES'\n" + panes + "\nPANES\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
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

	fakeTmux(t, "0\t"+other+"\n1\t"+third)

	got := slugs(DetectAll(context.Background(), launch))
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
	fakeTmux(t, "1\t"+repo+"\n0\t"+repo+"\n0\t"+sub)

	got := DetectAll(context.Background(), repo)
	if len(got) != 1 || got[0].Slug != "keyolk/ghx" {
		t.Errorf("DetectAll = %v, want one keyolk/ghx entry", slugs(got))
	}
}

// The launch directory wins over a pane showing the same repository, so the
// leading tab keeps the stronger cwd source.
func TestDetectAllPrefersWorkingDirectoryOverPane(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/keyolk/ghx.git")
	fakeTmux(t, "1\t"+repo)

	got := DetectAll(context.Background(), repo)
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

	fakeTmux(t, "1\t"+plain+"\n0\t"+gitlab+"\n0\t"+noRemote+"\n0\t/definitely/not/here")

	got := slugs(DetectAll(context.Background(), launch))
	if len(got) != 1 || got[0] != "keyolk/ghx" {
		t.Errorf("DetectAll = %v, want only keyolk/ghx", got)
	}
}

// ghx is often launched from a scratch directory beside the code; the window's
// panes are then the only signal.
func TestDetectAllFindsPaneReposFromNonRepoLaunchDirectory(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/acme/widget.git")
	fakeTmux(t, "1\t"+repo)

	got := DetectAll(context.Background(), t.TempDir())
	if len(got) != 1 || got[0].Slug != "acme/widget" {
		t.Fatalf("DetectAll = %v, want acme/widget", slugs(got))
	}
	if got[0].Source != "tmux" {
		t.Errorf("source = %q, want tmux", got[0].Source)
	}
}

// Outside tmux only the working directory is consulted.
func TestDetectAllWithoutTmuxUsesOnlyWorkingDirectory(t *testing.T) {
	t.Setenv("TMUX", "")
	repo := t.TempDir()
	initRepo(t, repo, "https://github.com/keyolk/ghx.git")

	got := DetectAll(context.Background(), repo)
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
	fakeTmux(t, "1\t"+other)

	if got := Detect(context.Background(), launch); got.Slug != "keyolk/ghx" {
		t.Errorf("Detect = %q, want keyolk/ghx", got.Slug)
	}
}

// A window with nothing checked out anywhere yields nothing, rather than a
// guess.
func TestDetectAllEmptyWhenNothingIsARepository(t *testing.T) {
	fakeTmux(t, "1\t"+t.TempDir())
	if got := DetectAll(context.Background(), t.TempDir()); len(got) != 0 {
		t.Errorf("DetectAll = %v, want none", slugs(got))
	}
}
