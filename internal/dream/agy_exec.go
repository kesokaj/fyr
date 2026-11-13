package dream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// call runs the CLI in work with the prompt on stdin as one stream-json
// message, in env alone: the run passes childEnv, the allow-list and the
// branch's HOME (ADR-067).
func call(argv []string, prompt, work string, env []string) (string, string, error) {
	return callWithin(argv, prompt, work, env, cliTimeout, killGrace)
}

// callWithin is call with its two bounds explicit: timeout for the CLI, and
// grace for how long Run may wait, after the kill, on a grandchild that kept
// the output pipes.
func callWithin(argv []string, prompt, work string, env []string, timeout, grace time.Duration) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd, out, errb, err := cliCommand(ctx, argv, prompt, work, env)
	if err != nil {
		return "", "", err
	}
	err = runGroup(cmd, grace)
	return cliResult(argv[0], timeout, ctx.Err(), out.String(), errb.String(), err)
}

// runGroup runs cmd, made with exec.CommandContext, in its own process
// group and returns Wait's error, or Start's.
func runGroup(cmd *exec.Cmd, grace time.Duration) error {
	// The CLI gets its own process group, killed whole at the deadline, on an
	// interrupt, and once the CLI itself is done; Wait gives up on the output
	// pipes after grace. A helper the CLI started (a language or MCP server)
	// kept them open past the kill and held Run, and so dream.lock, for as
	// long as it lived (BUG-169); the same helper after a clean exit turned
	// the run into a failure, and Ctrl-C stopped reaching a CLI outside
	// fyr's own group (the fix's review).
	cmd.WaitDelay = grace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	killGroup := func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Cancel = func() error { killGroup(); return nil }
	if err := cmd.Start(); err != nil {
		return err
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, watchedSignals()...)
	waited, relayed := make(chan struct{}), make(chan struct{})
	go func() {
		relay(sigs, waited, func(sig os.Signal) {
			killGroup()
			// Then die of the signal, as fyr did before the group existed.
			signal.Reset(sig)
			syscall.Kill(os.Getpid(), sig.(syscall.Signal))
		})
		close(relayed)
	}()
	err := cmd.Wait()
	signal.Stop(sigs) // before waited closes: a signal in between still reaches the goroutine
	close(waited)
	<-relayed // a signal taken at the end is acted on before the next project starts
	killGroup()
	return err
}

// cliCommand is the CLI's command, with the prompt on its stdin as one
// stream-json message and its output captured.
func cliCommand(ctx context.Context, argv []string, prompt, work string, env []string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, error) {
	if env == nil {
		// exec would hand the child fyr's whole environment.
		return nil, nil, nil, errors.New("the CLI's environment is unset")
	}
	msg, err := json.Marshal(map[string]any{"event": "user", "message": map[string]string{"content": prompt}})
	if err != nil {
		return nil, nil, nil, err
	}
	stdin := string(msg) + "\n"
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = work
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	return cmd, &out, &errb, nil
}

// watchedSignals are the signals callWithin relays to the CLI's group.
func watchedSignals() []os.Signal {
	// A hangup or interrupt the parent ignored (nohup, a background job) stays
	// ignored: Notify would take it over, kill the group on it, and the
	// re-raise after Reset would find it ignored again, so the project failed
	// and fyr ran on. Go keeps only these two of an inherited ignore.
	watched := []os.Signal{syscall.SIGTERM, syscall.SIGQUIT}
	for _, s := range []os.Signal{os.Interrupt, syscall.SIGHUP} {
		if !signal.Ignored(s) {
			watched = append(watched, s)
		}
	}
	return watched
}

// cliResult is callWithin's answer once the CLI's group is gone: a timeout,
// a failure, or the output.
func cliResult(name string, timeout time.Duration, ctxErr error, out, errb string, err error) (string, string, error) {
	if ctxErr == context.DeadlineExceeded {
		return out, errb, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The CLI exited 0; only a helper it left behind kept the pipes.
		err = nil
	}
	if err != nil {
		return out, errb, fmt.Errorf("%s: %v", name, err)
	}
	return out, errb, nil
}

// relay hands the first signal to onSignal, or returns when the CLI is done.
// A signal already queued when the CLI ends is still handed on: select picks
// at random between ready cases, and the end alone dropped it half the time,
// so `dream --all` went on to the next project (the release gate's review of
// v2.6.2..13c5bde).
func relay(sigs <-chan os.Signal, waited <-chan struct{}, onSignal func(os.Signal)) {
	select {
	case sig := <-sigs:
		onSignal(sig)
	case <-waited:
		select {
		case sig := <-sigs:
			onSignal(sig)
		default:
		}
	}
}
