package isolate

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

	"github.com/kesokaj/fyr/internal/isolate/acp"
)

// ACPClient, ACPEvent, and PromptResponse re-export the ACP protocol types
// from internal/isolate/acp so callers of package isolate remain unchanged.
type (
	ACPClient      = acp.Client
	ACPEvent       = acp.Event
	PromptResponse = acp.PromptResponse
)

// ACPCommand returns the argv flags used to start agy in Agent Client Protocol
// stdio mode with process isolation per session.
func ACPCommand() []string {
	return acp.Command()
}

// NewACPClient starts the background reader loop over r and sends requests on w.
func NewACPClient(r io.Reader, w io.Writer, grants []string, onDenied func(tool string)) *ACPClient {
	return acp.NewClient(r, w, grants, onDenied)
}

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

func (b *Box) acpTurnContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if b.c.Timeout > 0 {
		return context.WithTimeout(ctx, b.c.Timeout)
	}
	return ctx, func() {}
}

func (b *Box) ensureACP(turnCtx context.Context, agy string, errf *os.File) (*acpProcess, error) {
	if p := b.currentACP(); p != nil {
		p.setErrWriter(errf)
		return p, nil
	}
	if err := b.startACP(turnCtx, agy, errf); err != nil {
		return nil, err
	}
	return b.currentACP(), nil
}

func (b *Box) acpSession(ctx context.Context, agy, prompt, outPath, errPath string, appendErr bool) int {
	out, errf, err := agyOutputs(outPath, errPath, appendErr)
	if err != nil {
		b.fileFailed(err, "%v", err)
		return -1
	}
	defer out.Close()
	defer errf.Close()

	turnCtx, cancel := b.acpTurnContext(ctx)
	defer cancel()
	p, err := b.ensureACP(turnCtx, agy, errf)
	if err != nil {
		b.handleACPError(ctx, turnCtx, err)
		b.closeACP()
		return -1
	}
	defer p.setErrWriter(nil)
	full := "The project root is the current directory, " + b.app + "; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. " + prompt
	resp, evBytes, err := p.client.Prompt(turnCtx, p.sessionID, b.c.Model, b.app, full, b.c.OnEvent)
	b.writeACPOutputs(out, resp, evBytes)
	if err != nil {
		b.handleACPError(ctx, turnCtx, err)
		b.closeACP()
		return -1
	}
	return 0
}

func (b *Box) startACP(turnCtx context.Context, agy string, errf *os.File) error {
	cmd, cleanup, err := b.buildAgentCmd(agy, ACPCommand())
	if err != nil {
		return err
	}
	setCmdSetsid(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return err
	}
	p := &acpProcess{cmd: cmd, stdin: stdin, done: make(chan error, 1), errOut: errf}
	cmd.Stderr = errf
	err = cmd.Start()
	cleanup()
	if err != nil {
		return err
	}
	b.setStarted()
	p.pgid = cmd.Process.Pid
	b.pgids = append(b.pgids, p.pgid)
	go func() {
		defer close(p.done)
		p.done <- cmd.Wait()
	}()
	p.client = NewACPClient(stdout, stdin, b.c.Grants, p.writeDenied)
	if err := b.initACPSession(turnCtx, p); err != nil {
		closeACPProcess(p)
		return err
	}
	b.setACP(p)
	return nil
}

func (b *Box) initACPSession(turnCtx context.Context, p *acpProcess) error {
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
	if err := p.client.SetSessionModel(turnCtx, sid, b.c.Model); err != nil {
		return err
	}
	return p.client.SetSessionMode(turnCtx, sid, "accept-edits")
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
		b.setTimedOut()
		return
	}
	b.Note("agy: %v", err)
}

func (b *Box) closeACP() {
	p := b.takeACP()
	if p == nil {
		return
	}
	closeACPProcess(p)
}

func closeACPProcess(p *acpProcess) {
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
