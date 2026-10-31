package check

import (
	"strings"
	"testing"
)

// staleAfterOneCode is a checkout whose spec line is added in its second
// commit and whose third commit changes the spec's code: stale by one.
func staleAfterOneCode(t *testing.T) string {
	t.Helper()
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": trailLine(t, "GUIDE-1", "guide", "current", "x")})
	commit(t, dir, map[string]string{".agents/trail.jsonl": trailLine(t, "GUIDE-1", "guide", "current", "x") + specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`")})
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
	return dir
}

func wantUnreadable(t *testing.T, rep Report) {
	t.Helper()
	same(t, rep.Findings)
	if !strings.Contains(notes(rep), "SPEC-a: trail history unreadable: spec not checked") {
		t.Errorf("notes %q, want SPEC-a's history unreadable", notes(rep))
	}
}

// A past trail.jsonl over the cap is refused from its size before it is
// read, and the spec gets the note, never a silent pass (security review
// 2026-10-31, finding 1).
func TestSpecRuleBlobOverCap(t *testing.T) {
	dir := staleAfterOneCode(t)
	defer func(n int64) { maxTrailBlob = n }(maxTrailBlob)
	maxTrailBlob = 64
	wantUnreadable(t, run(t, dir))
}

// The checkout's own replace refs do not rewrite the history read: a graft
// that drops the spec's commit from HEAD's ancestry would make the code
// commit carry the line and the spec clean (security review 2026-10-31,
// finding 2).
func TestSpecRuleIgnoresReplaceRefs(t *testing.T) {
	dir := staleAfterOneCode(t)
	git(t, dir, "replace", "--graft", "HEAD", "HEAD~2")
	if got := strings.Fields(git(t, dir, "rev-list", "--first-parent", "HEAD", "--", ".agents/trail.jsonl")); len(got) == 0 || got[0] != git(t, dir, "rev-parse", "HEAD") {
		t.Fatalf("the graft does not make HEAD the trail's newest commit, so it proves nothing: %q", got)
	}
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
}

// Merges nested past the depth bound end in the note, not a deeper search.
func TestSpecRuleMergeDepth(t *testing.T) {
	dir := mergedFixture(t, false)
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
	defer func(n int) { maxMergeDepth = n }(maxMergeDepth)
	maxMergeDepth = 0
	wantUnreadable(t, run(t, dir))
}

// A merge that adds the line itself, in its resolution, is the spec's
// commit: no merged side holds the line.
func TestSpecRuleMergeAddsTheLine(t *testing.T) {
	dir := mergedFixture(t, true)
	h, specs := history(t, dir)
	c, err := h.commitOf(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := git(t, dir, "rev-parse", "HEAD"); c != want {
		t.Errorf("commit %.12s, want the merge %.12s", c, want)
	}
	same(t, run(t, dir).Findings)
}

// A line added on a branch merged into the merged side is found there.
func TestSpecRuleNestedMerge(t *testing.T) {
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": trailLine(t, "GUIDE-1", "guide", "current", "x")})
	base := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, dir, "checkout", "-q", "-b", "side")
	commit(t, dir, map[string]string{"README.md": "side\n"})
	git(t, dir, "checkout", "-q", "-b", "inner")
	commit(t, dir, map[string]string{".agents/trail.jsonl": trailLine(t, "GUIDE-1", "guide", "current", "x") + specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`")})
	added := git(t, dir, "rev-parse", "HEAD")
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	git(t, dir, "checkout", "-q", "side")
	commit(t, dir, map[string]string{"README.md": "side 2\n"})
	git(t, dir, "merge", "-q", "--no-ff", "-m", "merge inner", "inner")
	git(t, dir, "checkout", "-q", base)
	commit(t, dir, map[string]string{"NOTES.md": "x\n"})
	git(t, dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")

	h, specs := history(t, dir)
	c, err := h.commitOf(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if c != added {
		t.Errorf("commit %.12s, want %.12s on the inner branch", c, added)
	}
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
}

// mergedFixture merges a side branch that changed the code; the spec's line
// is added on the side, or, with inMerge, by the merge's own resolution.
func mergedFixture(t *testing.T, inMerge bool) string {
	t.Helper()
	guide := trailLine(t, "GUIDE-1", "guide", "current", "x")
	spec := specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`")
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": guide})
	base := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, dir, "checkout", "-q", "-b", "side")
	if !inMerge {
		commit(t, dir, map[string]string{".agents/trail.jsonl": guide + spec})
	}
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	git(t, dir, "checkout", "-q", base)
	commit(t, dir, map[string]string{"README.md": "x\n"})
	if !inMerge {
		git(t, dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")
		return dir
	}
	git(t, dir, "merge", "-q", "--no-ff", "--no-commit", "side")
	commit(t, dir, map[string]string{".agents/trail.jsonl": guide + spec})
	return dir
}
