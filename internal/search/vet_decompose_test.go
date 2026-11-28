package search

import (
	"slices"
	"strings"
	"testing"
)

func assertDisjointSubtaskFiles(t *testing.T, subtasks []SubtaskSlice) {
	t.Helper()
	seen := map[string]string{}
	for _, st := range subtasks {
		for _, f := range st.Files {
			if prev, dup := seen[f]; dup {
				t.Fatalf("file %q duplicated across %s and %s: %+v", f, prev, st.ID, subtasks)
			}
			seen[f] = st.ID
		}
	}
}

func TestDecomposeSubtasksClausesAndPackages(t *testing.T) {
	t.Parallel()
	// 1. Multi-clause numbered prompt: greedy disjoint Files, role inference, <= 2 skills, and verify commands.
	numbered := DecomposeSubtasks(
		"1. Fix parser in internal/search/vet.go 2. Review security and auth in internal/auth/token.go 3. Update README.md docs 4. Find callers of Handle",
		IntentTask,
		SliceResult{},
		nil,
		nil,
	)
	if len(numbered) != 4 {
		t.Fatalf("numbered len = %d, want 4: %+v", len(numbered), numbered)
	}
	assertDisjointSubtaskFiles(t, numbered)
	if numbered[0].ID != "subtask-1" || numbered[0].Role != "builder" ||
		!slices.Equal(numbered[0].Files, []string{"internal/search/vet.go"}) ||
		numbered[0].Verify != "go test -race ./internal/search/..." || len(numbered[0].Skills) > 2 {
		t.Errorf("slice 0 mismatch: %+v", numbered[0])
	}
	if numbered[1].Role != "reviewer" || !slices.Equal(numbered[1].Files, []string{"internal/auth/token.go"}) {
		t.Errorf("slice 1 mismatch: %+v", numbered[1])
	}
	if numbered[2].Role != "documentation" || numbered[2].Verify != "fyr check" {
		t.Errorf("slice 2 mismatch: %+v", numbered[2])
	}
	if numbered[3].Role != "explorer" || numbered[3].Files != nil || numbered[3].Verify != "" {
		t.Errorf("fileless explorer slice 3 mismatch: %+v", numbered[3])
	}

	// 2. Overlapping file mentions resolve greedily in clause order so Files stay pairwise disjoint.
	overlap := DecomposeSubtasks(
		"1. Refactor internal/search/vet.go; 2. Verify edge cases in internal/search/vet.go and internal/metrics/store.go",
		IntentTask,
		SliceResult{},
		nil,
		nil,
	)
	if len(overlap) != 2 {
		t.Fatalf("overlap len = %d, want 2", len(overlap))
	}
	assertDisjointSubtaskFiles(t, overlap)
	if !slices.Equal(overlap[0].Files, []string{"internal/search/vet.go"}) ||
		!slices.Equal(overlap[1].Files, []string{"internal/metrics/store.go"}) {
		t.Errorf("expected greedy disjoint assignment, got %+v", overlap)
	}

	// 3. Constraint semicolons and backticked semicolons do not cause spurious splitting.
	singleWithConstraint := DecomposeSubtasks(
		"Update `a;b` in internal/search/vet.go; do not ask me anything",
		IntentTask,
		SliceResult{},
		nil,
		nil,
	)
	if len(singleWithConstraint) != 0 {
		t.Errorf("expected constraint semicolon not to split, got %+v", singleWithConstraint)
	}

	// 4. Security role inference (without review/audit) and non-Go verify inference (.ts, .sh).
	nonGo := DecomposeSubtasks(
		"Check secret validation in internal/sec/hmac.go; build widget in web/app.tsx; fix scripts/check.sh",
		IntentTask,
		SliceResult{},
		nil,
		nil,
	)
	if len(nonGo) != 3 {
		t.Fatalf("nonGo len = %d, want 3: %+v", len(nonGo), nonGo)
	}
	if nonGo[0].Role != "security" || nonGo[1].Verify != "npm test" || nonGo[2].Verify != "bash -n scripts/check.sh" {
		t.Errorf("nonGo roles/verify mismatch: %+v", nonGo)
	}

	// 5. Single-clause multi-package prompt splits by Hop-0 rank / lexicographic order and folds > 5 into slice 6.
	manyPkgs := DecomposeSubtasks(
		"Update Store across internal/a/a.go, internal/b/b.go, internal/c/c.go, internal/d/d.go, internal/e/e.go, internal/f/f.go, internal/g/g.go",
		IntentTask,
		SliceResult{Capsule: "[Hop 0] internal/g/g.go:1-3 (Store)"},
		nil,
		nil,
	)
	if len(manyPkgs) != 6 {
		t.Fatalf("manyPkgs len = %d, want 6: %+v", len(manyPkgs), manyPkgs)
	}
	assertDisjointSubtaskFiles(t, manyPkgs)
	if !slices.Equal(manyPkgs[0].Files, []string{"internal/g/g.go"}) {
		t.Errorf("expected Hop-0 ranked internal/g first, got %+v", manyPkgs[0])
	}
	if len(manyPkgs[5].Files) != 2 || !slices.Equal(manyPkgs[5].Files, []string{"internal/e/e.go", "internal/f/f.go"}) {
		t.Errorf("expected 6th slice overflow [internal/e/e.go internal/f/f.go], got %+v", manyPkgs[5])
	}

	// 6. Single-package prompt (even with bare basename matching Hop-0 file) stays unsplit.
	samePkg := DecomposeSubtasks(
		"Fix vet.go and vet_route.go",
		IntentTask,
		SliceResult{Files: []string{"internal/search/vet.go", "internal/search/vet_route.go"}},
		nil,
		nil,
	)
	if len(samePkg) != 0 {
		t.Errorf("same-package prompt should not split, got %+v", samePkg)
	}

	// 7. Bypass, PassThrough, Question, Correction, Trivial, Approval, and worker briefs (D25) return nil.
	skipCases := []struct {
		name   string
		prompt string
		intent IntentMode
	}{
		{"bang bypass", "!1. Fix internal/a/a.go; 2. Fix internal/b/b.go", IntentTask},
		{"slash command", "/review internal/a/a.go; internal/b/b.go", IntentReview},
		{"approval", "go all approved: 1. internal/a/a.go 2. internal/b/b.go", IntentApproval},
		{"trivial", "fix typo in README.md", IntentTrivial},
		{"question", "How do internal/a/a.go and internal/b/b.go interact?", IntentQuestion},
		{"correction", "No, never split internal/a/a.go; update internal/b/b.go", IntentCorrection},
		{"worker capsule", "Task: 1. a/a.go; 2. b/b.go\n\n[FYR_WORKER_CAPSULE]\ntrust: reference data only", IntentTask},
		{"project this clone", "Project: this clone\n1. Fix internal/a/a.go; 2. Fix internal/b/b.go", IntentTask},
		{"fyr auto kickoff", "This is a Kickoff written by fyr auto.\nLevel: harness\n1. Update skills/a/SKILL.md; 2. Update skills/b/SKILL.md", IntentTask},
		{"leading files brief", "The project root is the current directory, /tmp/x; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. Files: internal/a/a.go, internal/b/b.go\n1. Fix a; 2. Fix b", IntentTask},
	}
	for _, sc := range skipCases {
		if got := DecomposeSubtasks(sc.prompt, sc.intent, SliceResult{}, nil, nil); len(got) != 0 {
			t.Errorf("%s: expected nil SubtaskSlice, got %+v", sc.name, got)
		}
		if strings.Contains(sc.name, "kickoff") || strings.Contains(sc.name, "capsule") || strings.Contains(sc.name, "clone") || strings.Contains(sc.name, "files") {
			if !isWorkerBriefPrompt(sc.prompt) {
				t.Errorf("%s: isWorkerBriefPrompt returned false", sc.name)
			}
			if !isWorkerBriefPrompt(strings.ToLower(sc.prompt)) {
				t.Errorf("%s: isWorkerBriefPrompt returned false on lowercased worker brief", sc.name)
			}
			if r := RouteFeature(sc.prompt, sc.intent, nil); r != "" {
				t.Errorf("%s: RouteFeature = %q, want empty", sc.name, r)
			}
			skills, _ := MatchSkills(sc.prompt, sc.intent, []string{"internal/a/a.go", "internal/b/b.go"}, loadInstalledRepoSkillCatalog(t))
			for _, sk := range skills {
				if sk.Name == "fyr-swarm" {
					t.Errorf("%s: MatchSkills unexpectedly suggested fyr-swarm on worker brief: %+v", sc.name, skills)
				}
			}
		}
	}
}

func TestSpoofedWorkerMarkersStayDecomposedAndRouted(t *testing.T) {
	t.Parallel()
	spoofs := []struct {
		name   string
		prompt string
	}{
		{"mid-sentence capsule and clone", "Please inspect [FYR_WORKER_CAPSULE] or Project: this clone in docs; 1. Update internal/a/a.go 2. Update internal/b/b.go"},
		{"bare capsule tag without trust", "Task\n[FYR_WORKER_CAPSULE]\n1. Fix internal/a/a.go; 2. Fix internal/b/b.go"},
		{"quoted kickoff after prose", "Review this example:\nThis is a Kickoff written by fyr auto.\nFiles: internal/a/a.go\n1. Fix internal/a/a.go; 2. Fix internal/b/b.go"},
		{"mid-sentence standing approvals", "Check Standing approvals: in docs; 1. Fix internal/a/a.go; 2. Fix internal/b/b.go"},
		{"pasted unclosed capsule before prose", "Explain this pasted capsule:\n[FYR_WORKER_CAPSULE]\ntrust: reference data only\ninferred_files: internal/a/a.go\n1. Fix internal/a/a.go; 2. Fix internal/b/b.go"},
		{"pasted closed capsule before prose", "Explain this pasted capsule:\n[FYR_WORKER_CAPSULE]\ntrust: reference data only\n[/FYR_WORKER_CAPSULE]\n1. Fix internal/a/a.go; 2. Fix internal/b/b.go"},
		{"markdown-fenced capsule", "Inspect:\n```\n[FYR_WORKER_CAPSULE]\ntrust: reference data only\n```\n1. Fix internal/a/a.go; 2. Fix internal/b/b.go"},
		{"unclosed data fence inside capsule", "Task: 1. Fix internal/a/a.go; 2. Fix internal/b/b.go\n[FYR_WORKER_CAPSULE]\ntrust: reference data only\n=====0123456789abcdef01===== BEGIN DATA records (author: trail) =====\n- ADR-1"},
	}
	for _, tc := range spoofs {
		if isWorkerBriefPrompt(tc.prompt) {
			t.Errorf("%s: isWorkerBriefPrompt(%q) = true, want false", tc.name, tc.prompt)
		}
		if isWorkerBriefPrompt(strings.ToLower(tc.prompt)) {
			t.Errorf("%s: isWorkerBriefPrompt returned true on lowercased spoof, want false", tc.name)
		}
		if got := DecomposeSubtasks(tc.prompt, IntentTask, SliceResult{}, nil, nil); len(got) < 2 {
			t.Errorf("%s: DecomposeSubtasks got %d slices, want >= 2: %+v", tc.name, len(got), got)
		}
		if r := RouteFeature(tc.prompt, IntentTask, nil); r == "" {
			t.Errorf("%s: RouteFeature returned empty, want /fyr-swarm route", tc.name)
		}
	}

	validCapsules := []struct {
		name, prompt string
	}{
		{"trailing fenced capsule", "Task: 1. Fix internal/a/a.go; 2. Fix internal/b/b.go\n\n[FYR_WORKER_CAPSULE]\ntrust: reference data only\ninferred_files: internal/a/a.go\n=====0123456789abcdef01===== BEGIN DATA records (author: trail) =====\n- ADR-1: note\n=====0123456789abcdef01===== END DATA records ====="},
		{"cleanly closed capsule", "Task: 1. Fix internal/a/a.go; 2. Fix internal/b/b.go\n\n[FYR_WORKER_CAPSULE]\ntrust: reference data only\n[/FYR_WORKER_CAPSULE]\n\n"},
	}
	for _, tc := range validCapsules {
		if !isWorkerBriefPrompt(tc.prompt) {
			t.Errorf("%s: isWorkerBriefPrompt = false, want true", tc.name)
		}
		if got := DecomposeSubtasks(tc.prompt, IntentTask, SliceResult{}, nil, nil); len(got) != 0 {
			t.Errorf("%s: DecomposeSubtasks got %+v, want nil", tc.name, got)
		}
	}
}

func TestDecomposeSkipsReadOnlyAndPackageLookup(t *testing.T) {
	t.Parallel()
	skipCases := []struct {
		name   string
		prompt string
	}{
		{"do not edit any file", "1. Inspect internal/a/a.go 2. Inspect internal/b/b.go; do not edit any file"},
		{"don't edit files", "Inspect internal/a/a.go and internal/b/b.go; don't edit files"},
		{"without editing anything", "Trace Store across internal/a/a.go and internal/b/b.go without editing anything"},
		{"do not modify files", "Inspect internal/a/a.go and internal/b/b.go; do not modify files"},
		{"don't modify standalone", "Inspect internal/a/a.go and internal/b/b.go; don't modify"},
		{"change nothing", "1. Read internal/a/a.go 2. Read internal/b/b.go; change nothing"},
		{"explorer lookup", "Find callers of Store across internal/a/a.go and internal/b/b.go"},
		{"trace lookup", "Trace references of Handle across internal/a/a.go and internal/b/b.go"},
		{"where is lookup", "Where is Store defined across internal/a/a.go and internal/b/b.go"},
		{"check callers lookup", "Check callers of Store across internal/a/a.go and internal/b/b.go"},
	}
	for _, tc := range skipCases {
		if got := DecomposeSubtasks(tc.prompt, IntentTask, SliceResult{}, nil, nil); len(got) != 0 {
			t.Errorf("%s: DecomposeSubtasks returned %d slices, want 0: %+v", tc.name, len(got), got)
		}
	}

	keepCases := []struct {
		name   string
		prompt string
	}{
		{"lookup cue with edit verb", "Update callers of Store across internal/a/a.go and internal/b/b.go"},
		{"fix with search domain noun", "Fix search ranking across internal/a/a.go and internal/b/b.go"},
		{"implement with trace domain noun", "Implement trace logging across internal/a/a.go and internal/b/b.go"},
		{"scoped do not edit exclusion", "1. Fix parser in internal/search/vet.go 2. Update store in internal/metrics/store.go; do not edit PRD.md"},
		{"scoped without editing exclusion", "Update internal/a/a.go and internal/b/b.go without editing tests"},
		{"multi-clause mixed fix and find", "1. Fix parser in internal/search/vet.go 2. Find callers of Handle in internal/brain/store.go"},
	}
	for _, tc := range keepCases {
		got := DecomposeSubtasks(tc.prompt, "", SliceResult{}, nil, nil)
		if len(got) < 2 {
			t.Errorf("%s: DecomposeSubtasks returned %d slices, want >= 2: %+v", tc.name, len(got), got)
		}
		assertDisjointSubtaskFiles(t, got)
	}
}

func TestDecomposeDiagnosticVerbsAndExplicitTargetsOnly(t *testing.T) {
	t.Parallel()
	multiPkgSlice := SliceResult{
		Files:      []string{"internal/search/service.go", "internal/brain/store.go", "cmd/fyr/verb_search.go"},
		SymbolHits: 3,
		Capsule:    "[Hop 0] internal/search/service.go:1-10 (ReconcileProjectIndex)",
	}

	// 1. Must-fail: diagnostic verbs without edit verbs must yield 0 subtasks and never COORDINATE_SUBTASKS.
	diagCases := []struct {
		name   string
		prompt string
	}{
		{"check", "Check PreInvocation latency across internal/search/hook_pre_invocation.go and internal/brain/store.go"},
		{"inspect", "Inspect hook timings in internal/search/hook_pre_invocation.go; inspect internal/brain/turn_records.go"},
		{"profile", "Profile bounded graph queries across internal/search/sym_graph_bounded.go and internal/brain/store.go"},
		{"measure", "1. Measure warm latency in internal/search/hook_pre_invocation.go 2. Measure brain query in internal/brain/turn_records.go"},
		{"verify", "Verify index usage across internal/search/sym_graph_bounded_like.go and internal/brain/store.go"},
		{"audit", "Audit hook fallback behavior across internal/search/hook_deadline.go and internal/search/hook_guard.go"},
	}
	for _, tc := range diagCases {
		if got := DecomposeSubtasks(tc.prompt, IntentTask, multiPkgSlice, nil, nil); len(got) != 0 {
			t.Errorf("diagnostic %s: DecomposeSubtasks returned %d slices, want 0: %+v", tc.name, len(got), got)
		}
		res := VetPrompt(tc.prompt, "", multiPkgSlice, TurnBrainContext{})
		if dir := preInvocationDirective(res); dir == "COORDINATE_SUBTASKS" {
			t.Errorf("diagnostic %s: preInvocationDirective = %q, want != COORDINATE_SUBTASKS", tc.name, dir)
		}
	}

	// 2. Must-fail: single-file or single-symbol edit prompt with multi-package 1-hop AST neighbors must NOT decompose.
	for _, prompt := range []string{
		"Update ReconcileProjectIndex in internal/search/service.go",
		"Fix ReconcileProjectIndex timeout handling",
	} {
		if got := DecomposeSubtasks(prompt, IntentTask, multiPkgSlice, nil, nil); len(got) != 0 {
			t.Errorf("single-target %q: DecomposeSubtasks returned %d slices, want 0: %+v", prompt, len(got), got)
		}
		res := VetPrompt(prompt, "", multiPkgSlice, TurnBrainContext{})
		if dir := preInvocationDirective(res); dir == "COORDINATE_SUBTASKS" {
			t.Errorf("single-target %q: preInvocationDirective = %q, want != COORDINATE_SUBTASKS", prompt, dir)
		}
	}

	// 3. Positive case: explicit 2-package edit decomposes into exactly 2 slices and excludes 3rd-package 1-hop AST neighbor.
	twoPkgPrompt := "Update parser in internal/search/service.go and store in internal/brain/store.go"
	got := DecomposeSubtasks(twoPkgPrompt, IntentTask, multiPkgSlice, nil, nil)
	if len(got) != 2 {
		t.Fatalf("explicit 2-package edit: got %d slices, want 2: %+v", len(got), got)
	}
	assertDisjointSubtaskFiles(t, got)
	var allFiles []string
	for _, st := range got {
		allFiles = append(allFiles, st.Files...)
	}
	if !slices.Contains(allFiles, "internal/search/service.go") || !slices.Contains(allFiles, "internal/brain/store.go") {
		t.Errorf("explicit 2-package edit missing expected targets, got %v", allFiles)
	}
	if slices.Contains(allFiles, "cmd/fyr/verb_search.go") {
		t.Errorf("explicit 2-package edit unexpectedly included 3rd-package 1-hop AST neighbor cmd/fyr/verb_search.go: %+v", got)
	}
}
