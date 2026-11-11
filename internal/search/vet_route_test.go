package search

import (
	"bytes"
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestVetRoutesIntentToFeature(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		intent IntentMode
		skills []SkillMeta
		want   string
	}{
		{
			name:   "approval returns empty",
			prompt: "go all approved",
			intent: IntentApproval,
			want:   "",
		},
		{
			name:   "trivial returns empty",
			prompt: "Fix typo in README.md line 3",
			intent: IntentTrivial,
			want:   "",
		},
		{
			name:   "correction returns empty",
			prompt: "No, always run go test -race",
			intent: IntentCorrection,
			want:   "",
		},
		{
			name:   "worker kickoff does not self-route to fyr-work",
			prompt: "Project: this clone, the current directory; never another checkout. Read AGENTS.md; do Phase 4.\nFiles: internal/search/\nStanding approvals (closed set): tests and commit.\nNever: push; run fyr work;",
			intent: IntentTask,
			want:   "",
		},
		{
			name:   "single-phase feature routes to fyr-feature",
			prompt: "Add an OAuth2 token endpoint in internal/auth/token.go returning TokenResponse and verify with go test",
			intent: IntentFeature,
			want:   "/fyr-feature (pre-build interview first)",
		},
		{
			name:   "feature with constraint semicolon stays single-phase fyr-feature",
			prompt: "Add an OAuth2 token endpoint in internal/auth/token.go returning TokenResponse; do not ask me anything",
			intent: IntentFeature,
			want:   "/fyr-feature (pre-build interview first)",
		},
		{
			name:   "feature with semicolon tasks routes to fyr-work waves",
			prompt: "Implement OAuth2 login in internal/auth/login.go; implement webhook sink in internal/audit/sink.go",
			intent: IntentFeature,
			want:   "/fyr-work (waves with model:, judge:, base-check:)",
		},
		{
			name:   "feature with numbered tasks routes to fyr-work waves",
			prompt: "Build two components: 1. Add OAuth2 login in internal/auth/login.go 2. Add audit sink in internal/audit/sink.go",
			intent: IntentFeature,
			want:   "/fyr-work (waves with model:, judge:, base-check:)",
		},
		{
			name:   "feature with waves cue routes to fyr-work waves",
			prompt: "Implement Phase 1 and Phase 2 in waves across internal/auth and internal/audit",
			intent: IntentFeature,
			want:   "/fyr-work (waves with model:, judge:, base-check:)",
		},
		{
			name:   "feature with parallel cue routes to fyr-work waves",
			prompt: "Design and build 3 phases in parallel across internal/auth, internal/api, and internal/store",
			intent: IntentFeature,
			want:   "/fyr-work (waves with model:, judge:, base-check:)",
		},
		{
			name:   "task with two independent semicolon tasks routes to fyr-work task list",
			prompt: "Add TestHelloTab to internal/greet/greet_test.go; add Shout to internal/shout/shout.go",
			intent: IntentTask,
			want:   "/fyr-work <task>; <task>",
		},
		{
			name:   "task with numbered list routes to fyr-work task list",
			prompt: "1) Update flag parsing in cmd/a/main.go 2) Update config loader in internal/cfg/load.go",
			intent: IntentTask,
			want:   "/fyr-work <task>; <task>",
		},
		{
			name:   "bug routes to fyr-bug",
			prompt: "Fix panic in ReconcileProjectIndex in internal/search/service.go when root is empty",
			intent: IntentBug,
			want:   "/fyr-bug (reproduce with failing test first)",
		},
		{
			name:   "review routes to cross-family fyr-review judge",
			prompt: "Review internal/search/vet.go for regressions and edge cases",
			intent: IntentReview,
			want:   "/fyr-review --judge (cross-family)",
		},
		{
			name:   "release routes to fyr-preflight",
			prompt: "Ship it and cut v8.6.0",
			intent: IntentRelease,
			want:   "/fyr-preflight",
		},
		{
			name:   "question routes to fyr search --slice",
			prompt: "How does ReconcileProjectIndex work?",
			intent: IntentQuestion,
			want:   "fyr search --slice",
		},
		{
			name:   "lookup task where-is routes to fyr search --slice",
			prompt: "where is ReconcileProjectIndex defined in internal/search",
			intent: IntentTask,
			want:   "fyr search --slice",
		},
		{
			name:   "lookup task find callers and references routes to fyr search --slice",
			prompt: "Find all callers and references of VetPrompt across internal/search",
			intent: IntentTask,
			want:   "fyr search --slice",
		},
		{
			name:   "lookup task trace routes to fyr search --slice",
			prompt: "Trace the call chain from Search to buildHookEnvelope",
			intent: IntentTask,
			want:   "fyr search --slice",
		},
		{
			name:   "triage skill routes to fyr-triage kickoffs",
			prompt: "Triage open GitHub issues and backlog items",
			intent: IntentTask,
			skills: []SkillMeta{{Name: "fyr-triage"}},
			want:   "/fyr-triage → /fyr-work Kickoffs",
		},
		{
			name:   "docs skill routes to fyr-docs and fyr check",
			prompt: "Audit README.md and SDD.md documentation structure",
			intent: IntentTask,
			skills: []SkillMeta{{Name: "fyr-docs"}},
			want:   "fyr-docs + fyr check",
		},
		{
			name:   "handoff skill routes to fyr-handoff",
			prompt: "Wrap up for next session and write the handoff",
			intent: IntentTask,
			skills: []SkillMeta{{Name: "fyr-handoff"}},
			want:   "/fyr-handoff (commits, pushes, fyr metrics ingest once)",
		},
	}
	for _, tc := range cases {
		if got := RouteFeature(tc.prompt, tc.intent, tc.skills); got != tc.want {
			t.Errorf("%s: RouteFeature(%q, %q) = %q, want %q", tc.name, tc.prompt, tc.intent, got, tc.want)
		}
	}

	slice := SliceResult{
		Confident:  true,
		Pivots:     1,
		SymbolHits: 2,
		Files:      []string{"internal/search/service.go"},
		Capsule:    "[Hop 0] internal/search/service.go:3-5 (ReconcileProjectIndex)",
	}

	// Verify VetPrompt emits advisory `route:` inside [VET_IMPROVED_PROMPT] across routed intents without blocking Gate.
	vetChecks := []struct {
		name       string
		prompt     string
		wantGate   VetGate
		wantRoute  string
		inContract bool
	}{
		{
			name:       "anchored bug",
			prompt:     "Fix panic in ReconcileProjectIndex in internal/search/service.go:19 when root is empty and verify with go test",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: /fyr-bug (reproduce with failing test first)",
			inContract: true,
		},
		{
			name:       "anchored multi-phase feature",
			prompt:     "Implement Phase 1 caching in internal/search/service.go returning CacheHit; implement Phase 2 metrics in internal/metrics/store.go returning Count, and verify with go test",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: /fyr-work (waves with model:, judge:, base-check:)",
			inContract: true,
		},
		{
			name:       "anchored single-phase feature",
			prompt:     "Implement CacheIndex in internal/search/service.go returning CacheStats and verify with go test",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: /fyr-feature (pre-build interview first)",
			inContract: true,
		},
		{
			name:       "two independent tasks",
			prompt:     "Update internal/search/service.go; update internal/search/vet.go",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: /fyr-work <task>; <task>",
			inContract: true,
		},
		{
			name:       "review prompt",
			prompt:     "Review internal/search/service.go for race conditions",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: /fyr-review --judge (cross-family)",
			inContract: true,
		},
		{
			name:       "release prompt",
			prompt:     "Ship it and cut v8.6.0",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: /fyr-preflight",
			inContract: true,
		},
		{
			name:       "lookup task prompt",
			prompt:     "Find callers and references of ReconcileProjectIndex in internal/search/service.go",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: fyr search --slice",
			inContract: true,
		},
		{
			name:       "question prompt stays ANSWER_ONLY in contract with route in ImprovedPrompt",
			prompt:     "Where is ReconcileProjectIndex defined?",
			wantGate:   GateEnrichAndProceed,
			wantRoute:  "route: fyr search --slice",
			inContract: false,
		},
	}
	for _, vc := range vetChecks {
		res := VetPrompt(vc.prompt, "", slice, TurnBrainContext{})
		if res.Gate != vc.wantGate {
			t.Errorf("%s: Gate = %s, want %s", vc.name, res.Gate, vc.wantGate)
		}
		if !strings.Contains(res.ImprovedPrompt, "[VET_IMPROVED_PROMPT]") || !strings.Contains(res.ImprovedPrompt, vc.wantRoute) {
			t.Errorf("%s: ImprovedPrompt missing %q:\n%s", vc.name, vc.wantRoute, res.ImprovedPrompt)
		}
		if vc.inContract && !strings.Contains(res.TurnContract, vc.wantRoute) {
			t.Errorf("%s: TurnContract missing %q:\n%s", vc.name, vc.wantRoute, res.TurnContract)
		}
	}

	// Pass-through prompts never synthesize [VET_IMPROVED_PROMPT] or block.
	for _, pt := range []string{"go all approved", "Fix typo in README.md", "!Update internal/search/service.go"} {
		res := VetPrompt(pt, "", slice, TurnBrainContext{})
		if res.Gate != GatePassThrough || res.ImprovedPrompt != "" {
			t.Errorf("pass-through(%q): got gate=%s improved=%q, want PASS_THROUGH and empty ImprovedPrompt", pt, res.Gate, res.ImprovedPrompt)
		}
	}

	// Latency check: VetPrompt with RouteFeature stays well within the < 25 ms budget.
	const benchPrompt = "Implement Phase 1 in internal/search/service.go returning Stats; implement Phase 2 in internal/search/vet.go returning Route, and verify with go test"
	_ = VetPrompt(benchPrompt, "", slice, TurnBrainContext{})
	runtime.GC()
	samples := make([]time.Duration, 21)
	for i := range samples {
		t0 := time.Now()
		_ = VetPrompt(benchPrompt, "", slice, TurnBrainContext{})
		samples[i] = time.Since(t0)
	}
	slices.Sort(samples)
	if median := samples[len(samples)/2]; median >= 25*time.Millisecond {
		t.Errorf("VetPrompt median latency = %v (all: %v), want < 25ms", median, samples)
	}

	// Live pre-invocation hook emits [VET_IMPROVED_PROMPT] with advisory route: line.
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	inJSON, _ := json.Marshal(map[string]any{
		"conversationId": "conv-vet-route",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Fix ReconcileProjectIndex in service.go to handle empty root and run go test",
	})
	var hookOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(inJSON)}, &hookOut); err != nil {
		t.Fatalf("Search pre-invocation: %v", err)
	}
	var hookResp PreInvocationOutput
	if err := json.Unmarshal(hookOut.Bytes(), &hookResp); err != nil {
		t.Fatalf("unmarshal hook output: %v", err)
	}
	sysMsg := hookResp.InjectSteps[0].SystemMessage.SystemMessage
	for _, want := range []string{"[VET_IMPROVED_PROMPT]", "route: /fyr-bug (reproduce with failing test first)"} {
		if !strings.Contains(sysMsg, want) {
			t.Errorf("pre-invocation systemMessage missing %q:\n%s", want, sysMsg)
		}
	}
}
