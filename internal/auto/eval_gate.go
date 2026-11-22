package auto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
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
	base := o.Home
	if base == "" {
		base = o.realHome()
	}
	return filepath.Join(base, "sandbox", "fyr-auto", "holdout.md")
}

func (r *run) isHarnessCheckout(root string) bool {
	return r.o.Repo != "" && trail.SameDir(root, r.o.Repo)
}

func (r *run) evalGateBranch(ctx context.Context, root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult, manifest *simManifest, workerModel string) (string, []Finding) {
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
	holdoutPath := r.o.holdoutFile()
	seeded, err := ensureHoldoutSuite(r.o.Home, root, holdoutPath)
	if err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), nil)
	}
	var seedFindings []Finding
	if seeded {
		seedFindings = []Finding{{"eval-gate", "holdout-seeded", holdoutPath, "seeded missing holdout suite from .agents/eval/holdout-starter.md"}}
	}
	rows, err := collectGateRows(r.o.Home, root, holdoutPath, changed, t.Class)
	if err != nil {
		return r.rejectEvalGate(root, t, k, runnerWhy, nil, 0, err.Error(), seedFindings)
	}
	rows = appendPredictedGateRows(r.o.Home, root, rows, manifest)
	simCtx := newGateSIMCtx(root, t, snapBefore.head, candSHA, changed, manifest, workerModel)
	v, fs := r.runEvalGateRows(ctx, root, t, k, runner, runnerWhy, snapBefore, snapErr, res, simCtx, rows)
	return v, append(seedFindings, fs...)
}

func ensureHoldoutSuite(home, root, holdoutPath string) (bool, error) {
	if st, err := os.Lstat(holdoutPath); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || st.IsDir() {
			return false, fmt.Errorf("holdout suite %s: invalid file type", holdoutPath)
		}
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if root == "" {
		return false, nil
	}
	data, found, err := readStarterHoldout(root)
	if err != nil || !found {
		return false, err
	}
	if err := ensureSafeHoldoutParent(home, filepath.Dir(holdoutPath)); err != nil {
		return false, err
	}
	if err := harness.WriteAtomic(holdoutPath, data, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func readStarterHoldout(root string) ([]byte, bool, error) {
	openRoot, err := os.OpenRoot(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer openRoot.Close()
	relParts := []string{".agents", filepath.Join(".agents", "eval"), filepath.Join(".agents", "eval", "holdout-starter.md")}
	for i, rel := range relParts {
		st, lerr := openRoot.Lstat(rel)
		if errors.Is(lerr, fs.ErrNotExist) {
			return nil, false, nil
		}
		if lerr != nil {
			return nil, false, lerr
		}
		isLeaf := i == len(relParts)-1
		if st.Mode()&os.ModeSymlink != 0 || (isLeaf && !st.Mode().IsRegular()) || (!isLeaf && !st.IsDir()) {
			return nil, false, fmt.Errorf("holdout starter %s: symlink or invalid file type refused", filepath.Join(root, rel))
		}
	}
	f, err := openRoot.OpenFile(relParts[2], os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > 1<<20 {
		return nil, false, fmt.Errorf("holdout starter %s: exceeds 1 MiB", filepath.Join(root, relParts[2]))
	}
	return data, true, nil
}

func ensureSafeHoldoutParent(home, targetDir string) error {
	cleanTarget := filepath.Clean(targetDir)
	if home != "" {
		cleanHome := filepath.Clean(home)
		if rel, err := filepath.Rel(cleanHome, cleanTarget); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if err := checkLstatChain(cleanHome, rel); err != nil {
				return err
			}
			return os.MkdirAll(cleanTarget, 0o700)
		}
	}
	if err := checkLstatAncestors(cleanTarget); err != nil {
		return err
	}
	return os.MkdirAll(cleanTarget, 0o700)
}

func checkLstatChain(base, rel string) error {
	if err := rejectSymlinkOrNonDir(base); err != nil {
		return err
	}
	if rel == "" || rel == "." {
		return nil
	}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if err := rejectSymlinkOrNonDir(cur); err != nil {
			return err
		}
	}
	return nil
}

func checkLstatAncestors(cleanTarget string) error {
	for cur := cleanTarget; cur != "" && cur != "/" && cur != "." && cur != string(filepath.Separator); {
		if err := rejectSymlinkOrNonDir(cur); err != nil {
			return err
		}
		parent := filepath.Dir(cur)
		if parent == cur || parent == "/tmp" || parent == "/var" || parent == "/private" || parent == "/private/tmp" || parent == "/private/var" {
			break
		}
		cur = parent
	}
	return nil
}

func rejectSymlinkOrNonDir(p string) error {
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("holdout dir %s: symlink or non-directory refused", p)
	}
	return nil
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
		if len(parts) < 2 || parts[0] != "skills" || parts[1] == "" {
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

func (r *run) runEvalGateRows(ctx context.Context, root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult, simCtx *gateSIMCtx, rows []gateRow) (string, []Finding) {
	runsBefore := max(0, r.st.modelRunsToday(r.o.now())-1)
	var findings []Finding
	var scores []gateRowScore
	sessions := 0
	for i, gr := range rows {
		if sessions+2*gateRuns > maxEvalGateSessions {
			reason := fmt.Sprintf("stopped at %d eval sessions (%d of %d rows evaluated)", sessions, len(scores), len(rows))
			verdict := "not_kept"
			if len(scores) == 0 {
				verdict = "skipped"
			}
			return r.rejectEvalGateSIM(root, t, k, runnerWhy, simCtx, scores, sessions, reason, verdict, findings)
		}
		if i > 0 {
			if stopReason := r.betweenEvalRows(root, runsBefore, gr.ID, &findings); stopReason != "" {
				verdict := "not_kept"
				if len(scores) == 0 {
					verdict = "skipped"
				}
				return r.rejectEvalGateSIM(root, t, k, runnerWhy, simCtx, scores, sessions, "stopped between rows: "+stopReason, verdict, findings)
			}
		}
		pOut, cOut, err := r.evalPair(ctx, root, snapBefore.head, scCandSHA(simCtx), gr)
		if err != nil {
			simF := r.recordStageASIM(simCtx, scores, "skipped", scCandSHA(simCtx), "n/a", t.ID, "", "")
			v, af := r.applyFail(runnerWhy, k.Branch, "eval gate: "+err.Error())
			return v, append(append(findings, simF...), af...)
		}
		sessions += 2 * gateRuns
		scores = append(scores, gateRowScore{row: gr, parent: pOut, cand: cOut})
		findings = append(findings, Finding{"eval-gate", "row", gr.ID, fmt.Sprintf("parent %d/%d, candidate %d/%d", pOut.Passed, pOut.Graded, cOut.Passed, cOut.Graded)})
		if why := rowKeepError(gr, pOut, cOut); why != "" {
			return r.rejectEvalGateSIM(root, t, k, runnerWhy, simCtx, scores, sessions, why, "not_kept", findings)
		}
	}
	return r.applyKeptGateBranchSIM(root, t, k, runner, runnerWhy, snapBefore, snapErr, res, simCtx, scores, sessions, findings)
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
	return r.applyKeptGateBranchSIM(root, t, k, runner, runnerWhy, snapBefore, snapErr, res, nil, scores, sessions, gateFindings)
}

func (r *run) rejectEvalGate(root string, t Task, k Kickoff, runnerWhy string, scores []gateRowScore, sessions int, reason string, gateFindings []Finding) (string, []Finding) {
	propID := r.fileEvalGateProposal(root, t, k.Branch, scores, sessions, reason)
	msg := withRunner(fmt.Sprintf("eval-gate not kept (%s); proposal %s; branch %s kept", oneLine(reason), propID, k.Branch), runnerWhy)
	gateFindings = append(gateFindings, Finding{"eval-gate", "not-kept", k.Branch, msg})
	return msg, gateFindings
}
