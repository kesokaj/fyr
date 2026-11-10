package work

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// wideGrant is the worker's allow list: everything, in its clone (D20).
var wideGrant = []string{"command(*)", "read_file(*)", "write_file(*)"}

// safetyReasons fail a worker whose sandbox cannot be trusted: nothing of it
// is fetched, copied or printed, and the sandbox is kept (D38). A sandbox
// the machine could not write is among them, as it was while that failure
// read sandbox replaced (PLAN.md item 48).
var safetyReasons = append(slices.Clone(sandbox.IntegrityReasons), sandbox.ReasonCredentials, sandbox.ReasonRealConfig, ReasonLeadChanged)

// worker is one Kickoff's sandbox and what work adds to it.
type worker struct {
	o     Options
	p     prepared
	b     *sandbox.Box
	stamp string
	notes bytes.Buffer // the box's notes, printed with the result contract
	ran   bool         // agy was started
	start time.Time    // run's start on the wall clock: the window's from (D140)
	// collected: the branch was fetched and neither the drafts copy nor the
	// records copy failed (ADR-501)
	collected    bool
	baseRecords  map[string]Record
	baseCheckOut string   // post-build Base-Check: output passed to the isolated Judge
	dest         *os.Root // the lead's .agents/scratch/work/<stamp>/<name>/, once made
	res          Result
	tracker      *progressTracker
}

// dockerConfig is the box's container, nil on the host runner.
func (p prepared) dockerConfig(stamp string) *sandbox.Docker {
	if p.docker == nil {
		return nil
	}
	d := &sandbox.Docker{Image: p.docker.image, Agy: p.docker.agy, Stamp: stamp, Name: p.k.Name, Network: sandbox.RunNetwork(stamp)}
	if p.docker.ramGB > 0 {
		d.Memory = fmt.Sprintf("%dg", p.docker.ramGB)
	}
	if p.docker.cpus > 0 {
		d.CPUs = fmt.Sprintf("%d", p.docker.cpus)
	}
	if p.docker.diskGB > 0 {
		d.TmpfsSize = fmt.Sprintf("%dg", max(1, min(2, p.docker.diskGB/2)))
	}
	return d
}

func newWorker(o Options, p prepared, stamp, dir string, tracker *progressTracker) *worker {
	w := &worker{o: o, p: p, stamp: stamp, tracker: tracker}
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
		Docker: p.dockerConfig(stamp), ACP: o.ACP,
		OnEvent: func(ev sandbox.ACPEvent) { tracker.onEvent(p.k.Name, ev) },
	})
	w.res = Result{Name: p.k.Name, AgyExit: -1, Model: model, Base: p.base, Dir: dir, Runner: o.Runner, RunnerReason: o.RunnerReason}
	return w
}

// run is steps 1-9 of § Interfaces § One worker. A failure inside the
// setup skips the session and still runs the exit checks once the sandbox
// is the worker's own. collect is held over steps 6-8, so one worker at a
// time fetches into the lead and checks it.
func (w *worker) run(ctx context.Context, collect *sync.Mutex) Result {
	w.start = time.Now()
	w.tracker.onStart(w.p.k.Name)
	if w.b.Make() {
		if w.prepare(ctx) {
			w.res.AgyExit = w.b.Session(ctx, w.p.agy, w.p.tok, w.p.k.Text, "out.txt")
			w.ran = w.b.Started()
			w.b.After()
		}
		w.b.ExitChecks()
		w.checkDisk()
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
	res := w.result()
	w.tracker.onFinish(w.p.k.Name, res)
	return res
}

// unstarted is the result of a worker that never got a slot: no sandbox,
// failed reason.
func (w *worker) unstarted(reason string) Result {
	w.b.Fail(reason)
	w.res.Dir = ""
	res := w.result()
	w.tracker.onFinish(w.p.k.Name, res)
	return res
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
	w.seedRecords(ctx)
	if !w.runPreBaseCheck(ctx) {
		return false
	}
	w.b.Baseline()
	return ctx.Err() == nil
}

// diskUnitBytes is the byte multiplier for DiskGB (1 GiB in production;
// overridden in tests so ReasonDiskLimit is tested without writing gigabytes).
var diskUnitBytes int64 = 1 << 30

func (w *worker) checkDisk() {
	if w.o.DiskGB <= 0 || w.unsafe() || !w.b.Intact() {
		return
	}
	limit := int64(w.o.DiskGB) * diskUnitBytes
	var used int64
	var walkErr error
	_ = filepath.WalkDir(w.res.Dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			walkErr = err
			return err
		}
		if d.Type().IsRegular() {
			info, ierr := d.Info()
			if ierr != nil {
				walkErr = ierr
				return ierr
			}
			used += info.Size()
		}
		return nil
	})
	if walkErr != nil {
		fmt.Fprintf(&w.notes, "work: %s: sandbox disk check failed: %v\n", w.p.k.Name, walkErr)
		w.b.Fail(ReasonDiskLimit)
		return
	}
	if used > limit {
		fmt.Fprintf(&w.notes, "work: %s: sandbox used %d bytes, over --disk %dg\n", w.p.k.Name, used, w.o.DiskGB)
		w.b.Fail(ReasonDiskLimit)
	}
}

func (w *worker) seedRecords(ctx context.Context) {
	fyrBin := filepath.Join(w.b.Home(), ".local", "bin", "fyr")
	trailFile := filepath.Join(w.b.App(), trailRel)
	if _, err := os.Stat(trailFile); err == nil {
		cmd := exec.CommandContext(ctx, fyrBin, "record", "sync", "--home", w.b.Home())
		cmd.Dir = w.b.App()
		cmd.Env = sandbox.FyrEnv()
		_ = cmd.Run()
		return
	}
	for _, h := range []string{w.o.Home, w.o.RealHome} {
		if h == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(h, harness.StateDir, "brain.db")); err != nil {
			continue
		}
		exp := exec.CommandContext(ctx, fyrBin, "record", "export", "--repo", w.o.Root, "--home", h)
		exp.Dir = w.b.App()
		exp.Env = sandbox.FyrEnv()
		if err := exp.Run(); err != nil {
			_ = os.Remove(trailFile)
			_ = os.Remove(filepath.Dir(trailFile))
			continue
		}
		blob, err := os.ReadFile(trailFile)
		if err != nil {
			_ = os.Remove(trailFile)
			_ = os.Remove(filepath.Dir(trailFile))
			continue
		}
		recs, err := parseTrail(blob)
		if err != nil || len(recs) == 0 {
			_ = os.Remove(trailFile)
			_ = os.Remove(filepath.Dir(trailFile))
			continue
		}
		syncCmd := exec.CommandContext(ctx, fyrBin, "record", "sync", "--home", w.b.Home())
		syncCmd.Dir = w.b.App()
		syncCmd.Env = sandbox.FyrEnv()
		_ = syncCmd.Run()
		_ = os.Remove(trailFile)
		_ = os.Remove(filepath.Dir(trailFile))
		w.baseRecords = recs
		return
	}
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
