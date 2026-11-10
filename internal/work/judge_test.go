package work

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestKickoffBaseCheckAndJudge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "phase-1.md")
	text := "Project: /tmp/demo\n" +
		"Files: src/a.txt\n" +
		"Model: gemini-4-argon\n" +
		"Base-Check: grep -q work src/a.txt\n" +
		"Judge: Verify src/a.txt contains work and no debug leftovers.\n" +
		"Judge-Model: claude-opus-5-5\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := ParseKickoff(path)
	if err != nil {
		t.Fatalf("ParseKickoff: %v", err)
	}
	if k.BaseCheck != "grep -q work src/a.txt" {
		t.Errorf("BaseCheck = %q, want %q", k.BaseCheck, "grep -q work src/a.txt")
	}
	if k.Judge != "Verify src/a.txt contains work and no debug leftovers." {
		t.Errorf("Judge = %q", k.Judge)
	}
	if k.JudgeModel != "claude-opus-5-5" {
		t.Errorf("JudgeModel = %q, want claude-opus-5-5", k.JudgeModel)
	}

	// Duplicate Judge: line must fail.
	bad := filepath.Join(dir, "bad-judge.md")
	if err := os.WriteFile(bad, []byte(text+"Judge: second line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKickoff(bad); err == nil || !strings.Contains(err.Error(), "2 Judge: lines") {
		t.Errorf("duplicate Judge: err = %v, want 2 Judge: lines", err)
	}
}

func TestWorkerBaseCheck(t *testing.T) {
	// Case 1: Base-Check fails on HEAD (expected) and passes after the worker adds "work" to src/a.txt.
	r := newRig(t)
	kPass := filepath.Join(r.kick, "bc-pass.md")
	put(t, kPass, "Project: "+r.lead+"\nFiles: src/a.txt\nBase-Check: grep -q work src/a.txt\n")
	res := r.run(kPass)
	if res.Verdict != "PASS" {
		t.Fatalf("Case 1 (expected PASS): Verdict=%s Reasons=%v Notes=%v", res.Verdict, res.Reasons, res.Notes)
	}

	// Case 2: Base-Check already passes on HEAD before the worker runs -> ReasonBaseCheckPassed.
	r2 := newRig(t)
	kAlready := filepath.Join(r2.kick, "bc-already.md")
	put(t, kAlready, "Project: "+r2.lead+"\nFiles: src/a.txt\nBase-Check: grep -q a src/a.txt\n")
	res2 := r2.run(kAlready)
	if res2.Verdict != "FAIL" || !slices.Contains(res2.Reasons, ReasonBaseCheckPassed) {
		t.Fatalf("Case 2 (expected %q): Verdict=%s Reasons=%v", ReasonBaseCheckPassed, res2.Verdict, res2.Reasons)
	}

	// Case 3: Base-Check fails on HEAD and still fails after build -> ReasonBaseCheckFailed.
	r3 := newRig(t)
	kFailAfter := filepath.Join(r3.kick, "bc-fail-after.md")
	put(t, kFailAfter, "Project: "+r3.lead+"\nFiles: src/a.txt\nBase-Check: grep -q never-written src/a.txt\n")
	res3 := r3.run(kFailAfter)
	if res3.Verdict != "FAIL" || !slices.Contains(res3.Reasons, ReasonBaseCheckFailed) {
		t.Fatalf("Case 3 (expected %q): Verdict=%s Reasons=%v", ReasonBaseCheckFailed, res3.Verdict, res3.Reasons)
	}

	// Case 4: Worker modifies a file outside Files: -> scope() fails with ReasonOutside BEFORE runPostBaseCheck executes.
	r4 := newRig(t)
	r4.on("outside", "1")
	marker := filepath.Join(r4.tmp, "post-bc-ran")
	kOutside := filepath.Join(r4.kick, "bc-outside.md")
	put(t, kOutside, "Project: "+r4.lead+"\nFiles: src/a.txt\nBase-Check: test -f outside.txt && touch "+marker+"\n")
	res4 := r4.run(kOutside)
	if res4.Verdict != "FAIL" || !slices.Contains(res4.Reasons, ReasonOutside) {
		t.Fatalf("Case 4 (expected %q): Verdict=%s Reasons=%v", ReasonOutside, res4.Verdict, res4.Reasons)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("Case 4: runPostBaseCheck executed on a branch that failed scope() with ReasonOutside")
	}
}

func TestWorkerJudge(t *testing.T) {
	// Case 1: Cross-family judge returns VERDICT: PASS and writes review-judge.md into the worker's DraftsDir.
	r := newRig(t)
	r.on("models-list", "gemini-4-argon\tGemini 4 Argon\nclaude-opus-5-5\tClaude Opus 5.5\n")
	r.on("judge-out", "# Judge Report\nAll checks verified.\nVERDICT: PASS — src/a.txt updated cleanly\n")
	kPass := filepath.Join(r.kick, "judge-pass.md")
	put(t, kPass, "Project: "+r.lead+"\nFiles: src/a.txt\nJudge: Verify src/a.txt was updated cleanly.\n")
	res := r.run(kPass)
	if res.Verdict != "PASS" {
		t.Fatalf("Judge PASS case: Verdict=%s Reasons=%v Notes=%v", res.Verdict, res.Reasons, res.Notes)
	}
	judgeArgs := r.read("judge-args")
	if !strings.Contains(judgeArgs, "claude-opus-5-5") {
		t.Errorf("expected cross-family judge model claude-opus-5-5 in judge-args, got:\n%s", judgeArgs)
	}
	artifactPath := filepath.Join(r.lead, filepath.FromSlash(res.DraftsDir), "review-judge.md")
	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("reading %s: %v", artifactPath, err)
	}
	if !strings.Contains(string(artifactBytes), "VERDICT: PASS") {
		t.Errorf("review-judge.md missing VERDICT: PASS:\n%s", string(artifactBytes))
	}

	// Case 2: Cross-family judge returns VERDICT: FAIL -> ReasonJudgeFailed.
	r2 := newRig(t)
	r2.on("models-list", "gemini-4-argon\tGemini 4 Argon\nclaude-opus-5-5\tClaude Opus 5.5\n")
	r2.on("judge-out", "# Judge Report\nMissing invariant check.\nVERDICT: FAIL — invariant violated\n")
	kFail := filepath.Join(r2.kick, "judge-fail.md")
	put(t, kFail, "Project: "+r2.lead+"\nFiles: src/a.txt\nJudge: Verify invariant holds.\n")
	res2 := r2.run(kFail)
	if res2.Verdict != "FAIL" || !slices.Contains(res2.Reasons, ReasonJudgeFailed) {
		t.Fatalf("Judge FAIL case (expected %q): Verdict=%s Reasons=%v Notes=%v", ReasonJudgeFailed, res2.Verdict, res2.Reasons, res2.Notes)
	}

	// Case 3: Oversized judge stdout (> 64 KiB) triggers stdout.full / errModelsOver -> ReasonJudgeFailed.
	r3 := newRig(t)
	r3.on("models-list", "gemini-4-argon\tGemini 4 Argon\nclaude-opus-5-5\tClaude Opus 5.5\n")
	r3.on("judge-out", strings.Repeat("x", (64<<10)+1024)+"\nVERDICT: PASS — truncated\n")
	kOver := filepath.Join(r3.kick, "judge-over.md")
	put(t, kOver, "Project: "+r3.lead+"\nFiles: src/a.txt\nJudge: Verify oversized output fails closed.\n")
	res3 := r3.run(kOver)
	if res3.Verdict != "FAIL" || !slices.Contains(res3.Reasons, ReasonJudgeFailed) {
		t.Fatalf("Judge oversized stdout case (expected %q): Verdict=%s Reasons=%v Notes=%v", ReasonJudgeFailed, res3.Verdict, res3.Reasons, res3.Notes)
	}
}
