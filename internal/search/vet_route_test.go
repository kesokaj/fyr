package search

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestVetRoutesIntentToFeature(t *testing.T) {
	t.Parallel()
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
			name:   "feature with semicolon tasks routes to fyr-feature",
			prompt: "Implement OAuth2 login in internal/auth/login.go; implement webhook sink in internal/audit/sink.go",
			intent: IntentFeature,
			want:   "/fyr-feature (pre-build interview first)",
		},
		{
			name:   "feature with numbered tasks routes to fyr-feature",
			prompt: "Build two components: 1. Add OAuth2 login in internal/auth/login.go 2. Add audit sink in internal/audit/sink.go",
			intent: IntentFeature,
			want:   "/fyr-feature (pre-build interview first)",
		},
		{
			name:   "feature with waves cue routes to fyr-feature",
			prompt: "Implement Phase 1 and Phase 2 in waves across internal/auth and internal/audit",
			intent: IntentFeature,
			want:   "/fyr-feature (pre-build interview first)",
		},
		{
			name:   "feature with parallel cue routes to fyr-feature",
			prompt: "Design and build 3 phases in parallel across internal/auth, internal/api, and internal/store",
			intent: IntentFeature,
			want:   "/fyr-feature (pre-build interview first)",
		},
		{
			name:   "auto kickoff does not self-route to fyr-swarm",
			prompt: "This is a Kickoff written by fyr auto.\nFiles: internal/search/\nTask: update vet",
			intent: IntentTask,
			want:   "",
		},
		{
			name:   "task with two independent semicolon tasks routes to fyr-swarm",
			prompt: "Add TestHelloTab to internal/greet/greet_test.go; add Shout to internal/shout/shout.go",
			intent: IntentTask,
			want:   "/fyr-swarm (isolated subtasks; zero shared memory)",
		},
		{
			name:   "task with numbered list routes to fyr-swarm",
			prompt: "1) Update flag parsing in cmd/a/main.go 2) Update config loader in internal/cfg/load.go",
			intent: IntentTask,
			want:   "/fyr-swarm (isolated subtasks; zero shared memory)",
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
			prompt: "Trace the call chain from Search to buildHookEnvelopeWithModelTimed",
			intent: IntentTask,
			want:   "fyr search --slice",
		},
		{
			name:   "triage skill routes to fyr-triage",
			prompt: "Triage open GitHub issues and backlog items",
			intent: IntentTask,
			skills: []SkillMeta{{Name: "fyr-triage"}},
			want:   "/fyr-triage",
		},
		{
			name:   "docs skill routes to docs-ste100-80 and fyr check",
			prompt: "Audit README.md and SDD.md documentation structure",
			intent: IntentTask,
			skills: []SkillMeta{{Name: "docs-ste100-80"}},
			want:   "docs-ste100-80 + fyr check",
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

	// Verify VetPrompt emits advisory `route:` inside [FYR_TURN_CONTRACT] directly when
	// Score >= 80 or ANSWER_ONLY (omitting redundant [VET_IMPROVED_PROMPT]), and inside
	// [VET_IMPROVED_PROMPT] when 45 <= Score < 80.
	vetChecks := []struct {
		name               string
		prompt             string
		wantGate           VetGate
		wantRoute          string
		wantImprovedPrompt bool
	}{
		{
			name:               "mid-score bug emits improved prompt with route",
			prompt:             "Fix ReconcileProjectIndex in internal/search/service.go",
			wantGate:           GateEnrichAndProceed,
			wantRoute:          "/fyr-bug (reproduce with failing test first)",
			wantImprovedPrompt: true,
		},
		{
			name:      "anchored high-score bug puts route in TurnContract without redundant ImprovedPrompt",
			prompt:    "Fix panic in ReconcileProjectIndex in internal/search/service.go:19 when root is empty and verify with go test",
			wantGate:  GateEnrichAndProceed,
			wantRoute: "/fyr-bug (reproduce with failing test first)",
		},
		{
			name:               "anchored multi-phase feature",
			prompt:             "Implement Phase 1 caching in internal/search/service.go returning CacheHit; implement Phase 2 metrics in internal/metrics/store.go returning Count, and verify with go test",
			wantGate:           GateEnrichAndProceed,
			wantRoute:          "/fyr-swarm (isolated subtasks; zero shared memory)",
			wantImprovedPrompt: true,
		},
		{
			name:      "anchored single-phase feature",
			prompt:    "Implement CacheIndex in internal/search/service.go returning CacheStats and verify with go test",
			wantGate:  GateEnrichAndProceed,
			wantRoute: "/fyr-feature (pre-build interview first)",
		},
		{
			name:               "two independent tasks",
			prompt:             "Update internal/search/service.go; update internal/metrics/store.go",
			wantGate:           GateEnrichAndProceed,
			wantRoute:          "/fyr-swarm (isolated subtasks; zero shared memory)",
			wantImprovedPrompt: true,
		},
		{
			name:               "review prompt",
			prompt:             "Review internal/search/service.go for race conditions",
			wantGate:           GateEnrichAndProceed,
			wantRoute:          "/fyr-review --judge (cross-family)",
			wantImprovedPrompt: true,
		},
		{
			name:               "release prompt",
			prompt:             "Ship it and cut v8.6.0",
			wantGate:           GateEnrichAndProceed,
			wantRoute:          "/fyr-preflight",
			wantImprovedPrompt: true,
		},
		{
			name:               "lookup task prompt",
			prompt:             "Find callers and references of ReconcileProjectIndex in internal/search/service.go",
			wantGate:           GateEnrichAndProceed,
			wantRoute:          "fyr search --slice",
			wantImprovedPrompt: true,
		},
		{
			name:      "question prompt stays ANSWER_ONLY with route in TurnContract",
			prompt:    "Where is ReconcileProjectIndex defined?",
			wantGate:  GateEnrichAndProceed,
			wantRoute: "fyr search --slice",
		},
	}
	for _, vc := range vetChecks {
		res := VetPrompt(vc.prompt, "", slice, TurnBrainContext{})
		if res.Gate != vc.wantGate {
			t.Errorf("%s: Gate = %s, want %s", vc.name, res.Gate, vc.wantGate)
		}
		if vc.wantImprovedPrompt {
			if !strings.Contains(res.ImprovedPrompt, "[VET_IMPROVED_PROMPT]") {
				t.Errorf("%s: ImprovedPrompt missing [VET_IMPROVED_PROMPT]:\n%s", vc.name, res.ImprovedPrompt)
			}
			if vc.wantRoute != "" && !strings.Contains(res.ImprovedPrompt, "route: "+vc.wantRoute) {
				t.Errorf("%s: ImprovedPrompt missing %q:\n%s", vc.name, "route: "+vc.wantRoute, res.ImprovedPrompt)
			}
		} else if res.ImprovedPrompt != "" {
			t.Errorf("%s: expected empty ImprovedPrompt (Score=%d), got:\n%s", vc.name, res.Score, res.ImprovedPrompt)
		}
		parsed := parseStructuredBanner(t, res.TurnContract)
		if parsed.Route != vc.wantRoute {
			t.Errorf("%s: parsed Route = %q, want %q\n%s", vc.name, parsed.Route, vc.wantRoute, res.TurnContract)
		}
	}

	// Pass-through prompts never synthesize [VET_IMPROVED_PROMPT] or block.
	for _, pt := range []string{"go all approved", "Fix typo in README.md", "!Update internal/search/service.go"} {
		res := VetPrompt(pt, "", slice, TurnBrainContext{})
		if res.Gate != GatePassThrough || res.ImprovedPrompt != "" {
			t.Errorf("pass-through(%q): got gate=%s improved=%q, want PASS_THROUGH and empty ImprovedPrompt", pt, res.Gate, res.ImprovedPrompt)
		}
	}

	// Multi-phase prompt routes deterministically without flaky wall-clock checks.
	const benchPrompt = "Implement Phase 1 in internal/search/service.go returning Stats; implement Phase 2 in internal/search/vet.go returning Route, and verify with go test"
	benchRes := VetPrompt(benchPrompt, "", slice, TurnBrainContext{})
	if benchRes.Gate != GateEnrichAndProceed || !strings.HasPrefix(benchRes.Route, "/fyr-swarm") {
		t.Errorf("VetPrompt(multi-phase) = (gate=%s route=%q), want ENRICH_AND_PROCEED and /fyr-swarm prefix", benchRes.Gate, benchRes.Route)
	}

	// Live pre-invocation hook: mid-score bug emits structured Enrichment.ImprovedPrompt
	// alongside EXECUTE_IMPROVED_PROMPT and route inside [FYR_TURN_CONTRACT].
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	inJSON, _ := json.Marshal(map[string]any{
		"conversationId": "conv-vet-route",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Fix ReconcileProjectIndex in service.go",
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
	parsed := parseStructuredBanner(t, sysMsg)
	if parsed.Directive != "EXECUTE_IMPROVED_PROMPT" || parsed.Route != "/fyr-bug (reproduce with failing test first)" {
		t.Errorf("parsed envelope = %+v, want Directive=EXECUTE_IMPROVED_PROMPT Route=/fyr-bug (reproduce with failing test first)\nraw:\n%s", parsed, sysMsg)
	}
	if parsed.Enrichment == nil || !strings.Contains(parsed.Enrichment.ImprovedPrompt, "[VET_IMPROVED_PROMPT]") {
		t.Errorf("expected Enrichment.ImprovedPrompt with [VET_IMPROVED_PROMPT], got %+v\nraw:\n%s", parsed.Enrichment, sysMsg)
	}
}

func TestReadOnlyAndMultiPackageLookupSkipsDecomposeAndKeepsSliceRoute(t *testing.T) {
	t.Parallel()
	// (a) Read-only multi-package lookup with "do not edit" and phase/wave prose must not decompose
	// into COORDINATE_SUBTASKS / /fyr-swarm and must keep "fyr search --slice".
	readOnlyPrompt := "Find all callers and references of BannerFormatter.Format across internal/greet and internal/format in this repository: run fyr search --affected or --slice first (never grep), state each caller's path:start-end (Symbol) citation, and state which fyr command picks a live model for a builder role and which fyr workflow runs 2+ independent phases in waves. Do not edit any file and do not ask me anything."
	readOnlySlice := SliceResult{
		Files:      []string{"internal/greet/greet.go", "internal/format/format.go"},
		SymbolHits: 2,
	}
	if subtasks := DecomposeSubtasks(readOnlyPrompt, IntentTask, readOnlySlice, nil, nil); len(subtasks) != 0 {
		t.Errorf("(a) DecomposeSubtasks returned %d subtasks, want 0: %+v", len(subtasks), subtasks)
	}
	resA := VetPrompt(readOnlyPrompt, "", readOnlySlice, TurnBrainContext{})
	if got := preInvocationDirective(resA); got == "COORDINATE_SUBTASKS" {
		t.Errorf("(a) preInvocationDirective = %q, want != COORDINATE_SUBTASKS", got)
	}
	bannerA := parseStructuredBanner(t, resA.TurnContract)
	if bannerA.Directive == "COORDINATE_SUBTASKS" {
		t.Errorf("(a) banner.Directive = %q, want != COORDINATE_SUBTASKS", bannerA.Directive)
	}
	if bannerA.Route != "fyr search --slice" {
		t.Errorf("(a) banner.Route = %q, want %q", bannerA.Route, "fyr search --slice")
	}

	// (b) Single-clause multi-package lookup without "do not edit" must not split into subtasks
	// and keeps "fyr search --slice".
	lookupPrompt := "Find callers of Store across internal/a/a.go and internal/b/b.go"
	if subtasks := DecomposeSubtasks(lookupPrompt, IntentTask, SliceResult{}, nil, nil); len(subtasks) != 0 {
		t.Errorf("(b) DecomposeSubtasks returned %d subtasks, want 0: %+v", len(subtasks), subtasks)
	}
	if got := RouteFeature(lookupPrompt, IntentTask, nil); got != "fyr search --slice" {
		t.Errorf("(b) RouteFeature = %q, want %q", got, "fyr search --slice")
	}
	resB := VetPrompt(lookupPrompt, "", SliceResult{Files: []string{"internal/a/a.go", "internal/b/b.go"}, SymbolHits: 1}, TurnBrainContext{})
	if got := preInvocationDirective(resB); got == "COORDINATE_SUBTASKS" {
		t.Errorf("(b) VetPrompt preInvocationDirective = %q, want != COORDINATE_SUBTASKS", got)
	}
	if bannerB := parseStructuredBanner(t, resB.TurnContract); bannerB.Route != "fyr search --slice" {
		t.Errorf("(b) VetPrompt banner.Route = %q, want %q", bannerB.Route, "fyr search --slice")
	}

	// (c) Multi-clause numbered mixed prompt spanning 2 packages must still decompose into >= 2 slices and keep COORDINATE_SUBTASKS.
	mixedPrompt := "1. Fix parser in internal/search/vet.go 2. Find callers of Handle in internal/brain/store.go"
	mixedSubtasks := DecomposeSubtasks(mixedPrompt, "", SliceResult{}, nil, nil)
	if len(mixedSubtasks) < 2 {
		t.Fatalf("(c) DecomposeSubtasks returned %d slices, want >= 2: %+v", len(mixedSubtasks), mixedSubtasks)
	}
	resC := VetPrompt(mixedPrompt, "", SliceResult{}, TurnBrainContext{})
	if len(resC.Subtasks) < 2 {
		t.Fatalf("(c) VetPrompt Subtasks len = %d, want >= 2: %+v", len(resC.Subtasks), resC.Subtasks)
	}
	if got := preInvocationDirective(resC); got != "COORDINATE_SUBTASKS" {
		t.Errorf("(c) preInvocationDirective = %q, want COORDINATE_SUBTASKS", got)
	}
	if bannerC := parseStructuredBanner(t, resC.TurnContract); bannerC.Directive != "COORDINATE_SUBTASKS" {
		t.Errorf("(c) banner.Directive = %q, want COORDINATE_SUBTASKS", bannerC.Directive)
	}
}

func TestRouteFeatureSkipsSwarmOnDiagnosticPrompts(t *testing.T) {
	t.Parallel()
	multiPkgSlice := SliceResult{
		Files:      []string{"internal/search/hook_pre_invocation.go", "internal/brain/store.go", "cmd/fyr/verb_search_hook.go"},
		SymbolHits: 3,
		Capsule:    "[Hop 0] internal/search/hook_pre_invocation.go:1-20 (handlePreInvocation)",
	}
	diagPrompts := []string{
		"Check latency in internal/search/hook_pre_invocation.go; measure internal/brain/store.go",
		"Profile internal/search and verify internal/brain in parallel",
		"Audit Phase 1 and Phase 2 hook latency logs",
		"1. Inspect internal/search/hook_pre_invocation.go 2. Measure internal/brain/turn_records.go",
	}
	for _, p := range diagPrompts {
		if route := RouteFeature(p, IntentTask, nil); strings.HasPrefix(route, "/fyr-swarm") {
			t.Errorf("diagnostic prompt %q unexpectedly routed to %q", p, route)
		}
		res := VetPrompt(p, "", multiPkgSlice, TurnBrainContext{})
		if dir := preInvocationDirective(res); dir == "COORDINATE_SUBTASKS" {
			t.Errorf("diagnostic prompt %q emitted COORDINATE_SUBTASKS", p)
		}
		if strings.HasPrefix(res.Route, "/fyr-swarm") {
			t.Errorf("diagnostic prompt %q VetPrompt route = %q, want non-swarm", p, res.Route)
		}
	}

	// Contrast positive case: explicit 2-package edit emits COORDINATE_SUBTASKS + /fyr-swarm with only those 2 packages.
	editPrompt := "Update internal/search/hook_pre_invocation.go and internal/brain/store.go"
	resEdit := VetPrompt(editPrompt, "", multiPkgSlice, TurnBrainContext{})
	if len(resEdit.Subtasks) != 2 {
		t.Fatalf("explicit 2-package edit subtasks = %d, want 2: %+v", len(resEdit.Subtasks), resEdit.Subtasks)
	}
	if dir := preInvocationDirective(resEdit); dir != "COORDINATE_SUBTASKS" {
		t.Errorf("explicit 2-package edit directive = %q, want COORDINATE_SUBTASKS", dir)
	}
	if !strings.HasPrefix(resEdit.Route, "/fyr-swarm") {
		t.Errorf("explicit 2-package edit route = %q, want /fyr-swarm prefix", resEdit.Route)
	}
	for _, st := range resEdit.Subtasks {
		if slices.Contains(st.Files, "cmd/fyr/verb_search_hook.go") {
			t.Errorf("explicit 2-package edit leaked 3rd-package AST neighbor into %+v", st)
		}
	}
}

func BenchmarkVetPromptRouteFeature(b *testing.B) {
	slice := SliceResult{SymbolHits: 1, Files: []string{"internal/search/service.go"}, Capsule: "[Hop 0] internal/search/service.go:1-10 (ReconcileProjectIndex)"}
	const benchPrompt = "Implement Phase 1 in internal/search/service.go returning Stats; implement Phase 2 in internal/search/vet.go returning Route, and verify with go test"
	b.ResetTimer()
	for range b.N {
		res := VetPrompt(benchPrompt, "", slice, TurnBrainContext{})
		if res.Route == "" {
			b.Fatal("empty route")
		}
	}
}
