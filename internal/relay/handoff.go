package relay

import (
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

// RecordPassHandoff stores an immutable checkpoint handoff record in brain.db.
func RecordPassHandoff(opts Options, root string, plan ParsedPlan, res PhaseResult, nextPhase string) (string, error) {
	title := sanitizeTitle(fmt.Sprintf("Relay PASS %s (%s)", res.PhaseID, plan.ID))
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nPlan: %s\nPhase: %s\nRelay-Outcome: PASS\nCommits: %s..%s\n",
		title, plan.ID, sanitizeLine(res.PhaseTitle), shortSHA(res.HeadBefore), shortSHA(res.HeadAfter))
	if len(res.ChangedFiles) > 0 {
		fmt.Fprintf(&b, "Changed-Files: %s\n", sanitizeLine(strings.Join(res.ChangedFiles, ", ")))
	}
	fmt.Fprintf(&b, "Verify: PASS (%s)\n", shortSHA(res.VerifyTreeSHA, 12))
	if res.JudgeModel != "" {
		fmt.Fprintf(&b, "Judge: %s=%s\n", sanitizeLine(res.JudgeModel), sanitizeLine(res.JudgeVerdict))
	}
	fmt.Fprintf(&b, "Next-Phase: %s\n", sanitizeLine(nextPhase))
	return writeHandoffRecord(opts, root, title, b.String())
}

// RecordHaltHandoff stores an immutable diagnostic handoff record in brain.db on halt.
func RecordHaltHandoff(opts Options, root string, plan ParsedPlan, ph Phase, res PhaseResult, dirty []string) (string, error) {
	title := sanitizeTitle(fmt.Sprintf("Relay HALT %s %s (%s)", res.HaltReason, ph.ID, plan.ID))
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nPlan: %s\nPhase: %s\nRelay-Outcome: HALTED\nRelay-Halt-Reason: %s\n",
		title, plan.ID, sanitizeLine(ph.Title), sanitizeLine(res.HaltReason))
	if res.ErrorHash != "" {
		fmt.Fprintf(&b, "Relay-Error-Hash: %s\n", res.ErrorHash)
	}
	if res.HeadBefore != "" {
		fmt.Fprintf(&b, "Commits: %s..%s\n", shortSHA(res.HeadBefore), shortSHA(res.HeadAfter))
	}
	if len(res.ChangedFiles) > 0 {
		fmt.Fprintf(&b, "Changed-Files: %s\n", sanitizeLine(strings.Join(res.ChangedFiles, ", ")))
	}
	if len(dirty) > 0 {
		fmt.Fprintf(&b, "Dirty-Entries: %s\n", sanitizeLine(strings.Join(dirty, "; ")))
	}
	fmt.Fprintf(&b, "\n## Diagnostic\n\n%s\n\n## Recovery\n\n%s\n",
		sanitizeBlock(redact.Redact(res.Detail)), recoveryHint(res.HaltReason, res.HeadBefore, plan.ID))
	return writeHandoffRecord(opts, root, title, b.String())
}

func writeHandoffRecord(opts Options, root, title, body string) (string, error) {
	return brain.Add(
		brain.Options{Home: opts.Home, Dir: root, Now: opts.Now},
		brain.AddInput{Kind: "handoff", Title: title, Body: strings.NewReader(body)},
	)
}

func recoveryHint(reason, headBefore, planID string) string {
	switch reason {
	case HaltPRDModified:
		return fmt.Sprintf("Inspect commits from %s, revert PRD.md edits (approval + ADR required), then re-run `fyr relay --plan %s`.", shortSHA(headBefore), planID)
	case HaltTagCreated:
		return fmt.Sprintf("Delete unauthorized local git tag(s), inspect commits from %s, then re-run `fyr relay --plan %s`.", shortSHA(headBefore), planID)
	case HaltPushDetected:
		return fmt.Sprintf("Inspect remote-tracking refs and upstream history for unauthorized push since %s before resuming `fyr relay --plan %s`.", shortSHA(headBefore), planID)
	case HaltHistoryRewrite:
		return fmt.Sprintf("HEAD diverged from pre-phase commit %s (rewrite/reset detected); inspect `git reflog`, restore linear history, then re-run `fyr relay --plan %s`.", shortSHA(headBefore), planID)
	case HaltPlanTampered:
		return fmt.Sprintf("Plan %s changed outside checkbox markers during relay; inspect `fyr record get %s`, restore the approved plan body, then re-run `fyr relay --plan %s`.", planID, planID, planID)
	case HaltBudgetFloor:
		return fmt.Sprintf("Wait for Gemini quota pools above %d%% 5h / %d%% weekly, refresh `fyr models --refresh`, then resume `fyr relay --plan %s`.", GeminiFloorFive, GeminiFloorWeek, planID)
	case HaltRepeatingErrorHash:
		return fmt.Sprintf("Same normalized failure repeated twice. Resolve root cause manually, then resume `fyr relay --plan %s`.", planID)
	default:
		return fmt.Sprintf("Inspect `git status` and commits since %s, fix or reset the failing phase, verify cleanly, then re-run `fyr relay --plan %s`.", shortSHA(headBefore), planID)
	}
}

func sanitizeTitle(s string) string {
	s = sanitizeLine(s)
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func sanitizeLine(s string) string { return strings.Join(strings.Fields(search.Printable(s)), " ") }

func sanitizeBlock(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = search.Printable(l)
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if out == "" {
		return "(no additional diagnostic detail)"
	}
	if len(out) > 16<<10 {
		return truncateUTF8(out, 16<<10) + "\n... [diagnostic truncated]"
	}
	return out
}

func shortSHA(sha string, n ...int) string {
	limit := 8
	if len(n) > 0 && n[0] > 0 {
		limit = n[0]
	}
	if len(sha) <= limit {
		return sha
	}
	return sha[:limit]
}
