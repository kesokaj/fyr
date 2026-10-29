package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// Options is one invocation of eval.
type Options struct {
	Root     string // project checkout (suite, record scope, default fixture)
	Home     string // fyr's home: sandboxes, stores
	RealHome string // $HOME: the token, the fingerprint
	Fixture  string // the repository each row clones; "" is Root
	Self     string // os.Executable(): the fyr that installs and lists
	Repo     string // the install source tree (D16); "" is the harness embedded in Self
	Out      io.Writer
	Now      func() time.Time
}

// Result is one row's outcome.
type Result struct {
	Row, Verdict string // "PASS" | "FAIL"
	Reasons      []string
	AgyExit      int
	Seconds      int
	Dir          string // the sandbox
}

// The fixed reason strings of a FAIL (SPEC-fyr-eval § One row, step 15).
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
)

// ProcsChecked reports whether safety check e runs on this system (O1).
const ProcsChecked = sandbox.ProcsChecked

// ReasonAgent is the FAIL reason of an agent row whose agent the
// conversation store does not show invoked.
func ReasonAgent(agent string) string {
	return "no invoke_subagent of " + agent + " in the conversation store"
}

const tokenRel = sandbox.TokenRel

// SandboxRoot is where eval makes its sandboxes under fyr's home (D8).
func SandboxRoot(home string) string { return sandbox.Root(home, "eval") }

// Preflight is the refusals before step 1 that come before the suite is read
// (D20): root is a checkout, and the sandbox root passes guards a and b on
// its nearest existing ancestor. cmd calls it first; Run calls it again.
func Preflight(o Options) error {
	if err := sandbox.OutsideCheckout(o.Root); err == nil {
		return fmt.Errorf("%s is not a git checkout", o.Root)
	}
	return sandbox.CheckRoot(o.RealHome, o.Repo, SandboxRoot(o.Home))
}

// Run refuses (error, no row run) or runs every row and returns its results.
// On ctx's end the current row FAILs interrupted, the rows after it are not
// run, and the rows started are returned with a nil error (D22).
func Run(ctx context.Context, o Options, rows []Row) ([]Result, error) {
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
	tok := filepath.Join(o.RealHome, tokenRel)
	if st, err := os.Lstat(tok); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("no Antigravity login at %s", tok)
	}
	agy, err := exec.LookPath("agy")
	if err != nil {
		return nil, errors.New("agy not found on PATH")
	}
	stamp := o.Now().Format("2006-01-02-150405")
	var results []Result
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		res := runRow(ctx, o, r, agy, tok, filepath.Join(SandboxRoot(o.Home), stamp, r.ID))
		fmt.Fprintln(o.Out, resultLine(res))
		results = append(results, res)
	}
	return results, nil
}

func resultLine(r Result) string {
	v := r.Verdict
	if len(r.Reasons) > 0 {
		v += " (" + strings.Join(r.Reasons, "; ") + ")"
	}
	return sandbox.Printable(fmt.Sprintf("eval: %s  %s  agy exit %d  %ds  %s", r.Row, v, r.AgyExit, r.Seconds, r.Dir))
}

// runRow is steps 1-15 of SPEC-fyr-eval § One row for one row. A failure
// inside steps 1-8 skips 9-13 and still runs 14 once the sandbox is eval's
// own (the comments follow the spec's numbers, IN52 of the 2026-10-27 audit).
// Steps 1, 2, 4, 6-12 and 14 are internal/sandbox's.
func runRow(ctx context.Context, o Options, r Row, agy, tok, dir string) Result {
	t0 := time.Now()
	w := newRow(o, r, dir, o.Now().Format("2006-01-02-1504"))
	res := Result{Row: r.ID, AgyExit: -1, Dir: dir}
	finish := func() Result {
		res.Seconds = int(time.Since(t0).Seconds())
		res.Reasons = w.b.Reasons()
		res.Verdict = "PASS"
		if len(res.Reasons) > 0 {
			res.Verdict = "FAIL"
		}
		return res
	}
	// Step 1: a directory of this invocation, never one that already exists.
	if !w.b.Make() {
		return finish()
	}
	if w.prepare(ctx) {
		res.AgyExit = w.session(ctx, agy, tok)
		w.afterSession(ctx)
	}
	w.b.ExitChecks()
	if ctx.Err() != nil && !w.b.Has(ReasonInterrupted) {
		w.b.Fail(ReasonInterrupted)
	}
	return finish()
}
