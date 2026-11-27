package auto

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
)

type gateSIMCtx struct {
	root        string
	task        Task
	parentSHA   string
	candSHA     string
	changed     []string
	files       []string
	manifest    *simManifest
	workerModel string
}

type gateScoreGrade struct {
	fixed     []string
	missed    []string
	regressed []string
	passed    int
	total     int
}

func newGateSIMCtx(root string, t Task, parentSHA, candSHA string, changed []string, manifest *simManifest, workerModel string) *gateSIMCtx {
	return &gateSIMCtx{
		root: root, task: t, parentSHA: parentSHA, candSHA: candSHA,
		changed: changed, files: changedHarnessFiles(root, parentSHA, candSHA, changed),
		manifest: manifest, workerModel: strings.TrimSpace(workerModel),
	}
}

func scCandSHA(sc *gateSIMCtx) string {
	if sc == nil {
		return ""
	}
	return sc.candSHA
}

func changedHarnessFiles(root, base, ref string, changed []string) []string {
	var files []string
	if out, err := applyGit(root, nil, "diff", "--name-only", "--end-of-options", base, ref, "--"); err == nil {
		for _, raw := range strings.Split(out, "\n") {
			p := strings.TrimSpace(filepath.ToSlash(raw))
			if p != "" && p != ".agents/trail.jsonl" && strings.HasPrefix(p, "skills/") && !slices.Contains(files, p) {
				files = append(files, p)
			}
		}
	}
	if len(files) == 0 {
		for _, name := range changed {
			if name != "" {
				files = append(files, "skills/"+name+"/")
			}
		}
	}
	return files
}

func appendPredictedGateRows(home, root string, rows []gateRow, manifest *simManifest) []gateRow {
	if manifest == nil || len(manifest.PredictedRows) == 0 {
		return rows
	}
	suite, err := readCheckoutSuite(home, root)
	if err != nil {
		return rows
	}
	byID := make(map[string]eval.Row, len(suite))
	for _, sr := range suite {
		byID[sr.ID] = sr
	}
	seen := make(map[string]bool, len(rows))
	for _, gr := range rows {
		seen[gr.ID] = true
	}
	for _, id := range manifest.PredictedRows {
		sr, ok := byID[id]
		if !ok || seen[id] {
			continue
		}
		if (len(rows)+1)*2*gateRuns > maxEvalGateSessions {
			break
		}
		rows = append(rows, gateRow{ID: sr.ID, IsRegression: sr.Kind == eval.KindRegression})
		seen[id] = true
	}
	return rows
}

func gradeGateScores(scores []gateRowScore, predictedRows []string) gateScoreGrade {
	var g gateScoreGrade
	evaluated := make(map[string]bool, len(scores))
	fixedSet := make(map[string]bool, len(scores))
	for _, s := range scores {
		g.total++
		id := s.row.ID
		evaluated[id] = true
		if rowKeepError(s.row, s.parent, s.cand) == "" {
			g.passed++
		}
		if s.cand.Passed > s.parent.Passed && s.cand.Errors == 0 && !fixedSet[id] {
			fixedSet[id] = true
			g.fixed = append(g.fixed, id)
		}
		if (s.cand.Passed < s.parent.Passed || (s.cand.Errors > 0 && s.parent.Errors == 0)) && !slices.Contains(g.regressed, id) {
			g.regressed = append(g.regressed, id)
		}
	}
	for _, pid := range predictedRows {
		if evaluated[pid] && !fixedSet[pid] && !slices.Contains(g.missed, pid) {
			g.missed = append(g.missed, pid)
		}
	}
	return g
}

func (r *run) resolveGatePolicyModel(scores []gateRowScore) string {
	for _, s := range scores {
		if m := strings.TrimSpace(s.cand.PolicyModel); m != "" {
			return m
		}
	}
	for _, s := range scores {
		if m := strings.TrimSpace(s.parent.PolicyModel); m != "" {
			return m
		}
	}
	return dominantModelLast7d(r.o.Home, r.o.now())
}

func dominantModelLast7d(home string, now time.Time) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	dbPath := metrics.Path(home)
	if _, err := os.Lstat(dbPath); err != nil {
		return ""
	}
	s, err := metrics.Open(dbPath)
	if err != nil {
		return ""
	}
	defer s.Close()
	m, err := metrics.DominantModelSince(s, now.UTC().Add(-7*24*time.Hour))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(m)
}

func resolveHarnessVersion(root, parentSHA string) string {
	if ver, err := applyGit(root, nil, "describe", "--tags", "--always"); err == nil && ver != "" {
		return strings.TrimSpace(oneLine(ver))
	}
	return shortSHA(parentSHA)
}

func (r *run) applyKeptGateBranchSIM(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult, sc *gateSIMCtx, scores []gateRowScore, sessions int, gateFindings []Finding) (string, []Finding) {
	v, af := r.applyBranch(root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
	if r.appliedSHA == "" {
		propID := r.fileEvalGateProposal(root, t, k.Branch, scores, sessions, v)
		simF := r.recordStageASIM(sc, scores, "not_kept", scCandSHA(sc), "n/a", propID, "", v)
		return fmt.Sprintf("%s; proposal %s", v, propID), append(append(gateFindings, simF...), af...)
	}
	policyModel := ""
	if sc != nil {
		policyModel = r.resolveGatePolicyModel(scores)
	}
	v, af = r.finishKeptGateApply(root, t, policyModel, v, af)
	simF := r.recordStageASIM(sc, scores, "kept", r.appliedSHA, "pending_data", t.ID, policyModel, "")
	return v, append(append(gateFindings, simF...), af...)
}

func (r *run) rejectEvalGateSIM(root string, t Task, k Kickoff, runnerWhy string, sc *gateSIMCtx, scores []gateRowScore, sessions int, reason, evalVerdict string, gateFindings []Finding) (string, []Finding) {
	propID := r.fileEvalGateProposal(root, t, k.Branch, scores, sessions, reason)
	simF := r.recordStageASIM(sc, scores, evalVerdict, scCandSHA(sc), "n/a", propID, "", "")
	msg := withRunner(fmt.Sprintf("eval-gate not kept (%s); proposal %s; branch %s kept", oneLine(reason), propID, k.Branch), runnerWhy)
	gateFindings = append(gateFindings, simF...)
	gateFindings = append(gateFindings, Finding{"eval-gate", "not-kept", k.Branch, msg})
	return msg, gateFindings
}

func (r *run) recordStageASIM(sc *gateSIMCtx, scores []gateRowScore, evalVerdict, commitSHA, fieldStatus, proposalID, policyModel, applyFail string) []Finding {
	if sc == nil {
		return nil
	}
	var out []Finding
	if sc.manifest == nil && (sc.task.Class == ClassHarnessEdit || sc.task.Class == ClassDistill) {
		out = append(out, Finding{"draft", "missing-sim-manifest", sc.task.Class, "worker emitted no sim_manifest; recorded deterministic fallback attempt"})
	}
	if policyModel == "" {
		policyModel = r.resolveGatePolicyModel(scores)
	}
	in := r.buildStageASIMInput(sc, scores, evalVerdict, commitSHA, fieldStatus, proposalID, policyModel, applyFail)
	if _, err := brain.RecordSIMAttempt(r.o.Home, in); err != nil {
		out = append(out, Finding{Check: "sim", Rule: "record-failed", Where: sc.task.Class, Text: oneLine(err.Error())})
	}
	return out
}

func (r *run) buildStageASIMInput(sc *gateSIMCtx, scores []gateRowScore, evalVerdict, commitSHA, fieldStatus, proposalID, policyModel, applyFail string) brain.SIMRecordInput {
	problemID, rootCause, mechClass, what, predRows, predSigs := r.stageAManifestMeta(sc)
	if applyFail != "" {
		what = appendSIMApplyFailure(what, applyFail)
	}
	grade := gradeGateScores(scores, predRows)
	sigID := simSignalID(sc.task, sc.manifest)
	var fieldMetric string
	var fieldBase *float64
	if evalVerdict == "kept" {
		fieldMetric, fieldBase = captureSignalBaseline(r.projectSummary(sc.root), sigID)
	}
	if commitSHA == "applied-mock" {
		commitSHA = sc.candSHA
	}
	if proposalID == "failed" {
		proposalID = ""
	}
	scope := ""
	if r.isHarnessCheckout(sc.root) {
		scope = "harness"
	}
	return brain.SIMRecordInput{
		ProblemID: problemID, RootCause: rootCause, MechanismClass: mechClass,
		SignalID: sigID, SourceKind: simSourceKind(sc.task.Class),
		ProposalID: cmp.Or(sc.task.ID, proposalID), CommitSHA: commitSHA, ParentSHA: sc.parentSHA,
		HarnessVersion: resolveHarnessVersion(sc.root, sc.parentSHA),
		ProposerModel:  sc.workerModel, PolicyModel: policyModel, ProjectScope: scope,
		Files: sc.files, What: what, PredictedRows: predRows, PredictedSignals: predSigs,
		EvalVerdict: evalVerdict, FixedRows: grade.fixed, MissedRows: grade.missed,
		RegressedRows: grade.regressed, EvalPassed: grade.passed, EvalTotal: grade.total,
		FieldStatus: fieldStatus, FieldMetric: fieldMetric, FieldBaseline: fieldBase,
		Provenance: "derived", Now: r.o.now(),
	}
}

func (r *run) stageAManifestMeta(sc *gateSIMCtx) (problemID, rootCause, mechClass, what string, predRows, predSigs []string) {
	if sc.manifest != nil {
		problemID = sc.manifest.ProblemID
		if problemID == "" {
			rootCause = sc.manifest.RootCause
		}
		return problemID, rootCause, sc.manifest.MechanismClass, sc.manifest.Rationale, sc.manifest.PredictedRows, sc.manifest.PredictedSignals
	}
	rootCause, what = r.fallbackSIMMeta(sc)
	return "", rootCause, "prompt_guidance", what, nil, nil
}

func (r *run) fallbackSIMMeta(sc *gateSIMCtx) (rootCause, what string) {
	title := r.lookupProposalTitle(sc.task.ID)
	var skillPaths []string
	for _, s := range sc.changed {
		if s != "" {
			skillPaths = append(skillPaths, "skills/"+s+"/")
		}
	}
	base := cmp.Or(title, sc.task.SignalID)
	if base == "" {
		base = fmt.Sprintf("auto %s on %s", sc.task.Class, strings.Join(skillPaths, ", "))
	}
	var extras []string
	if sc.task.SignalID != "" && !strings.Contains(base, sc.task.SignalID) {
		extras = append(extras, sc.task.SignalID)
	}
	for _, sp := range skillPaths {
		if !strings.Contains(base, sp) {
			extras = append(extras, sp)
		}
	}
	if len(extras) > 0 {
		base += " (" + strings.Join(extras, "; ") + ")"
	}
	rootCause = strings.TrimSpace(trail.TruncateUTF8(strings.TrimSpace(oneLine(base)), simManifestMaxRootCauseBytes))
	if !hasSIMAlnum(rootCause) {
		rootCause = "auto " + sc.task.Class
	}
	what = strings.TrimSpace(trail.TruncateUTF8("fallback ("+sc.task.Class+"): "+rootCause, simManifestMaxRationaleBytes))
	return rootCause, what
}

func (r *run) lookupProposalTitle(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	recs, _, err := brain.Get(g, []string{id})
	if err != nil || len(recs) != 1 {
		return ""
	}
	return strings.TrimSpace(oneLine(recs[0].Title))
}

func simSignalID(t Task, m *simManifest) string {
	if isValidSignalID(t.SignalID) {
		return strings.TrimSpace(t.SignalID)
	}
	if m != nil && len(m.PredictedSignals) > 0 {
		return m.PredictedSignals[0]
	}
	return ""
}

func simSourceKind(class string) string {
	if class == ClassDistill {
		return "auto_distill"
	}
	return "auto_harness_edit"
}

func appendSIMApplyFailure(what, reason string) string {
	const prefix = "; apply failed: "
	cleanReason := strings.TrimSpace(oneLine(reason))
	suffix := prefix + cleanReason
	if len(what)+len(suffix) <= simManifestMaxRationaleBytes {
		return what + suffix
	}
	headCap := max(0, simManifestMaxRationaleBytes-len(prefix)-32)
	if len(what) > headCap {
		what = strings.TrimSpace(trail.TruncateUTF8(what, headCap))
	}
	return strings.TrimSpace(trail.TruncateUTF8(what+suffix, simManifestMaxRationaleBytes))
}
