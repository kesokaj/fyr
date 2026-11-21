package search

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

type hookRunResult struct {
	data []byte
	err  error
}

type hookInputMeta struct {
	dir   string
	input HookInput
}

func runHookWithDeadline(o Options, event string, in io.Reader, out io.Writer) error {
	if in == nil {
		in = os.Stdin
	}
	deadline := o.hookDeadline
	if deadline <= 0 {
		deadline = defaultHookDeadline
	}
	dispatchFn := o.hookDispatch
	if dispatchFn == nil {
		dispatchFn = dispatchHookEvent
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	ch := make(chan hookRunResult, 1)
	metaCh := make(chan hookInputMeta, 1)
	go runHookWorker(ctx, o, event, in, dispatchFn, ch, metaCh)

	select {
	case res := <-ch:
		if res.err == nil && len(res.data) > 0 {
			_, _ = out.Write(res.data)
			return nil
		}
		return writeHookAllowFallback(event, out)
	case <-ctx.Done():
		if event == "pre-invocation" {
			select {
			case m := <-metaCh:
				recordPreInvocationTimeoutBestEffort(o, m.dir, m.input, max(1, int(time.Since(start).Milliseconds())))
			default:
			}
		}
		return writeHookAllowFallback(event, out)
	}
}

func runHookWorker(ctx context.Context, o Options, event string, in io.Reader, dispatchFn func(context.Context, Options, string, string, HookInput, io.Writer) error, ch chan<- hookRunResult, metaCh chan<- hookInputMeta) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			select {
			case ch <- hookRunResult{}:
			default:
			}
		}
	}()
	if ctx.Err() != nil {
		return
	}
	input, decErr := decodeHookInput(o, in)
	if ctx.Err() != nil {
		return
	}
	dir := resolveHookDir(o, input)
	if decErr != nil || hooksDisabledForHome(o.Home, dir) {
		_ = writeHookAllowFallback(event, &buf)
		select {
		case ch <- hookRunResult{data: buf.Bytes()}:
		case <-ctx.Done():
		}
		return
	}
	select {
	case metaCh <- hookInputMeta{dir: dir, input: input}:
	default:
	}
	err := dispatchFn(ctx, o, event, dir, input, &buf)
	select {
	case ch <- hookRunResult{data: buf.Bytes(), err: err}:
	case <-ctx.Done():
	}
}

func recordPreInvocationTimeoutBestEffort(o Options, dir string, in HookInput, durationMS int) {
	if hooksDisabledForHome(o.Home, dir) {
		return
	}
	root, commonDir, ok := findGitRootFast(dir)
	if !ok || hooksDisabled(root) || guardLedgerFast(root, commonDir) != nil {
		return
	}
	st := openShortTimeoutStore(projectDB(root))
	if st == nil {
		return
	}
	defer st.close()
	convID := normalizeLedgerConvID(in.ConversationID)
	rawPrompt := StripSandboxPreamble(strings.TrimSpace(in.LastUserInput))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	tctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var maxTurn int
	_ = st.db.QueryRowContext(tctx, `SELECT COALESCE(MAX(turn_num), 0) FROM vet_history WHERE conversation_id = ?`, convID).Scan(&maxTurn)
	res := VetResult{Intent: ClassifyIntent(cleanPrompt, 0), Gate: GatePassThrough}
	_ = st.recordVetHistoryWithMetrics(convID, maxTurn+1, rawPrompt, res, nil, vetTurnMetrics{
		durationMS: durationMS,
		timedOut:   true,
	})
}

func openShortTimeoutStore(path string) *store {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil
	}
	if err := prepareFiles(path); err != nil {
		return nil
	}
	absPath := path
	if abs, err := filepath.Abs(path); err == nil {
		absPath = abs
	}
	u := url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(absPath),
		RawQuery: "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(15)&_pragma=foreign_keys(0)",
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: path}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil
	}
	return s
}

func dispatchHookEvent(ctx context.Context, o Options, event, dir string, input HookInput, out io.Writer) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch event {
	case "session-start":
		return handleSessionStart(ctx, o, dir, input, out)
	case "pre-invocation":
		return handlePreInvocation(ctx, o, dir, input, out)
	case "pre-tool-use":
		return handlePreToolUse(ctx, o, dir, input, out)
	case "post-tool-use":
		return handlePostToolUse(ctx, o, dir, input, out)
	default:
		return handleStopHook(ctx, o, dir, input, out)
	}
}

func writeHookAllowFallback(event string, out io.Writer) error {
	if event == "post-tool-use" {
		_, _ = out.Write([]byte("{}\n"))
		return nil
	}
	if event == "pre-invocation" || event == "session-start" {
		writePreInvocation(out, "", "")
		return nil
	}
	return writeDecision(out, "allow", "")
}

func hooksDisabledGlobal(home string) bool {
	h := strings.TrimSpace(home)
	if h == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(h, harness.StateDir, "no-hooks"))
	return err == nil
}

func hooksDisabledForHome(home, dir string) bool {
	return hooksDisabledGlobal(home) || hooksDisabled(dir)
}

// hooksDisabled reports whether dir (or its git checkout root) contains
// .agents/scratch/no-hooks, used by paired A/B eval control scenarios.
func hooksDisabled(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents", "scratch", "no-hooks")); err == nil {
		return true
	}
	if root, _, ok := findGitRootFast(dir); ok {
		if _, err := os.Stat(filepath.Join(root, ".agents", "scratch", "no-hooks")); err == nil {
			return true
		}
	}
	return false
}
