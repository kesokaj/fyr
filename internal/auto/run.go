package auto

import (
	"fmt"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
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
		say("G1 switch: on, level " + c.Level)
		st, err := usableState(o.Home, now)
		if err != nil {
			say(stateLine(o.Home, err))
			return &ExitError{Code: 1}
		}
		say("G2 lock: not taken in a dry run")
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
	r := &run{o: o, env: o.env(), cfg: c, say: say, st: st}
	r.sweep()
	r.st.LastRun, r.st.Verdict = now, r.verdict
	if err := saveState(o.Home, r.st); err != nil {
		say("auto: state: " + err.Error())
		return &ExitError{Code: 1}
	}
	if err := pruneLog(o.Home, now); err != nil {
		fmt.Fprintln(o.Out, "auto: log: "+oneLine(err.Error()))
	}
	return r.exit()
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

	unknownSaid bool
	usageRead   bool
	usageText   string
	usageErr    error
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

// again reads G1 and G3–G5 before the next project, so an `off` during a run
// stops it there (D7) and a level or threshold changed by `on` takes effect.
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

// sweep is G3–G5, then each project's G6–G8 and health, then the machine's
// health once, when a project reached it. Each project's result goes in the
// state as it ends, so a stop keeps what was reported.
func (r *run) sweep() {
	if stop := r.machineStop(); stop != "" {
		r.stopMachine(stop)
		return
	}
	// Listing a project runs git in it, as a read does: under a limit too.
	env, home := r.env, r.o.Home
	targets, err := limited(r.o.limit(projectLimit), func() ([]dream.Target, error) { return env.Targets(home) })
	if err != nil {
		r.stopMachine("G6 registered: " + oneLine(err.Error()))
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
		r.say("G6 registered: no project")
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
	r.verdict = fmt.Sprintf("%d of %d projects checked, level %s", reached, len(targets), r.cfg.Level)
	r.say(r.verdict)
}

// project is one project's gates and health; it says whether health ran.
func (r *run) project(root, skip string) bool {
	p := r.st.Projects[root]
	read := projectRead{stop: "G6 registered: " + skip}
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

// readProject is what a run reads of a project: G7, G8 and, past them on a
// real run, the free checks, under one time limit. A hostile config can hang
// git, and trail's calls take no deadline, so a read past the limit is left
// behind with its child, which the timer's manager ends with the run; it
// holds nothing of the run's.
func (r *run) readProject(root string) projectRead {
	idle, err := span(r.cfg.ProjectIdle)
	if err != nil {
		return projectRead{stop: "G7 idle: " + oneLine(err.Error())}
	}
	o, env, dry := r.o, r.env, r.dry
	got, err := limited(o.limit(projectLimit), func() ([]projectRead, error) {
		if !readable(root) {
			return []projectRead{{stop: "G6 registered: a partial clone this git cannot read safely"}}, nil
		}
		read := projectRead{stop: gateIdle(env, o.Home, root, idle, o.now())}
		if read.stop == "" {
			read.stop = gateClean(root)
		}
		if read.stop == "" && !dry {
			read.findings, read.failed = o.projectFindings(root)
		}
		return []projectRead{read}, nil
	})
	if err != nil {
		r.failed = true
		return projectRead{stop: "G7 idle: " + err.Error()}
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

// budget is G9. Nothing past it is built yet, so it is read and logged and no
// worker starts; `/usage` is read only for a project past G8, so the free
// gates spend no call.
func (r *run) budget() string {
	text, err := r.usage()
	line, _ := gateBudget(r.st.modelRunsToday(r.o.now()), text, err)
	return line
}
