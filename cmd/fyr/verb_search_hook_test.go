package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	fixedNow := time.Now().Truncate(time.Second)
	bo := brain.Options{Home: home, Dir: dir, Now: func() time.Time { return fixedNow }}
	goGlobal := brain.Options{Home: home, Dir: dir, Global: true, Now: func() time.Time { return fixedNow }}

	// Low validity (< 0.40): derived memory referencing service.go, then service.go content changes.
	lowID, err := brain.MemoryAdd(bo, brain.MemoryEntry{
		Type: "semantic", Name: "low-stale", Description: "Old fact about service.go", Provenance: "derived",
	}, strings.NewReader("Touches service.go and should drop below 0.40."))
	if err != nil {
		t.Fatalf("MemoryAdd low-stale: %v", err)
	}
	if err := os.WriteFile(svcPath, []byte("package svc\n// content updated\n"), 0o600); err != nil {
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

func TestExtractSkillDescription(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "single-line quoted",
			raw:  "---\nname: a\ndescription: \"Quoted summary.\"\n---\nbody\n",
			want: "Quoted summary.",
		},
		{
			name: "literal block scalar with blank line",
			raw:  "---\nname: b\ndescription: |\n  First paragraph.\n\n  Second paragraph.\nallowed-tools: Bash\n---\nbody\n",
			want: "First paragraph. Second paragraph.",
		},
		{
			name: "folded block scalar with blank line and chomp",
			raw:  "---\nname: c\ndescription: >-\n  Folded line one\n\n  line two.\n---\nbody\n",
			want: "Folded line one line two.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractSkillDescription(tc.raw); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMakeBrainReprime(t *testing.T) {
	reprime := makeBrainReprime()
	dir := recordRepo(t)

	if got := reprime(dir, ""); got != "" {
		t.Errorf("empty home: got %q, want empty", got)
	}
	if got := reprime(dir, t.TempDir()); got != "" {
		t.Errorf("missing brain.db: got %q, want empty", got)
	}

	home := t.TempDir()
	bo := brain.Options{Home: home, Dir: dir}
	if _, err := brain.MemoryAdd(bo, brain.MemoryEntry{
		Type: "procedural", Name: "run-race", Description: "Always run go test -race before finishing.",
	}, strings.NewReader("Details.\n")); err != nil {
		t.Fatal(err)
	}
	openPhasedID, err := brain.Add(bo, brain.AddInput{
		Kind: "plan", Title: "Phased open plan",
		Body: strings.NewReader("## Phase 1: Core\n- [ ] Wire handler\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	openPlainID, err := brain.Add(bo, brain.AddInput{
		Kind: "plan", Title: "Plain open plan",
		Body: strings.NewReader("- [ ] No phase headings yet.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	closedID, err := brain.Add(bo, brain.AddInput{
		Kind: "plan", Title: "Finished plan",
		Body: strings.NewReader("- [x] Done.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}

	got := reprime(dir, home)
	for _, want := range []string{
		"# Re-primed Project Memory",
		"Always run go test -race before finishing.",
		"# Active Open Plans",
		openPhasedID + ": Phased open plan",
		openPlainID + ": Plain open plan (open)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("reprime missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, closedID) {
		t.Errorf("reprime included closed plan %s:\n%s", closedID, got)
	}
}

func TestSearchHookRecordsFTSAndStopwords(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	bo := brain.Options{Home: home, Dir: dir}

	migADR, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "SQLite migration durability",
		Body: strings.NewReader("Validate transactional rollbacks before upgrading schema versions."),
	})
	if err != nil {
		t.Fatalf("Add migADR: %v", err)
	}
	stallBug, err := brain.Add(bo, brain.AddInput{
		Kind: "bug", Status: "open", Title: "Catalog refresh timeout",
		Body: strings.NewReader("Observed when agy models stalls on offline laptop."),
	})
	if err != nil {
		t.Fatalf("Add stallBug: %v", err)
	}
	if _, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Button palette",
		Body: strings.NewReader("Use slate tokens on web cards."),
	}); err != nil {
		t.Fatalf("Add unrelated ADR: %v", err)
	}

	cache := newTurnBrainCache(home, false)
	turnCtxFn := makeBrainTurnContext(cache)

	// 1. Porter stemming across title+body via records_fts ("stalling"/"refreshes" -> "stalls"/"refresh").
	ctxStall := turnCtxFn(dir, home, "Diagnose stalling refreshes when laptop is offline", nil)
	if len(ctxStall.Bugs) != 1 || ctxStall.Bugs[0].ID != stallBug {
		t.Errorf("records_fts stemmed bug match = %+v, want [%s]", ctxStall.Bugs, stallBug)
	}
	if len(ctxStall.ADRs) != 0 {
		t.Errorf("unrelated ADRs matched stall query: %+v", ctxStall.ADRs)
	}

	// 2. Inflected body terms ("transactional", "upgrading", "schemas") match migADR via records_fts.
	ctxMig := turnCtxFn(dir, home, "Verify transactional schemas while upgrading", nil)
	if len(ctxMig.ADRs) != 1 || ctxMig.ADRs[0].ID != migADR {
		t.Errorf("records_fts body match = %+v, want [%s]", ctxMig.ADRs, migADR)
	}

	// 3. Stopwords are stripped before querying records_fts and never match unrelated records.
	if terms := extractTurnQueryTerms("What model should they make with these files and tests during check"); len(terms) != 0 {
		t.Errorf("expected all stopwords filtered, got %v", terms)
	}
	ctxStop := turnCtxFn(dir, home, "What model should they make with these files and tests during check", nil)
	if len(ctxStop.ADRs) != 0 || len(ctxStop.Bugs) != 0 {
		t.Errorf("stopword prompt matched records: adrs=%+v bugs=%+v", ctxStop.ADRs, ctxStop.Bugs)
	}
	if cache.loadCount != 1 {
		t.Errorf("turnBrainCache.loadCount = %d after multiple FTS queries, want 1", cache.loadCount)
	}

	// 4. ReadTrail fallback works when brain.db does not exist under emptyHome.
	if _, err := brain.Export(bo); err != nil {
		t.Fatalf("Export trail: %v", err)
	}
	emptyHome := t.TempDir()
	trailCache := newTurnBrainCache(emptyHome, false)
	trailCtx := makeBrainTurnContext(trailCache)(dir, emptyHome, "Update SQLite migration durability", nil)
	if len(trailCtx.ADRs) != 1 || trailCtx.ADRs[0].ID != migADR {
		t.Errorf("ReadTrail fallback match = %+v, want [%s]", trailCtx.ADRs, migADR)
	}
}

func TestTurnBrainCacheConcurrentRace(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	bo := brain.Options{Home: home, Dir: dir}
	adrID, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Parallel PreInvocation caching",
		Body: strings.NewReader("Touches internal/search/hook_pre_invocation_fast.go and enforces mutex safety."),
	})
	if err != nil {
		t.Fatalf("Add adr: %v", err)
	}
	if _, err := brain.Add(bo, brain.AddInput{
		Kind: "bug", Status: "open", Title: "Data race on turnBrainCache maps",
		Body: strings.NewReader("Observed when internal/search/hook_pre_invocation_fast.go runs concurrently."),
	}); err != nil {
		t.Fatalf("Add bug: %v", err)
	}

	cache := newTurnBrainCache(home, false)
	turnCtxFn := makeBrainTurnContext(cache)
	refsForDir := makeBrainRefsForDir(cache)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan string, 24)
	for g := 0; g < 12; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < 8; i++ {
				if id%2 == 0 {
					ctx := turnCtxFn(dir, home, "Update internal/search/hook_pre_invocation_fast.go caching", []string{"internal/search/hook_pre_invocation_fast.go"})
					if len(ctx.ADRs) == 0 || ctx.ADRs[0].ID != adrID {
						errCh <- "BrainTurnContext missing expected ADR"
						return
					}
				} else {
					refs := refsForDir(dir, "internal/search/hook_pre_invocation_fast.go")
					if len(refs) == 0 {
						errCh <- "BrainRefsForDir returned empty refs"
						return
					}
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
	cache.mu.Lock()
	loads := cache.loadCount
	cache.mu.Unlock()
	if loads != 1 {
		t.Errorf("concurrent turnBrainCache.loadCount = %d, want 1", loads)
	}
}

func TestVerbSearchHookWarm1143RecordsAndContextBudget(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "service.go"), []byte("package sample\n\nfunc ReconcileProjectIndex(root string) error { return nil }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bo := brain.Options{Home: home, Dir: dir}
	planID, err := brain.Add(bo, brain.AddInput{Kind: "plan", Title: "Active V910 Plan", Body: strings.NewReader("## Phase 2\n- [ ] Optimize hook\n")})
	if err != nil {
		t.Fatal(err)
	}
	adrID, err := brain.Add(bo, brain.AddInput{Kind: "adr", Status: "accepted", Title: "QuorumSnapshot Budget", Body: strings.NewReader("Touches service.go and enforces QuorumSnapshot invariants.\n")})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(brain.Path(home))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	var scope string
	if err := db.QueryRow(`SELECT scope FROM records WHERE id = ?`, planID).Scan(&scope); err != nil {
		db.Close()
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance, memory_kind, validity, verified_at) VALUES (?, ?, ?, ?, '2025-01-01', ?, ?, 'demo', '2025-01-01T00:00:00Z', '', '', 1.0, '2025-01-01T00:00:00Z')`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	body := strings.Repeat("Historical rationale paragraph.\n", 100)
	for i := 1; i <= 1141; i++ {
		kind, status := "change", "closed"
		if i <= 250 {
			kind, status = "adr", "accepted"
		}
		if _, err := stmt.Exec(scope, fmt.Sprintf("REC-%04d", i), kind, status, fmt.Sprintf("Record %d", i), body); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	if err := search.Index(search.Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Canceled context must abort cleanly via makeBrainTurnContextCtx.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceledRes := makeBrainTurnContextCtx(newTurnBrainCache(home, false))(cctx, dir, home, "Inspect QuorumSnapshot in service.go", nil)
	if canceledRes.ActivePlan != nil || len(canceledRes.ADRs) != 0 {
		t.Fatalf("canceled makeBrainTurnContextCtx returned non-empty result: %+v", canceledRes)
	}

	// Warm PreInvocation with 1,143 seeded records must complete < 120ms (timed_out=0).
	callPreInv := func(convID string) (time.Duration, search.PreInvocationOutput) {
		raw, _ := json.Marshal(map[string]any{
			"conversationId": convID, "workspacePaths": []string{dir},
			"invocationNum": 1, "lastUserInput": "Investigate QuorumSnapshot in service.go",
		})
		cache := newTurnBrainCache(home, false)
		var out bytes.Buffer
		start := time.Now()
		if err := search.Search(search.Options{
			Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(raw),
			BrainTurnContext:    makeBrainTurnContext(cache),
			BrainTurnContextCtx: makeBrainTurnContextCtx(cache),
		}, &out); err != nil {
			t.Fatalf("PreInvocation(%s): %v", convID, err)
		}
		elapsed := time.Since(start)
		var resp search.PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal(%s): %v", convID, err)
		}
		return elapsed, resp
	}
	_, _ = callPreInv("conv-1143-warmup")
	elapsed, resp := callPreInv("conv-1143-warm")
	if elapsed >= 120*time.Millisecond {
		t.Errorf("warm PreInvocation on 1143 seeded brain records took %v, want < 120ms", elapsed)
	}
	if len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("expected 1 injectStep, got %+v", resp)
	}
	sys := resp.InjectSteps[0].SystemMessage.SystemMessage
	if !strings.Contains(sys, adrID) {
		t.Errorf("warm PreInvocation missing matched ADR %s:\n%s", adrID, sys)
	}
	sdb, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, ".agents", "scratch", "search.db"))+"?mode=ro")
	if err != nil {
		t.Fatalf("open search.db: %v", err)
	}
	defer sdb.Close()
	var timedOut int
	if err := sdb.QueryRow(`SELECT timed_out FROM vet_history WHERE conversation_id = 'conv-1143-warm' ORDER BY turn_num DESC LIMIT 1`).Scan(&timedOut); err != nil || timedOut != 0 {
		t.Errorf("conv-1143-warm vet_history timed_out = %d (err=%v), want 0", timedOut, err)
	}
}

func TestTurnBrainCacheDoesNotLeakPartialBodiesToBrainRefs(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	bo := brain.Options{Home: home, Dir: dir}
	oldestADR, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Legacy Storage Decision",
		Body: strings.NewReader("Controls legacy_storage_engine.go invariants.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 14; i++ {
		if _, err := brain.Add(bo, brain.AddInput{
			Kind: "adr", Status: "accepted", Title: fmt.Sprintf("Newer ADR %02d", i),
			Body: strings.NewReader(fmt.Sprintf("Unrelated newer decision %d.\n", i)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	cache := newTurnBrainCache(home, false)
	// First run BrainTurnContext on an unrelated prompt (selective hydration omits oldestADR body).
	_ = makeBrainTurnContextCtx(cache)(context.Background(), dir, home, "Refactor dashboard metrics rendering", nil)
	// BrainRefsForDir must still find oldestADR via its full Body, not cached partial records.
	refs := makeBrainRefsForDir(cache)(dir, "legacy_storage_engine.go")
	found := false
	for _, r := range refs {
		if r.ID == oldestADR {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("BrainRefsForDir missed %s after partial BrainTurnContext load; got %+v", oldestADR, refs)
	}
}
