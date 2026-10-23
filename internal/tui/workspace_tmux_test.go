package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/keyolk/tuikit/tmux"

	"github.com/keyolk/ghx/internal/repodetect"
)

// The sweep lists panes through the workspace's own Detector, so a scripted
// tmux decides what it sees. Without that seam the sweep ran `tmux` from PATH
// and a test driving it read the panes of the window `go test` was started in.
func TestSweepListsPanesThroughTheInjectedTmuxClient(t *testing.T) {
	a := workspaceApp(t)
	pane := repoOnDisk(t)
	cmd := exec.Command("git", "-C", pane, "remote", "add", "origin", "https://github.com/acme/widget.git")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	f := tmux.NewFake().On("list-panes", paneListRow(pane), nil)
	a.workspace.detect = repodetect.Detector{Tmux: tmux.Client{Runner: f}, Pane: "%3"}

	msg, ok := a.workspace.scanCmd(true, a.workspace.gen)().(workspaceScanMsg)
	if !ok {
		t.Fatal("the sweep returned something other than a scan result")
	}
	if len(msg.repos) != 1 || msg.repos[0].Slug != "acme/widget" {
		t.Errorf("sweep repos = %+v, want acme/widget from the scripted pane", msg.repos)
	}
	if calls := f.CallLines(); len(calls) != 1 || !strings.HasPrefix(calls[0], "list-panes -t %3 ") {
		t.Errorf("tmux calls = %q, want one list-panes of the own pane's window", calls)
	}
}

// paneListRow is one active pane in the row format tmux.Client parses.
func paneListRow(dir string) string {
	var b strings.Builder
	b.WriteString("%3\t@1\t$1\t0\t0\t100\t1\t1\t80\t24")
	for _, v := range []string{"main", "work", dir, "zsh", "title"} {
		fmt.Fprintf(&b, "\t%d:%s", len(v), v)
	}
	return b.String() + "\n"
}
