package fyr_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readSkill(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the text from the heading line that starts with head up to
// the next heading of the same or a higher level.
func section(t *testing.T, text, head string) string {
	t.Helper()
	i := strings.Index(text, "\n"+head)
	if i < 0 {
		t.Fatalf("no heading %q", head)
	}
	rest := text[i+1:]
	level := len(head) - len(strings.TrimLeft(head, "#"))
	next := regexp.MustCompile(`(?m)^#{1,` + string(rune('0'+level)) + `} `)
	if loc := next.FindStringIndex(rest[len(head):]); loc != nil {
		return rest[:len(head)+loc[0]]
	}
	return rest
}

// TestHandoffReportEndsWithTheKickoffInline: the operator said "Give it as a
// prompt always" (2026-10-25, project memory next-session-prompt-inline), and
// two sessions still closed with a pointer to the handoff file instead of the
// block. The handoff skill's report step prints the Kickoff in full, in a
// fenced block, as its last item, and says a pointer does not replace it.
func TestHandoffReportEndsWithTheKickoffInline(t *testing.T) {
	step := section(t, readSkill(t, "skills/fyr-handoff/SKILL.md"), "## Step 7")
	for _, want := range []string{"Kickoff block in full", "in a fenced block in the reply", "ready to paste", "Never a pointer to the file"} {
		if !strings.Contains(step, want) {
			t.Errorf("handoff Step 7 does not say %q", want)
		}
	}
	items := regexp.MustCompile(`(?m)^\d+\. .*$`).FindAllString(step, -1)
	if len(items) == 0 || !strings.Contains(items[len(items)-1], "Kickoff") {
		t.Errorf("the Kickoff is not the report's last numbered item: %q", items)
	}
}

// TestPushStepsRunTheTrailerCheck: BUG-177's rule graduated into a script
// (plan 2026-10-25-harness-audit-fix D13); both skills that push run it
// before the push, by its installed path.
func TestPushStepsRunTheTrailerCheck(t *testing.T) {
	for path, head := range map[string]string{
		"skills/fyr-handoff/SKILL.md":   "## Step 6",
		"skills/fyr-preflight/SKILL.md": "## Step 6",
	} {
		step := section(t, readSkill(t, path), head)
		if !strings.Contains(step, "scripts/check-trailers.sh") || !strings.Contains(step, "~/.gemini/config/skills/fyr-handoff") {
			t.Errorf("%s %s does not run check-trailers.sh by its installed path", path, head)
		}
		run, push := strings.Index(step, "check-trailers.sh"), strings.LastIndex(step, "push")
		if run < 0 || push < run {
			t.Errorf("%s %s: the trailer check must come before the push", path, head)
		}
	}
}

// TestWorkflowSkillsCarryNoStaleNames pins Phase 3's grep expects (WS23,
// WS24, WS27, WS28): the agent format of ADR-062, the global context's
// section name, one CLI, and one Session form.
func TestWorkflowSkillsCarryNoStaleNames(t *testing.T) {
	gone := map[string][]string{
		"skills/fyr-retro/SKILL.md":                    {"`agents/<name>.md`", "`.agents/agents/<name>.md`"},
		"skills/fyr-harness-audit/references/areas.md": {"derive.go", "`agents/*.md`"},
		"skills/fyr-handoff/SKILL.md":                  {"Resume Protocol", "either vendor", "Session ID"},
		"skills/review-standards/SKILL.md":             {"the six criteria below"},
		"skills/fyr-review/SKILL.md":                   {"all six criteria"},
		"skills/fyr-preflight/SKILL.md":                {"Step 2 item 1"},
		"skills/fyr-feature/SKILL.md":                  {"a tool-call budget the dispatch names"},
		"skills/fyr-bug/SKILL.md":                      {"a tool-call budget the dispatch names"},
		"skills/fyr-explain-project/SKILL.md":          {"a tool-call budget the dispatch names"},
	}
	for path, words := range gone {
		text := readSkill(t, path)
		for _, w := range words {
			if strings.Contains(text, w) {
				t.Errorf("%s still says %s", path, w)
			}
		}
	}
	const form = "<first 8 chars of the conversation id>"
	for _, path := range []string{"skills/fyr-handoff/SKILL.md", "skills/fyr-feedback-log/SKILL.md", "skills/fyr-retro/SKILL.md", "skills/fyr-distill/SKILL.md"} {
		if !strings.Contains(readSkill(t, path), form) {
			t.Errorf("%s does not use the one Session form %s", path, form)
		}
	}
	if !strings.Contains(readSkill(t, "skills/fyr-retro/SKILL.md"), "<name>/agent.md") {
		t.Error("retro's subagent row does not name <name>/agent.md")
	}
}

// TestWorkSkillRunsFeatureSteps5And6AfterLastMerge pins BUG-516 (Item 21 of
// PLAN-2026-11-02-1806-fyr-speed): after the last wave's merge in plan mode,
// the lead runs feature Steps 5 and 6 over the whole range from the first
// wave's base to HEAD before the lead's closing handoff.
func TestWorkSkillRunsFeatureSteps5And6AfterLastMerge(t *testing.T) {
	text := readSkill(t, "skills/fyr-work/SKILL.md")
	loop := section(t, text, "## The loop")
	for _, want := range []string{
		"After the last merge",
		"fyr-feature` Steps 5 and 6",
		"from the first wave's base to HEAD",
		"`reviewer`",
		"`verifier`",
		"`<short>-<phase>-fix`",
	} {
		if !strings.Contains(loop, want) {
			t.Errorf("skills/fyr-work/SKILL.md ## The loop does not say %q", want)
		}
	}
	afterIdx := strings.Index(loop, "11. **After the last merge**")
	endIdx := strings.Index(loop, "12. **End the loop**")
	if afterIdx < 0 || endIdx <= afterIdx {
		t.Errorf("skills/fyr-work/SKILL.md ## The loop must order step 11 (After the last merge) before step 12 (End the loop)")
	}
}

// TestFeatureSkillEmptyRepoAndWorkersDRow pins Item 18 and I4 of
// PLAN-2026-11-02-1806-fyr-speed: feature Step 1 skips explorer in a
// repository that has no source files yet, and Step 3 shows the workers D-row
// shape that the work skill reads.
func TestFeatureSkillEmptyRepoAndWorkersDRow(t *testing.T) {
	text := readSkill(t, "skills/fyr-feature/SKILL.md")
	step1 := section(t, text, "## Step 1: Scope")
	for _, want := range []string{
		"repository already has source files",
		"no source files yet",
		"skip `explorer`",
		"no source yet",
	} {
		if !strings.Contains(step1, want) {
			t.Errorf("skills/fyr-feature/SKILL.md ## Step 1: Scope does not say %q", want)
		}
	}
	step3 := section(t, text, "## Step 3: Cold-reader check")
	for _, want := range []string{
		"D<n> — <date> — workers: yes | no, <why> — by: planner",
	} {
		if !strings.Contains(step3, want) {
			t.Errorf("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check does not say %q", want)
		}
	}
}

// TestColdReaderBudget pins Item 19 of PLAN-2026-11-02-1806-fyr-speed: the
// plan's cold reader gets a 15-tool-call budget and a 10-finding/question
// output cap (< 80 lines), and skips review-standards code-diff references.
func TestColdReaderBudget(t *testing.T) {
	step3 := section(t, readSkill(t, "skills/fyr-feature/SKILL.md"), "## Step 3: Cold-reader check")
	for _, want := range []string{
		"a budget of 15 tool calls",
		"at most 10 findings and questions",
		"under 80 lines",
	} {
		if !strings.Contains(step3, want) {
			t.Errorf("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check does not say %q", want)
		}
	}
	if strings.Contains(step3, "a budget of 30 tool calls") {
		t.Error("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check still says 30 tool calls")
	}

	rev := readSkill(t, "agents/reviewer/agent.md")
	for _, want := range []string{
		"15 tool calls",
		"10 findings and questions",
		"under 80 lines",
		"skip on a plan cold-read",
	} {
		if !strings.Contains(rev, want) {
			t.Errorf("agents/reviewer/agent.md does not say %q", want)
		}
	}
}

// TestResumeAndGitCheckAndSingleCommit pins Items 20, 28, and 29 of
// PLAN-2026-11-02-1806-fyr-speed: context/AGENTS.md uses fyr resume and fyr
// git-check, and context/AGENTS.md, skills/fyr-feature/SKILL.md, and
// skills/fyr-handoff/SKILL.md commit, push, and ingest once per conversation.
func TestResumeAndGitCheckAndSingleCommit(t *testing.T) {
	ctx := readSkill(t, "context/AGENTS.md")
	for _, want := range []string{
		"`fyr resume`",
		"`fyr git-check`: an unnamed path, commit or tag is a stop sign.",
		"commits, pushes and runs `fyr metrics ingest` once",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context/AGENTS.md does not say %q", want)
		}
	}
	for _, gone := range []string{
		"as three separate commands, never joined on one line",
		"(Checkpoint box, `change` record, commit, push, `fyr metrics ingest`)",
	} {
		if strings.Contains(ctx, gone) {
			t.Errorf("context/AGENTS.md still says %q", gone)
		}
	}

	feat := readSkill(t, "skills/fyr-feature/SKILL.md")
	if strings.Contains(feat, "commit and push as separate calls, then the handoff") {
		t.Error("skills/fyr-feature/SKILL.md still commits and pushes before the handoff")
	}
	if !strings.Contains(feat, "commits, pushes and runs `fyr metrics ingest` once") {
		t.Error("skills/fyr-feature/SKILL.md does not state that /fyr-handoff commits, pushes and ingests once")
	}

	handoff := section(t, readSkill(t, "skills/fyr-handoff/SKILL.md"), "## Step 6")
	if !strings.Contains(handoff, "one commit, one push and one `fyr metrics ingest` per conversation") {
		t.Error("skills/fyr-handoff/SKILL.md Step 6 does not state one commit, push and ingest per conversation")
	}
}
