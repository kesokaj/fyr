package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// StampRelPath matches trail.VerifyStampRelPath for stop-hook compatibility.
const StampRelPath = trail.VerifyStampRelPath

const defaultVerifyTimeout = 30 * time.Minute

// Stamp records a passing verification run bound to a Go source tree digest.
type Stamp struct {
	TreeSHA256 string `json:"treeSha256"`
	UpdatedAt  string `json:"updatedAt"`
	Branch     string `json:"branch,omitempty"`
	Head       string `json:"head,omitempty"`
	OK         bool   `json:"ok"`
}

// VerifyOptions configures Verify.
type VerifyOptions struct {
	Dir     string
	Home    string
	Vocab   check.Vocabulary
	Timeout time.Duration
	Now     func() time.Time
}

// StepResult records one deterministic verification step.
type StepResult struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	DurationMs int64  `json:"durationMs"`
	Detail     string `json:"detail,omitempty"`
}

// VerifyReport summarizes a Verify run.
type VerifyReport struct {
	OK         bool         `json:"ok"`
	Root       string       `json:"root"`
	Branch     string       `json:"branch,omitempty"`
	Head       string       `json:"head,omitempty"`
	TreeSHA256 string       `json:"treeSha256"`
	StampPath  string       `json:"stampPath,omitempty"`
	UpdatedAt  string       `json:"updatedAt,omitempty"`
	Steps      []StepResult `json:"steps"`
}

// Verify runs deterministic repo checks and writes StampRelPath iff all pass
// and the Go source tree did not change mid-run.
func Verify(ctx context.Context, opts VerifyOptions) (VerifyReport, error) {
	rep, top, err := initVerify(ctx, opts)
	if err != nil {
		return rep, err
	}
	before, err := search.ComputeGoTreeState(top)
	if err != nil {
		return rep, err
	}
	rep.TreeSHA256 = before.TreeSHA256
	rep.Branch = before.Branch

	if err := runVerifySteps(ctx, top, opts, &rep); err != nil {
		_ = removeStaleStamp(top)
		return rep, err
	}
	after, err := search.ComputeGoTreeState(top)
	if err != nil {
		_ = removeStaleStamp(top)
		return rep, err
	}
	if after.TreeSHA256 != before.TreeSHA256 {
		_ = removeStaleStamp(top)
		return rep, refusef("tree changed during verify (%s != %s)", before.TreeSHA256[:12], after.TreeSHA256[:12])
	}
	return finishVerify(top, opts, rep)
}

func initVerify(ctx context.Context, opts VerifyOptions) (VerifyReport, string, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	_, top, err := trail.Roots(dir)
	if err != nil {
		return VerifyReport{}, "", refusef("resolve checkout root: %v", err)
	}
	if err := checkScratchNoSymlinks(top); err != nil {
		return VerifyReport{Root: top}, "", err
	}
	if err := ensureStampNotSymlink(top); err != nil {
		return VerifyReport{Root: top}, "", err
	}
	head, _ := gitLine(ctx, top, "rev-parse", "HEAD")
	return VerifyReport{Root: top, Head: head}, top, nil
}

func finishVerify(top string, opts VerifyOptions, rep VerifyReport) (VerifyReport, error) {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	updatedAt := nowFn().UTC().Format(time.RFC3339)
	st := Stamp{
		TreeSHA256: rep.TreeSHA256,
		UpdatedAt:  updatedAt,
		Branch:     rep.Branch,
		Head:       rep.Head,
		OK:         true,
	}
	stampPath, err := WriteStamp(top, st)
	if err != nil {
		return rep, err
	}
	rep.OK = true
	rep.StampPath = stampPath
	rep.UpdatedAt = updatedAt
	return rep, nil
}

func runVerifySteps(ctx context.Context, top string, opts VerifyOptions, rep *VerifyReport) error {
	if hasGoMod(top) {
		if err := runGofmtStep(ctx, top, rep); err != nil {
			return err
		}
		if err := runCmdStep(ctx, top, rep, "go-vet", "go", "vet", "./..."); err != nil {
			return err
		}
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = defaultVerifyTimeout
		}
		if err := runCmdStep(ctx, top, rep, "go-test", "go", "test", "-race", "-timeout", timeout.String(), "./..."); err != nil {
			return err
		}
	}
	return runCheckStep(top, opts, rep)
}

func hasGoMod(top string) bool {
	info, err := os.Lstat(filepath.Join(top, "go.mod"))
	return err == nil && info.Mode().IsRegular()
}

const gofmtBatchSize = 200

func runGofmtStep(ctx context.Context, top string, rep *VerifyReport) error {
	start := time.Now()
	files, err := nonHiddenGoFiles(top)
	if err != nil {
		rep.Steps = append(rep.Steps, StepResult{Name: "gofmt", DurationMs: time.Since(start).Milliseconds(), Detail: err.Error()})
		return err
	}
	if len(files) == 0 {
		rep.Steps = append(rep.Steps, StepResult{Name: "gofmt", OK: true, DurationMs: time.Since(start).Milliseconds(), Detail: "no non-hidden .go files"})
		return nil
	}
	unformatted, out, err := runGofmtBatches(ctx, top, files, gofmtBatchSize)
	dt := time.Since(start).Milliseconds()
	if err != nil {
		rep.Steps = append(rep.Steps, StepResult{Name: "gofmt", DurationMs: dt, Detail: out})
		return failf("gofmt failed: %s", out)
	}
	if len(unformatted) > 0 {
		joined := strings.Join(unformatted, "\n")
		rep.Steps = append(rep.Steps, StepResult{Name: "gofmt", DurationMs: dt, Detail: joined})
		return failf("gofmt unformatted files:\n%s", joined)
	}
	rep.Steps = append(rep.Steps, StepResult{Name: "gofmt", OK: true, DurationMs: dt})
	return nil
}

func runGofmtBatches(ctx context.Context, top string, files []string, batchSize int) ([]string, string, error) {
	if batchSize <= 0 {
		batchSize = gofmtBatchSize
	}
	var unformatted []string
	for i := 0; i < len(files); i += batchSize {
		end := min(i+batchSize, len(files))
		args := append([]string{"-l"}, files[i:end]...)
		out, err := runTool(ctx, top, "gofmt", args...)
		if err != nil {
			return nil, out, err
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				unformatted = append(unformatted, line)
			}
		}
	}
	return unformatted, "", nil
}

func nonHiddenGoFiles(top string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(top, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == top {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") {
			rel, relErr := filepath.Rel(top, path)
			if relErr != nil {
				return relErr
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func runCmdStep(ctx context.Context, top string, rep *VerifyReport, name, bin string, args ...string) error {
	start := time.Now()
	out, err := runTool(ctx, top, bin, args...)
	dt := time.Since(start).Milliseconds()
	if err != nil {
		rep.Steps = append(rep.Steps, StepResult{Name: name, DurationMs: dt, Detail: out})
		return failf("%s failed: %s", name, out)
	}
	rep.Steps = append(rep.Steps, StepResult{Name: name, OK: true, DurationMs: dt})
	return nil
}

func runTool(ctx context.Context, top, bin string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = top
	cmd.Env = withoutGitRepoVars(os.Environ())
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

func runCheckStep(top string, opts VerifyOptions, rep *VerifyReport) error {
	start := time.Now()
	var cr check.Report
	var err error
	if opts.Home != "" {
		cr, err = check.Run(top, opts.Vocab, opts.Home)
	} else {
		cr, err = check.Run(top, opts.Vocab)
	}
	dt := time.Since(start).Milliseconds()
	if err != nil {
		rep.Steps = append(rep.Steps, StepResult{Name: "fyr-check", DurationMs: dt, Detail: err.Error()})
		return err
	}
	if len(cr.Findings) > 0 {
		detail := formatCheckFindings(cr.Findings)
		rep.Steps = append(rep.Steps, StepResult{Name: "fyr-check", DurationMs: dt, Detail: detail})
		return failf("fyr check: %d finding(s)\n%s", len(cr.Findings), detail)
	}
	rep.Steps = append(rep.Steps, StepResult{Name: "fyr-check", OK: true, DurationMs: dt})
	return nil
}

func formatCheckFindings(findings []check.Finding) string {
	lines := make([]string, 0, len(findings))
	for _, f := range findings {
		if f.Line > 0 {
			lines = append(lines, fmt.Sprintf("[%s] %s:%d: %s", f.Rule, f.Where, f.Line, f.Text))
		} else {
			lines = append(lines, fmt.Sprintf("[%s] %s: %s", f.Rule, f.Where, f.Text))
		}
	}
	return strings.Join(lines, "\n")
}

func ensureStampNotSymlink(root string) error {
	p := filepath.Join(root, filepath.FromSlash(StampRelPath))
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return refusef("stamp path %s is not a regular file", p)
	}
	return nil
}

func removeStaleStamp(root string) error {
	if err := checkScratchNoSymlinks(root); err != nil {
		return err
	}
	if err := ensureStampNotSymlink(root); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(root, filepath.FromSlash(StampRelPath)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ReadStamp reads and validates StampRelPath under root without following symlinks.
func ReadStamp(root string) (Stamp, error) {
	st, err := trail.ReadVerifyStamp(root)
	if err != nil {
		if errors.Is(err, trail.ErrStampSymlink) || errors.Is(err, trail.ErrStampNotRegular) {
			return Stamp{}, refusef("%v", err)
		}
		return Stamp{}, err
	}
	return Stamp{
		TreeSHA256: st.TreeSHA256,
		UpdatedAt:  st.UpdatedAt,
		Branch:     st.Branch,
		Head:       st.Head,
		OK:         st.OK,
	}, nil
}

// WriteStamp atomically writes StampRelPath under root with 0600 permissions.
func WriteStamp(root string, st Stamp) (string, error) {
	if err := checkScratchNoSymlinks(root); err != nil {
		return "", err
	}
	if err := ensureStampNotSymlink(root); err != nil {
		return "", err
	}
	scratchDir := filepath.Join(root, ".agents", "scratch")
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(scratchDir, "verify.stamp.*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	dst := filepath.Join(root, filepath.FromSlash(StampRelPath))
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	return dst, nil
}
