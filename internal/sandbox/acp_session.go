package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type acpProcess struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	client    *ACPClient
	sessionID string
	pgid      int
	done      chan error
	errMu     sync.Mutex
	errOut    io.Writer
}

func (p *acpProcess) setErrWriter(w io.Writer) {
	p.errMu.Lock()
	p.errOut = w
	p.errMu.Unlock()
}

func (p *acpProcess) writeDenied(tool string) {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	if p.errOut != nil {
		_, _ = fmt.Fprintf(p.errOut, "tool call auto-denied: %s\n", tool)
	}
}

func (b *Box) acpSession(ctx context.Context, agy, prompt, outPath, errPath string, appendErr bool) int {
	out, errf, err := agyOutputs(outPath, errPath, appendErr)
	if err != nil {
		b.fileFailed(err, "%v", err)
		return -1
	}
	defer out.Close()
	defer errf.Close()

	turnCtx := ctx
	var cancel context.CancelFunc
	if b.c.Timeout > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, b.c.Timeout)
		defer cancel()
	}
	if b.acpProc == nil {
		if err := b.startACP(turnCtx, agy, errf); err != nil {
			b.handleACPError(ctx, turnCtx, err)
			b.closeACP()
			return -1
		}
	} else {
		b.acpProc.setErrWriter(errf)
	}
	defer b.acpProc.setErrWriter(nil)
	full := "The project root is the current directory, " + b.app + "; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. " + prompt
	resp, evBytes, err := b.acpProc.client.Prompt(turnCtx, b.acpProc.sessionID, b.c.Model, b.app, full, b.c.OnEvent)
	b.writeACPOutputs(out, resp, evBytes)
	if err != nil {
		b.handleACPError(ctx, turnCtx, err)
		b.closeACP()
		return -1
	}
	return 0
}

func (b *Box) startACP(turnCtx context.Context, agy string, errf *os.File) error {
	cmd := exec.Command(agy, ACPCommand()...)
	cmd.Dir = b.app
	cmd.Env = b.agyEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	p := &acpProcess{cmd: cmd, stdin: stdin, done: make(chan error, 1), errOut: errf}
	cmd.Stderr = errf
	if err := cmd.Start(); err != nil {
		return err
	}
	b.started = true
	p.pgid = cmd.Process.Pid
	b.pgids = append(b.pgids, p.pgid)
	go func() {
		defer close(p.done)
		p.done <- cmd.Wait()
	}()
	p.client = NewACPClient(stdout, stdin, b.c.Grants, p.writeDenied)
	b.acpProc = p

	if err := p.client.Initialize(turnCtx); err != nil {
		return err
	}
	sid, err := p.client.NewSession(turnCtx, b.app)
	if err != nil {
		return err
	}
	p.sessionID = sid
	if err := b.writeSessionID(sid); err != nil {
		return err
	}
	_ = p.client.SetSessionModel(turnCtx, sid, b.c.Model)
	_ = p.client.SetSessionMode(turnCtx, sid, "accept-edits")
	return nil
}

func (b *Box) writeSessionID(sid string) error {
	f, err := OpenRegular(filepath.Join(b.dir, "session.id"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		b.fileFailed(err, "%v", err)
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, sid)
	return err
}

func (b *Box) writeACPOutputs(out *os.File, resp *PromptResponse, evBytes []byte) {
	if resp != nil && resp.Text != "" {
		_, _ = out.WriteString(resp.Text)
		if !strings.HasSuffix(resp.Text, "\n") {
			_, _ = out.WriteString("\n")
		}
	}
	if len(evBytes) > 0 {
		if ef, err := OpenRegular(filepath.Join(b.dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND); err == nil {
			_, _ = ef.Write(evBytes)
			_ = ef.Close()
		}
	}
}

func (b *Box) handleACPError(parentCtx, turnCtx context.Context, err error) {
	if errors.Is(turnCtx.Err(), context.DeadlineExceeded) && parentCtx.Err() == nil {
		b.Note("agy timed out after %s; agy itself was killed", b.c.Timeout)
		b.timedOut = true
		return
	}
	b.Note("agy: %v", err)
}

// Steer sends a mid-session steering message to an active ACP session.
func (b *Box) Steer(ctx context.Context, text string) error {
	if b.acpProc == nil || b.acpProc.client == nil || b.acpProc.sessionID == "" {
		return errors.New("no active ACP session")
	}
	return b.acpProc.client.Steer(ctx, b.acpProc.sessionID, text)
}

// SessionID returns the active ACP session ID, or "" if none is active.
func (b *Box) SessionID() string {
	if b.acpProc != nil {
		return b.acpProc.sessionID
	}
	data, err := os.ReadFile(filepath.Join(b.dir, "session.id"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (b *Box) closeACP() {
	p := b.acpProc
	if p == nil {
		return
	}
	b.acpProc = nil
	if p.client != nil && p.sessionID != "" {
		cctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = p.client.CloseSession(cctx, p.sessionID)
		cancel()
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.client != nil {
		_ = p.client.Close()
	}
	select {
	case <-p.done:
	case <-time.After(500 * time.Millisecond):
	}
	if p.pgid > 0 {
		_ = syscall.Kill(-p.pgid, syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(300 * time.Millisecond):
			_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
			<-p.done
		}
	}
}

func isCommandTool(name, kind string) bool {
	l := strings.ToLower(name)
	k := strings.ToLower(kind)
	return k == "execute" || k == "command" || k == "bash" || k == "terminal" ||
		strings.Contains(l, "run_command") || strings.Contains(l, "bash") ||
		strings.Contains(l, "terminal") || strings.HasPrefix(l, "command(")
}

func isReadTool(name, kind string) bool {
	l := strings.ToLower(name)
	k := strings.ToLower(kind)
	return k == "read" || k == "search" ||
		strings.Contains(l, "read_file") || strings.Contains(l, "view_file") ||
		strings.Contains(l, "list_dir") || strings.Contains(l, "grep") ||
		strings.Contains(l, "find_by_name") || strings.Contains(l, "glob")
}

func isWriteTool(name, kind string) bool {
	l := strings.ToLower(name)
	k := strings.ToLower(kind)
	return k == "edit" || k == "write" ||
		strings.Contains(l, "write_file") || strings.Contains(l, "edit_file") ||
		strings.Contains(l, "replace_file") || strings.Contains(l, "write_to_file")
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
