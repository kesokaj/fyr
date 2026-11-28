package isolate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/isolate/worker"
	"github.com/kesokaj/fyr/internal/models"
)

var (
	modelRe                = models.ModelIDRe
	transcriptPollInterval = 500 * time.Millisecond
)

func (b *Box) watchTranscript(ctx context.Context, cancel context.CancelFunc) (stop func()) {
	return worker.WatchTranscript(ctx, cancel, worker.TranscriptWatchConfig{
		SandboxHome:  b.sb,
		AppDir:       b.app,
		IdleTimeout:  b.c.IdleTimeout,
		PollInterval: transcriptPollInterval,
		OnEvent:      b.c.OnEvent,
		Note:         b.Note,
	})
}

// Session is steps 9-10 for one prompt: the token linked by the first
// session, then agy, its output to outName and its errors to err.txt in the
// sandbox directory (appended to after the first session). A later session
// runs only on an intact sandbox: it opens its output through the same
// paths. It returns agy's exit status (-1: none, killed).
func (b *Box) Session(ctx context.Context, agy, tok, prompt, outName string) int {
	if b.sessions > 0 && !b.Intact() {
		return -1
	}
	if !b.linked {
		if err := b.linkLogin(tok); err != nil {
			b.Note("linking the token: %v", err)
			b.Fail(ReasonToken)
			return -1
		}
		b.linked = true
	}
	appendErr := b.sessions > 0
	b.sessions++
	return b.agy(ctx, agy, prompt, filepath.Join(b.dir, outName), filepath.Join(b.dir, "err.txt"), appendErr)
}

// linkLogin links the operator's login token into the sandbox home.
func (b *Box) linkLogin(tok string) error {
	if tok == "" {
		return errors.New("missing login token")
	}
	b.tok = tok // After checks the real token whether or not the link was made
	if err := os.Symlink(tok, filepath.Join(b.sb, filepath.FromSlash(TokenRel))); err != nil {
		return err
	}
	b.tokLinked = true
	return nil
}

// agy runs one session in its own process group (D18): at the timeout only
// agy itself is killed, as `timeout --foreground` did, so what it started
// stays for check e; on ctx's end the whole group gets SIGTERM, then
// SIGKILL after StopGrace.
func (b *Box) agy(ctx context.Context, agy, prompt, outPath, errPath string, appendErr bool) int {
	if b.c.Model != "" && !modelRe.MatchString(b.c.Model) {
		b.Note("isolate: invalid model ID %q", b.c.Model)
		return -1
	}
	if b.c.ACP {
		return b.acpSession(ctx, agy, prompt, outPath, errPath, appendErr)
	}
	cmd, cleanup, err := b.hostCmd(agy, prompt)
	if err != nil {
		b.Note("isolate: %v", err)
		return -1
	}
	out, errf, err := agyOutputs(outPath, errPath, appendErr)
	if err != nil {
		cleanup()
		b.fileFailed(err, "%v", err)
		return -1
	}
	defer out.Close()
	defer errf.Close()
	cmd.Stdout, cmd.Stderr = out, errf
	err = cmd.Start()
	cleanup()
	if err != nil {
		b.Note("agy: %v", err)
		return -1
	}
	b.setStarted()
	pgid := cmd.Process.Pid
	b.pgids = append(b.pgids, pgid)
	if b.c.OnEvent != nil || b.c.IdleTimeout > 0 {
		watchCtx, cancelWatch := context.WithCancel(ctx)
		defer cancelWatch()
		stopWatch := b.watchTranscript(watchCtx, cancelWatch)
		defer stopWatch()
		return agyExit(b.waitAgy(watchCtx, cmd, pgid))
	}
	return agyExit(b.waitAgy(ctx, cmd, pgid))
}

// agyArgs is agy's argv after its name, the prompt with its prefix: the
// same on the host and in a container.
func (b *Box) agyArgs(prompt string) []string {
	full := "The project root is the current directory, " + b.app + "; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. " + prompt
	args := []string{"-p", full, "--mode", "accept-edits", "--print-timeout", b.c.PrintTimeout}
	if b.c.Model != "" && modelRe.MatchString(b.c.Model) {
		args = append(args, "--model", b.c.Model)
	}
	return args
}

// hostCmd prepares agy in the clone under OS confinement (or unconfined only
// in tripwire unit tests) in its own process group.
func (b *Box) hostCmd(agy, prompt string) (*exec.Cmd, func(), error) {
	cmd, cleanup, err := b.buildAgentCmd(agy, b.agyArgs(prompt))
	if err != nil {
		return nil, nil, err
	}
	setCmdSetsid(cmd)
	return cmd, cleanup, nil
}

func setCmdSetsid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// agyOutputs opens the session's output and error files, the error file
// appended to for a later session.
func agyOutputs(outPath, errPath string, appendErr bool) (out, errf *os.File, err error) {
	// A file the box writes that is not a regular file was put there by the
	// run (a session cannot start without it): the sandbox was replaced,
	// unless the machine failed the open before any session (fileFailed).
	out, err = OpenRegular(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return nil, nil, err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendErr {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	errf, err = OpenRegular(errPath, flags)
	if err != nil {
		out.Close()
		return nil, nil, err
	}
	return out, errf, nil
}

// waitAgy waits for the started session: at Timeout agy alone is killed; on
// ctx's end its group gets SIGTERM, then SIGKILL after StopGrace.
func (b *Box) waitAgy(ctx context.Context, cmd *exec.Cmd, pgid int) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(b.c.Timeout)
	defer timer.Stop()
	var err error
	select {
	case err = <-done:
	case <-timer.C:
		b.Note("agy timed out after %s; agy itself was killed", b.c.Timeout)
		b.setTimedOut()
		cmd.Process.Kill()
		err = <-done
	case <-ctx.Done():
		syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(b.c.StopGrace):
			syscall.Kill(-pgid, syscall.SIGKILL)
			err = <-done
		}
	}
	return err
}

// agyExit is a session's exit status: -1 for one that did not exit.
func agyExit(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Bash runs a setup or check script in the clone with env, its output to
// logName in the sandbox directory, and reports exit 0 (eval's steps 5 and
// 13). A log it cannot open fails the box as fileFailed says, and the
// script does not run.
func (b *Box) Bash(ctx context.Context, script string, env []string, logName string) bool {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = b.app
	cmd.Env = env
	log, err := OpenRegular(filepath.Join(b.dir, logName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		b.fileFailed(err, "%v", err)
		return false
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	return cmd.Run() == nil
}
