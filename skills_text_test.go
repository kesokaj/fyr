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
	step := section(t, readSkill(t, "skills/handoff/SKILL.md"), "## Step 7")
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
		"skills/handoff/SKILL.md":   "## Step 6",
		"skills/preflight/SKILL.md": "## Step 6",
	} {
		step := section(t, readSkill(t, path), head)
		if !strings.Contains(step, "scripts/check-trailers.sh") || !strings.Contains(step, "~/.gemini/config/skills/handoff") {
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
		"skills/retro/SKILL.md":                    {"`agents/<name>.md`", "`.agents/agents/<name>.md`"},
		"skills/harness-audit/references/areas.md": {"derive.go", "`agents/*.md`"},
		"skills/handoff/SKILL.md":                  {"Resume Protocol", "either vendor", "Session ID"},
		"skills/review-standards/SKILL.md":         {"the six criteria below"},
		"skills/review/SKILL.md":                   {"all six criteria"},
		"skills/preflight/SKILL.md":                {"Step 2 item 1"},
		"skills/feature/SKILL.md":                  {"a tool-call budget the dispatch names"},
		"skills/bug/SKILL.md":                      {"a tool-call budget the dispatch names"},
		"skills/explain-project/SKILL.md":          {"a tool-call budget the dispatch names"},
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
	for _, path := range []string{"skills/handoff/SKILL.md", "skills/feedback-log/SKILL.md", "skills/retro/SKILL.md", "skills/distill/SKILL.md"} {
		if !strings.Contains(readSkill(t, path), form) {
			t.Errorf("%s does not use the one Session form %s", path, form)
		}
	}
	if !strings.Contains(readSkill(t, "skills/retro/SKILL.md"), "<name>/agent.md") {
		t.Error("retro's subagent row does not name <name>/agent.md")
	}
}
