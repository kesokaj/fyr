package auto

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	return o.locked(ctx, c, now, say)
}

// locked is a real run past G1.
func (o Options) locked(ctx context.Context, c Config, now time.Time, say func(string)) error {
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
	return o.finishSweep(ctx, c, st, now, say, preFailed)
}

func (o Options) finishSweep(ctx context.Context, c Config, st State, now time.Time, say func(string), preFailed bool) error {
	r := &run{ctx: ctx, o: o, env: o.env(), cfg: c, say: say, st: st}
	r.sweep()
	r.st.LastRun, r.st.Verdict = now, r.verdict
	r.st.OpenAsks = reconcileOpenAsks(r.o.Home, r.o.Now, r.st.OpenAsks)
	if err := saveState(o.Home, r.st); err != nil {
		say("auto: state: " + err.Error())
		r.recordSweepRun(now, true, say)
		return &ExitError{Code: 1}
	}
	o.prune(now)
	r.recordSweepRun(now, preFailed, say)
	o.runDailyReport(say)
	if err := r.exit(); err != nil {
		return err
	}
	if preFailed {
		return &ExitError{Code: 1}
	}
	return nil
}

func (o Options) runDailyReport(say func(string)) {
	if o.DryRun || o.Report == nil {
		return
	}
	if err := o.Report(); err != nil {
		say("report: " + oneLine(err.Error()))
	}
}

func (r *run) recordSweepRun(start time.Time, extraFail bool, say func(string)) {
	if r.dry {
		return
	}
	outcome := "pass"
	if extraFail || r.failed {
		outcome = "fail"
	}
	workerRan, applied := 0, 0
	if r.workerRan {
		workerRan = 1
	}
	if r.appliedSHA != "" {
		applied = 1
	}
	err := r.o.recordBackgroundRun(metrics.BackgroundRunRow{
		TS:              start.UnixMilli(),
		Kind:            "auto",
		Outcome:         outcome,
		DurationMS:      max(int64(0), r.o.now().Sub(start).Milliseconds()),
		ProjectsChecked: r.projectsChecked,
		WorkerRan:       workerRan,
		Applied:         applied,
	})
	if err != nil {
		say("auto: background_runs: " + oneLine(err.Error()))
	}
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
	ctx     context.Context
	o       Options
	env     Env
	cfg     Config
	dry     bool
	say     func(string)
	st      State
	verdict string
	failed  bool
	halted  bool // again stopped the run inside a project

	usageRead        bool
	usageText        string
	usageErr         error
	projectsChecked  int
	workerRan        bool
	projectWorkerRan bool
	pendingReportID  string
	appliedSHA       string
	installPending   bool
	sweepCache       *projectPassCache
}

func (r *run) context() context.Context {
	if r.ctx != nil {
		return r.ctx
	}
	return context.Background()
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
