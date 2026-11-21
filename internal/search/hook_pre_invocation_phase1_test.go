package search

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
		{"reviewer", "conv-sub-reviewer", map[string]any{"agentName": "reviewer"}, "reviewer"},
		{"security", "conv-sub-security", map[string]any{"agentName": "security"}, "security"},
		{"verifier", "conv-sub-verifier", map[string]any{"agentName": "verifier"}, "verifier"},
		{"explorer", "conv-sub-explorer", map[string]any{"agentName": "explorer"}, "explorer"},
		{"parent_conv_child", "conv-sub-child", map[string]any{"parentConversationId": "parent-conv-1"}, "self"},
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
