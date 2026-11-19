package gate

import (
	"context"
	"os"
	"path"
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

func loadLeasePatterns(opts GateOptions) ([]string, error) {
	var patterns []string
	for _, raw := range opts.Lease {
		for _, p := range strings.Split(raw, ",") {
			cleaned, err := normalizeLeasePattern(p)
			if err != nil {
				return nil, err
			}
			if cleaned != "" {
				patterns = append(patterns, cleaned)
			}
		}
	}
	if opts.LeaseFile != "" {
		data, err := os.ReadFile(opts.LeaseFile)
		if err != nil {
			return nil, refusef("read lease file %s: %v", opts.LeaseFile, err)
		}
		fromFile, err := ParseLease(string(data))
		if err != nil {
			return nil, err
		}
		if len(fromFile) == 0 {
			return nil, refusef("lease file %s contained no patterns", opts.LeaseFile)
		}
		patterns = append(patterns, fromFile...)
	}
	return patterns, nil
}

// ParseLease extracts file/glob lease patterns from plain lists or markdown/brief
// `Files:` sections.
func ParseLease(text string) ([]string, error) {
	var out []string
	inFiles := false
	hasFilesHeader := hasFilesSection(text)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if handled, keepIn, items := parseLeaseLine(line, hasFilesHeader, inFiles); handled {
			inFiles = keepIn
			for _, item := range items {
				clean, err := normalizeLeasePattern(item)
				if err != nil {
					return nil, err
				}
				if clean != "" {
					out = append(out, clean)
				}
			}
		} else {
			inFiles = keepIn
		}
	}
	return out, nil
}

func stripHeadingHashes(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return line, false
	}
	return strings.TrimSpace(strings.TrimLeft(line, "#")), true
}

func parseLeaseHeader(line string) (string, bool) {
	body, _ := stripHeadingHashes(line)
	low := strings.ToLower(body)
	if strings.HasPrefix(low, "files:") || strings.HasPrefix(low, "lease:") {
		_, rest, _ := strings.Cut(body, ":")
		return rest, true
	}
	return "", false
}

func hasFilesSection(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		if _, ok := parseLeaseHeader(strings.TrimSpace(raw)); ok {
			return true
		}
	}
	return false
}

func parseLeaseLine(line string, hasHeader, inFiles bool) (bool, bool, []string) {
	if line == "" {
		return false, inFiles, nil
	}
	if rest, ok := parseLeaseHeader(line); ok {
		return true, true, splitLeaseTokens(rest)
	}
	if strings.HasPrefix(line, "#") {
		return false, false, nil
	}
	if hasHeader {
		if !inFiles {
			return false, false, nil
		}
		item, ok := stripBulletPrefix(line)
		if !ok {
			return false, false, nil
		}
		return true, true, splitLeaseTokens(item)
	}
	item, _ := stripBulletPrefix(line)
	return true, false, splitLeaseTokens(item)
}

func stripBulletPrefix(line string) (string, bool) {
	for _, p := range []string{"- ", "* "} {
		if strings.HasPrefix(line, p) {
			return strings.TrimSpace(line[len(p):]), true
		}
	}
	return line, false
}

func splitLeaseTokens(s string) []string {
	var out []string
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		tok := strings.Trim(field, "`\"'")
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func normalizeLeasePattern(p string) (string, error) {
	p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), "`\"'"))
	if p == "" {
		return "", nil
	}
	slash := filepath.ToSlash(p)
	if strings.HasPrefix(slash, "/") || strings.Contains(slash, "..") {
		return "", refusef("invalid lease pattern %q", p)
	}
	slash = strings.TrimPrefix(slash, "./")
	return slash, nil
}

func matchesAnyLease(patterns []string, relPath string) bool {
	relPath = filepath.ToSlash(strings.TrimPrefix(relPath, "./"))
	for _, pat := range patterns {
		if matchLeasePattern(pat, relPath) {
			return true
		}
	}
	return false
}

func matchLeasePattern(pat, relPath string) bool {
	if strings.HasSuffix(pat, "/") {
		return relPath == strings.TrimSuffix(pat, "/") || strings.HasPrefix(relPath, pat)
	}
	if !strings.Contains(pat, "**") {
		ok, err := path.Match(pat, relPath)
		return err == nil && ok
	}
	return matchSegments(strings.Split(pat, "/"), strings.Split(relPath, "/"))
}

func matchSegments(pats, segs []string) bool {
	if len(pats) == 0 {
		return len(segs) == 0
	}
	if pats[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pats[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pats[0], segs[0])
	return err == nil && ok && matchSegments(pats[1:], segs[1:])
}
