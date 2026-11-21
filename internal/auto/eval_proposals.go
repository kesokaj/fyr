package auto

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/trail"
)

var proposalSkillRe = regexp.MustCompile(`\bskills/([A-Za-z0-9][A-Za-z0-9_-]*)\b`)

type proposalImpact struct {
	sessions int
	signals  int
	targets  int
	when     time.Time
}

type proposalCandidate struct {
	rec     brain.Record
	entries []string
	score   proposalImpact
}

func (r *run) triggerHarnessEdit(root string) *Task {
	for _, c := range r.collectHarnessCandidates(root, true) {
		if t := preparedTask(ClassHarnessEdit, c.entries, c.rec.ID, len(c.entries)); t != nil {
			t.SignalID, t.ReportDate = extractProposalAttribution(c.rec)
			return t
		}
	}
	return nil
}

func (r *run) collectHarnessCandidates(root string, expireStale bool) []proposalCandidate {
	if !r.isHarnessCheckout(root) {
		return nil
	}
	recs, err := r.sweepOpenProposals()
	if err != nil {
		return nil
	}
	targets := listHarnessTargets(root)
	var cands []proposalCandidate
	for _, rec := range recs {
		if !eligibleHarnessProposal(root, rec) {
			continue
		}
		entries := matchProposalTargets(rec, targets)
		if len(entries) == 0 {
			if expireStale {
				r.expireStaleProposal(root, rec)
			}
			continue
		}
		cands = append(cands, proposalCandidate{
			rec:     rec,
			entries: entries,
			score:   scoreProposalImpact(rec, len(entries)),
		})
	}
	slices.SortFunc(cands, compareProposalCandidates)
	return cands
}

func (r *run) expireStaleProposal(root string, rec brain.Record) {
	missing := missingProposalSkills(root, rec.Title+"\n"+rec.Body)
	if len(missing) == 0 {
		return
	}
	rejected := "rejected"
	note := fmt.Sprintf("## Auto-rejected\n\nTarget skill(s) no longer exist in checkout: %s.", strings.Join(missing, ", "))
	body := strings.TrimRight(rec.Body, "\n") + "\n\n" + note
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	_ = brain.Edit(g, brain.EditInput{ID: rec.ID, Status: &rejected, Body: strings.NewReader(redact.Redact(body))})
}

func missingProposalSkills(root, text string) []string {
	matches := proposalSkillRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var missing []string
	for _, m := range matches {
		slug := m[1]
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		if _, err := os.Stat(filepath.Join(root, "skills", slug, "SKILL.md")); err == nil {
			return nil
		}
		missing = append(missing, "skills/"+slug)
	}
	return missing
}

func scoreProposalImpact(rec brain.Record, targets int) proposalImpact {
	sessions := 0
	signals := map[string]bool{}
	signalLines := 0
	for _, raw := range strings.Split(rec.Body, "\n") {
		line := strings.TrimSpace(raw)
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "sessions:") {
			if rest := strings.Fields(strings.TrimSpace(line[len("sessions:"):])); len(rest) > 0 {
				if n, err := strconv.Atoi(strings.TrimRight(rest[0], ",;")); err == nil && n > sessions {
					sessions = n
				}
			}
		}
		if v, ok := recordFieldVal(line, "signal_id:", "signal:"); ok && v != "" {
			signalLines++
		}
		for _, tok := range strings.FieldsFunc(line, isNonRecordRune) {
			if recordForm.MatchString(tok) && !strings.HasPrefix(tok, "PROPOSAL-") {
				signals[tok] = true
			}
		}
	}
	return proposalImpact{sessions: sessions, signals: len(signals) + signalLines, targets: targets, when: recordTime(rec)}
}

func isNonRecordRune(r rune) bool {
	return !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-')
}

func compareProposalCandidates(a, b proposalCandidate) int {
	if a.score.sessions != b.score.sessions {
		return b.score.sessions - a.score.sessions
	}
	if a.score.signals != b.score.signals {
		return b.score.signals - a.score.signals
	}
	if a.score.targets != b.score.targets {
		return b.score.targets - a.score.targets
	}
	if !a.score.when.Equal(b.score.when) {
		if a.score.when.After(b.score.when) {
			return -1
		}
		return 1
	}
	return strings.Compare(b.rec.ID, a.rec.ID)
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
		if v, ok := recordFieldVal(line, "signal:", "signal_id:"); ok && isValidSignalID(v) {
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
