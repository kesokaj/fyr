package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

const (
	cliTimeout = 10 * time.Minute
	killGrace  = 30 * time.Second
	keepFailed = 30 * 24 * time.Hour
)

// realRun takes the lock, then reads the state and the projects, dreams over
// every changed project and writes what the CLI proposes. Every failure before
// the first project is a run log line, last_run.error where the state can be
// written, and exit 2 (the plan's Interfaces, code review 2026-09-30).
func (o Options) realRun() error {
	conf, err := harness.ConfigDir(o.Home)
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	unlock, held, err := lock(filepath.Join(conf, "dream.lock"))
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	if held {
		fmt.Fprintln(o.Out, "fyr dream: already running")
		return nil
	}
	defer unlock()

	r := &dreamRun{o: o, conf: conf, t0: o.Now(), logf: logger(filepath.Join(conf, "dream.log"), o.Now)}
	if r.st, err = readState(o.Home); err != nil {
		// The state is not rewritten: that would erase the watermarks.
		r.logf("- run 0 projects, 0 proposals, error " + err.Error())
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	removeCopies(conf, o.Now())
	prune(filepath.Join(conf, "dream-failed"), r.t0)
	o.ingestFirst(r.logf)
	projects, skips, err := o.targets()
	if err != nil {
		return r.fail(err.Error())
	}
	for _, s := range skips {
		r.logf(s.root + " skip " + s.why)
	}
	r.argv = command()
	if err := checkAgy(runtime.GOOS); err != nil {
		return r.fail(err.Error())
	}
	agyHomeDir, cleanup, err := agyHome(conf, runtime.GOOS)
	if err != nil {
		return r.fail(err.Error())
	}
	defer cleanup()
	r.env = childEnv(os.Environ(), agyHomeDir)
	r.work = filepath.Join(conf, "dream-work")
	if err := os.MkdirAll(r.work, 0o700); err != nil {
		return r.fail(err.Error())
	}
	for _, p := range projects {
		r.project(p)
	}
	return r.result(len(projects))
}

// dreamRun is one run's state across its projects.
type dreamRun struct {
	o         Options
	conf      string
	t0        time.Time
	logf      func(string)
	st        state
	argv, env []string
	work      string
	failed    int   // projects that failed
	total     int   // proposals written
	stateErr  error // the first state write that failed
}

// finish records the run in the state and the log.
func (r *dreamRun) finish(n, proposals int, runErr string) error {
	r.st.LastRun = lastRun{At: r.t0, Projects: n, Proposals: proposals, Error: runErr}
	line := fmt.Sprintf("- run %d projects, %d proposals", n, proposals)
	if runErr != "" {
		line += ", error " + runErr
	}
	r.logf(line)
	return saveState(r.o.Home, r.st)
}

// fail ends a run that failed before its first project: exit 2.
func (r *dreamRun) fail(msg string) error {
	r.finish(0, 0, msg)
	return &ExitError{2, "fyr dream: " + msg}
}

// project dreams over one project and writes what the CLI proposes.
func (r *dreamRun) project(p search.Project) {
	if why := skipReason(p); why != "" {
		r.logf(p.Root + " skip " + why)
		return
	}
	if why := r.o.inUse(p); why != "" {
		r.logf(p.Root + " skip in use (" + why + ")")
		return
	}
	c, err := collect(r.o.Home, p, r.o.since(r.st, p.Root), r.o.Now(), maxPrompt)
	if err != nil {
		r.failed++
		r.logf(p.Root + " fail " + scrub(err.Error(), r.o.Home))
		return
	}
	if !c.Changed {
		r.logf(p.Root + " unchanged")
		return
	}
	resp, ok := r.ask(p, c)
	if !ok {
		return
	}
	if err := r.o.write(r.conf, c, resp, r.argv[0], r.t0); err != nil {
		r.failed++
		r.logf(p.Root + " fail " + err.Error())
		return
	}
	// Short of the run's start when the cap dropped news: the next run
	// offers it again (ADR-069).
	r.st.Projects[p.Root] = watermark(c, r.t0)
	if err := saveState(r.o.Home, r.st); err != nil && r.stateErr == nil {
		r.stateErr = err
	}
	r.total += len(resp.Proposals)
	r.logf(fmt.Sprintf("%s ok %d proposals", p.Root, len(resp.Proposals)))
}

// ask calls the CLI with the project's prompt and returns its answer; a
// failed call or an answer that does not parse fails the project.
func (r *dreamRun) ask(p search.Project, c Collected) (response, bool) {
	stdout, stderr, err := call(r.argv, c.Prompt, r.work, r.env)
	if err != nil {
		r.failed++
		r.o.keepFailure(r.conf, c, r.t0, stdout, stderr)
		reason := err.Error()
		if hint := agyAuthHint(stdout, stderr); hint != "" {
			reason += ": " + hint
		}
		r.logf(p.Root + " fail " + reason)
		return response{}, false
	}
	text, aerr := answer(stdout)
	resp, ok := parse(text)
	if aerr != nil || !ok {
		r.failed++
		r.o.keepFailure(r.conf, c, r.t0, stdout, stderr)
		reason := "invalid output"
		if aerr != nil {
			reason = aerr.Error()
		}
		if hint := agyAuthHint(stdout, stderr); hint != "" {
			reason += ": " + hint
		}
		r.logf(p.Root + " fail " + reason)
		return response{}, false
	}
	return resp, true
}

// result records the run and returns its exit: 2 when the watermarks could
// not be saved, 1 when a project failed.
func (r *dreamRun) result(n int) error {
	runErr := ""
	if r.failed > 0 {
		runErr = fmt.Sprintf("%d project(s) failed", r.failed) // so `status` never reads a run with failures as clean
	}
	if err := r.finish(n, r.total, runErr); err != nil && r.stateErr == nil {
		r.stateErr = err
	}
	if r.stateErr != nil {
		return &ExitError{2, "fyr dream: the watermarks could not be saved, so the next run re-sends this one: " + r.stateErr.Error()}
	}
	if r.failed > 0 {
		return &ExitError{1, fmt.Sprintf("fyr dream: %d project(s) failed; see %s", r.failed, filepath.Join(r.conf, "dream.log"))}
	}
	return nil
}
