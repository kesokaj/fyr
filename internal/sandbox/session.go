package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

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

// linkLogin links the operator's login into the sandbox home: TokenRel when
// tok is set (or when no Keychain directory is used), and on darwin in host
// mode KeychainsRel when RealHome holds a Keychain directory (BUG-166).
func (b *Box) linkLogin(tok string) error {
	b.tok = tok // After checks the real token whether or not the link was made
	var kc string
	if b.c.Docker == nil && hostOS == "darwin" && b.c.RealHome != "" {
		p := filepath.Join(b.c.RealHome, filepath.FromSlash(KeychainsRel))
		if st, err := os.Lstat(p); err == nil && st.IsDir() {
			kc = p
		}
	}
	b.keychains = kc
	if tok != "" || kc == "" {
		if err := os.Symlink(tok, filepath.Join(b.sb, filepath.FromSlash(TokenRel))); err != nil {
			return err
		}
		b.tokLinked = true
	}
	if kc != "" {
		entries, _ := os.ReadDir(kc)
		for _, e := range entries {
			if e.Type().IsRegular() {
				b.keychainFiles = append(b.keychainFiles, filepath.Join(kc, e.Name()))
			}
		}
		if err := os.MkdirAll(filepath.Join(b.sb, "Library"), 0o700); err != nil {
			return err
		}
		if err := os.Symlink(kc, filepath.Join(b.sb, filepath.FromSlash(KeychainsRel))); err != nil {
			return err
		}
		b.keychainsLinked = true
	}
	return nil
}

// agy runs one session in its own process group (D18): at the timeout only
// agy itself is killed, as `timeout --foreground` did, so what it started
// stays for check e; on ctx's end the whole group gets SIGTERM, then
// SIGKILL after StopGrace. In docker mode the container is killed instead
// (dockerAgy).
func (b *Box) agy(ctx context.Context, agy, prompt, outPath, errPath string, appendErr bool) int {
	if b.c.Docker != nil {
		return b.dockerAgy(ctx, prompt, outPath, errPath, appendErr)
	}
	cmd := b.hostCmd(agy, prompt)
	out, errf, err := agyOutputs(outPath, errPath, appendErr)
	if err != nil {
		b.fileFailed(err, "%v", err)
		return -1
	}
	defer out.Close()
	defer errf.Close()
	cmd.Stdout, cmd.Stderr = out, errf
	if err := cmd.Start(); err != nil {
		b.Note("agy: %v", err)
		return -1
	}
	b.started = true
	pgid := cmd.Process.Pid
	b.pgids = append(b.pgids, pgid)
	return agyExit(b.waitAgy(ctx, cmd, pgid))
}

// agyArgs is agy's argv after its name, the prompt with its prefix: the
// same on the host and in a container.
func (b *Box) agyArgs(prompt string) []string {
	full := "The project root is the current directory, " + b.app + "; ignore any other checkout. " + prompt
	args := []string{"-p", full, "--mode", "accept-edits", "--print-timeout", b.c.PrintTimeout}
	if b.c.Model != "" {
		args = append(args, "--model", b.c.Model)
	}
	return args
}

// hostCmd is the session on the host: agy in the clone, agyEnv's allowlist,
// its own process group.
func (b *Box) hostCmd(agy, prompt string) *exec.Cmd {
	cmd := exec.Command(agy, b.agyArgs(prompt)...)
	cmd.Dir = b.app
	cmd.Env = b.agyEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
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
		b.timedOut = true
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
