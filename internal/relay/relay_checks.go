package relay

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/redact"
)

func checkPrePhaseTree(ctx context.Context, opts Options, top string, plan ParsedPlan, ph Phase, idx int, rep *Report) error {
	dirty, err := gitDirtyEntries(ctx, top)
	if err != nil {
		return err
	}
	rep.GitClean = len(dirty) == 0
	if len(dirty) == 0 {
		return nil
	}
	if idx == 0 {
		return refusef("working tree is dirty (%d entry/entries); commit or discard changes first", len(dirty))
	}
	res := PhaseResult{PhaseID: ph.ID, PhaseTitle: ph.Title, Status: StatusHalted, HaltReason: HaltDirtyTree, Detail: redact.Redact("dirty working tree before phase: " + strings.Join(dirty, ", "))}
	return recordHaltAndAppend(opts, top, plan, ph, res, dirty, nil, rep)
}

func checkPrePhaseQuota(ctx context.Context, opts Options, top string, plan ParsedPlan, ph Phase, rep *Report) error {
	q, err := runQuotaCheck(ctx, opts)
	if err != nil {
		return err
	}
	rep.QuotaOK, rep.QuotaNote = q.OK, redact.Redact(q.Note)
	if q.OK {
		return nil
	}
	res := PhaseResult{PhaseID: ph.ID, PhaseTitle: ph.Title, Status: StatusHalted, HaltReason: HaltBudgetFloor, Detail: redact.Redact(q.Note)}
	return recordHaltAndAppend(opts, top, plan, ph, res, nil, nil, rep)
}

func runQuotaCheck(ctx context.Context, opts Options) (QuotaStatus, error) {
	if opts.QuotaCheck != nil {
		return opts.QuotaCheck(ctx, opts.Home)
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	return CheckBudgetFloorAt(opts.Home, opts.RequireQuota, now)
}

func invokePhaseSession(ctx context.Context, opts Options, top string, plan ParsedPlan, ph Phase, tracker *ErrorTracker, res *PhaseResult) error {
	runner := opts.SessionRunner
	if runner == nil {
		runner = RunAgySession
	}
	timeout := opts.SessionTimeout
	if timeout <= 0 {
		timeout = DefaultSessionTimeout
	}
	sRes, err := runner(ctx, SessionRequest{
		RepoDir: top, Home: opts.Home, PlanID: plan.ID, PhaseID: ph.ID,
		Model: ph.Model, Prompt: BuildKickoff(top, plan, ph), Timeout: timeout, PrintTimeout: timeout.String(),
	})
	if err == nil && sRes.ExitCode == 0 {
		return nil
	}
	var parts []string
	if err != nil {
		parts = append(parts, err.Error())
	}
	if sRes.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit=%d", sRes.ExitCode))
	}
	if msg := strings.TrimSpace(sRes.Stderr); msg != "" {
		parts = append(parts, msg)
	}
	return setTrackedHaltErr(res, HaltSessionError, strings.Join(parts, "; "), err, tracker)
}

func verifyPostPhase(ctx context.Context, opts Options, top string, plan ParsedPlan, ph Phase, snapBefore gitSnapshot, initialHash string, tracker *ErrorTracker, res *PhaseResult) error {
	if err := checkRemoteRefsUnchanged(ctx, top, snapBefore, res); err != nil {
		return err
	}
	if err := checkProgressAndTree(ctx, top, tracker, res); err != nil {
		return err
	}
	if err := checkHardSafety(ctx, opts, top, plan, ph, snapBefore, initialHash, tracker, res); err != nil {
		return err
	}
	if err := runPhaseVerify(ctx, opts, top, tracker, res); err != nil {
		return err
	}
	return runPhaseJudge(ctx, opts, top, plan, ph, tracker, res)
}

func checkRemoteRefsUnchanged(ctx context.Context, top string, snapBefore gitSnapshot, res *PhaseResult) error {
	remoteAfter, err := gitRemoteRefs(ctx, top)
	if err != nil {
		return setGuardHalt(res, HaltFail, "read remote-tracking refs: "+err.Error())
	}
	if diffs := changedRemoteRefs(snapBefore.remoteRefs, remoteAfter, snapBefore.upstream, gitUpstreamRef(ctx, top)); len(diffs) > 0 {
		return setGuardHalt(res, HaltPushDetected, "remote-tracking or upstream ref changed during phase: "+strings.Join(diffs, ", "))
	}
	return nil
}

func checkProgressAndTree(ctx context.Context, top string, tracker *ErrorTracker, res *PhaseResult) error {
	headAfter, err := gitHead(ctx, top)
	if err != nil {
		return setTrackedHalt(res, HaltFail, "resolve post-phase HEAD: "+err.Error(), tracker)
	}
	res.HeadAfter = headAfter
	dirty, err := gitDirtyEntries(ctx, top)
	if err != nil {
		return setTrackedHalt(res, HaltFail, "check post-phase status: "+err.Error(), tracker)
	}
	if headAfter == res.HeadBefore {
		msg := "session exited without advancing HEAD (zero commits)"
		if len(dirty) > 0 {
			msg += "; uncommitted: " + strings.Join(dirty, ", ")
		}
		return setTrackedHalt(res, HaltZeroProgress, msg, tracker)
	}
	anc, err := gitIsAncestor(ctx, top, res.HeadBefore, headAfter)
	if err != nil {
		return setTrackedHalt(res, HaltFail, "verify HEAD ancestry: "+err.Error(), tracker)
	}
	if !anc {
		return setGuardHalt(res, HaltHistoryRewrite, fmt.Sprintf("HEAD %s is not a descendant of pre-phase HEAD %s (history rewrite/reset detected)", shortSHA(headAfter), shortSHA(res.HeadBefore)))
	}
	if len(dirty) > 0 {
		return setTrackedHalt(res, HaltDirtyTree, "session left dirty working tree after committing: "+strings.Join(dirty, ", "), tracker)
	}
	return nil
}

func checkHardSafety(ctx context.Context, opts Options, top string, plan ParsedPlan, ph Phase, snapBefore gitSnapshot, initialHash string, tracker *ErrorTracker, res *PhaseResult) error {
	tagsAfter, err := gitTags(ctx, top)
	if err != nil {
		return setTrackedHalt(res, HaltFail, "read git tags: "+err.Error(), tracker)
	}
	if added := newTagsBetween(snapBefore.tags, tagsAfter); len(added) > 0 {
		return setGuardHalt(res, HaltTagCreated, "unauthorized git tag(s) created: "+strings.Join(added, ", "))
	}
	if _, err := loadAndVerifyPlanHash(opts.Home, top, plan.ID, initialHash); err != nil {
		return setGuardHalt(res, HaltPlanTampered, err.Error())
	}
	changed, err := gitDiffNames(ctx, top, res.HeadBefore, res.HeadAfter)
	if err != nil {
		return setTrackedHalt(res, HaltFail, "read git diff names: "+err.Error(), tracker)
	}
	res.ChangedFiles = changed
	for _, f := range changed {
		if filepath.ToSlash(filepath.Clean(f)) == "PRD.md" {
			return setGuardHalt(res, HaltPRDModified, "PRD.md was modified during phase (strictly forbidden without approval + ADR)")
		}
	}
	if outside := outOfLeaseFiles(changed, ph.Files); len(outside) > 0 {
		return setTrackedHalt(res, HaltLeaseOrGateFail, "two-dot diff outside Files: lease: "+strings.Join(outside, ", "), tracker)
	}
	return runDeterministicGate(ctx, opts, top, ph, tracker, res)
}

func runDeterministicGate(ctx context.Context, opts Options, top string, ph Phase, tracker *ErrorTracker, res *PhaseResult) error {
	gateFn := opts.GateFn
	if gateFn == nil {
		gateFn = gate.Gate
	}
	gRep, gErr := gateFn(ctx, gate.GateOptions{
		Dir: top, Target: top, Base: res.HeadBefore, Home: opts.Home,
		Lease: ph.Files, MaxFiles: opts.MaxFiles, MaxLines: opts.MaxLines,
	})
	if gErr == nil && gRep.OK {
		return nil
	}
	return setTrackedHalt(res, HaltLeaseOrGateFail, formatGateFindings(gRep, gErr), tracker)
}

func runPhaseVerify(ctx context.Context, opts Options, top string, tracker *ErrorTracker, res *PhaseResult) error {
	verifyFn := opts.VerifyFn
	if verifyFn == nil {
		verifyFn = gate.Verify
	}
	vRep, vErr := verifyFn(ctx, gate.VerifyOptions{
		Dir: top, Home: opts.Home, Vocab: opts.Vocab, Timeout: opts.VerifyTimeout, Now: opts.Now,
	})
	if vErr == nil && vRep.OK {
		res.VerifyTreeSHA = vRep.TreeSHA256
		return nil
	}
	return setTrackedHalt(res, HaltFail, formatVerifyFailure(vRep, vErr), tracker)
}

func runPhaseJudge(ctx context.Context, opts Options, top string, plan ParsedPlan, ph Phase, tracker *ErrorTracker, res *PhaseResult) error {
	judgeFn := opts.JudgeFn
	if judgeFn == nil {
		judgeFn = gate.Gate
	}
	jRep, jErr := judgeFn(ctx, gate.GateOptions{
		Dir: top, Target: top, Base: res.HeadBefore, Home: opts.Home,
		Lease: ph.Files, MaxFiles: opts.MaxFiles, MaxLines: opts.MaxLines,
		Judge: BuildJudgeCriterion(plan, ph), JudgeModel: opts.JudgeModel, RecordVerdict: opts.RecordVerdict,
	})
	res.JudgeModel, res.JudgeVerdict = jRep.JudgeModel, jRep.JudgeVerdict
	if jErr == nil && jRep.OK {
		return nil
	}
	return setTrackedHalt(res, HaltFail, formatGateFindings(jRep, jErr), tracker)
}

func finishPassingPhase(opts Options, top string, plan ParsedPlan, ph Phase, initialHash string, res PhaseResult, rep *Report) (bool, error) {
	updatedBody, tamperErr, err := tickLatestPlanPhase(opts, top, plan, ph, initialHash)
	if tamperErr != nil {
		return false, haltPlanTamper(opts, top, plan, ph, tamperErr.Error(), tamperErr, rep)
	}
	if err != nil {
		return false, err
	}
	nextTitle, allDone := resolveNextPhaseAfterTick(plan, updatedBody)
	hid, err := RecordPassHandoff(opts, top, plan, res, nextTitle)
	if err != nil {
		return false, fmt.Errorf("store pass handoff: %w", err)
	}
	res.Status, res.HandoffID = "PASS", hid
	rep.Phases = append(rep.Phases, res)
	return allDone, nil
}

func loadAndVerifyPlanHash(home, top, planID, initialHash string) (ParsedPlan, error) {
	latest, err := LoadPlan(home, top, planID)
	if err != nil {
		return ParsedPlan{}, fmt.Errorf("reload %s failed: %w", planID, err)
	}
	if planStructureHash(latest.Body) != initialHash {
		return ParsedPlan{}, fmt.Errorf("plan %s changed outside checkbox markers during relay", planID)
	}
	return latest, nil
}

func haltPlanTamper(opts Options, top string, plan ParsedPlan, ph Phase, detail string, cause error, rep *Report) error {
	res := PhaseResult{PhaseID: ph.ID, PhaseTitle: ph.Title, Status: StatusHalted, HaltReason: HaltPlanTampered, Detail: redact.Redact(detail)}
	return recordHaltAndAppend(opts, top, plan, ph, res, nil, cause, rep)
}

func tickLatestPlanPhase(opts Options, top string, plan ParsedPlan, ph Phase, initialHash string) (updatedBody string, tamperErr, err error) {
	latest, tErr := loadAndVerifyPlanHash(opts.Home, top, plan.ID, initialHash)
	if tErr != nil {
		return "", tErr, nil
	}
	target := ph
	for _, cand := range latest.Phases {
		if cand.ID == ph.ID && cand.Title == ph.Title {
			target = cand
			break
		}
	}
	updatedBody = TickPhaseBoxes(latest.Body, target)
	if updatedBody == latest.Body {
		return updatedBody, nil, nil
	}
	if err := brain.Edit(brain.Options{Home: opts.Home, Dir: top, Now: opts.Now}, brain.EditInput{ID: plan.ID, Body: strings.NewReader(updatedBody)}); err != nil {
		return "", nil, fmt.Errorf("tick completed phase in %s: %w", plan.ID, err)
	}
	return updatedBody, nil, nil
}

func resolveNextPhaseAfterTick(plan ParsedPlan, updatedBody string) (string, bool) {
	updated, err := ParsePlanBody(plan.ID, plan.Title, plan.Status, updatedBody)
	if err != nil {
		return "unknown", false
	}
	next, hasNext, err := NextReadyPhase(updated)
	if err != nil {
		return "blocked", false
	}
	if !hasNext {
		return StatusComplete, true
	}
	return next.Title, false
}

func setGuardHalt(res *PhaseResult, reason, detail string) error {
	clean := redact.Redact(detail)
	res.Status, res.HaltReason, res.Detail = StatusHalted, reason, clean
	return haltf("%s: %s", reason, clean)
}

func setTrackedHalt(res *PhaseResult, defaultReason, detail string, tracker *ErrorTracker) error {
	return setTrackedHaltErr(res, defaultReason, detail, nil, tracker)
}

func setTrackedHaltErr(res *PhaseResult, defaultReason, detail string, cause error, tracker *ErrorTracker) error {
	clean := redact.Redact(detail)
	hash, repeated := tracker.Record(defaultReason + ": " + clean)
	res.Status, res.ErrorHash, res.Detail, res.HaltReason = StatusHalted, hash, clean, defaultReason
	if repeated {
		res.HaltReason = HaltRepeatingErrorHash
	}
	return wrapHaltf(cause, "%s: %s", res.HaltReason, clean)
}

func recordHaltAndAppend(opts Options, top string, plan ParsedPlan, ph Phase, res PhaseResult, dirty []string, cause error, rep *Report) error {
	res.Detail = redact.Redact(res.Detail)
	hid, herr := RecordHaltHandoff(opts, top, plan, ph, res, dirty)
	if herr == nil {
		res.HandoffID = hid
	}
	rep.Phases = append(rep.Phases, res)
	rep.OK, rep.Status, rep.HaltReason, rep.HaltDetail = false, StatusHalted, res.HaltReason, res.Detail
	if herr != nil {
		return wrapHaltf(cause, "%s: %s (handoff store error: %v)", res.HaltReason, res.Detail, herr)
	}
	return wrapHaltf(cause, "%s: %s", res.HaltReason, res.Detail)
}

func formatGateFindings(gRep gate.GateReport, err error) string {
	var parts []string
	for _, f := range gRep.Findings {
		if f.Path != "" {
			parts = append(parts, fmt.Sprintf("[%s] %s: %s", f.Check, f.Path, f.Detail))
		} else {
			parts = append(parts, fmt.Sprintf("[%s] %s", f.Check, f.Detail))
		}
	}
	if len(parts) == 0 && err != nil {
		return err.Error()
	}
	return strings.Join(parts, "; ")
}

func formatVerifyFailure(vRep gate.VerifyReport, err error) string {
	var parts []string
	for _, s := range vRep.Steps {
		if !s.OK {
			parts = append(parts, fmt.Sprintf("%s: %s", s.Name, s.Detail))
		}
	}
	if len(parts) == 0 && err != nil {
		return err.Error()
	}
	return strings.Join(parts, "; ")
}
