package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
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

	t0 := o.Now()
	logf := logger(filepath.Join(conf, "dream.log"), o.Now)
	st, err := readState(o.Home)
	if err != nil {
		// The state is not rewritten: that would erase the watermarks.
		logf("- run 0 projects, 0 proposals, error " + err.Error())
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	finish := func(n, proposals int, runErr string) error {
		st.LastRun = lastRun{At: t0, Projects: n, Proposals: proposals, Error: runErr}
		line := fmt.Sprintf("- run %d projects, %d proposals", n, proposals)
		if runErr != "" {
			line += ", error " + runErr
		}
		logf(line)
		return saveState(o.Home, st)
	}
	fail := func(msg string) error {
		finish(0, 0, msg)
		return &ExitError{2, "fyr dream: " + msg}
	}

	removeCopies(conf, o.Now())
	prune(filepath.Join(conf, "dream-failed"), t0)
	o.ingestFirst(logf)
	projects, err := o.targets()
	if err != nil {
		return fail(err.Error())
	}
	argv := command()
	if err := checkAgy(runtime.GOOS); err != nil {
		return fail(err.Error())
	}
	agyHomeDir, cleanup, err := agyHome(conf, runtime.GOOS)
	if err != nil {
		return fail(err.Error())
	}
	defer cleanup()
	env := withHome(os.Environ(), agyHomeDir)
	work := filepath.Join(conf, "dream-work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return fail(err.Error())
	}

	failed, total := 0, 0
	var stateErr error
	for _, p := range projects {
		if why := skipReason(p); why != "" {
			logf(p.Root + " skip " + why)
			continue
		}
		if why := o.inUse(p); why != "" {
			logf(p.Root + " skip in use (" + why + ")")
			continue
		}
		c, err := collect(o.Home, p, o.since(st, p.Root), o.Now(), maxPrompt)
		if err != nil {
			failed++
			logf(p.Root + " fail " + scrub(err.Error(), o.Home))
			continue
		}
		if !c.Changed {
			logf(p.Root + " unchanged")
			continue
		}
		stdout, stderr, err := call(argv, c.Prompt, work, env)
		if err != nil {
			failed++
			o.keepFailure(conf, c, t0, stdout, stderr)
			reason := err.Error()
			if hint := agyAuthHint(stdout, stderr); hint != "" {
				reason += ": " + hint
			}
			logf(p.Root + " fail " + reason)
			continue
		}
		text, aerr := answer(stdout)
		resp, ok := parse(text)
		if aerr != nil || !ok {
			failed++
			o.keepFailure(conf, c, t0, stdout, stderr)
			reason := "invalid output"
			if aerr != nil {
				reason = aerr.Error()
			}
			if hint := agyAuthHint(stdout, stderr); hint != "" {
				reason += ": " + hint
			}
			logf(p.Root + " fail " + reason)
			continue
		}
		if err := o.write(conf, c, resp, argv[0], t0); err != nil {
			failed++
			logf(p.Root + " fail " + err.Error())
			continue
		}
		st.Projects[p.Root] = t0
		if err := saveState(o.Home, st); err != nil && stateErr == nil {
			stateErr = err
		}
		total += len(resp.Proposals)
		logf(fmt.Sprintf("%s ok %d proposals", p.Root, len(resp.Proposals)))
	}
	runErr := ""
	if failed > 0 {
		runErr = fmt.Sprintf("%d project(s) failed", failed) // so `status` never reads a run with failures as clean
	}
	if err := finish(len(projects), total, runErr); err != nil && stateErr == nil {
		stateErr = err
	}
	if stateErr != nil {
		return &ExitError{2, "fyr dream: the watermarks could not be saved, so the next run re-sends this one: " + stateErr.Error()}
	}
	if failed > 0 {
		return &ExitError{1, fmt.Sprintf("fyr dream: %d project(s) failed; see %s", failed, filepath.Join(conf, "dream.log"))}
	}
	return nil
}
