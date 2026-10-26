package eval

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Bounds of one CLI session, variables so a test can shorten them: agy's own
// --print-timeout is 20m, and 25m is the kit's `timeout --foreground` around it.
var (
	agyTimeout = 25 * time.Minute
	stopGrace  = 5 * time.Second
)

// session is steps 9-10: the token linked, then agy, and prompt2 after a
// clean first exit. It returns the last agy exit status (-1: none, killed).
func (w *row) session(ctx context.Context, agy, tok string) int {
	if err := os.Symlink(tok, filepath.Join(w.sb, tokenRel)); err != nil {
		w.note("linking the token: %v", err)
		w.fail(ReasonToken)
		return -1
	}
	w.linked = true
	vars := map[string]string{"CLONE": w.app, "SB": w.sb, "FIXDIR": w.o.Fixture, "STAMP": w.stamp}
	errPath := filepath.Join(w.dir, "err.txt")
	rc := w.agy(ctx, agy, Expand(w.r.Prompt, vars), filepath.Join(w.dir, "out.txt"), errPath, false)
	// The first session may have replaced the sandbox: the second opens its
	// output through the same paths, so it runs only on an intact one.
	if w.r.Prompt2 != "" && rc == 0 && ctx.Err() == nil && w.intact() {
		rc = w.agy(ctx, agy, Expand(w.r.Prompt2, vars), filepath.Join(w.dir, "out2.txt"), errPath, true)
	}
	return rc
}

// agy runs one session in its own process group (D18): at the timeout only
// agy itself is killed, as `timeout --foreground` did, so what it started
// stays for check e; on ctx's end the whole group gets SIGTERM, then
// SIGKILL after stopGrace.
func (w *row) agy(ctx context.Context, agy, prompt, outPath, errPath string, appendErr bool) int {
	full := "The project root is the current directory, " + w.app + "; ignore any other checkout. " + prompt
	cmd := exec.Command(agy, "-p", full, "--mode", "accept-edits", "--print-timeout", "20m")
	cmd.Dir = w.app
	cmd.Env = w.agyEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A file eval writes that is not a regular file was put there by the run
	// (a session cannot start without it): the sandbox was replaced.
	out, err := openRegular(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		w.replaced("%v", err)
		return -1
	}
	defer out.Close()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendErr {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	errf, err := openRegular(errPath, flags)
	if err != nil {
		w.replaced("%v", err)
		return -1
	}
	defer errf.Close()
	cmd.Stdout, cmd.Stderr = out, errf
	if err := cmd.Start(); err != nil {
		w.note("agy: %v", err)
		return -1
	}
	pgid := cmd.Process.Pid
	w.pgids = append(w.pgids, pgid)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(agyTimeout)
	defer timer.Stop()
	select {
	case err = <-done:
	case <-timer.C:
		w.note("agy timed out after %s; agy itself was killed", agyTimeout)
		cmd.Process.Kill()
		err = <-done
	case <-ctx.Done():
		syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(stopGrace):
			syscall.Kill(-pgid, syscall.SIGKILL)
			err = <-done
		}
	}
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// bash runs a setup or check script in the clone with env, its output to
// logName in the sandbox directory, and reports exit 0.
func (w *row) bash(ctx context.Context, script string, env []string, logName string) bool {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = w.app
	cmd.Env = env
	log, err := openRegular(filepath.Join(w.dir, logName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		w.note("%v", err)
		return false
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	return cmd.Run() == nil
}
