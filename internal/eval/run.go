package eval

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// Options is one invocation of eval.
type Options struct {
	Root       string // project checkout (suite, record scope, default fixture)
	Home       string // fyr's home: sandboxes, stores
	RealHome   string // $HOME: the token, the fingerprint
	Fixture    string // the repository each row clones; "" is Root
	Self       string // os.Executable(): the fyr that installs and lists
	Repo       string // the install source tree (D16); "" is the harness embedded in Self
	Runs       int    // trials per row, 1 to MaxRuns; 0 is 1
	Parallel   int    // trials at once, 1 to MaxParallel; 0 is 1
	Out        io.Writer
	Now        func() time.Time
	BrainWatch func(dbPath, scratchDir string, scopes []string) (string, bool) // check c per-scope brain.db watch (ADR-506)
}

// MaxRuns is the most trials of one row an invocation runs (the
// eval-metrics plan D25).
const MaxRuns = 25

// MaxParallel is the most trials an invocation runs at once (Item 23 of
// PLAN-2026-11-02-1806-fyr-speed).
const MaxParallel = MaxRuns

// Result is one trial's outcome: one run of a row in its own sandbox.
type Result struct {
	Row    string
	Trial  int    // 1 to Options.Runs
	Run    string // "<stamp>/<row>/<trial>", the sandbox under ProjectSandboxRoot; the trial's key
	Status string // StatusPass, StatusFail or StatusError
	Cause  string // classify's closed set; "" for a PASS
	// Reasons are the fixed reason strings, in the order the steps gave
	// them; Cause names one of them.
	Reasons        []string
	AgyExit        int
	Started        time.Time // the trial's start
	Seconds        int       // the whole trial
	SessionSeconds int       // the agy session(s); -1 when none started
	Dir            string    // the sandbox
}

// The fixed reason strings of a FAIL or an ERROR (SPEC-fyr-eval § One row,
// step 15); classify maps each to its status and cause.
// The steps internal/sandbox runs carry its strings; eval's own are records,
// check, interrupted and ReasonAgent.
const (
	ReasonExists      = sandbox.ReasonExists
	ReasonClone       = sandbox.ReasonClone
	ReasonRecords     = "records list"
	ReasonSetup       = sandbox.ReasonSetup
	ReasonInstall     = sandbox.ReasonInstall
	ReasonSettings    = sandbox.ReasonSettings
	ReasonToken       = sandbox.ReasonToken
	ReasonCheck       = "check failed"
	ReasonDenied      = sandbox.ReasonDenied
	ReasonInvalid     = sandbox.ReasonInvalid
	ReasonProcs       = sandbox.ReasonProcs
	ReasonRealConfig  = sandbox.ReasonRealConfig
	ReasonCredentials = sandbox.ReasonCredentials
	ReasonInterrupted = "interrupted"
	ReasonReplaced    = sandbox.ReasonReplaced
	ReasonWrite       = sandbox.ReasonWrite
)

// ProcsChecked reports whether safety check e runs on this system (O1).
const ProcsChecked = sandbox.ProcsChecked

// ReasonAgent is the FAIL reason of an agent row whose agent the
// conversation store does not show invoked.
func ReasonAgent(agent string) string {
	return agentPrefix + agent + " in the conversation store"
}

// agentPrefix is ReasonAgent's fixed start, which classify matches.
const agentPrefix = "no invoke_subagent of "

const tokenRel = sandbox.TokenRel

// hostOS is runtime.GOOS, a variable so a test can run the darwin branch.
var hostOS = runtime.GOOS

// ProjectSandboxRoot is where eval makes its sandboxes for project root under
// fyr's home: <home>/.config/fyr/eval/<project> (D9 of PLAN-2026-11-02-1806-fyr-speed).
func ProjectSandboxRoot(home, root string) string { return sandbox.ProjectRoot(home, "eval", root) }

// Preflight is the refusals before step 1 that come before the suite is read
// (D20): root is a checkout, and the sandbox root passes guards a and b on
// its nearest existing ancestor. cmd calls it first; Run calls it again.
func Preflight(o Options) error {
	if err := sandbox.OutsideCheckout(o.Root); err == nil {
		return fmt.Errorf("%s is not a git checkout", o.Root)
	}
	return sandbox.CheckRoot(o.RealHome, o.Repo, ProjectSandboxRoot(o.Home, o.Root))
}

// Run refuses (error, no row run) or runs every row Options.Runs times, up
// to Options.Parallel trials at once, printing in trial order (rows in the
// order given and trials innermost), and returns one result per started
// trial. On ctx's end in-flight trials finish as ERROR (interrupted),
// unstarted trials are not run, and the trials started are returned with a
// nil error (D22; the eval-metrics plan D18; Item 23).
func Run(ctx context.Context, o Options, rows []Row) ([]Result, error) {
	if o.Runs == 0 {
		o.Runs = 1
	}
	if o.Runs < 1 || o.Runs > MaxRuns {
		return nil, fmt.Errorf("runs %d: want 1 to %d", o.Runs, MaxRuns)
	}
	if o.Parallel == 0 {
		o.Parallel = 1
	}
	if o.Parallel < 1 || o.Parallel > MaxParallel {
		return nil, fmt.Errorf("parallel %d: want 1 to %d", o.Parallel, MaxParallel)
	}
	if o.Fixture == "" {
		o.Fixture = o.Root
	}
	if abs, err := filepath.Abs(o.Fixture); err == nil {
		o.Fixture = abs
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := Preflight(o); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("no row to run")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.ID] {
			return nil, fmt.Errorf("row %s is named twice", r.ID)
		}
		seen[r.ID] = true
	}
	tok, err := sandbox.CheckLogin(o.RealHome, hostOS)
	if err != nil {
		return nil, err
	}
	agy, err := exec.LookPath("agy")
	if err != nil {
		return nil, errors.New("agy not found on PATH")
	}
	return runTrials(ctx, o, rows, agy, tok, o.Now().Format("2006-01-02-150405")), nil
}

type trialSpec struct {
	row   Row
	trial int
	run   string
	dir   string
}

type trialDone struct {
	idx int
	res Result
	out string
}

func trialSpecs(o Options, rows []Row, stamp string) []trialSpec {
	root := ProjectSandboxRoot(o.Home, o.Root)
	specs := make([]trialSpec, 0, len(rows)*o.Runs)
	for _, r := range rows {
		for i := 1; i <= o.Runs; i++ {
			run := path.Join(stamp, r.ID, strconv.Itoa(i))
			specs = append(specs, trialSpec{
				row:   r,
				trial: i,
				run:   run,
				dir:   filepath.Join(root, filepath.FromSlash(run)),
			})
		}
	}
	return specs
}

// runTrials runs each row o.Runs times under one stamp, trials innermost, up
// to o.Parallel at once, printing each trial's notes and result line in trial
// order, and stops launching once ctx has ended.
func runTrials(ctx context.Context, o Options, rows []Row, agy, tok, stamp string) []Result {
	specs := trialSpecs(o, rows, stamp)
	results := make([]Result, len(specs))
	outs := make([]string, len(specs))
	done := make([]bool, len(specs))
	ch := make(chan trialDone, len(specs))
	next, inFlight, launched, printed := 0, 0, 0, 0
	launch := func() {
		for next < len(specs) && inFlight < o.Parallel && ctx.Err() == nil {
			idx, s := next, specs[next]
			next++
			inFlight++
			launched++
			go func() {
				var buf bytes.Buffer
				tOpts := o
				tOpts.Out = &buf
				res := runRow(ctx, tOpts, s.row, agy, tok, s.dir)
				res.Trial, res.Run = s.trial, s.run
				ch <- trialDone{idx: idx, res: res, out: buf.String()}
			}()
		}
	}
	launch()
	for inFlight > 0 {
		d := <-ch
		inFlight--
		results[d.idx], outs[d.idx], done[d.idx] = d.res, d.out, true
		for printed < launched && done[printed] {
			fmt.Fprint(o.Out, outs[printed])
			fmt.Fprintln(o.Out, resultLine(results[printed]))
			printed++
		}
		launch()
	}
	return results[:launched]
}

// resultLine is a trial's line (SPEC-fyr-eval § The verb): the row and its
// trial, the status with the cause before the reasons, and the session's
// seconds when one started.
func resultLine(r Result) string {
	v := r.Status
	if r.Cause != "" || len(r.Reasons) > 0 {
		// A reason that is the cause word for word (interrupted) is said once.
		said := []string{r.Cause}
		for _, x := range r.Reasons {
			if x != r.Cause {
				said = append(said, x)
			}
		}
		v += " (" + strings.Join(said, "; ") + ")"
	}
	secs := fmt.Sprintf("%ds", r.Seconds)
	if r.SessionSeconds >= 0 {
		secs += fmt.Sprintf(" (%ds session)", r.SessionSeconds)
	}
	return sandbox.Printable(fmt.Sprintf("eval: %s#%d  %s  agy exit %d  %s  %s", r.Row, r.Trial, v, r.AgyExit, secs, r.Dir))
}

// runRow is steps 1-15 of SPEC-fyr-eval § One row for one trial. A failure
// inside steps 1-8 skips 9-13 and still runs 14 once the sandbox is eval's
// own (the comments follow the spec's numbers, IN52 of the 2026-10-27 audit).
// Steps 1, 2, 4, 6-12 and 14 are internal/sandbox's.
func runRow(ctx context.Context, o Options, r Row, agy, tok, dir string) Result {
	t0 := time.Now()
	w := newRow(o, r, dir, o.Now().Format("2006-01-02-1504"))
	res := Result{Row: r.ID, AgyExit: -1, Started: t0, SessionSeconds: -1, Dir: dir}
	finish := func() Result {
		res.Seconds = int(time.Since(t0).Seconds())
		res.Reasons = w.b.Reasons()
		res.Status, res.Cause = classify(res.Reasons, w.b.TimedOut())
		return res
	}
	// Step 1: a directory of this invocation, never one that already exists.
	if !w.b.Make() {
		return finish()
	}
	if w.prepare(ctx) {
		s0 := time.Now()
		res.AgyExit = w.session(ctx, agy, tok)
		if w.b.Started() {
			res.SessionSeconds = int(time.Since(s0).Seconds())
		}
		w.afterSession(ctx)
	}
	w.b.ExitChecks()
	if ctx.Err() != nil && !w.b.Has(ReasonInterrupted) {
		w.b.Fail(ReasonInterrupted)
	}
	return finish()
}
