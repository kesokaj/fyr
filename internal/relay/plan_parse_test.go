package relay

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParsePlanBodyAndTickBoxes(t *testing.T) {
	body := "# Plan — Test Relay\n\n### Wave 1\n\n#### Phase 1 — Setup parser\n" +
		"`Files:` `internal/relay/*.go`, `cmd/fyr/verb_relay.go`\n\n" +
		"- [x] Already done item\n- [ ] Implement parser\n" +
		"- **Verify:** `go test -race ./internal/relay/...`\n- **Judge:** Verify parser handles fences\n- **Model:** `gemini-3-pro`\n\n" +
		"```markdown\n#### Phase 99 — Inside Fence\n- [ ] Not a real task\n```\n\n" +
		"### Phase 2 — Add runner (after: Phase 1)\nFiles:\n- internal/relay/runner.go\n- internal/relay/runner_test.go\n\n" +
		"- [ ] Build runner\n- [ ] Add tests\n\n## Re-plan If\n\n- [ ] Stray box outside phase\n"
	parsed, err := ParsePlanBody("PLAN-1", "Test Relay", "open", body)
	if err != nil || len(parsed.Phases) != 2 {
		t.Fatalf("ParsePlanBody: err=%v phases=%+v", err, parsed.Phases)
	}
	p1, p2 := parsed.Phases[0], parsed.Phases[1]
	if p1.ID != "Phase 1" || len(p1.OpenTasks) != 1 || len(p1.DoneTasks) != 1 ||
		!slices.Equal(p1.Files, []string{"internal/relay/*.go", "cmd/fyr/verb_relay.go"}) ||
		p1.Verify != "go test -race ./internal/relay/..." || p1.Judge != "Verify parser handles fences" || p1.Model != "gemini-3-pro" {
		t.Fatalf("unexpected Phase 1: %+v", p1)
	}
	if p2.ID != "Phase 2" || !slices.Equal(p2.Deps, []string{"Phase 1"}) || len(p2.OpenTasks) != 2 ||
		!slices.Equal(p2.Files, []string{"internal/relay/runner.go", "internal/relay/runner_test.go"}) {
		t.Fatalf("unexpected Phase 2: %+v", p2)
	}
	if next, ok, err := NextReadyPhase(parsed); err != nil || !ok || next.ID != "Phase 1" {
		t.Fatalf("NextReadyPhase = %+v, %v, %v", next, ok, err)
	}
	ticked := TickPhaseBoxes(body, p1)
	if !strings.Contains(ticked, "- [x] Implement parser") || !strings.Contains(ticked, "- [ ] Build runner") ||
		!strings.Contains(ticked, "- [ ] Not a real task") || !strings.Contains(ticked, "- [ ] Stray box outside phase") {
		t.Fatalf("TickPhaseBoxes wrong output:\n%s", ticked)
	}
	parsed2, err := ParsePlanBody("PLAN-1", "Test Relay", "open", ticked)
	if err != nil {
		t.Fatal(err)
	}
	if next2, ok, err := NextReadyPhase(parsed2); err != nil || !ok || next2.ID != "Phase 2" {
		t.Fatalf("after Phase 1 ticked, NextReadyPhase = %+v, %v, %v", next2, ok, err)
	}
}

func TestBuildKickoffCapAndSafetyRules(t *testing.T) {
	huge := make([]string, 200)
	for i := range huge {
		huge[i] = strings.Repeat("task-detail-", 80)
	}
	prompt := BuildKickoff("/repo/path", ParsedPlan{ID: "PLAN-CAP", Title: "Big Plan"}, Phase{
		ID: "Phase 1", Title: "Phase 1 — Huge", Files: []string{"internal/relay/*.go"},
		Verify: "go test -race ./internal/relay/...", OpenTasks: huge,
	})
	if len(prompt) >= MaxKickoffBytes {
		t.Fatalf("kickoff len=%d >= %d", len(prompt), MaxKickoffBytes)
	}
	for _, want := range []string{
		"/repo/path", "PLAN-CAP", "internal/relay/*.go",
		"NEVER modify PRD.md", "NEVER create git tags", "NEVER run git push",
		"NEVER edit files outside the Files: lease", "truncated to stay below 120 KiB",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("kickoff missing %q", want)
		}
	}
}

func TestBuildAgyArgsAndRunAgySession(t *testing.T) {
	args := BuildAgyArgs(SessionRequest{Prompt: "hello", Model: "gemini-3-pro", Timeout: 5 * time.Minute})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p hello", "--disable-slash-commands", "--output-format json", "--print-timeout 5m0s", "--model gemini-3-pro"} {
		if !strings.Contains(joined, want) {
			t.Errorf("BuildAgyArgs missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "dangerously-skip-permissions") {
		t.Fatalf("BuildAgyArgs must never pass --dangerously-skip-permissions: %v", args)
	}
	if _, err := RunAgySession(context.Background(), SessionRequest{Prompt: strings.Repeat("a", MaxKickoffBytes)}); err == nil {
		t.Fatal("expected prompt size error")
	}
	dir := t.TempDir()
	fakeAgy := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nif [ \"$1\" = \"-p\" ] && [ \"$2\" = \"sleep\" ]; then sleep 10; exit 0; fi\nprintf 'relay=%s plan=%s\\n' \"$FYR_RELAY\" \"$FYR_RELAY_PLAN\"\n"
	if err := os.WriteFile(fakeAgy, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := RunAgySession(context.Background(), SessionRequest{
		RepoDir: dir, PlanID: "PLAN-7", PhaseID: "Phase 1", AgyPath: fakeAgy, Prompt: "go", Timeout: 5 * time.Second,
	})
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "relay=1 plan=PLAN-7") {
		t.Fatalf("RunAgySession ok case: res=%+v err=%v", res, err)
	}
	if _, err = RunAgySession(context.Background(), SessionRequest{RepoDir: dir, AgyPath: fakeAgy, Prompt: "sleep", Timeout: 150 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}
