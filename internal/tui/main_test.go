package tui

import (
	"fmt"
	"os"
	"testing"
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
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, "set HOME:", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintln(os.Stderr, "remove test home:", err)
	}
	os.Exit(code)
}
