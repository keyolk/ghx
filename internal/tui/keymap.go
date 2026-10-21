package tui

import (
	"slices"
	"strings"

	"github.com/keyolk/tuikit/keys"
)

// keymap.go is the one table that drives key dispatch, every footer, the `?`
// overlay and the `:` palette. Before it, the keys lived in a msg.String()
// switch per view, the footers were hand-written fmtHints calls and the help
// overlay was a third hand-written list — three copies that drifted (the help
// once listed h / l twice with opposite meanings, and M merged from the detail
// view for months while no footer said so).
//
// A context is a pane or a modal. The app passes the active ones innermost
// first (activeStack); the first enabled binding for the key wins.

const (
	ctxGlobal keys.Ctx = "global"
	// ctxPR holds the actions on whichever PR is in focus — the open one in the
	// detail view, the selected rows in the list.
	ctxPR   keys.Ctx = "pr"
	ctxList keys.Ctx = "list"
	// ctxDetail holds what every detail tab shares; each tab adds its own
	// context in front of it.
	ctxDetail   keys.Ctx = "detail"
	ctxOverview keys.Ctx = "overview"
	ctxFiles    keys.Ctx = "files"
	ctxDiff     keys.Ctx = "diff"
	ctxComments keys.Ctx = "comments"
	ctxCommits  keys.Ctx = "commits"
	ctxChecks   keys.Ctx = "checks"
	// ctxLogs replaces ctxChecks while a log is open, rather than sitting in
	// front of it: the checks list's enter and h must not act behind the log.
	ctxLogs keys.Ctx = "logs"

	// Modals. Each owns the keyboard alone, so its stack is just itself.
	ctxHelp     keys.Ctx = "help"
	ctxConfirm  keys.Ctx = "confirm"
	ctxSuggest  keys.Ctx = "suggest"
	ctxMerge    keys.Ctx = "merge"
	ctxFailure  keys.Ctx = "failure"
	ctxStatus   keys.Ctx = "status"
	ctxLabels   keys.Ctx = "labels"
	ctxRepos    keys.Ctx = "repos"
	ctxPalette  keys.Ctx = "palette"
	ctxSearch   keys.Ctx = "search"
	ctxComposer keys.Ctx = "composer"
)

// Help sections, in the order the overlay shows them.
const (
	grpNav     = "Navigation"
	grpReview  = "Review"
	grpPR      = "PR actions"
	grpViews   = "Views"
	grpFilters = "Filters & status"
	grpOther   = "Other"
	grpPrompt  = "Prompts"
)

// Stacks the app can be in, innermost first. The conflict test and
// LoadOverrides check every one of them.
var (
	stackList      = []keys.Ctx{ctxList, ctxPR, ctxGlobal}
	stackOverview  = detailStack(ctxOverview)
	stackFiles     = detailStack(ctxFiles)
	stackDiff      = detailStack(ctxDiff)
	stackComments  = detailStack(ctxComments)
	stackCommits   = detailStack(ctxCommits)
	stackChecks    = detailStack(ctxChecks)
	stackLogs      = detailStack(ctxLogs)
	stackHelp      = []keys.Ctx{ctxHelp}
	stackConfirm   = []keys.Ctx{ctxConfirm}
	stackSuggest   = []keys.Ctx{ctxSuggest}
	stackMerge     = []keys.Ctx{ctxMerge}
	stackFailure   = []keys.Ctx{ctxFailure}
	stackStatus    = []keys.Ctx{ctxStatus}
	stackLabels    = []keys.Ctx{ctxLabels}
	stackRepos     = []keys.Ctx{ctxRepos}
	stackPalette   = []keys.Ctx{ctxPalette}
	stackSearch    = []keys.Ctx{ctxSearch}
	stackComposer  = []keys.Ctx{ctxComposer}
	viewStacks     = [][]keys.Ctx{stackList, stackOverview, stackFiles, stackDiff, stackComments, stackCommits, stackChecks, stackLogs}
	allStacks      = append(slices.Clone(viewStacks), stackHelp, stackConfirm, stackSuggest, stackMerge, stackFailure, stackStatus, stackLabels, stackRepos, stackPalette, stackSearch, stackComposer)
	detailTabCtxes = map[detailTabKind]keys.Ctx{
		tabOverview: ctxOverview, tabFiles: ctxFiles, tabDiff: ctxDiff,
		tabComments: ctxComments, tabCommits: ctxCommits, tabChecks: ctxChecks,
	}
)

func detailStack(inner ...keys.Ctx) []keys.Ctx {
	return append(inner, ctxDetail, ctxPR, ctxGlobal)
}

// AllStacks is every context stack of the main app, for LoadOverrides.
func AllStacks() [][]keys.Ctx { return slices.Clone(allStacks) }

// nav returns the movement bindings a context shares. One action name per
// movement across every context, so `down: [j, n]` in keys.yaml moves them all
// and the help overlay lists each once.
func nav(ctx keys.Ctx, unit string, footer int, withEnds bool) []keys.Binding {
	bs := []keys.Binding{
		{Action: "down", Keys: []string{"j", "down"}, Ctx: ctx, Group: grpNav, Help: "move down", Footer: footer, FooterGroup: unit},
		{Action: "up", Keys: []string{"k", "up"}, Ctx: ctx, Group: grpNav, Help: "move up", Footer: footer, FooterGroup: unit},
	}
	if withEnds {
		bs = append(bs,
			keys.Binding{Action: "top", Keys: []string{"g", "home"}, Ctx: ctx, Group: grpNav, Help: "jump to top"},
			keys.Binding{Action: "bottom", Keys: []string{"G", "end"}, Ctx: ctx, Group: grpNav, Help: "jump to bottom"},
			keys.Binding{Action: "pagedown", Keys: []string{"ctrl+f", "pgdown"}, Ctx: ctx, Group: grpNav, Help: "page down"},
			keys.Binding{Action: "pageup", Keys: []string{"ctrl+b", "pgup"}, Ctx: ctx, Group: grpNav, Help: "page up"},
		)
	}
	return bs
}

// DefaultKeymap returns the built-in table. Footer numbers are priorities:
// lower survives a narrow terminal longer; 0 keeps a binding out of the footer
// (it is still in `?` and the palette).
//
// LatinOnly marks the uppercase keys (M, L, A, C, J/K, X) a Korean input
// source cannot type. ghx bound them before the CJK mapping existed; the
// physical keys work once the source is switched, and every one is also
// reachable from the palette (`:` types under either source). Actions with a
// second, non-letter key (R, G/end, H/L with {/}) need no mark.
func DefaultKeymap() keys.Keymap {
	var bs []keys.Binding
	add := func(b ...keys.Binding) { bs = append(bs, b...) }

	// global — q and ? are pinned: they are how a lost user leaves or finds the
	// keys that did not fit. ctrl+c is also checked before any modal (handleKey),
	// so it quits even from the composer, where q is text.
	add(
		keys.Binding{Action: "quit", Keys: []string{"q", "ctrl+c"}, Ctx: ctxGlobal, Group: grpOther, Help: "quit", Pinned: true, Footer: 99},
		keys.Binding{Action: "help", Keys: []string{"?"}, Ctx: ctxGlobal, Group: grpOther, Help: "toggle this help", Short: "help", Pinned: true, Footer: 98},
		keys.Binding{Action: "palette", Keys: []string{":"}, Ctx: ctxGlobal, Group: grpOther, Help: "command palette", Short: "palette", Footer: 30},
	)

	// pr — the PR in focus. L rather than l: lowercase l is the vim "right"
	// alias, which opens the preview in the list and cycles tabs in the detail.
	add(
		keys.Binding{Action: "approve", Keys: []string{"a"}, Ctx: ctxPR, Group: grpPR, Help: "approve (asks first)", Short: "approve", Footer: 10},
		keys.Binding{Action: "close", Keys: []string{"x"}, Ctx: ctxPR, Group: grpPR, Help: "close / reopen (asks first)", Short: "close", Footer: 12},
		keys.Binding{Action: "merge", Keys: []string{"M"}, LatinOnly: true, Ctx: ctxPR, Group: grpPR, Help: "merge (asks first)", Short: "merge", Footer: 11},
		keys.Binding{Action: "labels", Keys: []string{"L"}, LatinOnly: true, Ctx: ctxPR, Group: grpPR, Help: "edit common labels", Short: "labels", Footer: 14},
		keys.Binding{Action: "ready", Keys: []string{"d"}, Ctx: ctxPR, Group: grpPR, Help: "toggle ready / draft (asks first)", Short: "ready/draft", Footer: 15},
		keys.Binding{Action: "request", Keys: []string{"r"}, Ctx: ctxPR, Group: grpReview, Help: "request changes", Short: "request", Footer: 13},
		keys.Binding{Action: "browser", Keys: []string{"o"}, Ctx: ctxPR, Group: grpPR, Help: "open in the browser", Short: "browser", Footer: 16},
		keys.Binding{Action: "copy", Keys: []string{"y"}, Ctx: ctxPR, Group: grpPR, Help: "copy the PR URLs to the clipboard", Short: "copy", Footer: 17},
	)

	// list
	add(nav(ctxList, "move", 0, true)...)
	add(
		keys.Binding{Action: "open", Keys: []string{"enter"}, Ctx: ctxList, Group: grpNav, Help: "open the PR", Short: "open", Footer: 3},
		keys.Binding{Action: "select", Keys: []string{" "}, Ctx: ctxList, Group: grpPR, Help: "toggle the row for multi-select", Short: "select", Footer: 1},
		keys.Binding{Action: "selectall", Keys: []string{"A"}, LatinOnly: true, Ctx: ctxList, Group: grpPR, Help: "toggle every visible row", Short: "all", Footer: 2},
		keys.Binding{Action: "showpreview", Keys: []string{"l", "right"}, Ctx: ctxList, Group: grpNav, Help: "show / focus the preview"},
		keys.Binding{Action: "hidepreview", Keys: []string{"h", "left"}, Ctx: ctxList, Group: grpNav, Help: "unfocus / hide the preview"},
		keys.Binding{Action: "narrower", Keys: []string{"["}, Ctx: ctxList, Group: grpNav, Help: "shrink the list pane"},
		keys.Binding{Action: "wider", Keys: []string{"]"}, Ctx: ctxList, Group: grpNav, Help: "grow the list pane"},
		keys.Binding{Action: "nextsource", Keys: []string{"tab"}, Ctx: ctxList, Group: grpNav, Help: "next source tab"},
		keys.Binding{Action: "prevsource", Keys: []string{"shift+tab"}, Ctx: ctxList, Group: grpNav, Help: "previous source tab"},
		// The digit jumps go by position in Keys (KeyIndex), so a user who
		// rebinds them to F1..F9 still jumps by position.
		keys.Binding{Action: "source", Keys: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, KeyLabel: "1-9", NoPalette: true,
			Ctx: ctxList, Group: grpNav, Help: "jump to source tab N (* marks the repo you are in)"},
		keys.Binding{Action: "status", Keys: []string{"f"}, Ctx: ctxList, Group: grpFilters, Help: "filter by PR status", Short: "status", Footer: 20},
		keys.Binding{Action: "search", Keys: []string{"/"}, Ctx: ctxList, Group: grpFilters, Help: "search text; combines with status filters", Short: "search", Footer: 22},
		keys.Binding{Action: "clear", Keys: []string{"esc"}, Ctx: ctxList, Group: grpFilters, Help: "clear text, then status, then selection"},
		keys.Binding{Action: "repo", Keys: []string{"e"}, Ctx: ctxList, Group: grpOther, Help: "open a repository's PRs as a tab", Short: "repo", Footer: 21},
		keys.Binding{Action: "refresh", Keys: []string{"R"}, Ctx: ctxList, Group: grpOther, Help: "refresh", Footer: 8},
	)

	// detail — shared by every tab
	add(
		keys.Binding{Action: "back", Keys: []string{"esc"}, Ctx: ctxDetail, Group: grpNav, Help: "back to the list", Short: "back", Footer: 9},
		keys.Binding{Action: "detailtab", Keys: []string{"1", "2", "3", "4", "5", "6"}, KeyLabel: "1-6", NoPalette: true, Ctx: ctxDetail, Group: grpViews,
			Help: "tab: 1 Overview · 2 Files · 3 Diff · 4 Comments · 5 Commits · 6 Checks"},
		keys.Binding{Action: "prcomment", Keys: []string{"C"}, LatinOnly: true, Ctx: ctxDetail, Group: grpReview, Help: "PR-level comment"},
		// M opens the strategy prompt here rather than the list's confirmation:
		// with one PR open there is a strategy to choose.
		keys.Binding{Action: "merge", Keys: []string{"M"}, LatinOnly: true, Ctx: ctxDetail, Group: grpPR, Help: "merge (asks first)", Short: "merge", Footer: 11},
		// Explicitly asking to refresh must not be answered from disk.
		keys.Binding{Action: "refresh", Keys: []string{"R"}, Ctx: ctxDetail, Group: grpOther, Help: "refresh"},
		// h/l cycle tabs wherever the tab has no use for them. The diff claims
		// them only while they would fold or switch columns (Enabled), so on a
		// unified line or an already-folded file they still cycle.
		keys.Binding{Action: "prevtab", Keys: []string{"h", "left"}, Ctx: ctxDetail, Group: grpNav, Help: "previous tab", Footer: 1, FooterGroup: "cycle"},
		keys.Binding{Action: "nexttab", Keys: []string{"l", "right"}, Ctx: ctxDetail, Group: grpNav, Help: "next tab", Footer: 1, FooterGroup: "cycle"},
	)
	// overview
	add(nav(ctxOverview, "scroll", 0, false)...)
	add(
		keys.Binding{Action: "top", Keys: []string{"g", "home"}, Ctx: ctxOverview, Group: grpNav, Help: "jump to top"},
		keys.Binding{Action: "pagedown", Keys: []string{"ctrl+f", "pgdown"}, Ctx: ctxOverview, Group: grpNav, Help: "page down"},
		keys.Binding{Action: "pageup", Keys: []string{"ctrl+b", "pgup"}, Ctx: ctxOverview, Group: grpNav, Help: "page up"},
	)

	// files
	add(nav(ctxFiles, "file", 1, false)...)
	add(
		keys.Binding{Action: "top", Keys: []string{"g", "home"}, Ctx: ctxFiles, Group: grpNav, Help: "jump to top"},
		keys.Binding{Action: "bottom", Keys: []string{"G", "end"}, Ctx: ctxFiles, Group: grpNav, Help: "jump to bottom"},
		keys.Binding{Action: "openfile", Keys: []string{"enter", "l", "right"}, Ctx: ctxFiles, Group: grpViews, Help: "open the file in the diff", Short: "open in diff", Footer: 2},
	)

	// diff
	// On a file header h/l fold rather than switch columns, and in a visual
	// range j/k extend it: the same actions, so only the footer label follows
	// the cursor (GroupLabels, picked by diffView.keyState).
	diffNav := nav(ctxDiff, "line", 1, true)
	for i := range diffNav[:2] {
		diffNav[i].GroupLabels = map[string]string{"visual": "extend"}
	}
	add(diffNav...)
	add(
		keys.Binding{Action: "nexthunk", Keys: []string{"J"}, LatinOnly: true, Ctx: ctxDiff, Group: grpNav, Help: "next hunk", Footer: 2, FooterGroup: "hunk"},
		keys.Binding{Action: "prevhunk", Keys: []string{"K"}, LatinOnly: true, Ctx: ctxDiff, Group: grpNav, Help: "previous hunk", Footer: 2, FooterGroup: "hunk"},
		keys.Binding{Action: "prevfile", Keys: []string{"H", "{"}, Ctx: ctxDiff, Group: grpNav, Help: "previous file", Footer: 3, FooterGroup: "file"},
		keys.Binding{Action: "nextfile", Keys: []string{"L", "}"}, Ctx: ctxDiff, Group: grpNav, Help: "next file", Footer: 3, FooterGroup: "file"},
		keys.Binding{Action: "left", Keys: []string{"h", "left"}, Ctx: ctxDiff, Group: grpReview, Help: "old column (side-by-side); fold the file on its header",
			Footer: 4, FooterGroup: "column", GroupLabels: map[string]string{"header": "fold"}},
		keys.Binding{Action: "right", Keys: []string{"l", "right"}, Ctx: ctxDiff, Group: grpReview, Help: "new column (side-by-side); unfold the file on its header",
			Footer: 4, FooterGroup: "column", GroupLabels: map[string]string{"header": "fold"}},
		keys.Binding{Action: "comment", Keys: []string{"c"}, Ctx: ctxDiff, Group: grpReview, Help: "comment on the line (reply on a thread)", Short: "comment",
			Labels: map[string]string{"thread": "reply"}, Footer: 5},
		keys.Binding{Action: "visual", Keys: []string{"v"}, Ctx: ctxDiff, Group: grpReview, Help: "visual range, then c to comment on it", Short: "visual",
			Labels: map[string]string{"on": "cancel"}, Footer: 6},
		keys.Binding{Action: "layout", Keys: []string{"s"}, Ctx: ctxDiff, Group: grpReview, Help: "unified / side-by-side diff",
			Labels: map[string]string{"unified": "side-by-side", "sbs": "unified"}, Footer: 7},
		keys.Binding{Action: "thread", Keys: []string{"enter"}, Ctx: ctxDiff, Group: grpReview, Help: "expand a thread / reply to it", Short: "thread",
			Labels: map[string]string{"replies": "replies", "reply": "reply"}, Footer: 8},
		keys.Binding{Action: "apply", Keys: []string{"A"}, LatinOnly: true, Ctx: ctxDiff, Group: grpReview, Help: "apply the suggestion under the cursor (asks first)", Short: "apply suggestion", Footer: 2},
		keys.Binding{Action: "fold", Keys: []string{"o"}, Ctx: ctxDiff, Group: grpReview, Help: "fold / unfold the file", Short: "fold", Footer: 9},
	)

	// comments
	add(nav(ctxComments, "row", 1, false)...)
	add(
		keys.Binding{Action: "reply", Keys: []string{"c"}, Ctx: ctxComments, Group: grpReview, Help: "reply to the thread or PR comment", Short: "reply", Footer: 2},
		keys.Binding{Action: "resolve", Keys: []string{"X"}, LatinOnly: true, Ctx: ctxComments, Group: grpReview, Help: "resolve / unresolve the thread", Short: "resolve", Footer: 3},
		keys.Binding{Action: "jumpdiff", Keys: []string{"d"}, Ctx: ctxComments, Group: grpReview, Help: "jump to the thread in the diff", Short: "diff", Footer: 4},
		keys.Binding{Action: "resolvedfilter", Keys: []string{"t"}, Ctx: ctxComments, Group: grpReview,
			Help: "show / hide resolved threads (○ open · ✓ resolved · ? unknown)", Labels: map[string]string{"hiding": "show all", "showing": "hide done"}, Footer: 5},
		keys.Binding{Action: "expand", Keys: []string{"enter"}, Ctx: ctxComments, Group: grpReview, Help: "expand the thread"},
		linkBinding(ctxComments, 6),
	)

	// commits
	add(nav(ctxCommits, "scroll", 0, false)...)

	// checks
	add(nav(ctxChecks, "check", 1, false)...)
	add(keys.Binding{Action: "logs", Keys: []string{"enter"}, Ctx: ctxChecks, Group: grpViews, Help: "open the check's log", Short: "logs", Footer: 2})
	add(linkBinding(ctxChecks, 3))
	add(nav(ctxLogs, "scroll", 1, false)...)
	add(
		keys.Binding{Action: "pagedown", Keys: []string{"ctrl+f", "pgdown"}, Ctx: ctxLogs, Group: grpNav, Help: "page down"},
		keys.Binding{Action: "pageup", Keys: []string{"ctrl+b", "pgup"}, Ctx: ctxLogs, Group: grpNav, Help: "page up"},
		keys.Binding{Action: "closelogs", Keys: []string{"esc"}, Ctx: ctxLogs, Group: grpViews, Help: "back to the checks", Short: "back to checks", Footer: 2},
	)

	// modals
	add(
		keys.Binding{Action: "close", Keys: []string{"?", "esc", "q"}, Ctx: ctxHelp, Group: grpOther, Help: "close", Footer: 1},
		keys.Binding{Action: "down", Keys: []string{"j", "down"}, Ctx: ctxHelp, Group: grpNav, Help: "scroll down", Footer: 2, FooterGroup: "scroll"},
		keys.Binding{Action: "up", Keys: []string{"k", "up"}, Ctx: ctxHelp, Group: grpNav, Help: "scroll up", Footer: 2, FooterGroup: "scroll"},

		keys.Binding{Action: "yes", Keys: []string{"y", "Y"}, Ctx: ctxConfirm, Group: grpPrompt, Help: "yes", Footer: 1},
		keys.Binding{Action: "no", Keys: []string{"n", "N", "esc", "q"}, Ctx: ctxConfirm, Group: grpPrompt, Help: "no", Footer: 2},
		keys.Binding{Action: "yes", Keys: []string{"y", "Y"}, Ctx: ctxSuggest, Group: grpPrompt, Help: "apply", Footer: 1},
		keys.Binding{Action: "no", Keys: []string{"n", "N", "esc", "q"}, Ctx: ctxSuggest, Group: grpPrompt, Help: "cancel", Footer: 2},

		keys.Binding{Action: "yes", Keys: []string{"y"}, Ctx: ctxMerge, Group: grpPrompt, Help: "merge", Footer: 1},
		keys.Binding{Action: "squash", Keys: []string{"s"}, Ctx: ctxMerge, Group: grpPrompt, Help: "squash", Footer: 2},
		keys.Binding{Action: "mergecommit", Keys: []string{"m"}, Ctx: ctxMerge, Group: grpPrompt, Help: "merge commit", Footer: 3},
		keys.Binding{Action: "rebase", Keys: []string{"b"}, Ctx: ctxMerge, Group: grpPrompt, Help: "rebase", Footer: 4},
		keys.Binding{Action: "no", Keys: []string{"esc", "n", "q"}, Ctx: ctxMerge, Group: grpPrompt, Help: "cancel", Footer: 5},

		keys.Binding{Action: "rebase", Keys: []string{"r"}, Ctx: ctxFailure, Group: grpPrompt, Help: "rebase onto base", Footer: 1},
		keys.Binding{Action: "dismiss", Keys: []string{"enter", "esc", "q", " "}, Ctx: ctxFailure, Group: grpPrompt, Help: "dismiss", Footer: 2},

		keys.Binding{Action: "down", Keys: []string{"j", "down"}, Ctx: ctxStatus, Group: grpNav, Help: "move down", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: "up", Keys: []string{"k", "up"}, Ctx: ctxStatus, Group: grpNav, Help: "move up", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: "toggle", Keys: []string{" ", "tab"}, Ctx: ctxStatus, Group: grpFilters, Help: "toggle", Footer: 2},
		keys.Binding{Action: "apply", Keys: []string{"enter"}, Ctx: ctxStatus, Group: grpFilters, Help: "apply", Footer: 3},
		keys.Binding{Action: "clear", Keys: []string{"c"}, Ctx: ctxStatus, Group: grpFilters, Help: "clear", Footer: 4},
		keys.Binding{Action: "cancel", Keys: []string{"esc", "q"}, Ctx: ctxStatus, Group: grpFilters, Help: "cancel", Footer: 5},

		// The label picker types to filter, so only keys that cannot be part
		// of a label name navigate. j/k are the historic exception.
		keys.Binding{Action: "down", Keys: []string{"down", "j", "ctrl+n"}, Ctx: ctxLabels, Group: grpNav, Help: "move down", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: "up", Keys: []string{"up", "k", "ctrl+p"}, Ctx: ctxLabels, Group: grpNav, Help: "move up", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: "toggle", Keys: []string{" ", "tab"}, Ctx: ctxLabels, Group: grpPR, Help: "toggle", Footer: 2},
		keys.Binding{Action: "apply", Keys: []string{"enter"}, Ctx: ctxLabels, Group: grpPR, Help: "apply", Footer: 3},
		keys.Binding{Action: "cancel", Keys: []string{"esc"}, Ctx: ctxLabels, Group: grpPR, Help: "cancel", Footer: 4},
		keys.Binding{Action: "erase", Keys: []string{"backspace"}, Ctx: ctxLabels, Group: grpPR, Help: "erase a character"},

		// The repo picker types any letter (slugs), so arrows and ^n/^p move.
		keys.Binding{Action: "down", Keys: []string{"down", "ctrl+n"}, Ctx: ctxRepos, Group: grpNav, Help: "move down", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: "up", Keys: []string{"up", "ctrl+p"}, Ctx: ctxRepos, Group: grpNav, Help: "move up", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: "open", Keys: []string{"enter"}, Ctx: ctxRepos, Group: grpOther, Help: "open", Footer: 2},
		keys.Binding{Action: "cancel", Keys: []string{"esc"}, Ctx: ctxRepos, Group: grpOther, Help: "cancel", Footer: 3},
		keys.Binding{Action: "erase", Keys: []string{"backspace"}, Ctx: ctxRepos, Group: grpOther, Help: "erase a character"},

		keys.Binding{Action: "down", Keys: []string{"down", "ctrl+n"}, Ctx: ctxPalette, Group: grpNav, Help: "next match", Footer: 2, FooterGroup: "pick"},
		keys.Binding{Action: "up", Keys: []string{"up", "ctrl+p"}, Ctx: ctxPalette, Group: grpNav, Help: "previous match", Footer: 2, FooterGroup: "pick"},
		keys.Binding{Action: "run", Keys: []string{"enter"}, Ctx: ctxPalette, Group: grpOther, Help: "run", Footer: 1},
		keys.Binding{Action: "cancel", Keys: []string{"esc"}, Ctx: ctxPalette, Group: grpOther, Help: "cancel", Footer: 3},
		keys.Binding{Action: "erase", Keys: []string{"backspace"}, Ctx: ctxPalette, Group: grpOther, Help: "erase a character"},

		keys.Binding{Action: "apply", Keys: []string{"enter"}, Ctx: ctxSearch, Group: grpFilters, Help: "apply", Footer: 1},
		keys.Binding{Action: "clear", Keys: []string{"esc"}, Ctx: ctxSearch, Group: grpFilters, Help: "clear", Footer: 2},
		keys.Binding{Action: "erase", Keys: []string{"backspace"}, Ctx: ctxSearch, Group: grpFilters, Help: "erase a character"},

		keys.Binding{Action: "post", Keys: []string{"enter"}, Ctx: ctxComposer, Group: grpReview, Help: "post (a one-line body)", Short: "post", Footer: 1},
		keys.Binding{Action: "submit", Keys: []string{"ctrl+s"}, Ctx: ctxComposer, Group: grpReview, Help: "post", Footer: 2},
		keys.Binding{Action: "editor", Keys: []string{"ctrl+e"}, Ctx: ctxComposer, Group: grpReview, Help: "$EDITOR", Footer: 3},
		keys.Binding{Action: "cancel", Keys: []string{"esc"}, Ctx: ctxComposer, Group: grpReview, Help: "cancel", Footer: 4},
	)
	return keys.New(bs...)
}

// LoadKeymap applies the user's overrides to the default table: the keymap
// section of config.yaml first, then keys.yaml beside it (the tuikit
// convention, keys.ConfigPath). A bad override is reported and the defaults
// are kept — a typo in a key file must not stop ghx from starting.
func LoadKeymap(fromConfig map[string][]string) (keys.Keymap, error) {
	km := DefaultKeymap()
	var errs []string
	if len(fromConfig) > 0 {
		out, err := km.Override(fromConfig, allStacks...)
		if err != nil {
			errs = append(errs, "config.yaml "+err.Error())
		} else {
			km = out
		}
	}
	out, err := km.LoadOverrides(keys.ConfigPath("ghx"), allStacks...)
	if err != nil {
		errs = append(errs, err.Error())
	} else {
		km = out
	}
	if len(errs) > 0 {
		return km, keymapError(strings.Join(errs, "; "))
	}
	return km, nil
}

type keymapError string

func (e keymapError) Error() string { return string(e) }

// keyNames displays keys the way ghx always has: glyphs for enter and the
// arrows, ^x for ctrl chords.
var keyNames = keys.Names{Glyphs: true, Caret: true}

// footerStyle is the kit footer in ghx's hint colours.
func footerStyle() keys.FooterStyle {
	return keys.FooterStyle{
		Key: helpKeyStyle, Desc: helpStyle, More: dimStyle,
		Names: keyNames, GroupSeparator: "/",
	}
}

// keyName is the key that runs action in stack right now, as the footer
// names it, so prose that tells the user what to press follows their
// overrides. ok is false when no key reaches it; the caller drops the advice.
func keyName(km keys.Keymap, stack []keys.Ctx, action string, o keys.Opts) (string, bool) {
	k, ok := km.Or(defaultKeymap).KeyFor(stack, action, o)
	if !ok {
		return "", false
	}
	return keyNames.Name(k), true
}
