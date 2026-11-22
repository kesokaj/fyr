package relay

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MaxKickoffBytes caps the Kickoff prompt safely below Linux MAX_ARG_STRLEN (128 KiB).
	MaxKickoffBytes        = 120 << 10
	maxJudgeCriterionBytes = 8 << 10
)

// BuildKickoff builds a self-contained fresh-session Kickoff prompt strictly
// below MaxKickoffBytes while preserving repo path, lease, approvals, and Never rules.
func BuildKickoff(repoDir string, plan ParsedPlan, ph Phase) string {
	head, tail := formatKickoffHeader(repoDir, plan, ph), formatKickoffFooter(ph)
	var tb strings.Builder
	for _, t := range ph.OpenTasks {
		fmt.Fprintf(&tb, "- [ ] %s\n", strings.TrimSpace(t))
	}
	tasks := tb.String()
	if len(head)+len(tasks)+len(tail) < MaxKickoffBytes {
		return head + tasks + tail
	}
	const note = "\n- ... [task list truncated to stay below 120 KiB]\n"
	avail := MaxKickoffBytes - 1 - len(head) - len(tail) - len(note)
	if avail <= 0 {
		headBudget := MaxKickoffBytes - 1 - len(tail) - len(note)
		if headBudget > 0 {
			return truncateUTF8(head, headBudget) + note + tail
		}
		return truncateUTF8(tail, MaxKickoffBytes-1)
	}
	return head + truncateUTF8(tasks, avail) + note + tail
}

func formatKickoffHeader(repoDir string, plan ParsedPlan, ph Phase) string {
	lease := "(no explicit lease; keep edits tightly scoped to this phase)"
	if len(ph.Files) > 0 {
		lease = strings.Join(ph.Files, ", ")
	}
	verifyCmd := strings.TrimSpace(ph.Verify)
	if verifyCmd == "" {
		verifyCmd = "fyr verify"
	}
	return fmt.Sprintf(
		"Project: this clone, the current directory; never another checkout.\n"+
			"RELAY OVERRIDE: Do NOT run git push or git tag (this supersedes global commit-and-push rules).\n"+
			"Plan: %s — %s\n"+
			"Target Phase: %s\n"+
			"Verify: %s\n"+
			"Files: %s\n\n"+
			"Kickoff: Execute one phase of %s in %s.\n"+
			"Repository: %s (work ONLY in this checkout; never another checkout).\n\n"+
			"## Open Tasks\n",
		plan.ID, plan.Title, ph.Title, verifyCmd, lease, plan.ID, repoDir, repoDir)
}

func formatKickoffFooter(ph Phase) string {
	judgeLine := ""
	if ph.Judge != "" {
		j := ph.Judge
		if len(j) > 2048 {
			j = truncateUTF8(j, 2000) + "... [truncated]"
		}
		judgeLine = "- Alignment criterion: " + j + "\n"
	}
	return "\n## Standing Approvals\n" +
		"- Operator pre-approved this phase in the plan; proceed autonomously.\n" +
		"- Run scoped tests first, verify cleanly, and commit all verified changes on the current branch before exiting.\n" +
		"- Leave a clean working tree (`git status --porcelain` empty) with zero forbidden commit trailers.\n" +
		judgeLine +
		"\n## Never\n" +
		"- NEVER modify PRD.md.\n" +
		"- NEVER create git tags or cut releases.\n" +
		"- NEVER run git push or push unverified changes.\n" +
		"- NEVER edit files outside the Files: lease above.\n" +
		"- NEVER ask interactive questions unless a PRD.md or ADR conflict exists.\n" +
		"- NEVER add Co-Authored-By or Claude-Session commit trailers.\n"
}

// BuildJudgeCriterion builds the post-phase alignment criterion for gate.Gate.
func BuildJudgeCriterion(plan ParsedPlan, ph Phase) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Phase alignment check for %s / %s.\n", plan.ID, ph.Title)
	if j := strings.TrimSpace(ph.Judge); j != "" {
		fmt.Fprintf(&b, "Explicit criterion: %s\n", j)
	}
	b.WriteString("Verify the diff satisfies all open tasks below, includes tests, respects PRD.md without editing it, and stays scoped:\n")
	for _, t := range ph.OpenTasks {
		fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(t))
	}
	out := b.String()
	if len(out) <= maxJudgeCriterionBytes {
		return out
	}
	return truncateUTF8(out, maxJudgeCriterionBytes-32) + "\n... [criterion truncated]"
}

func truncateUTF8(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	b := []byte(s[:limit])
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	if idx := strings.LastIndexByte(string(b), '\n'); idx > 0 {
		return string(b[:idx+1])
	}
	return string(b)
}
