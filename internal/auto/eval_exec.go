package auto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/trail"
)

// EvalRequest is one invocation of `fyr eval` from auto (eval-gate).
type EvalRequest struct {
	Dir       string
	Home      string
	Fixture   string
	SuiteFile string
	Commit    string
	Runs      int
	Row       string
}

// EvalOutcome is the result of one `fyr eval` invocation.
type EvalOutcome struct {
	Row      string
	Passed   int
	Graded   int
	Errors   int
	RecordID string
	Output   string
}

var evalSummaryRe = regexp.MustCompile(`(?m)^eval: (\d+) of (\d+) PASS, (\d+) errors?; record (\S+)\s*$`)

func (o Options) evalHome() string {
	if o.EvalHome != "" {
		return o.EvalHome
	}
	return filepath.Join(o.realHome(), "sandbox", "fyr-auto", "eval-home")
}

// evalFixture is the repository each row clones; "" clones root itself. A
// root with a tracked suite gets "": its setups run the fixture script in
// the clone (ADR-500), which another repository does not hold.
func (o Options) evalFixture(root string) string {
	if o.EvalFixture != "" {
		return o.EvalFixture
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(eval.SuitePath))); err == nil {
		return ""
	}
	def := filepath.Join(o.realHome(), "sandbox", "fyr-eval", "fixture")
	if st, err := os.Stat(filepath.Join(def, ".git")); err == nil && st.IsDir() {
		return def
	}
	return ""
}

func (r *run) betweenEvalRows(root string, runsBefore int, rowID string, findings *[]Finding) string {
	if !r.again(root) {
		*findings = append(*findings, Finding{"eval-gate", "stopped", rowID, "stopped before row by machine or switch gate"})
		return "stopped between rows"
	}
	r.usageRead = false
	text, uerr := r.usage()
	line, stop := gateBudget(runsBefore, text, uerr)
	r.st.Budget = strings.TrimPrefix(line, "G9 ")
	if stop {
		*findings = append(*findings, Finding{"eval-gate", "budget", rowID, "stopped before row: " + line})
		return line
	}
	return ""
}

// readCheckoutSuite is the suite `fyr eval` reads in root: the tracked file
// when root has one (ADR-500), else the GUIDE-eval record of root's scope.
func readCheckoutSuite(home, root string) ([]eval.Row, error) {
	if rows, found, err := eval.ReadTrackedSuite(root, false); found || err != nil {
		return rows, err
	}
	recs, ok, err := brain.ReadScope(home, root)
	if err != nil || !ok {
		return nil, fmt.Errorf("no trail in %s", root)
	}
	for _, rec := range recs {
		if rec.ID == "GUIDE-eval" {
			return eval.ParseSuite(rec.Body)
		}
	}
	return nil, fmt.Errorf("no GUIDE-eval in %s", root)
}

type harnessTarget struct {
	name, relPath string
}

func listHarnessTargets(root string) []harnessTarget {
	var out []harnessTarget
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "google-catalog" {
			continue
		}
		rel := filepath.Join("skills", e.Name())
		if _, err := os.Stat(filepath.Join(root, rel, "SKILL.md")); err == nil {
			out = append(out, harnessTarget{name: e.Name(), relPath: rel})
		}
	}
	return out
}

func rowNamesTarget(r eval.Row, name string) bool {
	if name == "" {
		return false
	}
	if r.Agent == name || r.ID == name || strings.HasPrefix(r.ID, name+"-") {
		return true
	}
	return mentionsWord(r.Prompt, name) || mentionsWord(r.Prompt2, name)
}

func mentionsWord(text, word string) bool {
	for start := 0; start < len(text); {
		idx := strings.Index(text[start:], word)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(word)
		beforeOK := pos == 0 || !isIdentByte(text[pos-1])
		afterOK := end == len(text) || !isIdentByte(text[end])
		if beforeOK && afterOK {
			return true
		}
		start = pos + 1
	}
	return false
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

func (r *run) runEvalRow(ctx context.Context, req EvalRequest) (EvalOutcome, error) {
	if ctx == nil {
		ctx = r.context()
	}
	tctx, cancel := context.WithTimeout(ctx, r.o.limit(workerTimeout))
	defer cancel()
	if r.o.Eval != nil {
		return r.o.Eval(tctx, req)
	}
	return runFyrEval(tctx, r.o.self(), req)
}

func runFyrEval(ctx context.Context, self string, req EvalRequest) (EvalOutcome, error) {
	if strings.TrimSpace(self) == "" {
		return EvalOutcome{}, errors.New("fyr executable path unknown")
	}
	if err := ensureDir(req.Home); err != nil {
		return EvalOutcome{}, err
	}
	tmp, err := os.MkdirTemp(dir(req.Home), "eval-clone-")
	if err != nil {
		return EvalOutcome{}, err
	}
	defer os.RemoveAll(tmp)
	cloneDir := filepath.Join(tmp, "repo")
	if out, err := trail.Command(req.Dir, "clone", "--quiet", "--local", "--", req.Dir, cloneDir).CombinedOutput(); err != nil {
		return EvalOutcome{}, fmt.Errorf("clone for eval: %v: %s", err, oneLine(string(out)))
	}
	if req.Commit != "" {
		if out, err := applyGit(cloneDir, nil, "checkout", "--detach", "--end-of-options", req.Commit); err != nil {
			return EvalOutcome{}, fmt.Errorf("checkout %s for eval: %v: %s", req.Commit, err, oneLine(out))
		}
	}
	cmd := exec.CommandContext(ctx, self, evalArgv(req)...)
	cmd.Dir = cloneDir
	cmd.Env = evalCleanEnv()
	raw, runErr := cmd.CombinedOutput()
	return parseEvalOutcome(req.Row, string(raw), runErr)
}

func evalArgv(req EvalRequest) []string {
	argv := []string{"eval", "--home", req.Home, "--runs", strconv.Itoa(max(req.Runs, 1))}
	if req.Fixture != "" {
		argv = append(argv, "--fixture", req.Fixture)
	}
	if req.SuiteFile != "" {
		argv = append(argv, "--suite-file", req.SuiteFile)
	}
	return append(argv, req.Row)
}

func evalCleanEnv() []string {
	var env []string
	for _, kv := range isolate.WithoutGit(os.Environ()) {
		if !strings.HasPrefix(kv, "FYR_REPO=") {
			env = append(env, kv)
		}
	}
	return env
}

func parseEvalOutcome(row, out string, runErr error) (EvalOutcome, error) {
	m := evalSummaryRe.FindStringSubmatch(out)
	if m == nil {
		if runErr != nil {
			return EvalOutcome{}, fmt.Errorf("fyr eval %s: %v: %s", row, runErr, oneLine(out))
		}
		return EvalOutcome{}, fmt.Errorf("fyr eval %s: no summary line in output", row)
	}
	ok, _ := strconv.Atoi(m[1])
	graded, _ := strconv.Atoi(m[2])
	errs, _ := strconv.Atoi(m[3])
	return EvalOutcome{Row: row, Passed: ok, Graded: graded, Errors: errs, RecordID: m[4], Output: out}, nil
}
