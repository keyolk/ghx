package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/pr"
)

// The report: gate-nest#486 carries an Atlantis plan posted by sendbird-codelight
// as a terraform plan inside a ```diff fence inside <details>. The comments tab
// printed the fence, the HTML tags, and the plan's +/- markers reflowed into the
// middle of word-wrapped prose.
const atlantisPlan = "Ran Plan for dir: `aws/web-prod/usw2/gate/vpc` workspace: `default`\n" +
	"\n" +
	"<details><summary>Show Output</summary>\n" +
	"\n" +
	"```diff\n" +
	"Terraform used the selected providers to generate the following execution\n" +
	"plan. Resource actions are indicated with the following symbols:\n" +
	"  + create\n" +
	"  ~ update in-place\n" +
	"\n" +
	"Terraform will perform the following actions:\n" +
	"\n" +
	"  # aws_route53_record.clickhouse will be created\n" +
	"  + resource \"aws_route53_record\" \"clickhouse\" {\n" +
	"      + name    = \"clickhouse.sendbird.in\"\n" +
	"      + type    = \"A\"\n" +
	"      ~ zone_id = (known after apply)\n" +
	"    }\n" +
	"\n" +
	"Plan: 6 to add, 2 to change, 0 to destroy.\n" +
	"```\n" +
	"</details>\n"

func planComment() pr.ThreadComment {
	return pr.ThreadComment{
		Body:      atlantisPlan,
		Author:    pr.User{Login: "sendbird-codelight"},
		CreatedAt: time.Date(2026, 9, 8, 9, 21, 0, 0, time.UTC),
	}
}

// The fence and the HTML are markup, not content. Printing them verbatim was
// the most visible half of the bug.
func TestCommentBodyDropsFencesAndHTML(t *testing.T) {
	got := strings.Join(renderCommentBody(atlantisPlan, 90), "\n")
	for _, unwanted := range []string{"```", "<details>", "<summary>", "</summary>"} {
		if strings.Contains(stripANSI(got), unwanted) {
			t.Errorf("rendered body still shows %q:\n%s", unwanted, stripANSI(got))
		}
	}
	// The summary is what the fold was labelled with; dropping the tag must not
	// drop the label, or the block below it becomes unattributed output.
	if !strings.Contains(stripANSI(got), "Show Output") {
		t.Errorf("the <summary> label was lost:\n%s", stripANSI(got))
	}
}

// The point of the plan is its markers. They must still start their lines.
func TestPlanLinesKeepTheirLeadingMarkers(t *testing.T) {
	lines := renderCommentBody(atlantisPlan, 90)
	want := map[string]bool{
		`+ resource "aws_route53_record" "clickhouse" {`:  false,
		`~ zone_id = (known after apply)`:                 false,
		`# aws_route53_record.clickhouse will be created`: false,
	}
	for _, l := range lines {
		trimmed := strings.TrimSpace(stripANSI(l))
		if _, ok := want[trimmed]; ok {
			want[trimmed] = true
		}
	}
	for line, found := range want {
		if !found {
			t.Errorf("plan line %q did not survive rendering:\n%s",
				line, stripANSI(strings.Join(lines, "\n")))
		}
	}
}

// A wrap that reflowed the plan is what moved the markers. Nothing inside a
// fence may be joined to the line above it.
func TestFencedBlockIsNotReflowed(t *testing.T) {
	lines := renderCommentBody(atlantisPlan, 90)
	joined := stripANSI(strings.Join(lines, "\n"))
	if strings.Contains(joined, "symbols: + create") ||
		strings.Contains(joined, "+ create ~ update") {
		t.Errorf("fenced lines were reflowed into prose:\n%s", joined)
	}
}

// Colour is the second half of readability here: an addition and an in-place
// update are different actions, and a plan that paints them alike is a plan
// that misreports itself.
func TestPlanMarkersAreColouredByKind(t *testing.T) {
	forceColor(t)
	lines := renderCommentBody(atlantisPlan, 90)
	var add, change string
	for _, l := range lines {
		trimmed := strings.TrimSpace(stripANSI(l))
		if strings.HasPrefix(trimmed, "+ resource") {
			add = l
		}
		if strings.HasPrefix(trimmed, "~ zone_id") {
			change = l
		}
	}
	if add == "" || change == "" {
		t.Fatalf("did not find both marker lines in:\n%s",
			stripANSI(strings.Join(lines, "\n")))
	}
	if !strings.Contains(add, "\x1b[") || !strings.Contains(change, "\x1b[") {
		t.Errorf("plan markers are unstyled: add=%q change=%q", add, change)
	}
	if ansiPrefix(add) == ansiPrefix(change) {
		t.Errorf("a create and an in-place update render identically (%q)", ansiPrefix(add))
	}
}

func ansiPrefix(s string) string {
	if i := strings.Index(s, "m"); strings.HasPrefix(s, "\x1b[") && i > 0 {
		return s[:i+1]
	}
	return ""
}

// An untagged fence carrying a plan is still a plan — Atlantis does not always
// tag the language — but ordinary prose in a code block must not be coloured as
// a diff just because a line starts with a hyphen.
func TestUntaggedFenceIsOnlyTreatedAsDiffWhenItLooksLikeOne(t *testing.T) {
	plan := "```\n  + create\n  ~ update in-place\n  - destroy\n```\n"
	prose := "```\n- a note about the -f flag\nplain text\nmore text\nand more\n```\n"
	if !looksLikeDiff(strings.Split("  + create\n  ~ update in-place\n  - destroy", "\n")) {
		t.Error("a plan body was not recognized as a diff")
	}
	if looksLikeDiff(strings.Split("- a note about the -f flag\nplain text\nmore text\nand more", "\n")) {
		t.Error("prose with one hyphenated line was mistaken for a diff")
	}
	// Both must still render without markup leaking.
	for _, body := range []string{plan, prose} {
		if got := strings.Join(renderCommentBody(body, 60), "\n"); strings.Contains(got, "```") {
			t.Errorf("fence leaked into output: %s", got)
		}
	}
}

// A code line too wide for the pane is chunked, not wrapped at word
// boundaries — and the continuation is indented so it is not misread as a line
// the plan actually contains.
func TestLongCodeLineIsChunkedNotWrapped(t *testing.T) {
	body := "```diff\n+ " + strings.Repeat("verylongtoken", 12) + "\n```\n"
	lines := renderCommentBody(body, 40)
	if len(lines) < 2 {
		t.Fatalf("a 158-cell line was not chunked to 40: %v", lines)
	}
	for _, l := range lines {
		if w := lipglossWidth(stripANSI(l)); w > 40 {
			t.Errorf("chunk is %d cells wide, want <= 40: %q", w, stripANSI(l))
		}
	}
	if !strings.HasPrefix(stripANSI(lines[1]), "  ") {
		t.Errorf("continuation chunk is not indented: %q", stripANSI(lines[1]))
	}
}

// The comments tab is the surface the report came from, so assert end to end
// through it rather than only against the renderer.
func TestExpandedConversationRendersThePlan(t *testing.T) {
	c := commentsApp(t, nil, []pr.Conversation{{
		ID: "IC_1", Kind: "comment", Body: atlantisPlan,
		Author: pr.User{Login: "sendbird-codelight"},
	}})
	c.expanded[conversationIdentity(c.conversations[0])] = true

	got := stripANSI(c.render(120, 60))
	if strings.Contains(got, "```") || strings.Contains(got, "<details>") {
		t.Errorf("markup leaked into the comments tab:\n%s", got)
	}
	if !strings.Contains(got, "Plan: 6 to add, 2 to change, 0 to destroy.") {
		t.Errorf("the plan's conclusion is missing:\n%s", got)
	}
}

// A collapsed row has one line and a plan does not fit in it. It used to be
// filled with the fence marker and the plan's boilerplate preamble, which said
// nothing about the PR.
func TestPreviewNamesTheBlockInsteadOfQuotingIt(t *testing.T) {
	got := commentPreview(atlantisPlan)
	if strings.Contains(got, "Terraform used the selected providers") {
		t.Errorf("preview quotes the plan's preamble: %q", got)
	}
	if strings.Contains(got, "diff") && !strings.Contains(got, "[diff block]") {
		t.Errorf("preview leaked the fence info string: %q", got)
	}
	if !strings.Contains(got, "Ran Plan for dir") {
		t.Errorf("preview dropped the prose that says what this is: %q", got)
	}
	if !strings.Contains(got, "[diff block]") {
		t.Errorf("preview does not say a block is there: %q", got)
	}
}

// A comment with no fence must be unaffected — most comments are prose, and a
// block tag on one would be a lie.
func TestPreviewOfProseCarriesNoBlockTag(t *testing.T) {
	if got := commentPreview("Looks good, just rename the variable."); strings.Contains(got, "block]") {
		t.Errorf("prose preview grew a block tag: %q", got)
	}
}

// The thread path renders the same bodies; a reply carrying a suggestion is the
// common case there.
func TestThreadCommentRendersASuggestionBlock(t *testing.T) {
	c := newCommentsView()
	c.setThreads([]pr.ReviewThread{{
		ID: "T1", Path: "app.go", Line: 12, ResolutionKnown: true,
		Comments: []pr.ThreadComment{planComment(), {
			Body:   "Try this:\n\n```suggestion\n\tif err != nil {\n\t\treturn err\n\t}\n```\n",
			Author: pr.User{Login: "reviewer"},
		}},
	}})
	c.expanded["T1"] = true

	got := stripANSI(c.render(120, 80))
	if strings.Contains(got, "```suggestion") {
		t.Errorf("suggestion fence leaked:\n%s", got)
	}
	if !strings.Contains(got, "if err != nil {") {
		t.Errorf("suggestion body is missing:\n%s", got)
	}
}

// Lists, headings and tables are the rest of what bots post. None may be
// silently dropped.
func TestOrdinaryMarkdownSurvives(t *testing.T) {
	body := "## Findings\n\n- first item that is quite long and will need to wrap somewhere\n" +
		"- second\n\n| col | col2 |\n| --- | --- |\n| a | b |\n\n> quoted note\n"
	got := stripANSI(strings.Join(renderCommentBody(body, 50), "\n"))
	for _, want := range []string{"Findings", "first item", "second", "| a | b |", "quoted note"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered body dropped %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## ") {
		t.Errorf("heading marker was not consumed:\n%s", got)
	}
}

// An unterminated fence is what a comment discussing fences looks like. It must
// not swallow the rest of the body into nothing.
func TestUnterminatedFenceStillRendersItsContents(t *testing.T) {
	got := stripANSI(strings.Join(renderCommentBody("intro\n\n```diff\n+ added\n", 60), "\n"))
	if !strings.Contains(got, "intro") || !strings.Contains(got, "+ added") {
		t.Errorf("unterminated fence lost content:\n%s", got)
	}
}
