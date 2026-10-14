package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"

	"github.com/keyolk/ghx/internal/pr"
)

// The table must not bind one key to two actions in a context, nor let an
// inner context hide quit or help. Uppercase keys a Korean input source cannot
// type (M, A, L…) are reported as Unreachable by the kit; ghx has bound them
// since before the CJK mapping existed, the physical keys still work with the
// input source switched, and every one of them is also reachable from the
// palette — so that kind alone is accepted.
func TestKeymapHasNoConflicts(t *testing.T) {
	for _, c := range DefaultKeymap().Conflicts(allStacks...) {
		if c.Kind == keys.Unreachable {
			continue
		}
		t.Error(c)
	}
}

// msgFor builds the tea.KeyMsg whose String() is key, so a test can press any
// key the table names.
func msgFor(t *testing.T, key string) tea.KeyMsg {
	t.Helper()
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
		"backspace": tea.KeyBackspace, " ": tea.KeySpace,
		"ctrl+c": tea.KeyCtrlC, "ctrl+b": tea.KeyCtrlB, "ctrl+f": tea.KeyCtrlF,
		"ctrl+n": tea.KeyCtrlN, "ctrl+p": tea.KeyCtrlP, "ctrl+s": tea.KeyCtrlS, "ctrl+e": tea.KeyCtrlE,
	}
	if kt, ok := named[key]; ok {
		return tea.KeyMsg{Type: kt}
	}
	if r := []rune(key); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r}
	}
	t.Fatalf("no KeyMsg for %q", key)
	return tea.KeyMsg{}
}

// Every action the help overlay lists must be reachable by a key it lists: in
// some stack, pressing that key resolves to that action. A help entry no key
// reaches is the drift this table exists to prevent.
func TestEveryHelpEntryIsReachable(t *testing.T) {
	km := DefaultKeymap()
	for _, sec := range km.Help(keys.Opts{}, viewStacks...) {
		for _, e := range sec.Entries {
			if len(e.Keys) == 0 {
				t.Errorf("%s (%s) is listed without a key", e.Action, sec.Title)
				continue
			}
			for _, key := range e.Keys {
				reached := false
				for _, stack := range viewStacks {
					if b, ok := km.Resolve(stack, msgFor(t, key), keys.Opts{}); ok && b.Action == e.Action {
						reached = true
						break
					}
				}
				if !reached {
					t.Errorf("help lists %s for %q, but %s reaches it in no view", keyNames.Name(key), e.Action, key)
				}
			}
		}
	}
}

// appIn builds an App showing the view a context belongs to, so a binding of
// that context can be run where it would really run.
func appIn(t *testing.T, ctx keys.Ctx) *App {
	t.Helper()
	a := testApp(t, sampleRows)
	a.copyClipboard = func(context.Context, string, string) error { return nil }
	tab, ok := map[keys.Ctx]detailTabKind{
		ctxDetail: tabOverview, ctxOverview: tabOverview, ctxFiles: tabFiles, ctxDiff: tabDiff,
		ctxDiffHeader: tabDiff, ctxVisual: tabDiff, ctxComments: tabComments,
		ctxCommits: tabCommits, ctxChecks: tabChecks, ctxLogs: tabChecks,
	}[ctx]
	if !ok {
		return a
	}
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 101, "acme/one")
	a.detail.detail = &pr.Detail{Number: 101, BaseRefName: "main", URL: "https://x/pull/101"}
	a.detail.activeTab = tab
	a.detail.resize(a.width, a.height)
	return a
}

// Every view binding has a handler: running it reports handled. A binding the
// dispatcher does not know is a key the footer and help offer that does
// nothing.
func TestEveryViewBindingHasAHandler(t *testing.T) {
	isView := map[keys.Ctx]bool{}
	for _, s := range viewStacks {
		for _, c := range s {
			isView[c] = true
		}
	}
	for _, b := range DefaultKeymap().Bindings() {
		if !isView[b.Ctx] {
			continue
		}
		a := appIn(t, b.Ctx)
		if _, handled := a.runAction(b, msgFor(t, b.Keys[0])); !handled {
			t.Errorf("%s.%s has no handler", b.Ctx, b.Action)
		}
	}
}

// The footer is derived, so it can only name keys the table binds; this pins
// that the list footer still offers the triage keys and stays one line.
func TestListFooterIsDerivedFromTheTable(t *testing.T) {
	a := testApp(t, sampleRows)
	got := stripANSI(a.list.helpLine(200))
	for _, want := range []string{"space select", "a approve", "M merge", "/ search", "? help", "q quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("list footer lacks %q: %s", want, got)
		}
	}
	if narrow := a.list.helpLine(40); lipglossWidth(narrow) > 40 || !strings.Contains(stripANSI(narrow), "q quit") {
		t.Errorf("narrow footer overflows or lost the pinned quit: %q", stripANSI(narrow))
	}
}

// keys.yaml rebinds by action name; the dispatcher follows because it never
// looks at keys.
func TestKeysYAMLOverrideRebindsDispatch(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	if err := os.MkdirAll(filepath.Join(cfgDir, "ghx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "ghx", "keys.yaml"), []byte("status: [F]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	km, err := LoadKeymap(nil)
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t, sampleRows)
	a.km = km
	a.handleKey(keyMsg("f"))
	if a.statusFilter != nil {
		t.Fatal("f still opens the status filter after rebinding it to F")
	}
	a.handleKey(keyMsg("F"))
	if a.statusFilter == nil {
		t.Fatal("F did not open the status filter")
	}
}

// config.yaml's keymap section is the same override, and a bad one keeps the
// defaults instead of stopping ghx.
func TestConfigKeymapOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	km, err := LoadKeymap(map[string][]string{"palette": {";"}})
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := km.Resolve(stackList, keyMsg(";"), keys.Opts{}); !ok || b.Action != "palette" {
		t.Errorf("; does not open the palette after the override")
	}
	km, err = LoadKeymap(map[string][]string{"nosuchaction": {"z"}})
	if err == nil {
		t.Fatal("an unknown action was accepted")
	}
	if b, ok := km.Resolve(stackList, keyMsg(":"), keys.Opts{}); !ok || b.Action != "palette" {
		t.Error("a bad override did not leave the defaults in place")
	}
}

// prActionKey runs msg if the PR context alone binds it — the old per-context
// entry point the action tests were written against.
func (a *App) prActionKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	b, ok := a.keymap().Resolve([]keys.Ctx{ctxPR}, msg, keys.Opts{})
	if !ok {
		return nil, false
	}
	return a.prAction(b.Action)
}

// detailActionKey is prActionKey for the detail-wide context.
func (a *App) detailActionKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	b, ok := a.keymap().Resolve([]keys.Ctx{ctxDetail}, msg, keys.Opts{})
	if !ok {
		return nil, false
	}
	return a.detailAction(b, msg)
}

// The palette ranks the view's bindings with the commands: a fuzzy word runs
// the key's action through the same dispatcher, and a typed command still runs
// as typed.
func TestPaletteRunsBindingsByName(t *testing.T) {
	a := testApp(t, sampleRows)
	a.handleKey(keyMsg(":"))
	if !a.palette.active {
		t.Fatal(": did not open the palette")
	}
	for _, r := range "appr" {
		a.handleKey(keyMsg(string(r)))
	}
	cmd := a.handleKey(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter ran nothing")
	}
	a.Update(cmd())
	if a.confirm == nil || a.confirm.kind != confirmApprove {
		t.Fatal("the palette's approve did not reach the approve confirmation")
	}

	a = testApp(t, sampleRows)
	a.handleKey(keyMsg(":"))
	for _, r := range "zzzz" {
		a.handleKey(keyMsg(string(r)))
	}
	msg := a.handleKey(keyMsg("enter"))()
	if run, ok := msg.(paletteRunMsg); !ok || run.line != "zzzz" {
		t.Fatalf("an unmatched line was not run as typed: %#v", msg)
	}
}
