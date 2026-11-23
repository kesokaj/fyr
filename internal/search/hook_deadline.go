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
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

const preInvocationTimeoutRecorderBudget = 45 * time.Millisecond

type hookRunResult struct {
	data []byte
	err  error
}

type hookInputMeta struct {
	dir      string
	input    HookInput
	storeRef *hookTimeoutStoreRef
}

type hookTimeoutStoreCtxKey struct{}

// hookTimeoutStoreRef lets a timed-out PreInvocation recorder reuse the worker's
// already-open SQLite store safely without concurrent close races.
type hookTimeoutStoreRef struct {
	mu     sync.Mutex
	st     *store
	root   string
	refs   int
	closed bool
}

func withHookTimeoutStoreRef(ctx context.Context, ref *hookTimeoutStoreRef) context.Context {
	return context.WithValue(ctx, hookTimeoutStoreCtxKey{}, ref)
}

func hookTimeoutStoreRefFromCtx(ctx context.Context) *hookTimeoutStoreRef {
	if ctx == nil {
		return nil
	}
	ref, _ := ctx.Value(hookTimeoutStoreCtxKey{}).(*hookTimeoutStoreRef)
	return ref
}

func (r *hookTimeoutStoreRef) attach(st *store, root string) {
	if r == nil || st == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.st = st
	r.root = root
	r.refs = 1
	r.closed = false
}

func (r *hookTimeoutStoreRef) acquire(root string) (*store, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.st == nil || r.closed || r.refs <= 0 || !sameCleanPath(r.root, root) {
		return nil, false
	}
	r.refs++
	return r.st, true
}

func (r *hookTimeoutStoreRef) release() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.refs <= 0 {
		r.mu.Unlock()
		return
	}
	r.refs--
	var toClose *store
	if r.refs == 0 {
		toClose = r.st
		r.st = nil
		r.closed = true
	}
	r.mu.Unlock()
	if toClose != nil {
		_ = toClose.close()
	}
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
		if event == "pre-tool-use" && isRelaySession() {
			return writeDecision(out, "deny", "[RELAY_GUARD] pre-tool-use hook error or panic denied during fyr relay session (fail-closed)")
		}
		return writeHookAllowFallback(event, out)
	case <-ctx.Done():
		if event == "pre-tool-use" && isRelaySession() {
			return writeDecision(out, "deny", "[RELAY_GUARD] pre-tool-use hook deadline exceeded during fyr relay session (fail-closed)")
		}
		err := writeHookAllowFallback(event, out)
		if event == "pre-invocation" {
			select {
			case m := <-metaCh:
				recordPreInvocationTimeoutBounded(o, m, max(1, int(time.Since(start).Milliseconds())))
			default:
			}
		}
		return err
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
	rawData, readErr := io.ReadAll(io.LimitReader(in, 4<<20))
	if ctx.Err() != nil {
		return
	}
	var input HookInput
	decErr := readErr
	if decErr == nil {
		input, decErr = decodeHookInput(o, bytes.NewReader(rawData))
	}
	if ctx.Err() != nil {
		return
	}
	dir := resolveHookDir(o, input)
	if decErr != nil {
		emitHookDecodeError(ctx, event, &buf, ch)
		return
	}
	if hookBypassedForEvent(o.Home, dir, event) {
		_ = writeHookAllowFallback(event, &buf)
		sendHookResult(ctx, ch, hookRunResult{data: buf.Bytes()})
		return
	}
	storeRef := &hookTimeoutStoreRef{}
	ctx = withHookSubagentMeta(ctx, extractHookSubagentMeta(rawData))
	ctx = withHookTimeoutStoreRef(ctx, storeRef)
	select {
	case metaCh <- hookInputMeta{dir: dir, input: input, storeRef: storeRef}:
	default:
	}
	err := dispatchFn(ctx, o, event, dir, input, &buf)
	sendHookResult(ctx, ch, hookRunResult{data: buf.Bytes(), err: err})
}

func emitHookDecodeError(ctx context.Context, event string, buf *bytes.Buffer, ch chan<- hookRunResult) {
	if event == "pre-tool-use" && isRelaySession() {
		_ = writeDecision(buf, "deny", "[RELAY_GUARD] malformed or oversized pre-tool-use payload denied during fyr relay session (fail-closed)")
	} else {
		_ = writeHookAllowFallback(event, buf)
	}
	sendHookResult(ctx, ch, hookRunResult{data: buf.Bytes()})
}

func sendHookResult(ctx context.Context, ch chan<- hookRunResult, res hookRunResult) {
	select {
	case ch <- res:
	case <-ctx.Done():
	}
}

func recordPreInvocationTimeoutBounded(o Options, m hookInputMeta, durationMS int) {
	tctx, cancel := context.WithTimeout(context.Background(), preInvocationTimeoutRecorderBudget)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		recordPreInvocationTimeoutBestEffort(tctx, o, m, durationMS)
	}()
	select {
	case <-done:
	case <-tctx.Done():
	}
}

func recordPreInvocationTimeoutBestEffort(tctx context.Context, o Options, m hookInputMeta, durationMS int) {
	if tctx.Err() != nil || hooksDisabledForHome(o.Home, m.dir) {
		return
	}
	root, commonDir, ok := findGitRootFast(m.dir)
	if !ok || hooksDisabled(root) || guardLedgerFast(root, commonDir) != nil {
		return
	}
	st, reused := m.storeRef.acquire(root)
	if reused {
		defer m.storeRef.release()
	} else {
		st = openShortTimeoutStoreCtx(tctx, projectDB(root))
		if st == nil {
			return
		}
		defer st.close()
	}
	convID := normalizeLedgerConvID(m.input.ConversationID)
	rawPrompt := StripSandboxPreamble(strings.TrimSpace(m.input.LastUserInput))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	stampedTurn := stampPreInvocationFallbackCtx(tctx, st, convID, m.input, rawPrompt)
	var maxTurn int
	_ = st.db.QueryRowContext(tctx, `SELECT COALESCE(MAX(turn_num), 0) FROM vet_history WHERE conversation_id = ?`, convID).Scan(&maxTurn)
	if tctx.Err() != nil {
		return
	}
	res := VetResult{Intent: ClassifyIntent(cleanPrompt, 0), Gate: GatePassThrough}
	_ = st.recordVetHistoryWithMetricsCtx(tctx, convID, max(maxTurn+1, stampedTurn), rawPrompt, res, nil, vetTurnMetrics{
		durationMS: durationMS,
		timedOut:   true,
	})
}

func openShortTimeoutStoreCtx(ctx context.Context, path string) *store {
	if ctx.Err() != nil {
		return nil
	}
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
		RawQuery: "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(25)&_pragma=foreign_keys(0)",
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: path}
	var v string
	if err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema'`).Scan(&v); err == nil && v == schemaVersion {
		return s
	}
	if ctx.Err() != nil {
		_ = db.Close()
		return nil
	}
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
	cur := filepath.Clean(h)
	fi, err := os.Lstat(cur)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(harness.StateDir), "/") {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		sfi, serr := os.Lstat(cur)
		if serr != nil || sfi.Mode()&os.ModeSymlink != 0 || !sfi.IsDir() {
			return false
		}
	}
	mfi, merr := os.Lstat(filepath.Join(cur, "no-hooks"))
	return merr == nil && mfi.Mode().IsRegular()
}

func isRelaySession() bool {
	return os.Getenv("FYR_RELAY") == "1"
}

func isRelayProtectedEvent(event string) bool {
	return (event == "pre-tool-use" || event == "post-tool-use") && isRelaySession()
}

func hookBypassedForEvent(home, dir, event string) bool {
	if isRelayProtectedEvent(event) {
		return false
	}
	if hooksDisabledGlobal(home) {
		return true
	}
	return hooksDisabled(dir)
}

func hooksDisabledForHome(home, dir string) bool {
	return hooksDisabledGlobal(home) || hooksDisabled(dir)
}

func hasRegularScratchNoHooks(base string) bool {
	if checkScratchNoSymlinks(base) != nil {
		return false
	}
	fi, err := os.Lstat(filepath.Join(base, ".agents", "scratch", "no-hooks"))
	return err == nil && fi.Mode().IsRegular()
}

// hooksDisabled reports whether dir (or its git checkout root) contains a
// regular (non-symlinked) .agents/scratch/no-hooks marker, used by paired A/B eval scenarios.
func hooksDisabled(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if hasRegularScratchNoHooks(dir) {
		return true
	}
	if root, _, ok := findGitRootFast(dir); ok && hasRegularScratchNoHooks(root) {
		return true
	}
	return false
}
