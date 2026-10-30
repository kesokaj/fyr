package work

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// wideGrant is the worker's allow list: everything, in its clone (D20).
var wideGrant = []string{"command(*)", "read_file(*)", "write_file(*)"}

// safetyReasons fail a worker whose sandbox cannot be trusted: nothing of it
// is fetched, copied or printed, and the sandbox is kept (D38).
var safetyReasons = []string{sandbox.ReasonCredentials, sandbox.ReasonRealConfig, sandbox.ReasonProcs, sandbox.ReasonReplaced, ReasonLeadChanged}

// worker is one Kickoff's sandbox and what work adds to it.
type worker struct {
	o     Options
	p     prepared
	b     *sandbox.Box
	stamp string
	notes bytes.Buffer // the box's notes, printed with the result contract
	ran   bool         // agy was started
	start time.Time    // run's start on the wall clock: the window's from (D140)
	// collected: the branch was fetched and the drafts copy did not fail
	collected bool
	res       Result
}

// dockerConfig is the box's container, nil on the host runner.
func (p prepared) dockerConfig(stamp string) *sandbox.Docker {
	if p.docker == nil {
		return nil
	}
	return &sandbox.Docker{Image: p.docker.image, Agy: p.docker.agy, Stamp: stamp, Name: p.k.Name}
}

func newWorker(o Options, p prepared, stamp, dir string) *worker {
	w := &worker{o: o, p: p, stamp: stamp}
	// The Kickoff's Model: line is this worker's alone (D103 item 1).
	model := p.k.Model
	if model == "" {
		model = o.Model
	}
	pt := (o.Timeout - 5*time.Minute) / time.Minute
	if pt < 1 {
		pt = 1
	}
	w.b = sandbox.New(sandbox.Config{
		Dir: dir, Source: o.Root, RealHome: o.RealHome, Repo: o.Repo, Self: o.Self,
		Grants: wideGrant, GitName: p.gitName, GitEmail: p.gitMail, Model: model, Zone: p.zone.name,
		Timeout: o.Timeout, SkipStores: true, PrintTimeout: fmt.Sprintf("%dm", pt),
		StopGrace: o.StopGrace, Prefix: "work: " + p.k.Name, Out: &w.notes,
		Docker: p.dockerConfig(stamp),
	})
	w.res = Result{Name: p.k.Name, AgyExit: -1, Model: model, Base: p.base, Dir: dir}
	return w
}

// run is steps 1-9 of § Interfaces § One worker. A failure inside the
// setup skips the session and still runs the exit checks once the sandbox
// is the worker's own. collect is held over steps 6-8, so one worker at a
// time fetches into the lead and checks it.
func (w *worker) run(ctx context.Context, collect *sync.Mutex) Result {
	w.start = time.Now()
	if w.b.Make() {
		if w.prepare(ctx) {
			w.res.AgyExit = w.b.Session(ctx, w.p.agy, w.p.tok, w.p.k.Text, "out.txt")
			w.ran = w.b.Started()
			w.b.After()
		}
		w.b.ExitChecks()
		if w.b.TimedOut() {
			w.b.Fail(ReasonTimedOut)
		}
		if ctx.Err() != nil {
			w.b.Fail(ReasonKilled)
		}
		collect.Lock()
		w.collect()
		collect.Unlock()
		w.remove()
	}
	w.res.Seconds = int(time.Since(w.start).Seconds())
	return w.result()
}

// unstarted is the result of a worker that never got a slot: no sandbox,
// failed reason.
func (w *worker) unstarted(reason string) Result {
	w.b.Fail(reason)
	w.res.Dir = ""
	return w.result()
}

// result is the Result from the box's reasons and notes.
func (w *worker) result() Result {
	w.res.Reasons = w.b.Reasons()
	w.res.Verdict = "PASS"
	if len(w.res.Reasons) > 0 {
		w.res.Verdict = "FAIL"
	}
	w.res.Notes = strings.Split(strings.TrimRight(w.notes.String(), "\n"), "\n")
	if w.notes.Len() == 0 {
		w.res.Notes = nil
	}
	return w.res
}

// prepare is steps 1-3 after Make; false means no session runs.
func (w *worker) prepare(ctx context.Context) bool {
	if !w.b.Clone(ctx) || !w.branch() {
		return false
	}
	w.b.Fingerprint()
	if !w.b.Install(ctx) || !w.b.Settings() {
		return false
	}
	w.b.Baseline()
	return ctx.Err() == nil
}

// branch is step 2: the clone loses its origin, which is the lead's
// checkout, so a worker's push has nowhere to go (D14), and starts the
// worker's branch.
func (w *worker) branch() bool {
	for _, args := range [][]string{{"remote", "remove", "origin"}, {"checkout", "-q", "-b", w.p.k.Branch()}} {
		if out, err := trail.Command(w.b.App(), args...).CombinedOutput(); err != nil {
			w.b.Note("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			w.b.Fail(ReasonBranchFailed)
			return false
		}
	}
	return true
}

// unsafe reports whether a safety reason failed the worker.
func (w *worker) unsafe() bool {
	for _, r := range safetyReasons {
		if w.b.Has(r) {
			return true
		}
	}
	return false
}

// remove is step 9: a collected worker's sandbox is removed unless --keep;
// an unfetched one, or one whose drafts were not copied, is kept, since it
// holds what is left of the work.
func (w *worker) remove() {
	if w.o.Keep || !w.collected || w.unsafe() {
		return
	}
	if w.b.Remove() {
		w.res.Dir = ""
	}
}
