package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The line arithmetic is where an off-by-one silently corrupts someone's file,
// and the result is pushed as a commit — there is no undo in the TUI.
func TestReplaceLinesSwapsASingleLine(t *testing.T) {
	src := "one\ntwo\nthree\n"
	got, err := ReplaceLines(src, 0, 2, "TWO", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "one\nTWO\nthree\n" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceLinesSwapsARange(t *testing.T) {
	src := "one\ntwo\nthree\nfour\n"
	got, err := ReplaceLines(src, 2, 3, "X\nY\nZ", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "one\nX\nY\nZ\nfour\n" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceLinesDeletes(t *testing.T) {
	got, err := ReplaceLines("one\ntwo\nthree\n", 2, 2, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "one\nthree\n" {
		t.Errorf("got %q", got)
	}
}

// A file's final newline must survive. Losing it rewrites the last line in
// every diff of that file from then on, for a change nobody asked for.
func TestReplaceLinesPreservesTheFinalNewline(t *testing.T) {
	with, err := ReplaceLines("a\nb\n", 1, 1, "A", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(with, "\n") {
		t.Errorf("the trailing newline was dropped: %q", with)
	}
	// And a file without one must not gain one.
	without, err := ReplaceLines("a\nb", 1, 1, "A", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(without, "\n") {
		t.Errorf("a trailing newline was invented: %q", without)
	}
}

// The first and last lines are where an off-by-one shows up.
func TestReplaceLinesHandlesTheEdges(t *testing.T) {
	first, err := ReplaceLines("a\nb\nc\n", 1, 1, "A", false)
	if err != nil {
		t.Fatal(err)
	}
	if first != "A\nb\nc\n" {
		t.Errorf("first line: got %q", first)
	}
	last, err := ReplaceLines("a\nb\nc\n", 3, 3, "C", false)
	if err != nil {
		t.Fatal(err)
	}
	if last != "a\nb\nC\n" {
		t.Errorf("last line: got %q", last)
	}
	whole, err := ReplaceLines("a\nb\nc\n", 1, 3, "X", false)
	if err != nil {
		t.Fatal(err)
	}
	if whole != "X\n" {
		t.Errorf("whole file: got %q", whole)
	}
}

// A suggestion written against an older revision can point past the end of the
// file. Writing it anyway would append the replacement somewhere arbitrary.
func TestReplaceLinesRefusesToWritePastTheEnd(t *testing.T) {
	if _, err := ReplaceLines("a\nb\n", 0, 9, "X", false); err == nil {
		t.Error("a suggestion targeting line 9 of a 2-line file was accepted")
	}
}

func TestReplaceLinesRefusesAnEmptyTarget(t *testing.T) {
	if _, err := ReplaceLines("a\n", 0, 0, "X", false); err == nil {
		t.Error("a suggestion with no target line was accepted")
	}
}

// GitHub reports a multi-line comment's bounds as (start_line, line). Nothing
// guarantees the caller passes them in order, and a reversed range would
// otherwise slice backwards.
func TestReplaceLinesToleratesAReversedRange(t *testing.T) {
	got, err := ReplaceLines("a\nb\nc\nd\n", 3, 2, "X", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\nX\nd\n" {
		t.Errorf("got %q", got)
	}
}

// A CRLF file must not be silently converted: the commit would rewrite every
// line. Splitting on \n leaves the \r attached to each line, which round-trips.
func TestReplaceLinesLeavesCRLFAlone(t *testing.T) {
	got, err := ReplaceLines("a\r\nb\r\nc\r\n", 2, 2, "B", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\r\nB\nc\r\n" {
		t.Errorf("got %q — the untouched lines must keep their endings", got)
	}
	if strings.Count(got, "\r") != 2 {
		t.Errorf("carriage returns changed on lines the suggestion did not touch: %q", got)
	}
}

// The mutation's variable is an input *object*, and neither -f nor -F can carry
// one: they are the same flag family and both send the value as a JSON string,
// which the server rejects with "Expected ... to be a key-value object". The
// whole path shipped that way and could never have committed anything, because
// nothing exercised it end to end. So this asserts the shape of the argv, which
// is the part that was wrong — the body must arrive as one document.
func TestApplySuggestionSendsTheInputAsADocument(t *testing.T) {
	// The input file is removed as soon as ApplySuggestion returns, so the shim
	// copies it aside — the body is the half of this assertion that matters.
	saved := filepath.Join(t.TempDir(), "sent.json")
	calls := countingGH(t, fmt.Sprintf(`
case "$*" in
  *headRefName*) cat <<'JSON'
{"data":{"repository":{"pullRequest":{"headRefName":"topic","headRefOid":"abc1234",
"headRepository":{"name":"n","owner":{"login":"o"}}}}}}
JSON
  ;;
  *contents*) printf '"%%s"\n' "$(printf 'one\ntwo\nthree\n' | base64)" ;;
  *) for a in "$@"; do case "$a" in *.json) cp "$a" %q ;; esac; done
     printf '{"data":{"createCommitOnBranch":{"commit":{"oid":"deadbee"}}}}\n' ;;
esac`, saved))

	err := NewClient(0).WithRepo("o/n").ApplySuggestion(context.Background(), 7,
		SuggestionTarget{Path: "f.txt", Line: 2}, "TWO", false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	var mutation string
	for _, c := range calls() {
		if strings.Contains(c, "--input") {
			mutation = c
		}
	}
	if mutation == "" {
		t.Fatalf("the commit did not go through --input; calls were %q", calls())
	}
	// -f query=... alongside --input would mean the variable is still being
	// passed as a flag, which is the bug.
	if strings.Contains(mutation, "query=") {
		t.Errorf("the query is still passed as a flag: %s", mutation)
	}

	body, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("the shim never saw an input document: %v", err)
	}
	var doc struct {
		Query     string `json:"query"`
		Variables struct {
			Input map[string]any `json:"input"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the body is not a GraphQL document: %v", err)
	}
	if doc.Variables.Input["expectedHeadOid"] != "abc1234" {
		t.Errorf("expectedHeadOid = %v, want the head the file was read at",
			doc.Variables.Input["expectedHeadOid"])
	}
}
