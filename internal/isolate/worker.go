package isolate

import (
	"context"
	"os"
	"os/exec"
	"slices"

	"github.com/kesokaj/fyr/internal/isolate/worker"
)

// Re-export worker orchestration, kickoff, capsule, and judge types so callers
// of package isolate remain unchanged.
type (
	WorkerOptions    = worker.Options
	WorkerRecord     = worker.Record
	WorkerResult     = worker.Result
	Kickoff          = worker.Kickoff
	CapsulePivot     = worker.CapsulePivot
	CapsuleDeps      = worker.CapsuleDeps
	JudgeExecOptions = worker.JudgeExecOptions
	JudgeOptions     = worker.JudgeOptions
	JudgeExecOutput  = worker.JudgeExecOutput
)

const (
	DefaultTimeout       = worker.DefaultTimeout
	DefaultJudgeTimeout  = worker.DefaultJudgeTimeout
	KickoffCapsuleBudget = worker.KickoffCapsuleBudget

	ReasonTimedOut     = worker.ReasonTimedOut
	ReasonKilled       = worker.ReasonKilled
	ReasonNoCommits    = worker.ReasonNoCommits
	ReasonNoHandoff    = worker.ReasonNoHandoff
	ReasonTwoHandoffs  = worker.ReasonTwoHandoffs
	ReasonOutside      = worker.ReasonOutside
	ReasonRewrote      = worker.ReasonRewrote
	ReasonLeadChanged  = worker.ReasonLeadChanged
	ReasonBranchFailed = worker.ReasonBranchFailed
	ReasonDiskLimit    = worker.ReasonDiskLimit
	ReasonJudgeFailed  = worker.ReasonJudgeFailed
	ReasonAgyFailed    = worker.ReasonAgyFailed
)

// ErrNoAuthToken is returned before spawning `agy -p` when no regular-file
// Antigravity OAuth token can be staged for the isolated judge.
var ErrNoAuthToken error = worker.ErrNoAuthToken

// JudgeExecFunc runs the cross-family judge; overridden in unit tests.
var JudgeExecFunc = RunJudgePrint

// SandboxRoot returns `<home>/.config/fyr/sandbox/<project>`.
func SandboxRoot(home, root string) string { return ProjectRoot(home, "sandbox", root) }

// RunWorker executes one or more Kickoff files in isolated worker sandboxes.
func RunWorker(ctx context.Context, o WorkerOptions, files ...string) ([]WorkerResult, error) {
	return worker.Run(ctx, workerDeps(), o, files...)
}

// RunJudgePrint executes `agy -p` inside a temporary confined scratch home
// under `<home>/<harness.StateDir>/judge-*` with Network: true and empty tool grants.
func RunJudgePrint(ctx context.Context, opts JudgeExecOptions) (JudgeExecOutput, error) {
	return worker.RunJudgePrint(ctx, judgeDeps(), opts)
}

// ParseJudgeVerdict parses either an `agy --output-format json` envelope or a
// bare `{"verdict":"PASS"|"FAIL","reason":"..."}` JSON object strictly.
func ParseJudgeVerdict(raw string) (verdict, reason string, err error) {
	return worker.ParseJudgeVerdict(raw)
}

// ParseKickoffCapsulePivots extracts full-body Hop-0 and Hop-1 file spans from
// a rendered --slice capsule for read_ledger pre-seeding.
func ParseKickoffCapsulePivots(capsule string) []CapsulePivot {
	return worker.ParseKickoffCapsulePivots(capsule)
}

// FormatKickoffCapsule appends a structural --slice capsule (<= 1,500 tokens)
// and pre-seeds read_ledger when .agents/scratch/search.db exists under root.
func FormatKickoffCapsule(
	k Kickoff,
	root string,
	sliceFn func(dir, query string, budget int) (string, error),
	preseedFn func(dir string, pivots []CapsulePivot) error,
) string {
	return worker.FormatKickoffCapsule(k, root, sliceFn, preseedFn)
}

func judgeDeps() worker.JudgeDeps {
	return worker.JudgeDeps{
		OAuthRel:           TokenRel,
		WorkAuthRel:        WorkTokenRel,
		EffectiveHostOS:    effectiveHostOS,
		SyncAuth:           SyncWorkToken,
		StageModelsCatalog: StageModelsCatalog,
		BuildConfinedCmd:   buildJudgeConfinedCmd,
		WithoutGit:         WithoutGit,
		Printable:          Printable,
	}
}

func workerDeps() worker.Deps {
	return worker.Deps{
		Judge: judgeDeps(),
		JudgeExec: func(ctx context.Context, opts JudgeExecOptions) (JudgeExecOutput, error) {
			return JudgeExecFunc(ctx, opts)
		},
		SafetyReasons:   append(slices.Clone(IntegrityReasons), ReasonCredentials, ReasonRealConfig, ReasonLeadChanged),
		Root:            Root,
		SandboxRoot:     SandboxRoot,
		CheckRoot:       CheckRoot,
		CheckWorkDir:    CheckWorkDir,
		OutsideCheckout: OutsideCheckout,
		ChooseRunner:    ChooseRunner,
		CheckAvailable:  CheckAvailable,
		CheckWorkLogin:  CheckWorkLogin,
		PrepareSelf:     PrepareSelf,
		OpenRegular:     OpenRegular,
		FyrEnv:          FyrEnv,
		NewBox:          newWorkerBox,
	}
}

func buildJudgeConfinedCmd(req worker.JudgeConfinedRequest) (*exec.Cmd, func(), error) {
	resolvedBin, err := resolveCommandBin(req.Bin, "", os.Getenv("PATH"))
	if err != nil {
		return nil, nil, err
	}
	extraRO := addPathPair(addPathPair(addPathPair(nil, req.Tok), req.AbsRepo), req.Dir)
	rw, ro := policyPaths(req.RealHome, resolvedBin, addPathPair(addPathPair(nil, req.ScratchHome), req.TmpDir), nil, nil, extraRO)
	pol := Policy{Argv: append([]string{resolvedBin}, req.Args...), Dir: req.Dir, Env: req.Env, RWPaths: rw, ROPaths: ro, Network: true}
	cmd, cleanup, err := prepareIsolatedCmd(pol)
	if err != nil {
		return nil, nil, err
	}
	setCmdSetsid(cmd)
	return cmd, cleanup, nil
}

func newWorkerBox(cfg worker.BoxConfig) worker.BoxHandle {
	return New(Config{
		Dir: cfg.Dir, Source: cfg.Source, RealHome: cfg.RealHome, Repo: cfg.Repo, Self: cfg.Self,
		Grants: cfg.Grants, GitName: cfg.GitName, GitEmail: cfg.GitEmail,
		Model: cfg.Model, Zone: cfg.Zone, Timeout: cfg.Timeout, SkipStores: cfg.SkipStores, HostOS: cfg.HostOS,
		PrintTimeout: cfg.PrintTimeout, StopGrace: cfg.StopGrace,
		Prefix: cfg.Prefix, Out: cfg.Out, ACP: cfg.ACP,
		TestRO: cfg.TestRO, TestRW: cfg.TestRW, TestUnconfined: cfg.TestUnconfined,
	})
}
