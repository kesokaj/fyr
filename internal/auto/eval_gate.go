package auto

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	gateRuns            = 3
	maxEvalGateSessions = 24
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
	return r.o.Repo != "" && trail.SameDir(root, r.o.Repo)
}

func (r *run) evalGateBranch(ctx context.Context, root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult) (string, []Finding) {
	if snapErr != nil || (res.Base != "" && res.Base != snapBefore.head) {
		return r.checkoutMovedFail(runnerWhy, t.Class, k.Branch)
	}
	if err := verifyBranchForApply(root, snapBefore.head, k.Branch, t.Class); err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), nil)
	}
	candSHA, err := applyGit(root, nil, "rev-parse", "--verify", "--end-of-options", "refs/heads/"+k.Branch)
	if err != nil || candSHA == "" {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, "missing candidate branch "+k.Branch, nil)
	}
	changed, err := changedHarnessTargets(root, snapBefore.head, "refs/heads/"+k.Branch)
	if err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), nil)
	}
	rows, err := collectGateRows(r.o.Home, root, r.o.holdoutFile(), changed, t.Class)
	if err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), nil)
	}
	return r.runEvalGateRows(ctx, root, t, k, runner, runnerWhy, snapBefore, snapErr, res, candSHA, rows)
}

func changedHarnessTargets(root, base, ref string) ([]string, error) {
	out, err := applyGit(root, nil, "diff", "--name-only", "--end-of-options", base, ref, "--")
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
		if len(parts) < 2 || parts[0] != "skills" || parts[1] == "" || parts[1] == "google-catalog" {
			return nil, fmt.Errorf("candidate touched %s outside skills/<s>/", p)
		}
		if !slices.Contains(names, parts[1]) {
			names = append(names, parts[1])
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("candidate changed no skill")
	}
	return names, nil
}

func collectGateRows(home, root, holdoutPath string, changed []string, class string) ([]gateRow, error) {
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
	if len(rows) == 0 && class != ClassDistill {
		return nil, fmt.Errorf("no GUIDE-eval row names %s", strings.Join(changed, ", "))
	}
	for _, r := range holdout {
		rows = append(rows, gateRow{ID: r.ID, SuiteFile: holdoutPath, IsHoldout: true, IsRegression: r.Kind == eval.KindRegression})
	}
	for _, r := range suite {
		if r.Kind == eval.KindRegression && !seen[r.ID] {
			rows = append(rows, gateRow{ID: r.ID, IsRegression: true})
			seen[r.ID] = true
		}
	}
	return rows, nil
}

func (r *run) runEvalGateRows(ctx context.Context, root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult, candSHA string, rows []gateRow) (string, []Finding) {
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
		pOut, cOut, err := r.evalPair(ctx, root, snapBefore.head, candSHA, gr)
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

func (r *run) evalPair(ctx context.Context, root, parentSHA, candSHA string, gr gateRow) (EvalOutcome, EvalOutcome, error) {
	pReq := EvalRequest{Dir: root, Home: r.o.evalHome(), Fixture: r.o.evalFixture(root), SuiteFile: gr.SuiteFile, Commit: parentSHA, Runs: gateRuns, Row: gr.ID}
	pOut, err := r.runEvalRow(ctx, pReq)
	if err != nil {
		return EvalOutcome{}, EvalOutcome{}, err
	}
	cReq := pReq
	cReq.Commit = candSHA
	cOut, err := r.runEvalRow(ctx, cReq)
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

func (r *run) applyKeptGateBranch(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult, scores []gateRowScore, sessions int, gateFindings []Finding) (string, []Finding) {
	v, af := r.applyBranch(root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
	if r.appliedSHA == "" {
		propID := r.fileEvalGateProposal(root, t, k.Branch, scores, sessions, v)
		v = fmt.Sprintf("%s; proposal %s", v, propID)
		return v, append(gateFindings, af...)
	}
	// D19 item 3: auto never installs into a real home; the report says install pending.
	r.installPending = true
	warn := ""
	if t.ID != "" {
		status := "applied:" + r.appliedSHA
		if err := brain.Edit(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.EditInput{ID: t.ID, Status: &status}); err != nil {
			warn = "; warning: proposal " + t.ID + " update failed: " + oneLine(err.Error())
		} else if r.st.OpenAsks > 0 {
			r.st.OpenAsks--
		}
	}
	suffix := "; install pending" + warn
	v += suffix
	for i := range af {
		if af[i].Check == "worker" {
			af[i].Text += suffix
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
