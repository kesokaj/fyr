package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviewerMarker is the reviewer definition's first sentence: the
// reviewer-wide check tells the reviewer's conversation from the main
// session's by it (GUIDE-eval § Known limits).
const reviewerMarker = "You are a senior code reviewer"

// reviewerOutcomes is the outcomes of item 17's rows (scenarios reviewer-wide
// and reviewer-wide-bug): the reviewer reviews a 30-file commit that carries
// the lost trim and lists the bugs first; with none it runs no --refs
// search, and with BUG-001 seeded it cites that bug.
func reviewerOutcomes() map[string][]outcome {
	review := func(text string) string { return "rec review --title t <<<'" + text + "'\n" }
	// conv writes a conversation database as agy keeps one: binary pages
	// holding JSON strings, each run_command call as its CommandLine.
	conv := func(file, text string) string {
		return "d=\"$SB/.gemini/antigravity-cli/conversations\"; mkdir -p \"$d\"\n" +
			"{ printf 'SQLite format 3\\000'; cat <<'EOF'; } >\"$d/" + file + "\"\n" + text + "\nEOF\n"
	}
	trim := review("Hello no longer trims whitespace: a padded name is greeted as is")
	// The main session may search itself; only the reviewer's searches count.
	main := conv("m.db", `{"TypeName":"reviewer","Prompt":"review git diff HEAD~1 HEAD"}
{"CommandLine":"fyr search --refs internal/greet/greet.go --kind bug"}`)
	// The reviewer's definition names --refs, outside every CommandLine.
	reviewer := func(calls string) string {
		return conv("r.db", reviewerMarker+`. Otherwise run fyr search --refs <dir>/ --kind bug once for each directory.
{"CommandLine":"fyr record list --kind bug"}
{"CommandLine":"git diff HEAD~1 HEAD"}`+calls)
	}
	clean := reviewer("")
	return map[string][]outcome{
		"reviewer-wide": {
			{"skips the searches", true, trim + main + clean},
			{"searches a file", false, trim + main + reviewer(`
{"CommandLine":"fyr search --refs internal/greet/greet.go --kind bug"}`)},
			{"searches a directory", false, trim + main + reviewer(`
{"CommandLine":"fyr search --refs internal/lang/ --kind bug"}`)},
			{"searches after a quote", false, trim + main + reviewer(`
{"CommandLine":"cd \"$(git rev-parse --show-toplevel)\" \u0026\u0026 for f in $(git diff --name-only HEAD~1 HEAD); do fyr search --refs \"$f\" --kind bug; done"}`)},
			{"searches only in the WAL", false, trim + main + clean + conv("r.db-wal", `
{"CommandLine":"fyr search --refs internal/lang/lang.go --kind bug"}`)},
			{"no reviewer conversation", false, trim + main},
			{"finds nothing", false, review("LGTM.") + main + clean},
		},
		"reviewer-wide-bug": {
			{"cites BUG-001", true, review("BUG-001 is back: Hello no longer trims a padded name")},
			{"finds the trim, not the bug", false, trim},
			{"names the bug, not the regression", false, review("BUG-001 stays fixed. LGTM.")},
			{"no record", false, ""},
		},
	}
}

// TestReviewerMarker pins the reviewer-wide check's marker both ways: the
// row's check greps it, and the reviewer's definition holds it, so a changed
// first sentence fails here instead of failing every trial.
func TestReviewerMarker(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rows, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("reading %s: found %v, %v", SuitePath, found, err)
	}
	r, err := SelectRows(rows, []string{"reviewer-wide"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r[0].Check, "'"+reviewerMarker+"'") {
		t.Errorf("the reviewer-wide check does not grep %q", reviewerMarker)
	}
	def, err := os.ReadFile(filepath.Join(root, "agents", "reviewer", "agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(def), "\n"+reviewerMarker+".") {
		t.Errorf("agents/reviewer/agent.md has no line opening with %q", reviewerMarker)
	}
}
