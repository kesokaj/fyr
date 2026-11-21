package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreInvocationWarmLatencyUnder40ms(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	for pkgIdx := 0; pkgIdx < 6; pkgIdx++ {
		for fileIdx := 0; fileIdx < 6; fileIdx++ {
			rel := fmt.Sprintf("internal/pkg%d/mod%d.go", pkgIdx, fileIdx)
			src := fmt.Sprintf("package pkg%d\n\nfunc HandleSlice%d_%d(input string) (string, error) {\n\treturn input, nil\n}\n", pkgIdx, pkgIdx, fileIdx)
			writeFile(t, dir, rel, src)
		}
	}
	writeFile(t, dir, "service.go", hookSampleServiceSrc)
	commitAll(t, dir, "populate warm latency fixture")

	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}

	makePayload := func(convID string) []byte {
		b, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"modelName":      "gemini-4-neon-high",
			"invocationNum":  1,
			"lastUserInput":  "Update ReconcileProjectIndex in service.go to validate empty root and run go test",
		})
		return b
	}

	// Prime SQLite driver / OS page cache once, then measure a fresh Turn-0 conversation.
	var primeOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(makePayload("conv-warm-prime"))}, &primeOut); err != nil {
		t.Fatalf("prime pre-invocation: %v", err)
	}

	//Forbid git subprocesses by putting an empty PATH during the warm Turn-0 measurement!
	t.Setenv("PATH", t.TempDir())
	payload := makePayload("conv-warm-turn0")
	var out bytes.Buffer
	start := time.Now()
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(payload)}, &out); err != nil {
		t.Fatalf("warm Turn-0 pre-invocation: %v", err)
	}
	warmLimit := 40 * time.Millisecond
	if defaultHookDeadline > 200*time.Millisecond {
		warmLimit = 200 * time.Millisecond // -race adds overhead under go test -race ./... contention
	}
	elapsed := time.Since(start)
	if elapsed >= warmLimit {
		t.Fatalf("warm Turn-0 PreInvocation took %v, want < %v", elapsed, warmLimit)
	}

	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("unmarshal warm output: err=%v raw=%s", err, out.String())
	}
	sysMsg := resp.InjectSteps[0].SystemMessage.SystemMessage
	if !strings.Contains(sysMsg, "ReconcileProjectIndex") {
		t.Errorf("warm Turn-0 missing ReconcileProjectIndex slice:\n%s", sysMsg)
	}

	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	defer st.close()
	rows, err := st.listConversationVetHistory("conv-warm-turn0", 5)
	if err != nil || len(rows) != 1 {
		t.Fatalf("listConversationVetHistory: err=%v rows=%+v", err, rows)
	}
	if rows[0].InjectedBytes != len(sysMsg) || rows[0].TimedOut || rows[0].DurationMS < 0 || rows[0].DurationMS >= int(warmLimit.Milliseconds()) {
		t.Errorf("unexpected v11 vet_history metrics: %+v (want InjectedBytes=%d, TimedOut=false, DurationMS<%d)", rows[0], len(sysMsg), warmLimit.Milliseconds())
	}
}

func TestVetHistoryV11TimeoutApprovalDedupAndKneeDedup(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	// 1,240,000 bytes => 310K tokens (>= 300K 60% knee for gemini-4-neon-high).
	if err := os.WriteFile(transcript, bytes.Repeat([]byte("a"), 1_240_000), 0o600); err != nil {
		t.Fatal(err)
	}

	runPreInv := func(convID, prompt string, extra int) PreInvocationOutput {
		t.Helper()
		if extra > 0 {
			if err := os.WriteFile(transcript, bytes.Repeat([]byte("a"), 1_240_000+extra), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		req, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"transcriptPath": transcript,
			"modelName":      "gemini-4-neon-high",
			"invocationNum":  0,
			"lastUserInput":  prompt,
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-invocation(%q): %v", prompt, err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return resp
	}

	// 1. Turn 1 above 300K knee emits [CONTEXT_KNEE_WARNING] once.
	r1 := runPreInv("conv-dedup-v11", "Inspect ReconcileProjectIndex in service.go", 0)
	if len(r1.InjectSteps) == 0 || !strings.Contains(r1.InjectSteps[0].SystemMessage.SystemMessage, "300K 60% model knee") {
		t.Fatalf("Turn 1 expected 300K knee warning, got %+v", r1)
	}

	// 2. Consecutive short IntentApproval ("go all approved") turns in the same knee bucket:
	//    - do NOT repeat [CONTEXT_KNEE_WARNING] (so PASS_THROUGH returns empty InjectSteps)
	//    - collapse into a single vet_history row instead of inserting duplicate rows!
	for i := 1; i <= 3; i++ {
		rApp := runPreInv("conv-dedup-v11", "go all approved", i*1000)
		if len(rApp.InjectSteps) != 0 {
			t.Fatalf("approval turn #%d repeated knee banner on no-op pass-through (%+v)", i, rApp.InjectSteps)
		}
	}

	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	rows, err := st.listConversationVetHistory("conv-dedup-v11", 10)
	st.close()
	if err != nil || len(rows) != 2 {
		t.Fatalf("expected 2 vet_history rows (1 question + 1 collapsed approval), got len=%d err=%v rows=%+v", len(rows), err, rows)
	}
	if rows[1].Intent != IntentApproval || rows[1].InjectedBytes != 0 {
		t.Errorf("collapsed approval row = %+v, want Intent=APPROVAL InjectedBytes=0", rows[1])
	}

	// 3. Hard deadline timeout records best-effort timed_out = 1 in vet_history cleanly.
	done := make(chan struct{})
	defer func() { <-done }()
	reqTimeout, _ := json.Marshal(map[string]any{
		"conversationId": "conv-timeout-v11",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Fix ReconcileProjectIndex in service.go",
	})
	var tOut bytes.Buffer
	err = Search(Options{
		Dir:          dir,
		Home:         home,
		Hook:         "pre-invocation",
		HookIn:       bytes.NewReader(reqTimeout),
		hookDeadline: 40 * time.Millisecond,
		hookDispatch: func(ctx context.Context, _ Options, _ string, _ string, _ HookInput, _ io.Writer) error {
			defer close(done)
			<-ctx.Done()
			return ctx.Err()
		},
	}, &tOut)
	if err != nil {
		t.Fatalf("timeout Search error: %v", err)
	}
	st2, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.close()
	tRows, err := st2.listConversationVetHistory("conv-timeout-v11", 5)
	if err != nil || len(tRows) != 1 || !tRows[0].TimedOut || tRows[0].DurationMS < 35 || tRows[0].InjectedBytes != 0 {
		t.Fatalf("timeout vet_history row = %+v, err=%v; want 1 row with TimedOut=true DurationMS>=35 InjectedBytes=0", tRows, err)
	}
}

func TestWarmSigInvalidatesOnUntrackedSubdirFileEdit(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "service.go", hookSampleServiceSrc)
	commitAll(t, dir, "init")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	st.saveGitWarmSig(dir)

	subFile := filepath.Join(dir, "untrackedpkg", "nested", "a.go")
	writeFile(t, dir, "untrackedpkg/nested/a.go", "package nested\nfunc One() {}\n")
	if st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatalf("warm sig matched after creating untracked subdirectory file")
	}
	st.saveGitWarmSig(dir)
	if !st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatalf("warm sig failed to match freshly saved untracked subdirectory file")
	}
	time.Sleep(5 * time.Millisecond)
	if err := os.WriteFile(subFile, []byte("package nested\nfunc OneRenamedForMoreBytes() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatalf("warm sig stayed valid after editing %s inside untracked subdirectory", subFile)
	}
}

func TestGitWarmSigHandlesOver128Dirs(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	for i := 0; i < 160; i++ {
		rel := fmt.Sprintf("internal/d%03d/mod.go", i)
		writeFile(t, dir, rel, fmt.Sprintf("package d%03d\nfunc fn%d() int { return %d }\n", i, i, i))
	}
	commitAll(t, dir, "160 directories")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	sig, ok := computeGitWarmSignature(dir)
	if !ok || sig == "" {
		t.Fatalf("computeGitWarmSignature on 160 dirs = (%q, %v), want non-empty, true", sig, ok)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	defer st.close()
	st.saveGitWarmSig(dir)
	if !st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatalf("matchesGitWarmSig = false after saveGitWarmSig on 160 dirs")
	}
}

func TestGitWarmSigSkipsTrackedAgentsScratch(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	writeFile(t, dir, ".agents/rules/r.md", "# Rule\n")
	writeFile(t, dir, ".agents/scratch/.gitkeep", "")
	writeFile(t, dir, "service.go", hookSampleServiceSrc)
	commitAll(t, dir, "scaffolded with .agents/scratch")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	for i := 0; i < 300; i++ {
		writeFile(t, dir, fmt.Sprintf(".agents/scratch/tmp/f%03d.txt", i), "x\n")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agents/scratch/sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "service.go"), filepath.Join(dir, ".agents/scratch/sub/sym")); err != nil {
		t.Fatal(err)
	}
	sig, ok := computeGitWarmSignature(dir)
	if !ok || sig == "" {
		t.Fatalf("computeGitWarmSignature with busy .agents/scratch = (%q, %v), want non-empty, true", sig, ok)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	st.saveGitWarmSig(dir)
	if !st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatal("matchesGitWarmSig = false right after saveGitWarmSig")
	}
	writeFile(t, dir, ".agents/scratch/more/extra.txt", "y\n")
	if !st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatal("matchesGitWarmSig invalidated by untracked file in .agents/scratch")
	}
	writeFile(t, dir, ".agents/rules/extra.md", "# Extra\n")
	if st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatal("matchesGitWarmSig stayed valid after adding untracked file in .agents/rules")
	}
}

func TestPreInvocationBoundedLatencyLargeRepo(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	for pkgIdx := 0; pkgIdx < 40; pkgIdx++ {
		for fileIdx := 0; fileIdx < 4; fileIdx++ {
			rel := fmt.Sprintf("internal/mod%02d/part%d.go", pkgIdx, fileIdx)
			src := fmt.Sprintf("package mod%02d\n\nfunc Worker%02d_%d(v int) int {\n\treturn helper%02d_%d(v)\n}\n\nfunc helper%02d_%d(v int) int {\n\treturn v + 1\n}\n", pkgIdx, pkgIdx, fileIdx, pkgIdx, fileIdx, pkgIdx, fileIdx)
			writeFile(t, dir, rel, src)
		}
	}
	writeFile(t, dir, "service.go", hookSampleServiceSrc)
	commitAll(t, dir, "large repo fixture")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}

	makePayload := func(convID, prompt string) []byte {
		b, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"modelName":      "gemini-4-neon-high",
			"invocationNum":  1,
			"lastUserInput":  prompt,
		})
		return b
	}

	var primeOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(makePayload("conv-large-prime", "Update ReconcileProjectIndex in service.go"))}, &primeOut); err != nil {
		t.Fatalf("prime: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	warmLimit := 40 * time.Millisecond
	if defaultHookDeadline > 200*time.Millisecond {
		warmLimit = defaultHookDeadline / 2 // absorb parallel go test -race ./... CPU contention
	}

	// 1. Targeted prompt hits ReconcileProjectIndex quickly with non-empty capsule.
	var out bytes.Buffer
	start := time.Now()
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(makePayload("conv-large-hit", "Update ReconcileProjectIndex in service.go to validate empty root and run go test"))}, &out); err != nil {
		t.Fatalf("warm hit: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= warmLimit {
		t.Fatalf("warm hit PreInvocation took %v, want < %v", elapsed, warmLimit)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("unmarshal warm hit: err=%v raw=%s", err, out.String())
	}
	if !strings.Contains(resp.InjectSteps[0].SystemMessage.SystemMessage, "ReconcileProjectIndex") {
		t.Errorf("warm hit missing ReconcileProjectIndex:\n%s", resp.InjectSteps[0].SystemMessage.SystemMessage)
	}

	// 2. Approval/trivial prompt stays fast and PASS_THROUGH (empty InjectSteps).
	var appOut bytes.Buffer
	appStart := time.Now()
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(makePayload("conv-large-app", "go all approved"))}, &appOut); err != nil {
		t.Fatalf("approval: %v", err)
	}
	if elapsed := time.Since(appStart); elapsed >= warmLimit {
		t.Fatalf("approval PreInvocation took %v, want < %v", elapsed, warmLimit)
	}
	var appResp PreInvocationOutput
	if err := json.Unmarshal(appOut.Bytes(), &appResp); err != nil {
		t.Fatalf("unmarshal approval: %v", err)
	}
	if len(appResp.InjectSteps) != 0 {
		t.Errorf("approval expected PASS_THROUGH empty InjectSteps, got %+v", appResp)
	}

	// 3. Off-topic conversational question stays fast with zero false AST pivots.
	offLimit := warmLimit
	if defaultHookDeadline > 200*time.Millisecond {
		// FTS5 fallback executes inside modernc.org/sqlite under -d=checkptr when -race is active.
		offLimit = defaultHookDeadline
	}
	var offOut bytes.Buffer
	offStart := time.Now()
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(makePayload("conv-large-off", "Why did the dog bark loudly at midnight yesterday?"))}, &offOut); err != nil {
		t.Fatalf("off-topic: %v", err)
	}
	if elapsed := time.Since(offStart); elapsed >= offLimit {
		t.Fatalf("off-topic PreInvocation took %v, want < %v", elapsed, offLimit)
	}

	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	defer st.close()
	rows, err := st.listConversationVetHistory("conv-large-hit", 5)
	if err != nil || len(rows) != 1 || rows[0].TimedOut || rows[0].InjectedBytes <= 0 {
		t.Fatalf("conv-large-hit vet_history = %+v, err=%v; want TimedOut=false InjectedBytes>0", rows, err)
	}
	offRows, err := st.listConversationVetHistory("conv-large-off", 5)
	if err != nil || len(offRows) != 1 || offRows[0].TimedOut || len(offRows[0].PivotFiles) != 0 {
		t.Fatalf("conv-large-off vet_history = %+v, err=%v; want TimedOut=false PivotFiles=[]", offRows, err)
	}
}

func TestBoundedSymGraphPriorityHopsAndSingleWordFTS(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()

	// 1. Active file with 85 symbols must not starve prompt symbol UniqueTargetSymbol or its caller.
	var helpers strings.Builder
	helpers.WriteString("package app\n\n")
	for i := 0; i < 85; i++ {
		fmt.Fprintf(&helpers, "func Helper%02d() int { return %d }\n", i, i)
	}
	writeFile(t, dir, "a_active.go", helpers.String())
	writeFile(t, dir, "z_target.go", "package app\n\n// Backpressure telemetry coordinator handles queue draining.\nfunc UniqueTargetSymbol() int {\n\treturn 42\n}\n")
	writeFile(t, dir, "z_caller.go", "package app\n\nfunc InvokeTargetAlpha() int {\n\treturn UniqueTargetSymbol()\n}\n")

	// 2. 4-file 3-hop chain for --hops 3 verification.
	writeFile(t, dir, "hop_a.go", "package app\nfunc ChainHopZero() int { return ChainHopOne() }\n")
	writeFile(t, dir, "hop_b.go", "package app\nfunc ChainHopOne() int { return ChainHopTwo() }\n")
	writeFile(t, dir, "hop_c.go", "package app\nfunc ChainHopTwo() int { return ChainHopThree() }\n")
	writeFile(t, dir, "hop_d.go", "package app\nfunc ChainHopThree() int { return 7 }\n")

	commitAll(t, dir, "priority hops and single-word fts fixture")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}

	sc, _, _, err := prepareHookScope(dir)
	if err != nil {
		t.Fatalf("prepareHookScope: %v", err)
	}
	defer sc.close()

	g, err := sc.loadBoundedSymGraph("Update UniqueTargetSymbol", map[string]bool{"a_active.go": true}, false, 2)
	if err != nil {
		t.Fatalf("loadBoundedSymGraph: %v", err)
	}
	if len(g.byPath["z_caller.go"]) == 0 {
		t.Fatalf("bounded graph starved z_caller.go; loaded paths=%v", mapKeysSlice(g.byPath))
	}

	// Single conceptual non-symbol word matches comment via 1-word FTS seeding.
	var singleBuf bytes.Buffer
	if err := BuildContextSlice(Options{Dir: dir, Home: home, Slice: "backpressure", Hops: 1, Budget: 600}, &singleBuf); err != nil {
		t.Fatalf("BuildContextSlice 1-word: %v", err)
	}
	if !strings.Contains(singleBuf.String(), "UniqueTargetSymbol") {
		t.Errorf("1-word FTS --slice missed UniqueTargetSymbol:\n%s", singleBuf.String())
	}

	// --hops 3 routes cleanly without error and emits full 0..2 hop slice.
	var hopsBuf bytes.Buffer
	if err := BuildContextSlice(Options{Dir: dir, Home: home, Slice: "ChainHopZero", Hops: 3, Budget: 1200}, &hopsBuf); err != nil {
		t.Fatalf("BuildContextSlice hops=3: %v", err)
	}
	for _, want := range []string{"ChainHopZero", "ChainHopOne", "ChainHopTwo"} {
		if !strings.Contains(hopsBuf.String(), want) {
			t.Errorf("--hops 3 slice missing %q:\n%s", want, hopsBuf.String())
		}
	}

	// Leading >12 stopwords must not crowd out trailing symbol or path seed tokens.
	stopCandidates := []string{
		"the", "and", "for", "with", "from", "that", "this", "what", "when", "where",
		"which", "who", "why", "how", "are", "was", "were", "have", "has", "had",
		"will", "would", "could", "should", "can", "not", "but", "into", "about",
		"there", "their", "then", "than", "your", "our",
	}
	var stops []string
	for _, w := range stopCandidates {
		if ftsFallbackStopwords[w] {
			stops = append(stops, w)
			if len(stops) == maxBoundedQueryTerms+3 {
				break
			}
		}
	}
	if len(stops) <= maxBoundedQueryTerms {
		t.Fatalf("need >%d stopwords for test, got %d", maxBoundedQueryTerms, len(stops))
	}
	stopPrefix := strings.Join(stops, " ")
	gStop, err := sc.loadBoundedSymGraph(stopPrefix+" UniqueTargetSymbol", nil, false, 1)
	if err != nil {
		t.Fatalf("loadBoundedSymGraph stopwords symbol: %v", err)
	}
	if len(gStop.byPath["z_target.go"]) == 0 || len(gStop.byPath["z_caller.go"]) == 0 {
		t.Fatalf("leading stopwords crowded out target/caller; paths=%v", mapKeysSlice(gStop.byPath))
	}
	gFileStop, err := sc.loadBoundedSymGraph(stopPrefix+" hop_d.go", nil, false, 0)
	if err != nil {
		t.Fatalf("loadBoundedSymGraph stopwords file: %v", err)
	}
	if len(gFileStop.byPath["hop_d.go"]) == 0 {
		t.Fatalf("leading stopwords crowded out hop_d.go file seed; paths=%v", mapKeysSlice(gFileStop.byPath))
	}
	gSlug, err := sc.loadBoundedSymGraph(".agents/skills/auto-distilled-uniquetargetsymbol-run", nil, false, 1)
	if err != nil {
		t.Fatalf("loadBoundedSymGraph compound slug: %v", err)
	}
	if len(gSlug.byPath["z_target.go"]) == 0 || len(gSlug.byPath["z_caller.go"]) == 0 {
		t.Fatalf("compound slug missed target/caller; paths=%v", mapKeysSlice(gSlug.byPath))
	}
}

func mapKeysSlice[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGitWarmSigSupportsReftable(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "service.go", hookSampleServiceSrc)
	commitAll(t, dir, "init reftable fixture")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	gitDir := filepath.Join(dir, ".git")
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rtDir := filepath.Join(gitDir, "reftable")
	if err := os.MkdirAll(rtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	listPath := filepath.Join(rtDir, "tables.list")
	if err := os.WriteFile(listPath, []byte("0x000000000001-0x000000000002-11111111.ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig1, ok := computeGitWarmSignature(dir)
	if !ok || sig1 == "" {
		t.Fatalf("computeGitWarmSignature on reftable = (%q, %v), want non-empty, true", sig1, ok)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	st.saveGitWarmSig(dir)
	if !st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatal("matchesGitWarmSig = false after saving reftable warm sig")
	}
	if err := os.WriteFile(listPath, []byte("0x000000000001-0x000000000003-22222222.ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig2, ok := computeGitWarmSignature(dir)
	if !ok || sig2 == sig1 {
		t.Fatalf("reftable mutation gave (%q, %v), want different sig from %q", sig2, ok, sig1)
	}
	if st.matchesGitWarmSig(context.Background(), dir) {
		t.Fatal("matchesGitWarmSig stayed true after tables.list change")
	}
	if err := os.Remove(listPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "service.go"), listPath); err != nil {
		t.Fatal(err)
	}
	if _, ok := computeGitWarmSignature(dir); ok {
		t.Fatal("symlink tables.list succeeded, want false")
	}
	if err := os.Remove(listPath); err != nil {
		t.Fatal(err)
	}
	if _, ok := computeGitWarmSignature(dir); ok {
		t.Fatal("missing tables.list succeeded, want false")
	}
}

func TestBoundedSymGraphQueryPlansUseIndexes(t *testing.T) {
	expanded := expandBoundedSymbolTerms([]string{"internal/search/reconcile.go"})
	for _, forbidden := range []string{"internal", "search", "internal/search/reconcile.go"} {
		for _, tok := range expanded {
			if tok == forbidden {
				t.Fatalf("expandBoundedSymbolTerms included %q in %v", forbidden, expanded)
			}
		}
	}
	if !sliceContainsString(expanded, "reconcile") {
		t.Fatalf("expandBoundedSymbolTerms missing basename stem reconcile in %v", expanded)
	}
	dir := newRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "internal/sub/sample.go", "package sub\n\nfunc MyCustomReconcileHelper() int { return HelperLeaf() }\nfunc HelperLeaf() int { return 7 }\n")
	commitAll(t, dir, "bounded sql fixture")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	assertQueryPlanUses(t, st, "SELECT f.project, f.path, sy.oid FROM symbols sy CROSS JOIN files f ON f.oid = sy.oid WHERE f.project IN (?) AND sy.oid IN (?)", []any{dir, "x"}, "symbols_oid", "files_oid")
	assertQueryPlanUses(t, st, "SELECT f.path, r.oid FROM sym_refs r CROSS JOIN files f ON f.oid = r.oid WHERE f.project IN (?) AND r.oid IN (?)", []any{dir, "x"}, "sym_refs_oid", "files_oid")
	assertQueryPlanUses(t, st, "SELECT f.path, r.oid FROM sym_refs r CROSS JOIN files f ON f.oid = r.oid WHERE f.project IN (?) AND r.to_name IN (?) ORDER BY f.path, r.line LIMIT 400", []any{dir, "x"}, "sym_refs_to_name", "files_oid")
	assertQueryPlanUses(t, st, "SELECT DISTINCT f.path, sy.oid FROM symbols sy CROSS JOIN files f ON f.oid = sy.oid WHERE f.project IN (?) AND sy.name IN (?)", []any{dir, "x"}, "symbols_name", "files_oid")
	assertQueryPlanSingle(t, st, "SELECT DISTINCT name FROM symbols", "COVERING INDEX symbols_name")
	assertFTSFallbackQueryPlan(t, st, dir)
	sc := &scope{st: st, projects: []string{dir}}
	g, err := sc.loadBoundedSymGraph("customreconcile", nil, false, 1)
	if err != nil {
		t.Fatalf("loadBoundedSymGraph fallback: %v", err)
	}
	if len(g.byName["MyCustomReconcileHelper"]) == 0 {
		t.Fatalf("lowercase substring fallback missed MyCustomReconcileHelper: syms=%d", len(g.syms))
	}
}

func explainQueryPlan(t *testing.T, st *store, query string, args ...any) []string {
	t.Helper()
	rows, err := st.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN %s: %v", query, err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	return lines
}

func assertQueryPlanUses(t *testing.T, st *store, query string, args []any, wantFirst, wantSecond string) {
	t.Helper()
	lines := explainQueryPlan(t, st, query, args...)
	if len(lines) < 2 || !strings.Contains(lines[0], wantFirst) || !strings.Contains(lines[1], wantSecond) {
		t.Fatalf("query plan = %v, want outer %q then %q", lines, wantFirst, wantSecond)
	}
}

func assertQueryPlanSingle(t *testing.T, st *store, query, want string) {
	t.Helper()
	lines := explainQueryPlan(t, st, query)
	if len(lines) < 1 || !strings.Contains(lines[0], want) {
		t.Fatalf("query plan = %v, want %q in first row", lines, want)
	}
}

func assertFTSFallbackQueryPlan(t *testing.T, st *store, dir string) {
	t.Helper()
	q := `WITH m AS MATERIALIZED (
			SELECT rowid AS id, bm25(chunks_fts, 5.0, 1.0, 1.0) AS score
			FROM chunks_fts WHERE chunks_fts MATCH ?
			ORDER BY score LIMIT ?
		)
		SELECT f.path, c.line, c.heading, c.body
		FROM m
		JOIN chunks c ON c.id = m.id
		JOIN files f ON f.oid = c.oid
		WHERE f.project IN (?)
		ORDER BY m.score`
	lines := explainQueryPlan(t, st, q, `"sample"`, ftsFallbackLimit*8, dir)
	wantOrder := []string{"MATERIALIZE m", "SCAN chunks_fts", "SCAN m", "SEARCH c", "SEARCH f"}
	wi := 0
	for _, line := range lines {
		if wi < len(wantOrder) && strings.Contains(line, wantOrder[wi]) {
			wi++
		}
	}
	if wi != len(wantOrder) {
		t.Fatalf("fts fallback query plan = %#v, want ordered substrings %v (matched %d/%d)", lines, wantOrder, wi, len(wantOrder))
	}
}

func sliceContainsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestBoundedSymGraphHop1ScopesToPrioritySymbol(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "target.go", "package sample\n\nfunc PriorityTargetFunc() int { return TargetCallee() }\nfunc NonPriorityHelper() { CloseAll() }\n")
	writeFile(t, dir, "callee.go", "package sample\n\nfunc TargetCallee() int { return 42 }\n")
	writeFile(t, dir, "caller.go", "package sample\n\nfunc PriorityCaller() int { return PriorityTargetFunc() }\n")
	writeFile(t, dir, "hub.go", "package sample\n\nfunc CloseAll() {}\n")
	for i := 1; i <= 5; i++ {
		writeFile(t, dir, fmt.Sprintf("noise%d.go", i), fmt.Sprintf("package sample\n\nfunc Noise%d() { CloseAll() }\n", i))
	}
	commitAll(t, dir, "priority hop1 scope fixture")
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	sc := &scope{st: st, projects: []string{dir}}
	g, err := sc.loadBoundedSymGraph("Modify PriorityTargetFunc in target.go", nil, false, 1)
	if err != nil {
		t.Fatalf("loadBoundedSymGraph: %v", err)
	}
	for _, want := range []string{"target.go", "callee.go", "caller.go"} {
		if len(g.byPath[want]) == 0 {
			t.Errorf("hop1 graph missing %s; got %v", want, mapKeysSlice(g.byPath))
		}
	}
	for _, unwanted := range []string{"hub.go", "noise1.go", "noise2.go", "noise3.go", "noise4.go", "noise5.go"} {
		if len(g.byPath[unwanted]) > 0 {
			t.Errorf("hop1 graph leaked unrelated CloseAll file %s; got %v", unwanted, mapKeysSlice(g.byPath))
		}
	}
}

func TestExtractFTSFallbackWordsStripsPathDirs(t *testing.T) {
	words := extractFTSFallbackWords("Update ReconcileProjectIndex in internal/search/reconcile.go")
	for _, want := range []string{"reconcileprojectindex", "reconcile"} {
		if !sliceContainsString(words, want) {
			t.Fatalf("extractFTSFallbackWords missing %q in %v", want, words)
		}
	}
	for _, forbidden := range []string{"internal", "search"} {
		if sliceContainsString(words, forbidden) {
			t.Fatalf("extractFTSFallbackWords included path dir %q in %v", forbidden, words)
		}
	}
}
