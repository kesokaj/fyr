package auto

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/work"
)

const (
	gateRuns            = 3
	maxEvalGateSessions = 24
	miniAuditInterval   = 7 * 24 * time.Hour
)

type gateRow struct {
	ID, SuiteFile           string
	IsRegression, IsHoldout bool
}

type gateRowScore struct {
	row          gateRow
	parent, cand EvalOutcome
}

func (o Options) holdoutFile() string {
	if o.HoldoutFile != "" {
		return o.HoldoutFile
	}
	return filepath.Join(o.realHome(), "sandbox", "fyr-auto", "holdout.md")
}

func (r *run) isHarnessCheckout(root string) bool {
	return r.cfg.Level == LevelHarness && r.o.Repo != "" && sameDir(root, r.o.Repo)
}

func (r *run) triggerHarnessEdit(root string) *Task {
	if !r.isHarnessCheckout(root) {
		return nil
	}
	recs, err := brain.List(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.ListFilter{Kind: "proposal", Open: true})
	if err != nil {
		return nil
	}
	targets := listHarnessTargets(root)
	for _, rec := range recs {
		if !eligibleHarnessProposal(root, rec) {
			continue
		}
		if entries := matchProposalTargets(rec, targets); len(entries) > 0 {
			if t := preparedTask(ClassHarnessEdit, LevelHarness, entries, rec.ID, len(entries)); t != nil {
				return t
			}
		}
	}
	return nil
}

func (r *run) triggerMiniAudit(root string, p *Project) *Task {
	if !r.isHarnessCheckout(root) {
		return nil
	}
	if p.LastMiniAudit.IsZero() {
		if !r.dry {
			p.LastMiniAudit = r.o.now()
		}
		return nil
	}
	age := r.o.now().Sub(p.LastMiniAudit)
	if age < miniAuditInterval {
		return nil
	}
	return preparedTask(ClassMiniAudit, LevelHarness, nil, "", int(age/(24*time.Hour)))
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
	return rec.Project != "" && sameDir(rec.Project, root)
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

func (r *run) evalGateBranch(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res work.Result) (string, []Finding) {
	if snapErr != nil || (res.Base != "" && res.Base != snapBefore.head) {
		return r.checkoutMovedFail(runnerWhy, t.Class, k.Branch)
	}
	if err := verifyBranchForApply(root, snapBefore.head, k.Branch, t.Class); err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), nil)
	}
	candSHA, err := applyGit(root, nil, "rev-parse", "--verify", "refs/heads/"+k.Branch)
	if err != nil || candSHA == "" {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, "missing candidate branch "+k.Branch, nil)
	}
	changed, err := changedHarnessTargets(root, snapBefore.head, "refs/heads/"+k.Branch)
	if err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), nil)
	}
	rows, err := collectGateRows(r.o.Home, root, r.o.holdoutFile(), changed)
	if err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), nil)
	}
	return r.runEvalGateRows(root, t, k, runner, runnerWhy, snapBefore, snapErr, res, candSHA, rows)
}

func changedHarnessTargets(root, base, ref string) ([]string, error) {
	out, err := applyGit(root, nil, "diff", "--name-only", base, ref)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, raw := range strings.Split(out, "\n") {
		p := strings.TrimSpace(filepath.ToSlash(raw))
		if p == "" || p == ".agents/trail.jsonl" {
			continue
		}
		parts := strings.Split(p, "/")
		if len(parts) < 2 || (parts[0] != "skills" && parts[0] != "agents") || parts[1] == "" || parts[1] == "google-catalog" {
			return nil, fmt.Errorf("candidate touched %s outside skills/<s>/ and agents/<a>/", p)
		}
		if !slices.Contains(names, parts[1]) {
			names = append(names, parts[1])
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("candidate changed no skill or agent")
	}
	return names, nil
}

func collectGateRows(home, root, holdoutPath string, changed []string) ([]gateRow, error) {
	suite, err := readCheckoutSuite(home, root)
	if err != nil {
		return nil, err
	}
	holdout, err := eval.ReadSuiteFile(holdoutPath)
	if err != nil {
		return nil, fmt.Errorf("holdout suite: %w", err)
	}
	if len(holdout) == 0 {
		return nil, fmt.Errorf("holdout suite %s has no rows", filepath.Base(holdoutPath))
	}
	var rows []gateRow
	seen := map[string]bool{}
	for _, r := range suite {
		if slices.ContainsFunc(changed, func(name string) bool { return rowNamesTarget(r, name) }) {
			rows = append(rows, gateRow{ID: r.ID, IsRegression: r.Kind == eval.KindRegression})
			seen[r.ID] = true
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no GUIDE-eval row names %s", strings.Join(changed, ", "))
	}
	for _, r := range suite {
		if r.Kind == eval.KindRegression && !seen[r.ID] {
			rows = append(rows, gateRow{ID: r.ID, IsRegression: true})
			seen[r.ID] = true
		}
	}
	for _, r := range holdout {
		rows = append(rows, gateRow{ID: r.ID, SuiteFile: holdoutPath, IsHoldout: true, IsRegression: r.Kind == eval.KindRegression})
	}
	return rows, nil
}

func (r *run) runEvalGateRows(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res work.Result, candSHA string, rows []gateRow) (string, []Finding) {
	runsBefore := max(0, r.st.modelRunsToday(r.o.now())-1)
	var findings []Finding
	var scores []gateRowScore
	sessions := 0
	for i, gr := range rows {
		if sessions+2*gateRuns > maxEvalGateSessions {
			reason := fmt.Sprintf("stopped at %d eval sessions (%d of %d rows evaluated)", sessions, len(scores), len(rows))
			return r.rejectEvalGate(root, t, k, runnerWhy, scores, sessions, reason, findings)
		}
		if i > 0 {
			if stopReason := r.betweenEvalRows(root, runsBefore, gr.ID, &findings); stopReason != "" {
				return r.rejectEvalGate(root, t, k, runnerWhy, scores, sessions, "stopped between rows: "+stopReason, findings)
			}
		}
		pOut, cOut, err := r.evalPair(root, snapBefore.head, candSHA, gr)
		if err != nil {
			return r.applyFail(runnerWhy, k.Branch, "eval gate: "+err.Error())
		}
		sessions += 2 * gateRuns
		scores = append(scores, gateRowScore{row: gr, parent: pOut, cand: cOut})
		findings = append(findings, Finding{"eval-gate", "row", gr.ID, fmt.Sprintf("parent %d/%d, candidate %d/%d", pOut.Passed, pOut.Graded, cOut.Passed, cOut.Graded)})
		if why := rowKeepError(gr, pOut, cOut); why != "" {
			return r.rejectEvalGate(root, t, k, runnerWhy, scores, sessions, why, findings)
		}
	}
	return r.applyKeptGateBranch(root, t, k, runner, runnerWhy, snapBefore, snapErr, res, scores, sessions, findings)
}

func (r *run) evalPair(root, parentSHA, candSHA string, gr gateRow) (EvalOutcome, EvalOutcome, error) {
	pReq := EvalRequest{Dir: root, Home: r.o.evalHome(), Fixture: r.o.evalFixture(root), SuiteFile: gr.SuiteFile, Commit: parentSHA, Runs: gateRuns, Row: gr.ID}
	pOut, err := r.runEvalRow(pReq)
	if err != nil {
		return EvalOutcome{}, EvalOutcome{}, err
	}
	cReq := pReq
	cReq.Commit = candSHA
	cOut, err := r.runEvalRow(cReq)
	return pOut, cOut, err
}

func rowKeepError(gr gateRow, parent, cand EvalOutcome) string {
	if parent.Errors > 0 || cand.Errors > 0 || parent.Graded < gateRuns || cand.Graded < gateRuns {
		return fmt.Sprintf("row %s had eval errors (parent %d/%d %d err, candidate %d/%d %d err)", gr.ID, parent.Passed, parent.Graded, parent.Errors, cand.Passed, cand.Graded, cand.Errors)
	}
	if gr.IsHoldout && cand.Passed < gateRuns {
		return fmt.Sprintf("held-out row %s did not hold (%d/%d vs parent %d/%d)", gr.ID, cand.Passed, cand.Graded, parent.Passed, parent.Graded)
	}
	if gr.IsRegression && cand.Passed < gateRuns {
		return fmt.Sprintf("regression row %s failed (%d/%d vs parent %d/%d)", gr.ID, cand.Passed, cand.Graded, parent.Passed, parent.Graded)
	}
	if cand.Passed < parent.Passed || cand.Passed == 0 {
		return fmt.Sprintf("row %s pass count fell (%d/%d vs parent %d/%d)", gr.ID, cand.Passed, cand.Graded, parent.Passed, parent.Graded)
	}
	return ""
}

func (r *run) applyKeptGateBranch(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res work.Result, scores []gateRowScore, sessions int, gateFindings []Finding) (string, []Finding) {
	v, af := r.applyBranch(root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
	if r.appliedSHA == "" {
		propID := r.fileEvalGateProposal(root, t, k.Branch, scores, sessions, v)
		v = fmt.Sprintf("%s; proposal %s", v, propID)
		return v, append(gateFindings, af...)
	}
	// D19 item 3: auto never installs into a real home; the report says install pending.
	r.installPending = true
	if t.ID != "" {
		status := "applied:" + r.appliedSHA
		_ = brain.Edit(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.EditInput{ID: t.ID, Status: &status})
	}
	v += "; install pending"
	for i := range af {
		if af[i].Check == "worker" {
			af[i].Text += "; install pending"
		}
	}
	return v, append(gateFindings, af...)
}

func (r *run) rejectEvalGate(root string, t Task, k Kickoff, runnerWhy string, scores []gateRowScore, sessions int, reason string, gateFindings []Finding) (string, []Finding) {
	propID := r.fileEvalGateProposal(root, t, k.Branch, scores, sessions, reason)
	msg := withRunner(fmt.Sprintf("eval-gate not kept (%s); proposal %s; branch %s kept", oneLine(reason), propID, k.Branch), runnerWhy)
	gateFindings = append(gateFindings, Finding{"eval-gate", "not-kept", k.Branch, msg})
	return msg, gateFindings
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
