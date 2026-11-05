package eval

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// Bounds of one CLI session, variables so a test can shorten them: agy's own
// --print-timeout is 20m, and 25m is the kit's `timeout --foreground` around it.
var (
	agyTimeout = 25 * time.Minute
	stopGrace  = 5 * time.Second
)

// namedGrants is the allow list of a named row (run.sh:212).
var namedGrants = func() []string {
	var out []string
	for _, c := range strings.Fields("git go gofmt cat ls date mkdir pwd grep find wc head tail printf echo cp ln test diff fyr chmod readlink bash python3 sleep govulncheck") {
		out = append(out, "command("+c+")")
	}
	return append(out, "read_file(*)", "write_file(*)")
}()

// row is one row's sandbox and what eval adds to it: the row's setup, check
// and agent, and the records baseline.
type row struct {
	o     Options
	r     Row
	b     *sandbox.Box
	stamp string // {{STAMP}}: the row's start, %F-%H%M
}

// newRow is row r's sandbox at dir, as Run builds one.
func newRow(o Options, r Row, dir, stamp string) *row {
	grants := namedGrants
	if _, wide := r.Wide(); wide {
		grants = []string{"command(*)", "read_file(*)", "write_file(*)"}
	}
	b := sandbox.New(sandbox.Config{
		Dir: dir, Source: o.Fixture, RealHome: o.RealHome, Repo: o.Repo, Self: o.Self,
		Grants: grants, GitName: "agy-rel", GitEmail: "agy-rel@localhost",
		Timeout: agyTimeout, SkipStores: true, BrainWatch: trialBrainWatch(o, dir),
		PrintTimeout: "20m", StopGrace: stopGrace,
		Prefix: "eval: " + r.ID, Out: o.Out,
	})
	return &row{o: o, r: r, b: b, stamp: stamp}
}

func trialBrainWatch(o Options, dir string) func(string) (string, bool) {
	scopes := []string{filepath.Join(dir, "app")}
	if o.Fixture != "" && !samePath(o.Fixture, o.Root) {
		scopes = append(scopes, o.Fixture)
	}
	scratch := filepath.Dir(dir)
	return func(dbPath string) (string, bool) {
		if o.BrainWatch == nil {
			return "", false
		}
		return o.BrainWatch(dbPath, scratch, scopes)
	}
}

func samePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	if ra, err := filepath.EvalSymlinks(ca); err == nil {
		ca = ra
	}
	if rb, err := filepath.EvalSymlinks(cb); err == nil {
		cb = rb
	}
	return ca == cb
}

// prepare is steps 2-8 of SPEC-fyr-eval § One row; false means the row has
// failed and no CLI runs.
func (w *row) prepare(ctx context.Context) bool {
	if !w.b.Clone(ctx) || !w.recordsBefore(ctx) {
		return false
	}
	w.b.Fingerprint()
	// Step 5: the row's setup, in the clone.
	if w.r.Setup != "" {
		trailPath := filepath.Join(w.b.App(), ".agents", "trail.jsonl")
		beforeTrail, _ := os.ReadFile(trailPath)
		if !w.b.Bash(ctx, w.r.Setup, w.setupEnv(), "setup.log") {
			w.b.Fail(ReasonSetup)
			return false
		}
		if afterTrail, err := os.ReadFile(trailPath); err == nil && !bytes.Equal(beforeTrail, afterTrail) {
			if !w.syncRecords(ctx) {
				return false
			}
		}
	}
	if !w.b.Install(ctx) {
		return false
	}
	if reason, wide := w.r.Wide(); wide {
		w.b.Note("runs with command(*) in its throwaway clone: %s", reason)
	}
	if !w.b.Settings() {
		return false
	}
	w.b.Baseline()
	return ctx.Err() == nil
}

// recordsBefore is step 3.
func (w *row) recordsBefore(ctx context.Context) bool {
	// Step 3: what the trail held before the agent ran; a check counts only
	// the records the run adds (run.sh:52-62).
	before := filepath.Join(w.b.Home(), "records-before.txt")
	var list []byte
	if _, err := os.Stat(filepath.Join(w.b.App(), ".agents", "trail.jsonl")); err == nil {
		if !w.syncRecords(ctx) {
			return false
		}
		cmd := exec.CommandContext(ctx, w.o.Self, "record", "list", "--home", w.b.Home())
		cmd.Dir = w.b.App()
		cmd.Env = sandbox.FyrEnv()
		var errb strings.Builder
		cmd.Stderr = &errb
		if list, err = cmd.Output(); err != nil {
			w.b.Note("record list: %v: %s", err, strings.TrimSpace(errb.String()))
			w.b.Fail(ReasonRecords)
			return false
		}
	}
	if err := os.WriteFile(before, list, 0o600); err != nil {
		w.b.Fail(ReasonRecords)
		return false
	}
	return true
}

func (w *row) syncRecords(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, w.o.Self, "record", "sync", "--home", w.b.Home())
	cmd.Dir = w.b.App()
	cmd.Env = sandbox.FyrEnv()
	var errb strings.Builder
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		w.b.Note("record sync: %v: %s", err, strings.TrimSpace(errb.String()))
		w.b.Fail(ReasonRecords)
		return false
	}
	return true
}

// session is steps 9-10: prompt, then prompt2 after a clean first exit. It
// returns the last agy exit status (-1: none, killed).
func (w *row) session(ctx context.Context, agy, tok string) int {
	vars := map[string]string{"CLONE": w.b.App(), "SB": w.b.Home(), "FIXDIR": w.o.Fixture, "STAMP": w.stamp}
	rc := w.b.Session(ctx, agy, tok, Expand(w.r.Prompt, vars), "out.txt")
	// The first session may have replaced the sandbox: the second opens its
	// output through the same paths, so it runs only on an intact one.
	if w.r.Prompt2 != "" && rc == 0 && ctx.Err() == nil && w.b.Intact() {
		rc = w.b.Session(ctx, agy, tok, Expand(w.r.Prompt2, vars), "out2.txt")
	}
	return rc
}

// afterSession is steps 11-13. A sandbox the session replaced is not
// followed: no removal through it, no check run in it.
func (w *row) afterSession(ctx context.Context) {
	if !w.b.After() || ctx.Err() != nil {
		return
	}
	// Step 13: the row's check.
	if !w.b.Bash(ctx, w.r.Check, w.checkEnv(), "check.log") {
		w.b.Fail(ReasonCheck)
	}
	// Step 13, check f: the agent the row names was invoked.
	if w.r.Agent != "" {
		ok, notes := invokedNotes(w.b.Home(), w.r.Agent)
		for _, n := range notes {
			w.b.Note("%s", n)
		}
		if !ok {
			w.b.Fail(ReasonAgent(w.r.Agent))
		}
	}
}

// checkEnv is run.sh's check environment (:237): env -i, eval's own PATH
// (the wrapper directory is never on it), HOME=SB, and the row's paths.
func (w *row) checkEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + w.b.Home(),
		"SB=" + w.b.Home(),
		"OUT=" + filepath.Join(w.b.Dir(), "out.txt"),
		"OUT2=" + filepath.Join(w.b.Dir(), "out2.txt"),
		"FIXDIR=" + w.o.Fixture,
		"CLONE=" + w.b.App(),
	}
}

// setupEnv is the check's, plus the git identity run.sh's gi() used, and FYR,
// this binary: setup runs before the install, so $SB/bin/fyr is not there yet.
func (w *row) setupEnv() []string {
	return append(w.checkEnv(), "FYR="+w.o.Self,
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@localhost",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@localhost")
}
