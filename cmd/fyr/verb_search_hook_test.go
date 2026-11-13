package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
)

func TestTurnBrainCacheMemoizesAndExtractsActionableSkillDigest(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	bo := brain.Options{Home: home, Dir: dir}
	adrID, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Model routing policy",
		Body: strings.NewReader("Touches internal/work/catalog.go and enforces cross-family review."),
	})
	if err != nil {
		t.Fatalf("Add adr: %v", err)
	}
	if _, err := brain.Add(bo, brain.AddInput{
		Kind: "bug", Status: "open", Title: "Timeout during catalog refresh",
		Body: strings.NewReader("Symptom: Observed when agy models stalls on offline host.\nRule: Retry with cached catalog.\n"),
	}); err != nil {
		t.Fatalf("Add bug: %v", err)
	}

	cache := newTurnBrainCache(home, false)
	refs := makeBrainRefs(cache, dir)
	refsForDir := makeBrainRefsForDir(cache)
	turnCtxFn := makeBrainTurnContext(cache)

	for i := 0; i < 20; i++ {
		_ = refs("internal/work/catalog.go")
		_ = refsForDir(dir, adrID)
	}
	ctx := turnCtxFn(dir, home, "Tell me some sort of model idea", nil)
	if cache.loadCount != 1 {
		t.Errorf("turnBrainCache.loadCount = %d across 21 lookups, want 1", cache.loadCount)
	}
	// Generic stopwords like "some sort model" must NOT match unrelated records.
	if len(ctx.ADRs) != 0 || len(ctx.Bugs) != 0 {
		t.Errorf("generic stopwords falsely matched records: adrs=%+v bugs=%+v", ctx.ADRs, ctx.Bugs)
	}
	// Pivot file hit matches ADR precisely.
	ctxHit := turnCtxFn(dir, home, "Update catalog", []string{"internal/work/catalog.go"})
	if len(ctxHit.ADRs) != 1 || ctxHit.ADRs[0].ID != adrID {
		t.Errorf("pivot file match failed: got %+v, want %s", ctxHit.ADRs, adrID)
	}

	// parseSkillMetaFile must populate Digest from body rules/steps without splitting multi-byte UTF-8 runes.
	skillPath := filepath.Join(t.TempDir(), "SKILL.md")
	longStep := strings.Repeat("verify — step → ok · ", 14) // multi-byte UTF-8 spanning > 180 and > 600 bytes
	skillMD := "---\nname: demo\ndescription: Generic YAML summary duplicated in system prompt.\n---\n\n# Demo Skill\n\n- Step 1: Run `go test -race ./...` before committing.\n- Step 2: Cite `path:start-end (Symbol)` — " + longStep + "\n- Step 3: " + longStep + "\n"
	if err := os.WriteFile(skillPath, []byte(skillMD), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, ok := parseSkillMetaFile("demo", skillPath)
	if !ok {
		t.Fatalf("parseSkillMetaFile failed")
	}
	if strings.Contains(meta.Digest, "Generic YAML summary") {
		t.Errorf("expected Digest to use actionable body instead of YAML description, got %q", meta.Digest)
	}
	if !strings.Contains(meta.Digest, "Step 1: Run") {
		t.Errorf("expected actionable step in Digest, got %q", meta.Digest)
	}
	if !utf8.ValidString(meta.Digest) || len(meta.Digest) > 600 {
		t.Errorf("expected valid UTF-8 Digest <= 600 bytes, got len=%d valid=%v", len(meta.Digest), utf8.ValidString(meta.Digest))
	}
}
