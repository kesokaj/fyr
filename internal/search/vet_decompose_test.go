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
		{"worker capsule", "Task\n[FYR_WORKER_CAPSULE]\n1. a/a.go; 2. b/b.go", IntentTask},
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
			if !isKickoffLow(strings.ToLower(sc.prompt)) {
				t.Errorf("%s: isKickoffLow returned false on lowercased worker brief", sc.name)
			}
			if r := RouteFeature(sc.prompt, sc.intent, nil); r != "" {
				t.Errorf("%s: RouteFeature = %q, want empty", sc.name, r)
			}
			skills, _ := MatchSkills(sc.prompt, sc.intent, []string{"internal/a/a.go", "internal/b/b.go"}, nil)
			for _, sk := range skills {
				if sk.Name == "fyr-swarm" {
					t.Errorf("%s: MatchSkills unexpectedly suggested fyr-swarm on worker brief: %+v", sc.name, skills)
				}
			}
		}
	}
}
