package diff

import (
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

func TestParseSimpleAdditionAndDeletion(t *testing.T) {
	in := `diff --git a/main.go b/main.go
index abc123..def456 100644
--- a/main.go
+++ b/main.go
@@ -10,4 +10,5 @@ func main() {
 	ctx := context.Background()
-	old := 1
+	newer := 2
+	extra := 3
 	_ = ctx
`
	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "main.go" {
		t.Errorf("path = %q, want main.go", f.Path)
	}
	if f.Additions != 2 || f.Deletions != 1 {
		t.Errorf("additions/deletions = %d/%d, want 2/1", f.Additions, f.Deletions)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 10 || h.OldCount != 4 || h.NewStart != 10 || h.NewCount != 5 {
		t.Errorf("hunk range = -%d,%d +%d,%d, want -10,4 +10,5",
			h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	}

	// Verify per-line LEFT/RIGHT mapping. This is the mapping inline comments
	// depend on, so assert exact numbers.
	want := []struct {
		kind pr.DiffLineKind
		old  int
		new  int
		text string
	}{
		{pr.DiffLineContext, 10, 10, "\tctx := context.Background()"},
		{pr.DiffLineDeletion, 11, 0, "\told := 1"},
		{pr.DiffLineAddition, 0, 11, "\tnewer := 2"},
		{pr.DiffLineAddition, 0, 12, "\textra := 3"},
		{pr.DiffLineContext, 12, 13, "\t_ = ctx"},
	}
	if len(h.Lines) != len(want) {
		t.Fatalf("want %d lines, got %d", len(want), len(h.Lines))
	}
	for i, w := range want {
		got := h.Lines[i]
		if got.Kind != w.kind || got.OldLineNo != w.old || got.NewLineNo != w.new {
			t.Errorf("line %d: kind=%v old=%d new=%d, want kind=%v old=%d new=%d",
				i, got.Kind, got.OldLineNo, got.NewLineNo, w.kind, w.old, w.new)
		}
		if got.Content != w.text {
			t.Errorf("line %d: content = %q, want %q", i, got.Content, w.text)
		}
	}
}

func TestParseMultipleFilesAndHunks(t *testing.T) {
	in := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,3 @@
 package a
+// added

@@ -20,2 +21,2 @@
-removed
+added
diff --git a/b.yaml b/b.yaml
--- a/b.yaml
+++ b/b.yaml
@@ -5,1 +5,2 @@
 key: value
+other: thing
`
	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Path != "a.go" || files[1].Path != "b.yaml" {
		t.Errorf("paths = %q, %q", files[0].Path, files[1].Path)
	}
	if len(files[0].Hunks) != 2 {
		t.Errorf("a.go: want 2 hunks, got %d", len(files[0].Hunks))
	}
	if len(files[1].Hunks) != 1 {
		t.Errorf("b.yaml: want 1 hunk, got %d", len(files[1].Hunks))
	}
	// Second hunk of a.go starts at 20/21 — verify the counters reset per hunk
	// rather than continuing from the first hunk.
	h2 := files[0].Hunks[1]
	if h2.OldStart != 20 || h2.NewStart != 21 {
		t.Errorf("second hunk starts = -%d +%d, want -20 +21", h2.OldStart, h2.NewStart)
	}
	if len(h2.Lines) != 2 {
		t.Fatalf("second hunk: want 2 lines, got %d", len(h2.Lines))
	}
	if h2.Lines[0].OldLineNo != 20 || h2.Lines[0].NewLineNo != 0 {
		t.Errorf("deletion mapping = old %d new %d, want old 20 new 0",
			h2.Lines[0].OldLineNo, h2.Lines[0].NewLineNo)
	}
	if h2.Lines[1].NewLineNo != 21 || h2.Lines[1].OldLineNo != 0 {
		t.Errorf("addition mapping = old %d new %d, want old 0 new 21",
			h2.Lines[1].OldLineNo, h2.Lines[1].NewLineNo)
	}
}

func TestParseRename(t *testing.T) {
	in := `diff --git a/old/name.go b/new/name.go
similarity index 95%
rename from old/name.go
rename to new/name.go
--- a/old/name.go
+++ b/new/name.go
@@ -1,1 +1,1 @@
-package old
+package new
`
	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "new/name.go" {
		t.Errorf("path = %q, want new/name.go", f.Path)
	}
	if f.OldPath != "old/name.go" {
		t.Errorf("oldPath = %q, want old/name.go", f.OldPath)
	}
}

func TestParseNewFileAndNoNewline(t *testing.T) {
	in := `diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+first
+second
\ No newline at end of file
`
	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "new.txt" {
		t.Errorf("path = %q, want new.txt", f.Path)
	}
	if f.Additions != 2 {
		t.Errorf("additions = %d, want 2", f.Additions)
	}
	// The "\ No newline" marker must not become a diff line.
	if got := len(f.Hunks[0].Lines); got != 2 {
		t.Errorf("lines = %d, want 2 (no-newline marker must be skipped)", got)
	}
	if f.Hunks[0].Lines[0].NewLineNo != 1 || f.Hunks[0].Lines[1].NewLineNo != 2 {
		t.Errorf("new line numbers = %d, %d, want 1, 2",
			f.Hunks[0].Lines[0].NewLineNo, f.Hunks[0].Lines[1].NewLineNo)
	}
}

func TestParseHunkHeaderOmittedCount(t *testing.T) {
	// git omits ",1" for single-line ranges: "@@ -5 +5 @@"
	in := `diff --git a/x b/x
--- a/x
+++ b/x
@@ -5 +5 @@
-a
+b
`
	files, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := files[0].Hunks[0]
	if h.OldStart != 5 || h.OldCount != 1 || h.NewStart != 5 || h.NewCount != 1 {
		t.Errorf("range = -%d,%d +%d,%d, want -5,1 +5,1",
			h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	}
}

func TestParseEmptyInput(t *testing.T) {
	files, err := Parse("")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("want 0 files for empty input, got %d", len(files))
	}
}

func TestCommentTarget(t *testing.T) {
	cases := []struct {
		name     string
		line     pr.DiffLine
		wantSide string
		wantLine int
		wantOK   bool
	}{
		{"addition→RIGHT", pr.DiffLine{Kind: pr.DiffLineAddition, NewLineNo: 42}, "RIGHT", 42, true},
		{"deletion→LEFT", pr.DiffLine{Kind: pr.DiffLineDeletion, OldLineNo: 17}, "LEFT", 17, true},
		{"context→RIGHT", pr.DiffLine{Kind: pr.DiffLineContext, OldLineNo: 8, NewLineNo: 9}, "RIGHT", 9, true},
		{"header→none", pr.DiffLine{Kind: pr.DiffLineHunkHeader}, "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			side, line, ok := CommentTarget(c.line)
			if side != c.wantSide || line != c.wantLine || ok != c.wantOK {
				t.Errorf("got (%q, %d, %v), want (%q, %d, %v)",
					side, line, ok, c.wantSide, c.wantLine, c.wantOK)
			}
		})
	}
}
