package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/keyolk/tuikit/link"
	"github.com/keyolk/tuikit/selection"
)

// TestMain gives the whole package one empty home directory.
//
// gh.NewClient reads the account's shared GraphQL budget from
// ~/.config/ghx/cache/budget.json, and the poll interval backs off when that
// budget runs low. With the real home, every cadence assertion here passed or
// failed depending on how much of the developer's own budget the ghx windows
// parked in tmux had spent that hour. One home for the package rather than
// t.Setenv per test: testApp is called several times per test, and those models
// expect to share a cache directory.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ghx-tui-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test home:", err)
		os.Exit(1)
	}
	// The XDG variables win over HOME when the developer has them set, and
	// ghx reads keys.yaml from one and writes ui.json under the other.
	for k, v := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "set "+k+":", err)
			os.Exit(1)
		}
	}
	// A decoy against the developer's tmux. The workspace sweep lists the
	// window's panes through repodetect.New(nil), which reads $TMUX; with it
	// unset that Client runs nothing, so a test that drives a sweep cannot
	// list — or depend on — the panes of the window `go test` was started in.
	// Tests that need panes hand the workspace a tmux.Fake.
	for _, k := range []string{"TMUX", "TMUX_PANE"} {
		if err := os.Unsetenv(k); err != nil {
			fmt.Fprintln(os.Stderr, "unset "+k+":", err)
			os.Exit(1)
		}
	}
	// Fail closed on the real clipboard and browser: a test that means to copy
	// or open installs its own recorder; one that forgets gets an error toast
	// instead of writing the developer's clipboard or opening a browser tab.
	selection.Clipboard = func(string) error { return errors.New("test: clipboard not stubbed") }
	link.Launcher = func(string) error { return errors.New("test: launcher not stubbed") }
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintln(os.Stderr, "remove test home:", err)
	}
	os.Exit(code)
}
