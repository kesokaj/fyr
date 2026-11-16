package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

func TestTurnBrainCacheMemoizesAndExtractsActionableSkillDigest(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	bo := brain.Options{Home: home, Dir: dir}
	adrID, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Model routing policy",
		Body: strings.NewReader("Touches internal/models/catalog.go and enforces cross-family review."),
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
		_ = refs("internal/models/catalog.go")
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
	ctxHit := turnCtxFn(dir, home, "Update catalog", []string{"internal/models/catalog.go"})
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

func TestPreInvocationProgressiveMemory(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	svcPath := filepath.Join(dir, "service.go")
	if err := os.WriteFile(svcPath, []byte("package svc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bo := brain.Options{Home: home, Dir: dir}
	goGlobal := brain.Options{Home: home, Dir: dir, Global: true}

	// Low validity (< 0.40): derived memory referencing service.go, then service.go modified later.
	lowID, err := brain.MemoryAdd(bo, brain.MemoryEntry{
		Type: "semantic", Name: "low-stale", Description: "Old fact about service.go", Provenance: "derived",
	}, strings.NewReader("Touches service.go and should drop below 0.40."))
	if err != nil {
		t.Fatalf("MemoryAdd low-stale: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(svcPath, future, future); err != nil {
		t.Fatal(err)
	}

	// Add 2 semantic, 1 procedural, 1 episodic project memories + 1 global procedural + 1 global semantic.
	for _, m := range []struct {
		opts brain.Options
		ent  brain.MemoryEntry
	}{
		{bo, brain.MemoryEntry{Type: "semantic", Name: "sem-proj-1", Description: "Semantic fact one about service.go"}},
		{bo, brain.MemoryEntry{Type: "semantic", Name: "sem-proj-2", Description: "Semantic fact two about service.go"}},
		{bo, brain.MemoryEntry{Type: "procedural", Name: "proc-proj", Description: "Procedural rule for service.go"}},
		{bo, brain.MemoryEntry{Type: "episodic", Name: "ep-proj", Description: "Episodic note for service.go"}},
		{goGlobal, brain.MemoryEntry{Type: "procedural", Name: "proc-glob", Description: "Global procedural rule for service.go", Global: true}},
		{goGlobal, brain.MemoryEntry{Type: "semantic", Name: "sem-glob", Description: "Global semantic note for service.go", Global: true}},
	} {
		if _, err := brain.MemoryAdd(m.opts, m.ent, strings.NewReader("Applies to service.go.")); err != nil {
			t.Fatalf("MemoryAdd %s: %v", m.ent.Name, err)
		}
	}

	// Add 2 bugs and 1 ADR matching service.go.
	for _, b := range []string{"Bug one in service.go", "Bug two in service.go"} {
		if _, err := brain.Add(bo, brain.AddInput{Kind: "bug", Status: "open", Title: b, Body: strings.NewReader("Touches service.go")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := brain.Add(bo, brain.AddInput{Kind: "adr", Status: "accepted", Title: "ADR for service.go", Body: strings.NewReader("Applies to service.go")}); err != nil {
		t.Fatal(err)
	}

	cache := newTurnBrainCache(home, false)
	turnCtxFn := makeBrainTurnContext(cache)

	start := time.Now()
	ctx := turnCtxFn(dir, home, "Fix service.go behavior", []string{"service.go"})
	elapsed := time.Since(start)
	if elapsed >= 200*time.Millisecond {
		t.Errorf("turnCtxFn took %v, want < 200ms", elapsed)
	}

	if len(ctx.Memories) != 4 {
		t.Fatalf("got %d memories, want 4: %+v", len(ctx.Memories), ctx.Memories)
	}
	for _, m := range ctx.Memories {
		if m.ID == lowID {
			t.Errorf("low-validity memory %s should be hidden (< 0.40), got %+v", lowID, m)
		}
		if m.Validity < brain.MinActiveValidity {
			t.Errorf("memory %s validity %.2f < %.2f", m.ID, m.Validity, brain.MinActiveValidity)
		}
		if m.MemoryKind == "" {
			t.Errorf("memory %s missing MemoryKind", m.ID)
		}
	}
	// Top 3 must be procedural/episodic (before semantic).
	for i := 0; i < 3; i++ {
		if ctx.Memories[i].MemoryKind != "procedural" && ctx.Memories[i].MemoryKind != "episodic" {
			t.Errorf("memories[%d] = %s (%s), want procedural/episodic before semantic", i, ctx.Memories[i].ID, ctx.Memories[i].MemoryKind)
		}
	}
	if ctx.Memories[3].MemoryKind != "semantic" {
		t.Errorf("memories[3] = %s (%s), want semantic", ctx.Memories[3].ID, ctx.Memories[3].MemoryKind)
	}
	var hasGlobal bool
	for _, m := range ctx.Memories {
		if m.ID == "MEMORY-proc-glob" {
			hasGlobal = true
		}
	}
	if !hasGlobal {
		t.Errorf("expected global memory MEMORY-proc-glob in %+v", ctx.Memories)
	}
	// Bugs/ADRs must not be starved (total cap 6: 4 memories + 2 others).
	total := len(ctx.Memories) + len(ctx.Bugs) + len(ctx.ADRs)
	if total != 6 {
		t.Errorf("total matched records = %d (mem=%d bugs=%d adrs=%d), want 6", total, len(ctx.Memories), len(ctx.Bugs), len(ctx.ADRs))
	}

	// Verify ADRs + Bugs match alongside memories when total < 6.
	var mixed search.TurnBrainContext
	recs := []brain.Record{
		{ID: "MEMORY-a", Kind: "memory", Status: "closed", Title: "Memory A", Body: "service.go", Provenance: "operator", MemoryKind: "procedural", Scope: dir},
		{ID: "MEMORY-b", Kind: "memory", Status: "closed", Title: "Memory B", Body: "service.go", Provenance: "operator", MemoryKind: "semantic", Scope: dir},
		{ID: "BUG-1", Kind: "bug", Status: "open", Title: "Bug 1", Body: "service.go"},
		{ID: "BUG-2", Kind: "bug", Status: "open", Title: "Bug 2", Body: "service.go"},
		{ID: "ADR-1", Kind: "adr", Status: "accepted", Title: "ADR 1", Body: "service.go"},
		{ID: "ADR-2", Kind: "adr", Status: "accepted", Title: "ADR 2", Body: "service.go"},
	}
	for _, r := range recs {
		classifyTurnRecord(&mixed, dir, "Update service.go", []string{"service.go"}, extractTurnQueryTerms("Update service.go"), r)
	}
	if len(mixed.Memories) != 2 || len(mixed.Bugs) != 2 || len(mixed.ADRs) != 2 {
		t.Errorf("mixed match got mem=%d bugs=%d adrs=%d, want 2/2/2", len(mixed.Memories), len(mixed.Bugs), len(mixed.ADRs))
	}
}
