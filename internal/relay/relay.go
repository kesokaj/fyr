// Package relay implements the opt-in hands-off fresh-session phase runner
// (`fyr relay`, ADR-532 D5).
package relay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	DefaultMaxPhases = 1
	MaxPhasesLimit   = 20

	StatusOK       = "OK"
	StatusComplete = "COMPLETE"
	StatusDryRun   = "DRY_RUN"
	StatusHalted   = "HALTED"

	HaltFail               = "FAIL"
	HaltZeroProgress       = "ZERO_PROGRESS"
	HaltDirtyTree          = "DIRTY_TREE"
	HaltRepeatingErrorHash = "REPEATING_ERROR_HASH"
	HaltPRDModified        = "PRD_MODIFIED"
	HaltTagCreated         = "TAG_CREATED"
	HaltPushDetected       = "PUSH_DETECTED"
	HaltHistoryRewrite     = "HISTORY_REWRITE"
	HaltPlanTampered       = "PLAN_TAMPERED"
	HaltLeaseOrGateFail    = "LEASE_OR_GATE_FAIL"
	HaltBudgetFloor        = "BUDGET_FLOOR"
	HaltSessionError       = "SESSION_ERROR"
)

var (
	// ErrRefused marks usage or precondition refusals (exit 2).
	ErrRefused = errors.New("refused")
	// ErrHalted marks a runtime halt, guard trigger, or phase failure (exit 1).
	ErrHalted = errors.New("halted")
)

type refusalErr struct{ msg string }

func (e *refusalErr) Error() string { return e.msg }
func (e *refusalErr) Unwrap() error { return ErrRefused }

type haltErr struct {
	msg   string
	cause error
}

func (e *haltErr) Error() string { return e.msg }
func (e *haltErr) Unwrap() []error {
	if e.cause != nil {
		return []error{ErrHalted, e.cause}
	}
	return []error{ErrHalted}
}

func refusef(format string, args ...any) error { return &refusalErr{msg: fmt.Sprintf(format, args...)} }
func haltf(format string, args ...any) error   { return &haltErr{msg: fmt.Sprintf(format, args...)} }
func wrapHaltf(cause error, format string, args ...any) error {
	return &haltErr{msg: fmt.Sprintf(format, args...), cause: cause}
}

// VerifyFunc runs post-phase deterministic verification; defaults to gate.Verify.
type VerifyFunc func(ctx context.Context, opts gate.VerifyOptions) (gate.VerifyReport, error)

// GateFunc runs deterministic gate or cross-family judge; defaults to gate.Gate.
type GateFunc func(ctx context.Context, opts gate.GateOptions) (gate.GateReport, error)

// Options configures a `fyr relay` invocation.
type Options struct {
	Dir, Home, PlanID, JudgeModel string
	MaxPhases, MaxFiles, MaxLines int
	DryRun, RequireQuota          bool
	SessionTimeout, VerifyTimeout time.Duration
	Vocab                         check.Vocabulary
	PriorErrorHashes              []string
	Now                           func() time.Time
	SessionRunner                 SessionRunner
	QuotaCheck                    QuotaCheckFunc
	VerifyFn                      VerifyFunc
	GateFn, JudgeFn               GateFunc
	RecordVerdict                 func(gate.JudgeVerdictRecord) error
}

// PhaseResult records the outcome of one executed relay phase.
type PhaseResult struct {
	PhaseID       string   `json:"phaseId"`
	PhaseTitle    string   `json:"phaseTitle"`
	Status        string   `json:"status"`
	HaltReason    string   `json:"haltReason,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	ErrorHash     string   `json:"errorHash,omitempty"`
	HeadBefore    string   `json:"headBefore,omitempty"`
	HeadAfter     string   `json:"headAfter,omitempty"`
	ChangedFiles  []string `json:"changedFiles,omitempty"`
	VerifyTreeSHA string   `json:"verifyTreeSha,omitempty"`
	JudgeModel    string   `json:"judgeModel,omitempty"`
	JudgeVerdict  string   `json:"judgeVerdict,omitempty"`
	HandoffID     string   `json:"handoffId,omitempty"`
}

// Report summarizes a `fyr relay` run for human and JSON output.
type Report struct {
	OK            bool          `json:"ok"`
	Status        string        `json:"status"`
	PlanID        string        `json:"planId"`
	PlanTitle     string        `json:"planTitle,omitempty"`
	RepoRoot      string        `json:"repoRoot"`
	MaxPhases     int           `json:"maxPhases"`
	PlanComplete  bool          `json:"planComplete,omitempty"`
	GitClean      bool          `json:"gitClean"`
	QuotaOK       bool          `json:"quotaOk"`
	QuotaNote     string        `json:"quotaNote,omitempty"`
	HaltReason    string        `json:"haltReason,omitempty"`
	HaltDetail    string        `json:"haltDetail,omitempty"`
	Phases        []PhaseResult `json:"phases,omitempty"`
	PlannedPhases []Phase       `json:"plannedPhases,omitempty"`
	DryRunKickoff string        `json:"dryRunKickoff,omitempty"`
}

// Run executes up to opts.MaxPhases ready phases of opts.PlanID, one isolated
// fresh session per phase, with deterministic safety/verify/judge gates and
// immutable brain.db handoff checkpoints.
func Run(ctx context.Context, opts Options) (Report, error) {
	maxPhases, top, err := validateOptions(opts)
	if err != nil {
		return Report{}, err
	}
	plan, err := LoadPlan(opts.Home, top, opts.PlanID)
	if err != nil {
		return Report{}, err
	}
	rep := Report{PlanID: plan.ID, PlanTitle: plan.Title, RepoRoot: top, MaxPhases: maxPhases, QuotaOK: true}
	next, hasNext, err := NextReadyPhase(plan)
	if err != nil {
		rep.Status, rep.HaltReason, rep.HaltDetail = StatusHalted, HaltFail, redact.Redact(err.Error())
		return rep, err
	}
	if !hasNext {
		dirty, derr := gitDirtyEntries(ctx, top)
		rep.OK, rep.Status, rep.PlanComplete = true, StatusComplete, true
		rep.GitClean = derr == nil && len(dirty) == 0
		return rep, nil
	}
	if opts.DryRun {
		return runDryRun(ctx, opts, top, plan, next, rep)
	}
	if opts.SessionRunner == nil {
		if err := checkLiveSessionHome(opts.Home); err != nil {
			return rep, err
		}
	}
	return runRelayLoop(ctx, opts, top, plan, maxPhases, rep)
}

func validateOptions(opts Options) (int, string, error) {
	if strings.TrimSpace(opts.PlanID) == "" {
		return 0, "", refusef("missing --plan ID")
	}
	maxPhases := opts.MaxPhases
	if maxPhases == 0 {
		maxPhases = DefaultMaxPhases
	}
	if maxPhases < 1 || maxPhases > MaxPhasesLimit {
		return 0, "", refusef("--max-phases must be between 1 and %d (got %d)", MaxPhasesLimit, maxPhases)
	}
	if jm := strings.TrimSpace(opts.JudgeModel); jm != "" && !isValidModelID(jm) {
		return 0, "", refusef("invalid --judge-model %q", jm)
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	_, top, err := trail.Roots(dir)
	if err != nil {
		return 0, "", refusef("resolve git checkout: %v", err)
	}
	return maxPhases, top, nil
}

func runDryRun(ctx context.Context, opts Options, top string, plan ParsedPlan, next Phase, rep Report) (Report, error) {
	dirty, err := gitDirtyEntries(ctx, top)
	if err != nil {
		return rep, err
	}
	q, err := runQuotaCheck(ctx, opts)
	if err != nil {
		return rep, err
	}
	rep.OK, rep.Status = true, StatusDryRun
	rep.GitClean, rep.QuotaOK, rep.QuotaNote = len(dirty) == 0, q.OK, q.Note
	rep.PlannedPhases = UpcomingOpenPhases(plan, rep.MaxPhases)
	rep.DryRunKickoff = BuildKickoff(top, plan, next)
	return rep, nil
}

func runRelayLoop(ctx context.Context, opts Options, top string, plan ParsedPlan, maxPhases int, rep Report) (Report, error) {
	tracker := NewErrorTracker(opts.PriorErrorHashes...)
	tracker.Seed(LoadPriorErrorHashes(opts.Home, top, plan.ID))
	for i := 0; i < maxPhases; i++ {
		if i > 0 {
			reloaded, err := LoadPlan(opts.Home, top, plan.ID)
			if err != nil {
				return rep, haltPlanTamper(opts, top, plan, Phase{ID: "plan", Title: plan.Title}, "reload plan failed: "+err.Error(), err, &rep)
			}
			if reloaded.Title != plan.Title || reloaded.Status != plan.Status || planStructureHash(reloaded.Body) != planStructureHash(plan.Body) {
				return rep, haltPlanTamper(opts, top, plan, Phase{ID: "plan", Title: plan.Title}, "plan record structure changed outside checkbox state during relay", nil, &rep)
			}
			plan = reloaded
		}
		ph, hasNext, err := NextReadyPhase(plan)
		if err != nil {
			rep.OK, rep.Status, rep.HaltReason, rep.HaltDetail = false, StatusHalted, HaltFail, redact.Redact(err.Error())
			return rep, err
		}
		if !hasNext {
			rep.PlanComplete = true
			break
		}
		done, haltErr := runSinglePhase(ctx, opts, top, plan, ph, i, tracker, &rep)
		if haltErr != nil {
			return rep, haltErr
		}
		if done {
			rep.PlanComplete = true
			break
		}
	}
	rep.OK, rep.Status = true, StatusOK
	return rep, nil
}

func runSinglePhase(ctx context.Context, opts Options, top string, plan ParsedPlan, ph Phase, idx int, tracker *ErrorTracker, rep *Report) (bool, error) {
	if err := checkPrePhaseTree(ctx, opts, top, plan, ph, idx, rep); err != nil {
		return false, err
	}
	if err := checkPrePhaseQuota(ctx, opts, top, plan, ph, rep); err != nil {
		return false, err
	}
	snapBefore, err := snapshotGitBefore(ctx, top)
	if err != nil {
		return false, err
	}
	res := PhaseResult{PhaseID: ph.ID, PhaseTitle: ph.Title, HeadBefore: snapBefore.head}
	if sessErr := invokePhaseSession(ctx, opts, top, plan, ph, tracker, &res); sessErr != nil {
		postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if pushErr := checkRemoteRefsUnchanged(postCtx, top, snapBefore, &res); pushErr != nil {
			return false, recordHaltAndAppend(opts, top, plan, ph, res, nil, pushErr, rep)
		}
		dirty, _ := gitDirtyEntries(postCtx, top)
		return false, recordHaltAndAppend(opts, top, plan, ph, res, dirty, sessErr, rep)
	}
	if postErr := verifyPostPhase(ctx, opts, top, plan, ph, snapBefore, tracker, &res); postErr != nil {
		postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		dirty, _ := gitDirtyEntries(postCtx, top)
		return false, recordHaltAndAppend(opts, top, plan, ph, res, dirty, postErr, rep)
	}
	return finishPassingPhase(opts, top, plan, ph, res, rep)
}
