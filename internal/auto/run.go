package auto

import (
	"errors"
	"fmt"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// Run is what the timer runs: G1, then G2 past a dry run, then the sweep. A
// dry run resolves the same config, state, gates and projects and refuses what
// the real run refuses; it takes no lock, reads no `/usage`, runs no check and
// writes nothing (review-standards § 8).
func Run(o Options, dryRun bool) error {
	now := o.now()
	say := func(line string) {
		fmt.Fprintln(o.Out, line)
		if !dryRun {
			if err := appendLog(o.Home, now, line); err != nil {
				fmt.Fprintln(o.Out, "auto: log: "+oneLine(err.Error()))
			}
		}
	}
	c, found, err := Load(o.Home)
	switch {
	case err != nil:
		say(configLine(err))
		return &ExitError{Code: 1}
	case !found || !c.Enabled:
		say("G1 switch: off")
		return nil
	}
	if dryRun {
		say("G1 switch: on, stale after " + c.ConversationIdle)
		st, err := usableState(o.Home, now)
		if err != nil {
			say(stateLine(o.Home, err))
			return &ExitError{Code: 1}
		}
		say("G2 lock: not taken in a dry run")
		say("metrics: not ingested in a dry run")
		say("dream: not run in a dry run")
		r := &run{o: o, env: o.env(), cfg: c, dry: true, say: say, st: st}
		r.sweep()
		say("budget: not read in a dry run")
		return r.exit()
	}
	return o.locked(c, now, say)
}

// locked is a real run past G1.
func (o Options) locked(c Config, now time.Time, say func(string)) error {
	if err := ensureDir(o.Home); err != nil {
		fmt.Fprintln(o.Out, "auto: "+oneLine(err.Error()))
		return &ExitError{Code: 1}
	}
	unlock, held, err := lock(o.Home)
	if err != nil {
		say("G2 lock: " + err.Error())
		return &ExitError{Code: 1}
	}
	if held {
		say("G2 lock: held by another run")
		return nil
	}
	defer unlock()
	// A corrupt state stops the run and stays: the day's model count in it
	// is G9's, and starting over would hand the budget back.
	st, err := usableState(o.Home, now)
	if err != nil {
		say(stateLine(o.Home, err))
		return &ExitError{Code: 1}
	}
	// Only a past date is a new day: a state up to a day ahead keeps its count.
	if today := now.Format("2006-01-02"); st.Date < today {
		st.Date, st.ModelRuns = today, 0
	}
	c, runSweep, preFailed := o.runPreSweep(c, say)
	if !runSweep {
		o.prune(now)
		if preFailed {
			return &ExitError{Code: 1}
		}
		return nil
	}
	return o.finishSweep(c, st, now, say, preFailed)
}

func (o Options) finishSweep(c Config, st State, now time.Time, say func(string), preFailed bool) error {
	r := &run{o: o, env: o.env(), cfg: c, say: say, st: st}
	r.sweep()
	r.st.LastRun, r.st.Verdict = now, r.verdict
	if err := saveState(o.Home, r.st); err != nil {
		say("auto: state: " + err.Error())
		return &ExitError{Code: 1}
	}
	o.prune(now)
	if err := r.exit(); err != nil {
		return err
	}
	if preFailed {
		return &ExitError{Code: 1}
	}
	return nil
}

func (o Options) prune(now time.Time) {
	if err := pruneLog(o.Home, now); err != nil {
		fmt.Fprintln(o.Out, "auto: log: "+oneLine(err.Error()))
	}
}

func (o Options) runPreSweep(c Config, say func(string)) (Config, bool, bool) {
	failed := false
	if o.Ingest != nil {
		line, err := o.Ingest()
		if line != "" {
			say(line)
		}
		if err != nil {
			say("metrics ingest failed: " + oneLine(err.Error()))
		}
	}
	if o.Dream != nil {
		if err := o.Dream(); err != nil {
			say("dream: " + oneLine(err.Error()))
			failed = true
		}
	}
	if o.Ingest != nil || o.Dream != nil {
		c2, found, err := Load(o.Home)
		if err != nil {
			say(configLine(err))
			return c, false, true
		}
		if !found || !c2.Enabled {
			say("G1 switch: off, stopped before the sweep")
			return c, false, failed
		}
		c = c2
	}
	return c, true, failed
}

// run is one pass over the machine and its projects.
type run struct {
	o       Options
	env     Env
	cfg     Config
	dry     bool
	say     func(string)
	st      State
	verdict string
	failed  bool
	halted  bool // again stopped the run inside a project

	usageRead       bool
	usageText       string
	usageErr        error
	workerRan       bool
	pendingReportID string
	appliedSHA      string
	installPending  bool
}

func (r *run) exit() error {
	if r.failed {
		return &ExitError{Code: 1}
	}
	return nil
}

// stopMachine ends the sweep with one line naming the gate.
func (r *run) stopMachine(line string) {
	r.say(line)
	r.verdict = line
	r.halted = true
}

// again reads G1 and G3 before the next project, so an `off` during a run
// stops it there (D7) and an updated conversation_idle takes effect.
func (r *run) again(root string) bool {
	c, found, err := Load(r.o.Home)
	switch {
	case err != nil:
		r.stopMachine(configLine(err))
		r.failed = true
		return false
	case !found || !c.Enabled:
		r.stopMachine("G1 switch: off, stopped before " + oneLine(root))
		return false
	}
	r.cfg = c
	if stop := r.machineStop(); stop != "" {
		r.stopMachine(stop)
		return false
	}
	return true
}

// sweep checks G3, discovers all targets, evaluates each target, and runs the
// machine's health once when a target reached it.
func (r *run) sweep() {
	if stop := r.machineStop(); stop != "" {
		r.stopMachine(stop)
		return
	}
	env, home := r.env, r.o.Home
	targets, err := limited(r.o.limit(projectLimit), func() ([]dream.Target, error) { return env.Targets(home) })
	if err != nil {
		r.stopMachine("targets: " + oneLine(err.Error()))
		r.failed = true
		return
	}
	reached := 0
	seen := map[string]bool{}
	for i, t := range targets {
		if i > 0 && !r.again(t.Root) {
			return
		}
		seen[t.Root] = true
		if r.project(t.Root, t.Skip) {
			reached++
		}
		if r.halted {
			return
		}
	}
	if len(targets) == 0 {
		r.say("no workspaces discovered")
	}
	for root := range r.st.Projects {
		if !seen[root] {
			delete(r.st.Projects, root)
		}
	}
	if reached > 0 && !r.dry {
		if !r.again("the machine's checks") {
			return
		}
		r.machineHealth()
		if r.halted {
			return
		}
	}
	r.verdict = fmt.Sprintf("%d of %d projects checked", reached, len(targets))
	r.say(r.verdict)
}

// project is one target's checks and health; it says whether health ran.
func (r *run) project(root, skip string) bool {
	p := r.st.Projects[root]
	read := projectRead{stop: "skipped: " + skip}
	if skip == "" {
		read = r.readProject(root)
	}
	verdict := short(read.stop)
	reached := read.stop == ""
	if reached {
		verdict = short(r.health(root, &p, read))
	}
	r.say(short(root) + ": " + verdict)
	p.LastRun, p.Verdict = r.o.now(), verdict
	if r.st.Projects == nil {
		r.st.Projects = map[string]Project{}
	}
	r.st.Projects[root] = p
	return reached
}

// readProject checks git readability and collects free findings only on
// readable git checkouts (non-git workspaces and GlobalWorkspace pass safely
// without G7/G8 blockers).
func (r *run) readProject(root string) projectRead {
	if root == search.GlobalWorkspace {
		return projectRead{}
	}
	o, dry := r.o, r.dry
	got, err := limited(o.limit(projectLimit), func() ([]projectRead, error) {
		if _, _, rerr := trail.Roots(root); errors.Is(rerr, trail.ErrNotCheckout) {
			return []projectRead{{}}, nil
		} else if rerr != nil {
			return []projectRead{{stop: "skipped: " + oneLine(rerr.Error())}}, nil
		}
		if !readable(root) {
			return []projectRead{{stop: "skipped: a partial clone this git cannot read safely"}}, nil
		}
		var read projectRead
		if !dry {
			read.findings, read.failed = o.projectFindings(root)
		}
		return []projectRead{read}, nil
	})
	if err != nil {
		r.failed = true
		return projectRead{stop: "read project: " + err.Error()}
	}
	return got[0]
}

// projectRead is readProject's answer: stop is the gate that held, if one did.
type projectRead struct {
	stop     string
	findings []Finding
	failed   bool
}

var readable = trail.Readable

const (
	projectLimit = 10 * time.Minute
	installLimit = 2 * time.Minute
	metricsLimit = 30 * time.Minute
)

// limited runs read and gives up on it after d. Its goroutine may outlive the
// run, so read must touch nothing the run still uses.
func limited[T any](d time.Duration, read func() ([]T, error)) ([]T, error) {
	type answer struct {
		got []T
		err error
	}
	done := make(chan answer, 1)
	go func() {
		got, err := read()
		done <- answer{got, err}
	}()
	select {
	case a := <-done:
		return a.got, a.err
	case <-time.After(d):
		return nil, fmt.Errorf("a read took over %s", d)
	}
}

// usage is one `/usage` reading per run, the health check's and G9's.
func (r *run) usage() (string, error) {
	if !r.usageRead {
		r.usageRead = true
		r.usageText, r.usageErr = r.env.Usage(r.o.Home, dir(r.o.Home))
	}
	return r.usageText, r.usageErr
}

// budget is G9: `/usage` is read only for a project past G8, so the free gates
// spend no call.
func (r *run) budget() (string, bool) {
	text, err := r.usage()
	return gateBudget(r.st.modelRunsToday(r.o.now()), text, err)
}
