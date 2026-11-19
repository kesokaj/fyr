package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

const (
	// DefaultMaxFiles caps changed files allowed by Gate unless overridden.
	DefaultMaxFiles = 50
	// DefaultMaxLines caps total added+deleted lines allowed by Gate unless overridden.
	DefaultMaxLines = 1500
)

// GateOptions configures deterministic worktree/branch gating.
type GateOptions struct {
	Dir           string
	Target        string
	Base          string
	Home          string
	Lease         []string
	LeaseFile     string
	MaxFiles      int
	MaxLines      int
	Judge         string
	JudgeModel    string
	JudgeRunner   func(context.Context, JudgeRequest) (JudgeResult, error)
	RecordVerdict func(JudgeVerdictRecord) error
}

// GateFinding describes a single deterministic gate violation.
// Secret findings never include matched line text.
type GateFinding struct {
	Check  string `json:"check"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail"`
}

// GateReport summarizes all 4 Gate checks and optional cross-family judge check.
type GateReport struct {
	OK            bool          `json:"ok"`
	MainRoot      string        `json:"mainRoot"`
	Target        string        `json:"target"`
	Branch        string        `json:"branch,omitempty"`
	Base          string        `json:"base"`
	MergeBase     string        `json:"mergeBase"`
	Head          string        `json:"head"`
	FilesChanged  int           `json:"filesChanged"`
	LinesAdded    int           `json:"linesAdded"`
	LinesDeleted  int           `json:"linesDeleted"`
	Findings      []GateFinding `json:"findings,omitempty"`
	JudgeModel    string        `json:"judgeModel,omitempty"`
	JudgeVerdict  string        `json:"judgeVerdict,omitempty"`
	JudgeFallback string        `json:"judgeFallback,omitempty"`
	DiffTruncated bool          `json:"diffTruncated,omitempty"`
}

type gateTarget struct {
	mainRoot  string
	wtRoot    string
	branch    string
	head      string
	base      string
	mergeBase string
}

// Gate runs the 4 deterministic checks (lease/mode/secret-names, numstat,
// added-line secrets, commit trailers) against a clean worktree or branch.
func Gate(ctx context.Context, opts GateOptions) (GateReport, error) {
	tgt, err := resolveGateTarget(ctx, opts)
	if err != nil {
		return GateReport{}, err
	}
	lease, err := loadLeasePatterns(opts)
	if err != nil {
		return GateReport{}, err
	}
	maxFiles, maxLines := gateLimits(opts)

	rep := GateReport{
		MainRoot:  tgt.mainRoot,
		Target:    opts.Target,
		Branch:    tgt.branch,
		Base:      tgt.base,
		MergeBase: tgt.mergeBase,
		Head:      tgt.head,
	}
	if err := runAllGateChecks(ctx, tgt, lease, maxFiles, maxLines, &rep); err != nil {
		return rep, err
	}
	if len(rep.Findings) > 0 {
		return rep, failf("gate failed with %d finding(s)", len(rep.Findings))
	}
	if strings.TrimSpace(opts.Judge) != "" {
		if err := applyGateJudge(ctx, opts, tgt, &rep); err != nil {
			return rep, err
		}
	}
	rep.OK = true
	return rep, nil
}

func gateLimits(opts GateOptions) (int, int) {
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	return maxFiles, maxLines
}

func resolveGateTarget(ctx context.Context, opts GateOptions) (gateTarget, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	if strings.TrimSpace(opts.Target) == "" {
		return gateTarget{}, refusef("missing target worktree or branch")
	}
	var tgt gateTarget
	if wtPath, ok := resolveWorktreeCandidate(dir, opts.Target); ok {
		if err := resolveWorktreeTarget(ctx, wtPath, &tgt); err != nil {
			return gateTarget{}, err
		}
	} else {
		if err := resolveBranchTarget(ctx, dir, opts.Target, &tgt); err != nil {
			return gateTarget{}, err
		}
	}
	return finalizeTargetBase(ctx, opts.Base, tgt)
}

func resolveWorktreeCandidate(dir, target string) (string, bool) {
	candidates := []string{target}
	if !filepath.IsAbs(target) && dir != "" && dir != "." {
		candidates = append(candidates, filepath.Join(dir, target))
	}
	for _, c := range candidates {
		info, err := os.Stat(c)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Lstat(filepath.Join(c, ".git")); err == nil {
			return c, true
		}
	}
	return "", false
}

func resolveWorktreeTarget(ctx context.Context, wtPath string, tgt *gateTarget) error {
	mainRoot, top, err := trail.Roots(wtPath)
	if err != nil {
		return refusef("resolve worktree %s: %v", wtPath, err)
	}
	dirty, err := checkoutDirty(ctx, top)
	if err != nil {
		return err
	}
	if dirty {
		return refusef("worktree %s is dirty; commit or discard changes first", top)
	}
	head, err := gitLine(ctx, top, "rev-parse", "HEAD")
	if err != nil {
		return refusef("resolve HEAD in %s: %v", top, err)
	}
	branch, _ := gitLine(ctx, top, "symbolic-ref", "--short", "-q", "HEAD")
	tgt.mainRoot = mainRoot
	tgt.wtRoot = top
	tgt.head = head
	tgt.branch = branch
	return nil
}

func resolveBranchTarget(ctx context.Context, dir, branch string, tgt *gateTarget) error {
	mainRoot, top, err := trail.Roots(dir)
	if err != nil {
		return refusef("resolve repo root: %v", err)
	}
	if mainRoot == "" {
		mainRoot = top
	}
	head, err := verifyCommitRef(ctx, mainRoot, "refs/heads/"+branch)
	if err != nil {
		head, err = verifyCommitRef(ctx, mainRoot, branch)
		if err != nil {
			return refusef("target %q is neither a worktree directory nor a valid ref", branch)
		}
	}
	tgt.mainRoot = mainRoot
	tgt.branch = strings.TrimPrefix(branch, "refs/heads/")
	tgt.head = head
	return nil
}

func finalizeTargetBase(ctx context.Context, baseOpt string, tgt gateTarget) (gateTarget, error) {
	baseRef := baseOpt
	if baseRef == "" {
		baseRef = "HEAD"
	}
	base, err := verifyCommitRef(ctx, tgt.mainRoot, baseRef)
	if err != nil {
		return gateTarget{}, refusef("resolve base %q: %v", baseRef, err)
	}
	mb, err := gitLine(ctx, tgt.mainRoot, "merge-base", base, tgt.head)
	if err != nil {
		return gateTarget{}, refusef("compute merge-base(%s, %s): %v", base, tgt.head, err)
	}
	tgt.base = base
	tgt.mergeBase = mb
	return tgt, nil
}
