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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestPreInvocationParallelSliceAndSubDeadlineDegradation(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	t.Run("parallel_under_race", func(t *testing.T) {
		sliceEntered := make(chan struct{})
		brainEntered := make(chan struct{})
		var sliceOnce, brainOnce sync.Once
		var overlapped atomic.Bool

		inJSON, _ := json.Marshal(map[string]any{
			"conversationId": "conv-p1-parallel",
			"workspacePaths": []string{dir},
			"invocationNum":  0,
			"lastUserInput":  "Investigate ReconcileProjectIndex failure in service.go",
		})
		var out bytes.Buffer
		err := Search(Options{
			Dir:    dir,
			Home:   home,
			Hook:   "pre-invocation",
			HookIn: bytes.NewReader(inJSON),
			BrainRefsForDir: func(d, target string) []LinkedRecord {
				sliceOnce.Do(func() {
					close(sliceEntered)
					select {
					case <-brainEntered:
						overlapped.Store(true)
					case <-time.After(150 * time.Millisecond):
					}
				})
				return nil
			},
			BrainTurnContext: func(d, h, prompt string, files []string) TurnBrainContext {
				brainOnce.Do(func() {
					close(brainEntered)
					select {
					case <-sliceEntered:
						overlapped.Store(true)
					case <-time.After(150 * time.Millisecond):
					}
				})
				return TurnBrainContext{
					ADRs: []MatchedRecord{{ID: "ADR-532", Kind: "adr", Status: "accepted", Title: "PreInvocation latency budget"}},
				}
			},
		}, &out)
		if err != nil {
			t.Fatalf("Search pre-invocation: %v", err)
		}
		if !overlapped.Load() {
			t.Errorf("expected buildVetSlice and BrainTurnContext to overlap concurrently")
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
			t.Fatalf("expected 1 injectStep, got %+v", resp)
		}
		sysMsg := resp.InjectSteps[0].SystemMessage.SystemMessage
		if !strings.Contains(sysMsg, "[Hop 0] service.go:3-5 (ReconcileProjectIndex)") || !strings.Contains(sysMsg, "ADR-532") {
			t.Errorf("expected both Hop 0 capsule and ADR-532 in systemMessage:\n%s", sysMsg)
		}
		st, _, err := openLedgerStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		row, err := st.lastVetHistory("conv-p1-parallel", 0)
		st.close()
		if err != nil || row == nil || row.TimedOut {
			t.Fatalf("vet_history = %+v (err=%v), want TimedOut=false", row, err)
		}
	})

	t.Run("slice_sub_deadline_degradation", func(t *testing.T) {
		if got := hookSliceDeadline(context.Background(), Options{hookDeadline: 200 * time.Millisecond}); got > 120*time.Millisecond {
			t.Fatalf("hookSliceDeadline(200ms) = %v, want <= 120ms", got)
		}
		releaseSlice := make(chan struct{})
		sliceDone := make(chan struct{})
		var enteredOnce sync.Once
		t.Cleanup(func() {
			select {
			case <-releaseSlice:
			default:
				close(releaseSlice)
			}
		})

		inJSON, _ := json.Marshal(map[string]any{
			"conversationId": "conv-p1-subdeadline",
			"workspacePaths": []string{dir},
			"invocationNum":  0,
			"lastUserInput":  "Investigate ReconcileProjectIndex failure in service.go",
		})
		var out bytes.Buffer
		err := Search(Options{
			Dir:          dir,
			Home:         home,
			Hook:         "pre-invocation",
			HookIn:       bytes.NewReader(inJSON),
			hookDeadline: 200 * time.Millisecond,
			BrainRefsForDir: func(d, target string) []LinkedRecord {
				enteredOnce.Do(func() {
					defer close(sliceDone)
					<-releaseSlice
				})
				return nil
			},
			BrainTurnContext: func(d, h, prompt string, files []string) TurnBrainContext {
				return TurnBrainContext{
					ADRs: []MatchedRecord{{ID: "ADR-532", Kind: "adr", Status: "accepted", Title: "Graceful slice degradation"}},
				}
			},
		}, &out)
		close(releaseSlice)
		select {
		case <-sliceDone:
		case <-time.After(time.Second):
		}
		if err != nil {
			t.Fatalf("Search degraded pre-invocation: %v", err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
			t.Fatalf("expected valid contract after slice sub-deadline expiry, got %+v", resp)
		}
		sysMsg := resp.InjectSteps[0].SystemMessage.SystemMessage
		if !strings.Contains(sysMsg, "[FYR_TURN_CONTRACT]") || !strings.Contains(sysMsg, "ADR-532") {
			t.Errorf("degraded contract missing [FYR_TURN_CONTRACT] or ADR-532:\n%s", sysMsg)
		}
		st, _, err := openLedgerStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		row, err := st.lastVetHistory("conv-p1-subdeadline", 0)
		st.close()
		if err != nil || row == nil || row.TimedOut || row.InjectedBytes <= 0 {
			t.Fatalf("degraded vet_history = %+v (err=%v), want TimedOut=false InjectedBytes>0", row, err)
		}
	})
}

func TestPreInvocationLightweightSubagentFastPath(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	multiPkgPrompt := "1. Update parser in internal/search/vet.go; 2. Update store in internal/brain/store.go"
	cases := []struct {
		name     string
		convID   string
		extra    map[string]any
		wantRole string
	}{
		{"reviewer", "conv-sub-reviewer", map[string]any{"agentName": "reviewer"}, "reviewer (budget 0/40)"},
		{"security", "conv-sub-security", map[string]any{"agentName": "security"}, "security (budget 0/40)"},
		{"verifier", "conv-sub-verifier", map[string]any{"agentName": "verifier"}, "verifier (budget 0/40)"},
		{"explorer", "conv-sub-explorer", map[string]any{"agentName": "explorer"}, "explorer (budget 0/40)"},
		{"unknown_audit_role", "conv-sub-custom", map[string]any{"agentName": "custom-audit"}, "custom-audit (budget 0/40)"},
		{"builder_child", "conv-sub-builder", map[string]any{"agentName": "builder-p7", "parentConversationId": "parent-conv-1"}, "builder-p7 (budget 0/120)"},
		{"parent_conv_child", "conv-sub-child", map[string]any{"parentConversationId": "parent-conv-1"}, "self (budget 0/120)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{
				"conversationId": tc.convID,
				"workspacePaths": []string{dir},
				"invocationNum":  0,
				"lastUserInput":  multiPkgPrompt,
			}
			for k, v := range tc.extra {
				payload[k] = v
			}
			inJSON, _ := json.Marshal(payload)
			var out bytes.Buffer
			err := Search(Options{
				Dir:    dir,
				Home:   home,
				Hook:   "pre-invocation",
				HookIn: bytes.NewReader(inJSON),
				BrainTurnContext: func(d, h, prompt string, files []string) TurnBrainContext {
					t.Errorf("%s: BrainTurnContext should be skipped on lightweight subagent fast path", tc.name)
					return TurnBrainContext{}
				},
				SkillCatalog: func(h string) []SkillMeta {
					t.Errorf("%s: SkillCatalog should be skipped on lightweight subagent fast path", tc.name)
					return nil
				},
			}, &out)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			var resp PreInvocationOutput
			if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
				t.Fatalf("expected 1 injectStep, got %+v", resp)
			}
			sysMsg := resp.InjectSteps[0].SystemMessage.SystemMessage
			if strings.Contains(sysMsg, "COORDINATE_SUBTASKS") {
				t.Errorf("subagent fast path must skip DecomposeSubtasks / COORDINATE_SUBTASKS:\n%s", sysMsg)
			}
			wantNotice := "subagent fast-path: " + tc.wantRole
			if !strings.Contains(sysMsg, wantNotice) {
				t.Errorf("expected notice %q in systemMessage:\n%s", wantNotice, sysMsg)
			}
			st, _, err := openLedgerStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			row, err := st.lastVetHistory(tc.convID, 0)
			st.close()
			if err != nil || row == nil || row.TimedOut || row.InjectedBytes <= 0 {
				t.Fatalf("subagent telemetry vet_history = %+v (err=%v), want TimedOut=false InjectedBytes>0", row, err)
			}
		})
	}

	// Safety conflict preservation on lightweight subagent fast path.
	safetyPayload, _ := json.Marshal(map[string]any{
		"conversationId": "conv-sub-safety",
		"workspacePaths": []string{dir},
		"invocationNum":  0,
		"agentName":      "reviewer",
		"lastUserInput":  "Implement a background daemon and mcp server for internal/search/vet.go",
	})
	var safetyOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(safetyPayload)}, &safetyOut); err != nil {
		t.Fatalf("safety subagent Search: %v", err)
	}
	var safetyResp PreInvocationOutput
	if err := json.Unmarshal(safetyOut.Bytes(), &safetyResp); err != nil {
		t.Fatalf("unmarshal safety: %v", err)
	}
	parsedSafety := parseStructuredBanner(t, safetyResp.InjectSteps[0].SystemMessage.SystemMessage)
	if parsedSafety.Gate != string(GateVetFirst) {
		t.Errorf("subagent safety conflict gate = %s, want VET_FIRST", parsedSafety.Gate)
	}

	// Watchdog budget preservation on lightweight subagent fast path.
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE session_meta SET tool_calls = 40 WHERE conversation_id = 'conv-sub-reviewer'`); err != nil {
		st.close()
		t.Fatal(err)
	}
	st.close()
	budgetPayload, _ := json.Marshal(map[string]any{
		"conversationId": "conv-sub-reviewer",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"agentName":      "reviewer",
		"lastUserInput":  "Finish checking service.go",
	})
	var budgetOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(budgetPayload)}, &budgetOut); err != nil {
		t.Fatalf("budget subagent Search: %v", err)
	}
	var budgetResp PreInvocationOutput
	if err := json.Unmarshal(budgetOut.Bytes(), &budgetResp); err != nil {
		t.Fatalf("unmarshal budget: %v", err)
	}
	budgetMsg := budgetResp.InjectSteps[0].SystemMessage.SystemMessage
	if !strings.Contains(budgetMsg, "[SUBAGENT_BUDGET]") || !strings.Contains(budgetMsg, "40/40") {
		t.Errorf("expected [SUBAGENT_BUDGET] 40/40 warning in systemMessage:\n%s", budgetMsg)
	}
}

func TestPreInvocationTimeoutFallbackBreaksMidTurnSpiral(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	const convID = "conv-p1-timeout-spiral"
	prompt1 := "Investigate ReconcileProjectIndex failure in service.go"
	releaseHung := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseHung:
		default:
			close(releaseHung)
		}
	})

	// 1. Turn 1 inv 0 times out BEFORE checkTurnOrMidTurnWithStore; fallback must stamp
	//    session_meta + __pre_invocation__ prompt hash and record TimedOut=true.
	t1Inv0, _ := json.Marshal(map[string]any{
		"conversationId":  convID,
		"workspacePaths":  []string{dir},
		"invocationNum":   0,
		"initialNumSteps": 0,
		"stepIdx":         0,
		"lastUserInput":   prompt1,
	})
	var out0 bytes.Buffer
	err := Search(Options{
		Dir:          dir,
		Home:         home,
		Hook:         "pre-invocation",
		HookIn:       bytes.NewReader(t1Inv0),
		hookDeadline: 40 * time.Millisecond,
		hookDispatch: func(ctx context.Context, o Options, event, d string, in HookInput, w io.Writer) error {
			select {
			case <-releaseHung:
			case <-time.After(300 * time.Millisecond):
			}
			return nil
		},
	}, &out0)
	close(releaseHung)
	if err != nil {
		t.Fatalf("turn 1 inv 0: %v", err)
	}
	var resp0 PreInvocationOutput
	if err := json.Unmarshal(out0.Bytes(), &resp0); err != nil {
		t.Fatalf("unmarshal inv 0: %v", err)
	}
	if len(resp0.InjectSteps) != 0 {
		t.Fatalf("timed-out inv 0 expected empty fallback {}, got %+v", resp0)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	row0, err := st.lastVetHistory(convID, 0)
	var stampedSum string
	_ = st.db.QueryRow(`SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND resolved_path = ? AND start_line = -3`, convID, preInvocationSentinel).Scan(&stampedSum)
	st.close()
	if err != nil || row0 == nil || !row0.TimedOut {
		t.Fatalf("inv 0 vet_history = %+v (err=%v), want TimedOut=true", row0, err)
	}
	if stampedSum != sha256PromptHex(prompt1) {
		t.Fatalf("timeout fallback stamp = %q, want sha256(%q)=%q", stampedSum, prompt1, sha256PromptHex(prompt1))
	}

	// 2. Turn 1 inv 1 mid-turn continuation with SAME prompt skips immediately ({}) without running heavy work.
	t1Inv1, _ := json.Marshal(map[string]any{
		"conversationId":     convID,
		"workspacePaths":     []string{dir},
		"invocationNum":      1,
		"initialNumSteps":    0,
		"stepIdx":            2,
		"continuationActive": true,
		"lastUserInput":      prompt1,
	})
	var out1 bytes.Buffer
	if err := Search(Options{
		Dir:    dir,
		Home:   home,
		Hook:   "pre-invocation",
		HookIn: bytes.NewReader(t1Inv1),
		BrainTurnContext: func(d, h, prompt string, files []string) TurnBrainContext {
			t.Errorf("mid-turn continuation after timeout fallback must skip BrainTurnContext")
			return TurnBrainContext{}
		},
	}, &out1); err != nil {
		t.Fatalf("turn 1 inv 1: %v", err)
	}
	var resp1 PreInvocationOutput
	if err := json.Unmarshal(out1.Bytes(), &resp1); err != nil {
		t.Fatalf("unmarshal inv 1: %v", err)
	}
	if len(resp1.InjectSteps) != 0 {
		t.Fatalf("mid-turn continuation expected {}, got %+v", resp1)
	}

	// 3. Turn 2 with a new prompt runs in full and emits a valid contract with TimedOut=false.
	t2Inv2, _ := json.Marshal(map[string]any{
		"conversationId":     convID,
		"workspacePaths":     []string{dir},
		"invocationNum":      2,
		"initialNumSteps":    4,
		"stepIdx":            4,
		"executionNum":       0,
		"continuationActive": false,
		"lastUserInput":      "Fix AlphaHelper error handling in service.go",
	})
	var out2 bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(t2Inv2)}, &out2); err != nil {
		t.Fatalf("turn 2 inv 2: %v", err)
	}
	var resp2 PreInvocationOutput
	if err := json.Unmarshal(out2.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal inv 2: %v", err)
	}
	if len(resp2.InjectSteps) != 1 || resp2.InjectSteps[0].SystemMessage == nil ||
		!strings.Contains(resp2.InjectSteps[0].SystemMessage.SystemMessage, "[FYR_TURN_CONTRACT]") {
		t.Fatalf("turn 2 expected full [FYR_TURN_CONTRACT], got %+v", resp2)
	}
	st2, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, turnCount := st2.currentGeneration(convID)
	row2, err := st2.lastVetHistory(convID, 0)
	st2.close()
	if turnCount != 2 {
		t.Errorf("after turn 2, turn_count = %d, want 2", turnCount)
	}
	if err != nil || row2 == nil || row2.TimedOut || row2.TurnNum != 2 {
		t.Errorf("turn 2 vet_history = %+v (err=%v), want TurnNum=2 TimedOut=false", row2, err)
	}
}

func TestBoundedSymGraphLikeSQLPushdownUsesCoveringIndex(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "helpers.go", "package sample\n\nfunc alpha_beta_helper() {}\nfunc alphaXbeta_helper() {}\n")
	writeFile(t, dir, "GUIDE.md", "# Alpha Beta Guide\n\nDetails.\n")
	commitAll(t, dir, "init covering index test")
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	db := openDB(t, dir)
	defer db.Close()

	q, args := buildSymbolNameMatchSQL(false, []string{"io", "alpha_beta"}, []string{"alpha_beta"})
	if !strings.Contains(q, "lower(name) IN ") || !strings.Contains(q, `lower(name) LIKE ? ESCAPE '\'`) {
		t.Fatalf("buildSymbolNameMatchSQL missing expected SQL pushdown:\n%s", q)
	}
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var planLines []string
	usesCovering := false
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		planLines = append(planLines, detail)
		if strings.Contains(detail, "COVERING INDEX symbols_name") {
			usesCovering = true
		}
		if strings.Contains(detail, "USE TEMP B-TREE") {
			t.Errorf("unexpected temp B-tree in query plan: %s", detail)
		}
	}
	if !usesCovering {
		t.Errorf("expected COVERING INDEX symbols_name in query plan:\n%s", strings.Join(planLines, "\n"))
	}

	// Escaped '_' in LIKE must match literal alpha_beta_helper and NOT alphaXbeta_helper or markdown heading.
	matched, _, err := matchSymbolNamesFromIndexCtx(context.Background(), db, false, []string{"alpha_beta"}, []string{"alpha_beta"})
	if err != nil {
		t.Fatalf("matchSymbolNamesFromIndexCtx: %v", err)
	}
	if len(matched) != 1 || matched[0] != "alpha_beta_helper" {
		t.Errorf("matchSymbolNamesFromIndexCtx = %v, want [alpha_beta_helper]", matched)
	}
}

func TestV910PreInvocationLatencyDedupCompactionAndNoHooks(t *testing.T) {
	t.Run("TestV910BrainSubDeadlineAndTimeoutRecorder", runV910BrainSubDeadlineAndTimeoutT)
	t.Run("TestV910PreInvocationDirtyFastPath", runV910DirtyFastPathT)
	t.Run("TestV910MidTurn", runV910MidTurnT)
	t.Run("TestV910Compaction", runV910CompactionT)
	t.Run("TestV910NoHooksSymlinkAndRelay", runV910NoHooksSymlinkAndRelayT)
}

func runV910BrainSubDeadlineAndTimeoutT(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if got := hookBrainDeadline(context.Background(), Options{hookDeadline: 200 * time.Millisecond}); got > 50*time.Millisecond {
		t.Fatalf("hookBrainDeadline(200ms) = %v, want <= 50ms", got)
	}
	releaseBrain := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseBrain:
		default:
			close(releaseBrain)
		}
	})
	inJSON, _ := json.Marshal(map[string]any{
		"conversationId": "conv-v910-brain-dl", "workspacePaths": []string{dir},
		"invocationNum": 1, "lastUserInput": "Investigate ReconcileProjectIndex in service.go",
	})
	var out bytes.Buffer
	err := Search(Options{
		Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(inJSON),
		hookDeadline: 200 * time.Millisecond,
		BrainTurnContext: func(d, h, p string, files []string) TurnBrainContext {
			<-releaseBrain
			return TurnBrainContext{}
		},
	}, &out)
	close(releaseBrain)
	if err != nil {
		t.Fatalf("Search degraded brain: %v", err)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 {
		t.Fatalf("brain sub-deadline degradation expected valid contract, got %+v (err=%v)", resp, err)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.lastVetHistory("conv-v910-brain-dl", 0)
	st.close()
	if err != nil || row == nil || row.TimedOut {
		t.Fatalf("brain sub-deadline vet_history = %+v (err=%v), want TimedOut=false", row, err)
	}
}

func runV910DirtyFastPathT(t *testing.T) {
	if got := dynamicDirtyFastPathCap(context.Background(), 500); got != 8 {
		t.Fatalf("dynamicDirtyFastPathCap(500) = %d, want 8", got)
	}
	if got := dynamicDirtyFastPathCap(context.Background(), 2000); got != 3 {
		t.Fatalf("dynamicDirtyFastPathCap(2000) = %d, want 3", got)
	}
	shortCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if got := dynamicDirtyFastPathCap(shortCtx, 500); got != 0 {
		t.Fatalf("dynamicDirtyFastPathCap(<60ms) = %d, want 0", got)
	}

	dir, home := newRepo(t), t.TempDir()
	writeFile(t, dir, "service.go", hookSampleServiceSrc)
	for i := 0; i < 5000; i++ {
		sub := fmt.Sprintf("pkg%02d/f%04d.txt", i%50, i)
		writeFile(t, dir, sub, fmt.Sprintf("synthetic file %d\n", i))
	}
	commitAll(t, dir, "seed 5000 files")
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index 5000: %v", err)
	}

	// 2 dirty files (<= 3 cap): applyDirtyPaths updates only those 2 files without full gitView.
	writeFile(t, dir, "service.go", hookSampleServiceSrc+"\nfunc DirtyFastUpdated() error { return nil }\n")
	writeFile(t, dir, "untracked_v910.go", "package sample\nfunc UntrackedV910() {}\n")
	assertV910DirtyHookTurnT(t, dir, home, "conv-v910-dirty-2", "Inspect DirtyFastUpdated in service.go", "DirtyFastUpdated", false)

	// 12 dirty files (> 3 cap, <= 15): serves cached AST snapshot via fast path under 150ms.
	for i := 0; i < 12; i++ {
		writeFile(t, dir, fmt.Sprintf("burst_%02d.go", i), fmt.Sprintf("package sample\nfunc Burst%02d() {}\n", i))
	}
	assertV910DirtyHookTurnT(t, dir, home, "conv-v910-dirty-12", "Inspect ReconcileProjectIndex in service.go", "burst_00.go", true)
}

func assertV910DirtyHookTurnT(t *testing.T, dir, home, convID, prompt, wantSub string, wantFastPath bool) {
	t.Helper()
	inJSON, _ := json.Marshal(map[string]any{
		"conversationId": convID, "workspacePaths": []string{dir},
		"invocationNum": 1, "lastUserInput": prompt,
	})
	var out bytes.Buffer
	opts := Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(inJSON)}
	limit := 150 * time.Millisecond
	if defaultHookDeadline > 200*time.Millisecond {
		opts.hookDeadline = 5 * time.Second
		limit = 5 * time.Second
	}
	start := time.Now()
	if err := Search(opts, &out); err != nil {
		t.Fatalf("Search(%s): %v", convID, err)
	}
	elapsed := time.Since(start)
	if elapsed >= limit {
		t.Errorf("%s took %v, want < %v", convID, elapsed, limit)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 {
		t.Fatalf("%s invalid resp: %+v (err=%v)", convID, resp, err)
	}
	sys := resp.InjectSteps[0].SystemMessage.SystemMessage
	if !strings.Contains(sys, wantSub) {
		t.Errorf("%s missing %q in:\n%s", convID, wantSub, sys)
	}
	if wantFastPath && !strings.Contains(sys, "# fast-path:") {
		t.Errorf("%s expected # fast-path: in:\n%s", convID, sys)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.lastVetHistory(convID, 0)
	st.close()
	if err != nil || row == nil || row.TimedOut {
		t.Fatalf("%s vet_history = %+v (err=%v), want TimedOut=false", convID, row, err)
	}
}

func runV910MidTurnT(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	call := func(inv int, prompt string) PreInvocationOutput {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{
			"conversationId": "conv-v910-mid", "workspacePaths": []string{dir},
			"invocationNum": inv, "lastUserInput": prompt,
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(raw)}, &out); err != nil {
			t.Fatalf("inv=%d: %v", inv, err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	p1 := "Investigate ReconcileProjectIndex in service.go"
	if r := call(1, p1); len(r.InjectSteps) != 1 {
		t.Fatalf("inv=1 turn 1 expected inject, got %+v", r)
	}
	if r := call(2, p1); len(r.InjectSteps) != 0 {
		t.Fatalf("inv=2 same prompt expected mid-turn skip {}, got %+v", r)
	}
	if r := call(3, p1); len(r.InjectSteps) != 0 {
		t.Fatalf("inv=3 same prompt expected mid-turn skip {}, got %+v", r)
	}
	if r := call(1, p1); len(r.InjectSteps) != 1 {
		t.Fatalf("inv=1 new user turn with same prompt must NOT skip, got %+v", r)
	}
	if r := call(2, "Fix AlphaHelper error handling in service.go"); len(r.InjectSteps) != 1 {
		t.Fatalf("inv=2 with different prompt must NOT skip, got %+v", r)
	}
}

func runV910CompactionT(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	trDir := t.TempDir()
	tr := filepath.Join(trDir, "transcript.jsonl")
	pad := strings.Repeat("a", 200)
	if err := os.WriteFile(tr, []byte(`{"type":"user","text":"`+pad+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Symlinked transcript must be refused by scanTranscriptCheckpoint.
	symTr := filepath.Join(trDir, "sym.jsonl")
	if err := os.Symlink(tr, symTr); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, ok := scanTranscriptCheckpoint(symTr, 0); ok {
		t.Fatalf("scanTranscriptCheckpoint followed symlink")
	}

	reprimeCount := 0
	reprime := func(d, h string) string { reprimeCount++; return "# Re-primed V910" }
	call := func(inv int, prompt string) PreInvocationOutput {
		raw, _ := json.Marshal(map[string]any{
			"conversationId": "conv-v910-cp", "workspacePaths": []string{dir},
			"transcriptPath": tr, "invocationNum": inv, "lastUserInput": prompt,
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(raw), BrainReprime: reprime}, &out); err != nil {
			t.Fatal(err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	_ = call(1, "Check ReconcileProjectIndex in service.go")
	// False-positive text mentioning CHECKPOINT inside assistant line must NOT trigger compaction.
	f, err := os.OpenFile(tr, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"assistant","text":"detecting \"type\":\"CHECKPOINT\" safely"}` + "\n")
	_ = f.Close()
	_ = call(1, "Check AlphaHelper in service.go")
	if reprimeCount != 0 {
		t.Fatalf("assistant line mentioning CHECKPOINT falsely triggered reprime=%d", reprimeCount)
	}

	// Append large (> 512 KiB) jump ending with spaced CHECKPOINT + small post-checkpoint event.
	f, err = os.OpenFile(tr, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"assistant","text":"` + strings.Repeat("b", 600*1024) + `"}` + "\n")
	_, _ = f.WriteString(`{"type": "CHECKPOINT", "summary": "compacted"}` + "\n")
	_, _ = f.WriteString(`{"type":"user","text":"post-cp"}` + "\n")
	_ = f.Close()
	r := call(2, "Check AlphaHelper in service.go")
	if reprimeCount != 1 || len(r.InjectSteps) != 1 || !strings.Contains(r.InjectSteps[0].SystemMessage.SystemMessage, "Re-primed V910") {
		t.Fatalf("append-only CHECKPOINT expected reprime=1 and banner, got reprime=%d resp=%+v", reprimeCount, r)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var genID, estTokens, cpOff int
	err = st.db.QueryRow(`SELECT generation_id, est_tokens, last_checkpoint_offset FROM session_meta WHERE conversation_id = 'conv-v910-cp'`).Scan(&genID, &estTokens, &cpOff)
	st.close()
	if err != nil || genID != 1 || cpOff <= 0 || estTokens >= 1000 {
		t.Fatalf("post-checkpoint session_meta = (gen=%d est=%d cpOff=%d err=%v), want gen=1 cpOff>0 est<1000", genID, estTokens, cpOff, err)
	}
}

func runV910NoHooksSymlinkAndRelayT(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	realMarker := filepath.Join(t.TempDir(), "real-marker")
	if err := os.WriteFile(realMarker, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 1. Symlinked repo .agents/scratch/no-hooks is refused.
	scratchDir := filepath.Join(dir, ".agents", "scratch")
	if err := os.MkdirAll(scratchDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realMarker, filepath.Join(scratchDir, "no-hooks")); err != nil {
		t.Fatal(err)
	}
	if hooksDisabled(dir) {
		t.Fatalf("hooksDisabled accepted symlinked .agents/scratch/no-hooks")
	}
	// 2. Symlinked global no-hooks is refused.
	stateDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	globalNoHooks := filepath.Join(stateDir, "no-hooks")
	if err := os.Symlink(realMarker, globalNoHooks); err != nil {
		t.Fatal(err)
	}
	if hooksDisabledGlobal(home) {
		t.Fatalf("hooksDisabledGlobal accepted symlinked global no-hooks")
	}
	// 3. Regular global no-hooks CANNOT bypass FYR_RELAY=1 pre-tool-use guards.
	_ = os.Remove(globalNoHooks)
	if err := os.WriteFile(globalNoHooks, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hooksDisabledGlobal(home) {
		t.Fatalf("hooksDisabledGlobal rejected regular global no-hooks")
	}
	t.Setenv("FYR_RELAY", "1")
	pushReq, _ := json.Marshal(map[string]any{
		"conversationId": "conv-v910-relay", "workspacePaths": []string{dir},
		"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "git push origin main", "Cwd": dir}},
	})
	var out bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(pushReq)}, &out); err != nil {
		t.Fatal(err)
	}
	var dec HookDecisionOutput
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil || dec.Decision != "deny" || !strings.Contains(dec.Reason, "[RELAY_GUARD]") {
		t.Fatalf("global no-hooks bypassed FYR_RELAY=1 git push guard: %+v (err=%v)", dec, err)
	}
	askReq, _ := json.Marshal(map[string]any{
		"conversationId": "conv-v910-relay", "workspacePaths": []string{dir},
		"toolCall": map[string]any{"name": "ask_question", "args": map[string]any{"questions": []any{map[string]any{"question": "Proceed?"}}}},
	})
	out.Reset()
	if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(askReq)}, &out); err != nil {
		t.Fatal(err)
	}
	dec = HookDecisionOutput{}
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil || dec.Decision != "deny" || !strings.Contains(dec.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("global no-hooks bypassed FYR_RELAY=1 ask_question guard: %+v (err=%v)", dec, err)
	}
}
