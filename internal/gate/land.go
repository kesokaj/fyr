package gate

import (
	"context"
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
	Dir       string
	Branch    string
	Message   string
	Home      string
	Vocab     check.Vocabulary
	Timeout   time.Duration
	Lease     []string
	LeaseFile string
	MaxFiles  int
	MaxLines  int
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

// Land runs Gate on opts.Branch, squash-merges into a detached scratch worktree,
// runs Verify there, fast-forwards the current branch, deletes opts.Branch,
// and refreshes verify.stamp on the main checkout.
func Land(ctx context.Context, opts LandOptions) (LandReport, error) {
	lc, err := prepareLand(ctx, opts)
	if err != nil {
		return LandReport{}, err
	}
	rep := LandReport{MainRoot: lc.root, Target: lc.mainBranch, Branch: opts.Branch}
	gateRep, err := Gate(ctx, GateOptions{
		Dir: lc.root, Target: opts.Branch, Base: lc.baseHead,
		Lease: opts.Lease, LeaseFile: opts.LeaseFile,
		MaxFiles: opts.MaxFiles, MaxLines: opts.MaxLines,
	})
	rep.Gate = &gateRep
	if err != nil {
		return rep, err
	}
	landCommit, verifyRep, err := verifyInScratchWorktree(ctx, lc, opts)
	rep.Verify = verifyRep
	if err != nil {
		return rep, err
	}
	return finishLand(ctx, lc, opts.Branch, landCommit, &rep)
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

func verifyInScratchWorktree(ctx context.Context, lc landCtx, opts LandOptions) (string, *VerifyReport, error) {
	scratchDir := filepath.Join(lc.root, ".agents", "scratch")
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return "", nil, err
	}
	wtDir, err := os.MkdirTemp(scratchDir, "land-wt-*")
	if err != nil {
		return "", nil, err
	}
	defer func() {
		_ = removeWorktree(context.Background(), lc.root, wtDir)
		_ = os.RemoveAll(wtDir)
	}()
	if _, err := gitBytes(ctx, lc.root, "worktree", "add", "--detach", wtDir, lc.baseHead); err != nil {
		return "", nil, err
	}
	if _, err := gitBytes(ctx, wtDir, "merge", "--squash", "--no-commit", "--", "refs/heads/"+opts.Branch); err != nil {
		return "", nil, failf("squash merge failed: %v", err)
	}
	if _, err := gitBytes(ctx, wtDir, "commit", "-m", lc.msg); err != nil {
		return "", nil, err
	}
	landCommit, err := gitLine(ctx, wtDir, "rev-parse", "HEAD")
	if err != nil {
		return "", nil, err
	}
	vRep, err := Verify(ctx, VerifyOptions{Dir: wtDir, Home: opts.Home, Vocab: opts.Vocab, Timeout: opts.Timeout})
	return landCommit, &vRep, err
}

func finishLand(ctx context.Context, lc landCtx, branch, landCommit string, rep *LandReport) (LandReport, error) {
	if err := checkoutUnchanged(ctx, lc.root, lc.snap); err != nil {
		return *rep, err
	}
	if _, err := gitBytes(ctx, lc.root, "merge", "--ff-only", landCommit); err != nil {
		return *rep, err
	}
	rep.LandCommit = landCommit
	if _, err := gitBytes(ctx, lc.root, "branch", "-D", "--", branch); err != nil {
		return *rep, err
	}
	rep.BranchDel = true
	if rep.Verify != nil {
		stampPath, err := refreshMainStamp(lc.root, lc.mainBranch, landCommit, rep.Verify)
		if err != nil {
			return *rep, err
		}
		rep.StampPath = stampPath
	}
	rep.OK = true
	return *rep, nil
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
