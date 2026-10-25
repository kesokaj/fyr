package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commit writes files into dir and commits them.
func commit(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "next")
}

// specTrail is a trail holding one current spec, stamped updated.
func specTrail(t *testing.T, id, updated, body string) string {
	t.Helper()
	l := trailLine(t, id, "spec", "current", body)
	return strings.Replace(l, `"updated":"2026-10-25T10:00:00Z"`, `"updated":"`+updated+`"`, 1)
}

func notes(rep Report) string { return strings.Join(rep.Notes, "\n") }

// Rule (b) by ancestry (D9, D10): a spec committed with its code is clean;
// a later commit to the code makes it stale, with the count; a later
// committed updated clears it; an uncommitted spec edit is not stale; a
// commit to trail.jsonl or a *.md file alone is not counted.
func TestSpecRuleStaleness(t *testing.T) {
	body := "Code: `internal/a` and `README.md`, `internal/a/a.go:1`."
	dir := repo(t, map[string]string{
		"README.md":           "x\n",
		"internal/a/a.go":     "package a\n",
		".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", body),
	})
	same(t, run(t, dir).Findings)

	commit(t, dir, map[string]string{"internal/a/a.go": "package a\n\nfunc A() {}\n"})
	commit(t, dir, map[string]string{"internal/a/b.go": "package a\n", "internal/a/NOTES.md": "x\n"})
	commit(t, dir, map[string]string{"README.md": "y\n", "internal/a/NOTES.md": "y\n"})
	rep := run(t, dir)
	same(t, rep.Findings, "SPEC-a:0 spec-stale")
	if len(rep.Findings) == 1 && !strings.HasPrefix(rep.Findings[0].Text, "2 commits to internal/a internal/a/a.go since ") {
		t.Errorf("stale text %q, want 2 commits to internal/a internal/a/a.go", rep.Findings[0].Text)
	}

	// An uncommitted re-stamp is not the spec's commit yet: the spec has no
	// commit, so it is not stale.
	restamped := specTrail(t, "SPEC-a", "2026-10-26T09:00:00Z", body)
	os.WriteFile(filepath.Join(dir, ".agents", "trail.jsonl"), []byte(restamped), 0o644)
	same(t, run(t, dir).Findings)

	// Committed, it clears the finding; a later commit to trail.jsonl alone
	// (another record) is not counted.
	commit(t, dir, nil)
	same(t, run(t, dir).Findings)
	commit(t, dir, map[string]string{".agents/trail.jsonl": restamped + trailLine(t, "GUIDE-b", "guide", "current", "x")})
	same(t, run(t, dir).Findings)
}

// A commit carrying both the code and the spec's update is not counted:
// that is the Definition of Done's normal case.
func TestSpecRuleSameCommit(t *testing.T) {
	dir := repo(t, map[string]string{
		"internal/a/a.go":     "package a\n",
		".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a/a.go`"),
	})
	commit(t, dir, map[string]string{
		"internal/a/a.go":     "package a\n\nvar X = 1\n",
		".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T11:00:00Z", "`internal/a/a.go`"),
	})
	same(t, run(t, dir).Findings)
}

// A spec naming only *.md files, .agents/trail.jsonl, a missing path and an
// ignored one is unlinked; a retired spec is not read.
func TestSpecRuleUnlinked(t *testing.T) {
	dir := repo(t, map[string]string{
		".gitignore":          ".agents/scratch/\n",
		"AGENTS.md":           "x\n",
		"internal/a.go":       "package a\n",
		".agents/kit/x.sh":    "#!/bin/sh\n",
		".agents/scratch/.ok": "",
		".agents/trail.jsonl": specTrail(t, "SPEC-u", "2026-10-25T10:00:00Z", "`AGENTS.md` `.agents/trail.jsonl` `.agents` `.agents/scratch`") +
			trailLine(t, "SPEC-old", "spec", "retired", "`AGENTS.md`"),
	})
	os.MkdirAll(filepath.Join(dir, ".agents", "scratch"), 0o755)
	same(t, run(t, dir).Findings, "SPEC-u:0 spec-unlinked")
}

// A shallow clone and a partial clone skip rule (b) with a note, and give
// no finding, though the spec's code moved on.
func TestSpecRuleSkipsAnIncompleteHistory(t *testing.T) {
	src := repo(t, map[string]string{
		"internal/a.go":       "package a\n",
		".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`"),
	})
	commit(t, src, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	same(t, run(t, src).Findings, "SPEC-a:0 spec-stale")

	shallow := filepath.Join(t.TempDir(), "shallow")
	git(t, src, "clone", "-q", "--depth", "1", "file://"+src, shallow)
	if got := git(t, shallow, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("the fixture clone is not shallow: %s", got)
	}
	rep := run(t, shallow)
	same(t, rep.Findings)
	if !strings.Contains(notes(rep), "a shallow clone: specs not checked") {
		t.Errorf("shallow notes: %q", notes(rep))
	}

	git(t, src, "config", "remote.origin.promisor", "true")
	rep = run(t, src)
	same(t, rep.Findings)
	if !strings.Contains(notes(rep), "a partial clone: specs not checked") {
		t.Errorf("partial notes: %q", notes(rep))
	}
}

// The spec's commit is the oldest that adds its current line: a later
// commit that only moves the line in trail.jsonl does not reset the count.
func TestSpecRuleOldestCommit(t *testing.T) {
	spec := specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`")
	others := trailLine(t, "GUIDE-1", "guide", "current", "x") + trailLine(t, "GUIDE-2", "guide", "current", "y") + trailLine(t, "GUIDE-3", "guide", "current", "z")
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": spec + others})
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	commit(t, dir, map[string]string{".agents/trail.jsonl": others + spec})
	if got := git(t, dir, "log", "-G", `"id":"SPEC-a"`, "--format=%H", "--", ".agents/trail.jsonl"); len(strings.Fields(got)) != 2 {
		t.Fatalf("the move is not seen by git log -G as a second commit: %q", got)
	}
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
}
