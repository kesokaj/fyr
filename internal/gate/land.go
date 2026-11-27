package gate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// LandOptions configures deterministic squash-landing of a branch onto main.
type LandOptions struct {
	Dir             string
	Branch          string
	Message         string
	Home            string
	RealHome        string
	Vocab           check.Vocabulary
	Timeout         time.Duration
	Lease           []string
	LeaseFile       string
	MaxFiles        int
	MaxLines        int
	Judge           string
	JudgeModel      string
	WorkerModel     string
	WorkerSessionID string
	JudgeRunner     func(context.Context, JudgeRequest) (JudgeResult, error)
	RecordVerdict   func(JudgeVerdictRecord) error
}

// LandReport summarizes a Land attempt.
type LandReport struct {
	OK         bool          `json:"ok"`
	MainRoot   string        `json:"mainRoot"`
	Target     string        `json:"targetBranch"`
	Branch     string        `json:"branch"`
	LandCommit string        `json:"landCommit,omitempty"`
	BranchDel  bool          `json:"branchDeleted"`
	StampPath  string        `json:"stampPath,omitempty"`
	Detail     string        `json:"detail,omitempty"`
	Gate       *GateReport   `json:"gate,omitempty"`
	Verify     *VerifyReport `json:"verify,omitempty"`
}

type landCtx struct {
	root       string
	mainBranch string
	baseHead   string
	msg        string
	snap       checkoutSnapshot
}

// Land runs deterministic Gate checks on opts.Branch, squash-merges into a
// detached scratch worktree, runs Verify (and any Judge check against the fresh
// verify.stamp) there, fast-forwards the current branch, deletes opts.Branch,
// and refreshes verify.stamp on the main checkout.
func Land(ctx context.Context, opts LandOptions) (LandReport, error) {
	lc, err := prepareLand(ctx, opts)
	if err != nil {
		return LandReport{Branch: opts.Branch, Detail: search.Printable(err.Error())}, err
	}
	rep := LandReport{MainRoot: lc.root, Target: lc.mainBranch, Branch: opts.Branch}
	gateOpts := GateOptions{
		Dir: lc.root, Target: opts.Branch, Base: lc.baseHead, Home: opts.Home, RealHome: opts.RealHome,
		Lease: opts.Lease, LeaseFile: opts.LeaseFile,
		MaxFiles: opts.MaxFiles, MaxLines: opts.MaxLines,
		JudgeModel: opts.JudgeModel, WorkerModel: opts.WorkerModel,
		WorkerSessionID: opts.WorkerSessionID, JudgeRunner: opts.JudgeRunner,
		RecordVerdict: opts.RecordVerdict,
	}
	gateRep, err := Gate(ctx, gateOpts)
	rep.Gate = &gateRep
	if err != nil {
		rep.Detail = summarizeLandFailure(rep, err)
		return rep, err
	}
	gatedHead := gateRep.Head
	if gatedHead == "" {
		err = refusef("gate returned empty head for branch %s", opts.Branch)
		rep.Detail = search.Printable(err.Error())
		return rep, err
	}
	gateOpts.Judge = opts.Judge
	landCommit, verifyRep, judgeRep, err := verifyInScratchWorktree(ctx, lc, gatedHead, opts, gateOpts)
	rep.Verify = verifyRep
	mergeGateJudgeReport(rep.Gate, judgeRep, err)
	if err != nil {
		rep.Detail = summarizeLandFailure(rep, err)
		return rep, err
	}
	return finishLand(ctx, lc, opts.Branch, gatedHead, landCommit, &rep)
}

func prepareLand(ctx context.Context, opts LandOptions) (landCtx, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	if strings.TrimSpace(opts.Branch) == "" {
		return landCtx{}, refusef("missing branch name")
	}
	mainRoot, top, err := trail.Roots(dir)
	if err != nil {
		return landCtx{}, refusef("resolve checkout root: %v", err)
	}
	if mainRoot != "" && top != mainRoot {
		return landCtx{}, refusef("land must run from main checkout %s, not %s", mainRoot, top)
	}
	return validateLandState(ctx, top, opts)
}

func validateLandState(ctx context.Context, root string, opts LandOptions) (landCtx, error) {
	branch, err := gitLine(ctx, root, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		return landCtx{}, refusef("main checkout HEAD is detached")
	}
	if branch == opts.Branch {
		return landCtx{}, refusef("cannot land current branch %s onto itself", branch)
	}
	dirty, err := checkoutDirty(ctx, root)
	if err != nil {
		return landCtx{}, err
	}
	if dirty {
		return landCtx{}, refusef("main checkout is dirty; commit or clean before landing")
	}
	if err := checkBranchPreconditions(ctx, root, opts.Branch); err != nil {
		return landCtx{}, err
	}
	msg, err := resolveLandMessage(opts)
	if err != nil {
		return landCtx{}, err
	}
	snap, err := takeSnapshot(ctx, root)
	if err != nil {
		return landCtx{}, err
	}
	return landCtx{root: root, mainBranch: branch, baseHead: snap.head, msg: msg, snap: snap}, nil
}

func checkBranchPreconditions(ctx context.Context, root, branch string) error {
	if _, err := verifyCommitRef(ctx, root, "refs/heads/"+branch); err != nil {
		return refusef("local branch %q does not exist", branch)
	}
	if wt, ok, err := branchCheckedOutAt(ctx, root, branch); err != nil {
		return err
	} else if ok {
		return refusef("branch %s is checked out in worktree %s; remove or detach it first", branch, wt)
	}
	if gitConfigValue(ctx, root, "user.name") == "" || gitConfigValue(ctx, root, "user.email") == "" {
		return refusef("git user.name and user.email must be configured")
	}
	return checkScratchNoSymlinks(root)
}

func branchCheckedOutAt(ctx context.Context, root, branch string) (string, bool, error) {
	out, err := gitBytes(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}
	targetRef := "refs/heads/" + branch
	var curWT string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			curWT = strings.TrimPrefix(line, "worktree ")
		} else if line == "branch "+targetRef && curWT != "" {
			return curWT, true, nil
		}
	}
	return "", false, nil
}

func resolveLandMessage(opts LandOptions) (string, error) {
	msg := strings.TrimSpace(opts.Message)
	if msg == "" {
		msg = "squash: land " + opts.Branch
	}
	if key, bad := hasForbiddenTrailer(msg); bad {
		return "", refusef("commit message contains forbidden trailer %s:", key)
	}
	return msg, nil
}

func verifyInScratchWorktree(ctx context.Context, lc landCtx, gatedHead string, opts LandOptions, judgeOpts GateOptions) (string, *VerifyReport, *GateReport, error) {
	scratchDir := filepath.Join(lc.root, ".agents", "scratch")
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return "", nil, nil, err
	}
	wtDir, err := os.MkdirTemp(scratchDir, "land-wt-*")
	if err != nil {
		return "", nil, nil, err
	}
	defer func() {
		_ = removeWorktree(context.Background(), lc.root, wtDir)
		_ = os.RemoveAll(wtDir)
	}()
	landCommit, err := commitSquashInWorktree(ctx, lc, wtDir, gatedHead)
	if err != nil {
		return "", nil, nil, err
	}
	vRep, err := Verify(ctx, VerifyOptions{Dir: wtDir, Home: opts.Home, Vocab: opts.Vocab, Timeout: opts.Timeout})
	if err != nil || strings.TrimSpace(judgeOpts.Judge) == "" {
		return landCommit, &vRep, nil, err
	}
	var jRep GateReport
	jTgt := gateTarget{
		mainRoot:  lc.root,
		wtRoot:    wtDir,
		branch:    opts.Branch,
		head:      landCommit,
		base:      lc.baseHead,
		mergeBase: lc.baseHead,
	}
	jErr := applyGateJudge(ctx, judgeOpts, jTgt, &jRep)
	return landCommit, &vRep, &jRep, jErr
}

func commitSquashInWorktree(ctx context.Context, lc landCtx, wtDir, gatedHead string) (string, error) {
	if _, err := gitBytes(ctx, lc.root, "worktree", "add", "--detach", wtDir, lc.baseHead); err != nil {
		return "", err
	}
	if _, err := gitBytes(ctx, wtDir, "merge", "--squash", "--no-commit", "--", gatedHead); err != nil {
		return "", failf("squash merge failed: %v", err)
	}
	if _, err := gitBytes(ctx, wtDir, "commit", "-m", lc.msg); err != nil {
		return "", err
	}
	return gitLine(ctx, wtDir, "rev-parse", "HEAD")
}

func mergeGateJudgeReport(dst, src *GateReport, err error) {
	if dst == nil || src == nil {
		return
	}
	dst.JudgeModel = src.JudgeModel
	dst.JudgeVerdict = src.JudgeVerdict
	dst.JudgeFallback = src.JudgeFallback
	dst.DiffTruncated = src.DiffTruncated
	dst.Findings = append(dst.Findings, src.Findings...)
	if err != nil {
		dst.OK = false
	}
}

func finishLand(ctx context.Context, lc landCtx, branch, gatedHead, landCommit string, rep *LandReport) (LandReport, error) {
	fail := func(err error) (LandReport, error) {
		rep.Detail = search.Printable(err.Error())
		return *rep, err
	}
	if err := checkoutUnchanged(ctx, lc.root, lc.snap); err != nil {
		return fail(err)
	}
	curHead, err := verifyCommitRef(ctx, lc.root, "refs/heads/"+branch)
	if err != nil {
		return fail(refusef("verify branch %s head: %v", branch, err))
	}
	if curHead != gatedHead {
		return fail(refusef("branch %s changed during land (%s != %s)", branch, curHead[:min(12, len(curHead))], gatedHead[:min(12, len(gatedHead))]))
	}
	if _, err := gitBytes(ctx, lc.root, "merge", "--ff-only", landCommit); err != nil {
		return fail(err)
	}
	rep.LandCommit = landCommit
	if _, err := gitBytes(ctx, lc.root, "update-ref", "-d", "refs/heads/"+branch, gatedHead); err != nil {
		return fail(err)
	}
	rep.BranchDel = true
	if rep.Verify != nil {
		stampPath, err := refreshMainStamp(lc.root, lc.mainBranch, landCommit, rep.Verify)
		if err != nil {
			return fail(err)
		}
		rep.StampPath = stampPath
	}
	rep.OK = true
	return *rep, nil
}

func summarizeLandFailure(rep LandReport, err error) string {
	if rep.Verify != nil {
		for _, s := range rep.Verify.Steps {
			if !s.OK {
				detail := firstLineCompact(s.Detail, 300)
				if detail != "" {
					return fmt.Sprintf("verify %s failed: %s", s.Name, detail)
				}
				return fmt.Sprintf("verify %s failed", s.Name)
			}
		}
	}
	if rep.Gate != nil && len(rep.Gate.Findings) > 0 {
		parts := make([]string, 0, min(len(rep.Gate.Findings), 3))
		for _, f := range rep.Gate.Findings {
			if len(parts) >= 3 {
				break
			}
			if f.Path != "" {
				parts = append(parts, fmt.Sprintf("[%s] %s: %s", f.Check, f.Path, search.Printable(f.Detail)))
			} else {
				parts = append(parts, fmt.Sprintf("[%s] %s", f.Check, search.Printable(f.Detail)))
			}
		}
		return strings.Join(parts, "; ")
	}
	if err != nil {
		return firstLineCompact(err.Error(), 300)
	}
	return "land failed"
}

func firstLineCompact(raw string, maxLen int) string {
	s := strings.TrimSpace(search.Printable(raw))
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if maxLen > 0 && len(s) > maxLen {
		return string(trail.TruncateUTF8Bytes([]byte(s), maxLen)) + "..."
	}
	return s
}

func refreshMainStamp(root, branch, head string, vRep *VerifyReport) (string, error) {
	st, err := search.ComputeGoTreeState(root)
	if err != nil {
		return "", err
	}
	if st.TreeSHA256 != vRep.TreeSHA256 {
		return "", refusef("post-land tree digest mismatch (%s != %s)", st.TreeSHA256[:12], vRep.TreeSHA256[:12])
	}
	return WriteStamp(root, Stamp{
		TreeSHA256: st.TreeSHA256,
		UpdatedAt:  vRep.UpdatedAt,
		Branch:     branch,
		Head:       head,
		OK:         true,
	})
}
