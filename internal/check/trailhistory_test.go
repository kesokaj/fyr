package check

import (
	"fmt"
	"math/bits"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
)

// history opens dir's checker and its trail history over the current specs.
func history(t *testing.T, dir string) (*trailHistory, []brain.Record) {
	t.Helper()
	k, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.root.Close() })
	recs, _, err := brain.ReadTrail(dir)
	if err != nil {
		t.Fatal(err)
	}
	var specs []brain.Record
	for _, r := range recs {
		if r.Kind == "spec" && r.Status == "current" {
			specs = append(specs, r)
		}
	}
	return k.trailHistory(specs), specs
}

// Rule (b) finds each spec's commit by bisection over the commits that
// changed trail.jsonl, each blob read once for every spec (2026-10-31):
// the work count is at most one read of the newest blob plus ceil(log2 n)
// per spec, where `git log -G` diffed all n revisions once per spec. The
// commits found are the ones that added each line.
func TestSpecCommitBisects(t *testing.T) {
	early := specTrail(t, "SPEC-early", "2026-10-25T10:00:00Z", "`internal/a.go`")
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": early})
	trailText, added := early, map[string]string{"SPEC-early": git(t, dir, "rev-parse", "HEAD")}
	const n = 40
	for i := 1; i < n; i++ {
		trailText += trailLine(t, fmt.Sprintf("GUIDE-%d", i), "guide", "current", "x")
		id := map[int]string{9: "SPEC-mid", 31: "SPEC-late"}[i]
		if id != "" {
			trailText += specTrail(t, id, "2026-10-26T10:00:00Z", "`internal/a.go`")
		}
		commit(t, dir, map[string]string{".agents/trail.jsonl": trailText})
		if id != "" {
			added[id] = git(t, dir, "rev-parse", "HEAD")
		}
	}
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})

	h, specs := history(t, dir)
	if len(specs) != 3 {
		t.Fatalf("fixture specs: %d, want 3", len(specs))
	}
	for _, r := range specs {
		c, err := h.commitOf(r)
		if err != nil {
			t.Fatal(err)
		}
		if c != added[r.ID] {
			t.Errorf("%s: commit %.12s, want %.12s, the one that added it", r.ID, c, added[r.ID])
		}
	}
	if bound := 1 + len(specs)*bits.Len(n-1); h.reads > bound || h.reads >= n {
		t.Errorf("%d blob reads over %d trail commits for %d specs, want at most %d", h.reads, n, len(specs), bound)
	}
	same(t, run(t, dir).Findings, "SPEC-early:0 spec-stale", "SPEC-mid:0 spec-stale", "SPEC-late:0 spec-stale")
}

// A line that reached HEAD through a merge has the commit that added it on
// the merged branch, as `git log -G` found it: a commit to the code on that
// branch after the spec's line is counted, not hidden behind the merge.
func TestSpecRuleMergedLine(t *testing.T) {
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": trailLine(t, "GUIDE-1", "guide", "current", "x")})
	base := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, dir, "checkout", "-q", "-b", "side")
	spec := specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`")
	commit(t, dir, map[string]string{".agents/trail.jsonl": trailLine(t, "GUIDE-1", "guide", "current", "x") + spec})
	added := git(t, dir, "rev-parse", "HEAD")
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	git(t, dir, "checkout", "-q", base)
	commit(t, dir, map[string]string{"README.md": "x\n"})
	git(t, dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")

	h, specs := history(t, dir)
	c, err := h.commitOf(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if c != added {
		t.Errorf("commit %.12s, want %.12s on the merged branch", c, added)
	}
	rep := run(t, dir)
	same(t, rep.Findings, "SPEC-a:0 spec-stale")
	if len(rep.Findings) == 1 && !strings.HasPrefix(rep.Findings[0].Text, "1 commit to internal/a.go since ") {
		t.Errorf("stale text %q, want 1 commit", rep.Findings[0].Text)
	}
}

// A commit that deleted trail.jsonl holds no line and is no error: the spec
// committed after it is found, and is stale after a later code commit.
func TestSpecRuleDeletedTrail(t *testing.T) {
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": trailLine(t, "GUIDE-1", "guide", "current", "x")})
	git(t, dir, "rm", "-q", ".agents/trail.jsonl")
	git(t, dir, "commit", "-q", "-m", "drop the trail")
	commit(t, dir, map[string]string{".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`")})
	for i := 0; i < 3; i++ {
		commit(t, dir, map[string]string{".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`") + trailLine(t, fmt.Sprintf("GUIDE-%d", i), "guide", "current", "x")})
	}
	same(t, run(t, dir).Findings)
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	rep := run(t, dir)
	same(t, rep.Findings, "SPEC-a:0 spec-stale")
	if n := notes(rep); strings.Contains(n, "spec not checked") {
		t.Errorf("notes %q, want no spec left unchecked", n)
	}
}
