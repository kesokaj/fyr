package brain

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixed = time.Date(2026, 10, 25, 14, 30, 0, 0, time.UTC)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo is a fresh git checkout with no trail at all: a migrated project.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	return dir
}

// opts is a project-scope call in dir against a scratch home, never the
// real one (R6).
func opts(dir, home string) Options {
	return Options{Home: home, Dir: dir, Now: func() time.Time { return fixed }}
}

func add(t *testing.T, o Options, kind, title, body string, extra ...string) string {
	t.Helper()
	in := AddInput{Kind: kind, Title: title, Body: strings.NewReader(body)}
	for i := 0; i+1 < len(extra); i += 2 {
		switch extra[i] {
		case "status":
			in.Status = extra[i+1]
		case "id":
			in.ID = extra[i+1]
		case "project":
			in.Project = extra[i+1]
		}
	}
	id, err := Add(o, in)
	if err != nil {
		t.Fatalf("add %s %q: %v", kind, title, err)
	}
	return id
}

func trailFile(dir string) string { return filepath.Join(dir, ".agents", "trail.jsonl") }

func trailText(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(trailFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func str(s string) *string { return &s }

func TestResumeMemoryStalenessAndPlanReadyPhases(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	o := opts(dir, home)

	if err := os.MkdirAll(filepath.Join(dir, "internal", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRepoFile := func(rel, content string, modTime time.Time) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	past := fixed.Add(-1 * time.Hour)
	future := fixed.Add(1 * time.Hour)

	writeRepoFile("AGENTS.md", "# AGENTS\n", past)
	writeRepoFile("PRD.md", "# PRD\n", past)
	writeRepoFile("TODO.md", "## Now\n\n- [ ] Phase 2\n", past)
	writeRepoFile("internal/core/engine.go", "package core\nfunc Run() {}\n", past)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")

	if _, err := MemoryAdd(o, MemoryEntry{
		Type:        "project",
		Name:        "engine-rule",
		Description: "Engine invariant in internal/core/engine.go",
	}, strings.NewReader("Always keep internal/core/engine.go lock-free.")); err != nil {
		t.Fatalf("MemoryAdd engine-rule: %v", err)
	}
	if _, err := MemoryAdd(o, MemoryEntry{
		Type:        "project",
		Name:        "fresh-rule",
		Description: "Fresh rule in AGENTS.md",
	}, strings.NewReader("See AGENTS.md for setup.")); err != nil {
		t.Fatalf("MemoryAdd fresh-rule: %v", err)
	}
	if _, err := MemoryAdd(o, MemoryEntry{
		Type:        "project",
		Name:        "hash-rule",
		Description: "Pinned hash rule alongside AGENTS.md",
	}, strings.NewReader("See AGENTS.md\nPRD.md (sha256 deadbeef01234567) must stay unchanged.")); err != nil {
		t.Fatalf("MemoryAdd hash-rule: %v", err)
	}
	// Matching sha256 pin should NOT be flagged stale even if mtime is in the future (Finding 3).
	todoSum := "e3b0c44298fc1c"
	if data, err := os.ReadFile(filepath.Join(dir, "TODO.md")); err == nil {
		s := sha256.Sum256(data)
		todoSum = hex.EncodeToString(s[:16])
	}
	if _, err := MemoryAdd(o, MemoryEntry{
		Type:        "project",
		Name:        "matching-hash-rule",
		Description: "Matching hash rule for TODO.md",
	}, strings.NewReader("TODO.md (sha256 "+todoSum+") is verified by content hash.")); err != nil {
		t.Fatalf("MemoryAdd matching-hash-rule: %v", err)
	}

	// Modify internal/core/engine.go after MEMORY-engine-rule was recorded.
	writeRepoFile("internal/core/engine.go", "package core\nfunc Run() { /* modified */ }\n", future)
	// Keep AGENTS.md and PRD.md mtimes in the past; touch TODO.md mtime to future without changing content.
	writeRepoFile("AGENTS.md", "# AGENTS\n", past)
	writeRepoFile("PRD.md", "# PRD\n", past)
	writeRepoFile("TODO.md", "## Now\n\n- [ ] Phase 2\n", future)

	planBody := `## Phases

### Phase 1 — Foundation (after: none)
- [x] Build store
- [x] Checkpoint

### Phase 2 — Slicer (after: Phase 1)
- [ ] Build tiered slicer
- [ ] Checkpoint

### Phase 3 — Hooks (after: Phase 1)
- [ ] Build hooks
- [ ] Checkpoint

### Phase 4 — Finalize (after: Phase 2, Phase 3)
- [ ] Run end-to-end
- [ ] Checkpoint
`
	add(t, o, "plan", "Second brain rollout", planBody, "id", "PLAN-2026-10-25-dag")

	res, err := Resume(o)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !strings.Contains(res, "[STALE: internal/core/engine.go modified since MEMORY-engine-rule]") {
		t.Errorf("Resume missing staleness flag for MEMORY-engine-rule in:\n%s", res)
	}
	if strings.Contains(res, "modified since MEMORY-fresh-rule") {
		t.Errorf("Resume should not flag fresh-rule as stale in:\n%s", res)
	}
	if !strings.Contains(res, "[STALE: PRD.md modified since MEMORY-hash-rule]") {
		t.Errorf("Resume missing sha256 mismatch staleness flag for MEMORY-hash-rule in:\n%s", res)
	}
	if strings.Contains(res, "modified since MEMORY-matching-hash-rule") {
		t.Errorf("Resume should not flag matching-hash-rule as stale when sha256 matches despite newer mtime:\n%s", res)
	}
	if !strings.Contains(res, "Ready phases: Phase 2 — Slicer, Phase 3 — Hooks") {
		t.Errorf("Resume missing Ready phases summary in:\n%s", res)
	}

	hits, err := Search(o, []string{"engine"}, "memory", 10)
	if err != nil {
		t.Fatalf("Search memory: %v", err)
	}
	if len(hits) != 1 || !strings.Contains(FormatHit(hits[0]), "[STALE: internal/core/engine.go modified since MEMORY-engine-rule]") {
		t.Errorf("Search memory hits missing [STALE] flag: %+v", hits)
	}

	refHits, err := Refs(o, "internal/core/engine.go", "memory", 10)
	if err != nil {
		t.Fatalf("Refs memory: %v", err)
	}
	if len(refHits) != 1 || !strings.Contains(FormatHit(refHits[0]), "[STALE: internal/core/engine.go modified since MEMORY-engine-rule]") {
		t.Errorf("Refs memory hits missing [STALE] flag: %+v", refHits)
	}
}
