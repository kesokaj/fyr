package auto

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/trail"
)

func (r *run) triggerHarnessEdit(root string) *Task {
	if !r.isHarnessCheckout(root) {
		return nil
	}
	recs, err := r.sweepOpenProposals()
	if err != nil {
		return nil
	}
	targets := listHarnessTargets(root)
	for _, rec := range recs {
		if !eligibleHarnessProposal(root, rec) {
			continue
		}
		if entries := matchProposalTargets(rec, targets); len(entries) > 0 {
			if t := preparedTask(ClassHarnessEdit, entries, rec.ID, len(entries)); t != nil {
				return t
			}
		}
	}
	return nil
}

func eligibleHarnessProposal(root string, rec brain.Record) bool {
	lowTitle := strings.ToLower(strings.TrimSpace(rec.Title))
	if rec.Status != "queued" || strings.HasPrefix(lowTitle, "auto ask:") || strings.Contains(lowTitle, "missing skill") {
		return false
	}
	if strings.Contains(rec.Body, "## Eval gate") || strings.Contains(strings.ToLower(rec.Body), "missing-skill:") {
		return false
	}
	return isHarnessTargetProposal(root, rec) && hasProposalEvidence(rec.Body)
}

func isHarnessTargetProposal(root string, rec brain.Record) bool {
	lowBody := strings.ToLower(rec.Body)
	if strings.Contains(lowBody, "target: project") || strings.Contains(lowBody, "scope: project") {
		return false
	}
	if strings.Contains(lowBody, "target: harness") || strings.Contains(lowBody, "scope: harness") {
		return true
	}
	lowTitle := strings.ToLower(rec.Title)
	if strings.HasPrefix(lowTitle, "dream, harness") || strings.HasPrefix(lowTitle, "auto, harness") {
		return true
	}
	return rec.Project != "" && trail.SameDir(rec.Project, root)
}

func hasProposalEvidence(body string) bool {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "evidence:") && strings.TrimSpace(line[len("evidence:"):]) != "" {
			return true
		}
		if strings.HasPrefix(low, "sessions:") {
			if rest := strings.Fields(strings.TrimSpace(line[len("sessions:"):])); len(rest) > 0 {
				if n, err := strconv.Atoi(strings.TrimRight(rest[0], ",;")); err == nil && n >= 2 {
					return true
				}
			}
		}
		for _, tok := range strings.FieldsFunc(line, func(r rune) bool {
			return !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-')
		}) {
			if recordForm.MatchString(tok) && !strings.HasPrefix(tok, "PROPOSAL-") {
				return true
			}
		}
	}
	return false
}

func matchProposalTargets(rec brain.Record, targets []harnessTarget) []string {
	text := rec.Title + "\n" + rec.Body
	var byPath, byWord []string
	for _, tgt := range targets {
		entry := filepath.ToSlash(tgt.relPath) + "/"
		if Denied(entry) {
			continue
		}
		if strings.Contains(text, tgt.relPath) && !slices.Contains(byPath, entry) {
			byPath = append(byPath, entry)
		}
		if mentionsWord(text, tgt.name) && !slices.Contains(byWord, entry) {
			byWord = append(byWord, entry)
		}
	}
	if len(byPath) > 0 {
		return byPath
	}
	return byWord
}

func (r *run) fileEvalGateProposal(root string, t Task, branch string, scores []gateRowScore, sessions int, reason string) string {
	section := formatEvalGateSection(branch, scores, sessions, reason)
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	if t.ID != "" {
		if recs, _, err := brain.Get(g, []string{t.ID}); err == nil && len(recs) == 1 {
			queued := "queued"
			body := strings.TrimRight(recs[0].Body, "\n") + "\n\n" + section
			if brain.Edit(g, brain.EditInput{ID: t.ID, Status: &queued, Body: strings.NewReader(redact.Redact(body))}) == nil {
				return t.ID
			}
		}
	}
	body := fmt.Sprintf("Target: harness\nClass: %s\nProvenance: derived\n\n%s", t.Class, section)
	id, err := brain.Add(g, brain.AddInput{
		Kind: "proposal", Status: "queued", Project: root,
		Title: fmt.Sprintf("Auto, harness — %s not kept (%s)", t.Class, oneLine(branch)),
		Body:  strings.NewReader(redact.Redact(body)),
	})
	if err != nil {
		r.failed = true
		return "failed"
	}
	r.st.OpenAsks++
	return id
}

func formatEvalGateSection(branch string, scores []gateRowScore, sessions int, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Eval gate\n\n- verdict: not kept (%s)\n- branch: `%s`\n- sessions: %d eval sessions\n", oneLine(reason), oneLine(branch), sessions)
	if len(scores) > 0 {
		b.WriteString("\n| row | suite | parent | candidate |\n|---|---|---|---|\n")
		for _, s := range scores {
			suite := "GUIDE-eval"
			if s.row.IsHoldout {
				suite = "holdout"
			} else if s.row.IsRegression {
				suite = "regression"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %d/%d (%d errors) | %d/%d (%d errors) |\n",
				s.row.ID, suite, s.parent.Passed, s.parent.Graded, s.parent.Errors, s.cand.Passed, s.cand.Graded, s.cand.Errors)
		}
	}
	return b.String()
}
