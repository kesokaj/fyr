package auto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// EvalRequest is one invocation of `fyr eval` from auto (eval-rerun or eval-gate).
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

type completedRerun struct {
	row EvalRerunRow
	out EvalOutcome
}

func (r *run) execEvalRerun(root string, p *Project, t Task) (string, []Finding) {
	r.workerRan = true
	p.LastClass = ClassEvalRerun
	runsBefore := r.st.modelRunsToday(r.o.now())
	if today := r.o.now().Format("2006-01-02"); r.st.Date < today {
		r.st.Date, r.st.ModelRuns = today, 0
	}
	r.st.ModelRuns++
	if err := saveState(r.o.Home, r.st); err != nil {
		r.failed = true
		return "eval-rerun state error: " + oneLine(err.Error()), []Finding{{"eval-rerun", "failed", ClassEvalRerun, oneLine(err.Error())}}
	}
	var findings []Finding
	var done []completedRerun
	stopNote := ""
	for i, rr := range t.Reruns {
		if i > 0 {
			if reason := r.betweenEvalRows(root, runsBefore, rr.ID, &findings); reason != "" {
				stopNote = "; " + reason
				break
			}
		}
		out, err := r.runEvalRow(EvalRequest{Dir: root, Home: r.o.evalHome(), Fixture: r.o.evalFixture(root), Runs: rr.Runs, Row: rr.ID})
		if err != nil {
			r.failed = true
			findings = append(findings, Finding{"eval-rerun", "failed", rr.ID, oneLine(err.Error())})
			break
		}
		done = append(done, completedRerun{row: rr, out: out})
		findings = append(findings, Finding{"eval-rerun", rr.Reason, rr.ID, fmt.Sprintf("%d of %d PASS (%d errors, k=%d)", out.Passed, out.Graded, out.Errors, rr.Runs)})
	}
	return r.finishEvalRerun(root, p, t, done, stopNote), findings
}

func (r *run) betweenEvalRows(root string, runsBefore int, rowID string, findings *[]Finding) string {
	if !r.again(root) {
		*findings = append(*findings, Finding{"eval-rerun", "stopped", rowID, "stopped before row by machine or switch gate"})
		return "stopped between rows"
	}
	r.usageRead = false
	text, uerr := r.usage()
	line, stop := gateBudget(runsBefore, text, uerr)
	r.st.Budget = strings.TrimPrefix(line, "G9 ")
	if stop {
		*findings = append(*findings, Finding{"eval-rerun", "budget", rowID, "stopped before row: " + line})
		return line
	}
	return ""
}

func (r *run) finishEvalRerun(root string, p *Project, t Task, done []completedRerun, stopNote string) string {
	if len(done) == 0 {
		return "eval-rerun: 0 rows completed" + stopNote
	}
	curCLI := r.currentAgyVersion()
	recID := r.storeEvalRerunRecord(root, curCLI, done, stopNote)
	if len(done) == len(t.Reruns) && !r.failed && stopNote == "" {
		p.LastEvalRerun = r.o.now()
		if curCLI != "" && !strings.HasPrefix(curCLI, "unknown") {
			p.LastEvalCLI = curCLI
		}
	}
	return fmt.Sprintf("eval-rerun: %d of %d rows run, record %s%s", len(done), len(t.Reruns), recID, stopNote)
}

func (r *run) storeEvalRerunRecord(root, cli string, done []completedRerun, stopNote string) string {
	pass, graded, errs := 0, 0, 0
	var b strings.Builder
	head := gitShortCommit(root)
	if head == "" {
		head = "unknown"
	}
	fmt.Fprintf(&b, "# Eval rerun at `%s`\n\n- cli: `%s`\n", head, oneLine(cli))
	if stopNote != "" {
		fmt.Fprintf(&b, "- note: `%s`\n", oneLine(strings.TrimPrefix(stopNote, "; ")))
	}
	b.WriteString("\n| row | reason | k | passed | graded | errors |\n|---|---|---|---|---|---|\n")
	for _, d := range done {
		pass += d.out.Passed
		graded += d.out.Graded
		errs += d.out.Errors
		fmt.Fprintf(&b, "| `%s` | `%s` | %d | %d | %d | %d |\n", d.row.ID, d.row.Reason, d.row.Runs, d.out.Passed, d.out.Graded, d.out.Errors)
	}
	id, err := brain.Add(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.AddInput{
		Kind:    "testing",
		Status:  "closed",
		Project: root,
		Title:   fmt.Sprintf("Eval rerun — %d of %d PASS at %s (%d errors)", pass, graded, head, errs),
		Body:    strings.NewReader(redact.Redact(b.String())),
	})
	if err != nil {
		r.failed = true
		return "failed"
	}
	return id
}

func (r *run) runEvalRow(req EvalRequest) (EvalOutcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.o.limit(workerTimeout))
	defer cancel()
	if r.o.Eval != nil {
		return r.o.Eval(ctx, req)
	}
	return runFyrEval(ctx, r.o.self(), req)
}

func runFyrEval(ctx context.Context, self string, req EvalRequest) (EvalOutcome, error) {
	if err := ensureDir(req.Home); err != nil {
		return EvalOutcome{}, err
	}
	tmp, err := os.MkdirTemp(dir(req.Home), "eval-clone-")
	if err != nil {
		return EvalOutcome{}, err
	}
	defer os.RemoveAll(tmp)
	cloneDir := filepath.Join(tmp, "repo")
	if out, err := trail.Command(req.Dir, "clone", "--quiet", "--local", req.Dir, cloneDir).CombinedOutput(); err != nil {
		return EvalOutcome{}, fmt.Errorf("clone for eval: %v: %s", err, oneLine(string(out)))
	}
	if req.Commit != "" {
		if out, err := applyGit(cloneDir, nil, "checkout", "--detach", req.Commit); err != nil {
			return EvalOutcome{}, fmt.Errorf("checkout %s for eval: %v: %s", req.Commit, err, oneLine(out))
		}
	}
	argv := []string{"eval", "--home", req.Home, "--runs", strconv.Itoa(max(req.Runs, 1))}
	if req.Fixture != "" {
		argv = append(argv, "--fixture", req.Fixture)
	}
	if req.SuiteFile != "" {
		argv = append(argv, "--suite-file", req.SuiteFile)
	}
	argv = append(argv, req.Row)
	cmd := exec.CommandContext(ctx, self, argv...)
	cmd.Dir = cloneDir
	for _, kv := range sandbox.WithoutGit(os.Environ()) {
		if !strings.HasPrefix(kv, "FYR_REPO=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	raw, runErr := cmd.CombinedOutput()
	out := string(raw)
	m := evalSummaryRe.FindStringSubmatch(out)
	if m == nil {
		if runErr != nil {
			return EvalOutcome{}, fmt.Errorf("fyr eval %s: %v: %s", req.Row, runErr, oneLine(out))
		}
		return EvalOutcome{}, fmt.Errorf("fyr eval %s: no summary line in output", req.Row)
	}
	pass, _ := strconv.Atoi(m[1])
	graded, _ := strconv.Atoi(m[2])
	errs, _ := strconv.Atoi(m[3])
	return EvalOutcome{Row: req.Row, Passed: pass, Graded: graded, Errors: errs, RecordID: m[4], Output: out}, nil
}
