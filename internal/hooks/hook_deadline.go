package hooks

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/vet"
)

// TimeoutRecorderBudget returns the active timeout-recorder deadline budget
// (45ms in production builds, 450ms under -race).
func TimeoutRecorderBudget() time.Duration {
	return preInvocationTimeoutRecorderBudget
}

type hookRunResult struct {
	data []byte
	err  error
}

type hookInputMeta struct {
	dir      string
	input    HookInput
	storeRef *hookTimeoutStoreRef
}

type hookTimeoutStoreCtx struct{}

// hookTimeoutStoreRef lets a timed-out PreInvocation recorder reuse the worker's
// already-open SQLite store safely without concurrent close races.
type hookTimeoutStoreRef struct {
	mu     sync.Mutex
	st     *Store
	root   string
	refs   int
	closed bool
}

func withHookTimeoutStoreRef(ctx context.Context, ref *hookTimeoutStoreRef) context.Context {
	return context.WithValue(ctx, hookTimeoutStoreCtx{}, ref)
}

func hookTimeoutStoreRefFromCtx(ctx context.Context) *hookTimeoutStoreRef {
	if ctx == nil {
		return nil
	}
	ref, _ := ctx.Value(hookTimeoutStoreCtx{}).(*hookTimeoutStoreRef)
	return ref
}

func (r *hookTimeoutStoreRef) attach(st *Store, root string) {
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

func (r *hookTimeoutStoreRef) acquire(root string) (*Store, bool) {
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
	var toClose *Store
	if r.refs == 0 {
		toClose = r.st
		r.st = nil
		r.closed = true
	}
	r.mu.Unlock()
	if toClose != nil {
		_ = toClose.CloseErr()
	}
}

func runHookWithDeadline(d Deps, o Options, event string, in io.Reader, out io.Writer) error {
	if in == nil {
		in = os.Stdin
	}
	deadline := o.HookDeadline
	if deadline <= 0 {
		deadline = DefaultHookDeadline
	}
	dispatchFn := o.HookDispatch
	if dispatchFn == nil {
		dispatchFn = func(ctx context.Context, opt Options, ev, dir string, inp HookInput, w io.Writer) error {
			return dispatchHookEvent(ctx, d, opt, ev, dir, inp, w)
		}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	ch := make(chan hookRunResult, 1)
	metaCh := make(chan hookInputMeta, 1)
	go runHookWorker(ctx, o, event, in, dispatchFn, ch, metaCh)

	select {
	case res := <-ch:
		return handleHookWorkerResult(event, res, out)
	case <-ctx.Done():
		return handleHookTimeout(d, o, event, metaCh, start, out)
	}
}

func handleHookWorkerResult(event string, res hookRunResult, out io.Writer) error {
	if res.err == nil && len(res.data) > 0 {
		_, _ = out.Write(res.data)
		return nil
	}
	if event == "pre-tool-use" && isRelaySession() {
		return writeDecision(out, "deny", "[RELAY_GUARD] pre-tool-use hook error or panic denied during fyr relay session (fail-closed)")
	}
	return writeHookAllowFallback(event, out)
}

func handleHookTimeout(d Deps, o Options, event string, metaCh <-chan hookInputMeta, start time.Time, out io.Writer) error {
	if event == "pre-tool-use" && isRelaySession() {
		return writeDecision(out, "deny", "[RELAY_GUARD] pre-tool-use hook deadline exceeded during fyr relay session (fail-closed)")
	}
	err := writeHookAllowFallback(event, out)
	if event != "pre-invocation" {
		return err
	}
	select {
	case m := <-metaCh:
		recordPreInvocationTimeoutBounded(d, o, m, max(1, int(time.Since(start).Milliseconds())))
	default:
	}
	return err
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
	if HookBypassedForEvent(o.Home, dir, event) {
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

func recordPreInvocationTimeoutBounded(d Deps, o Options, m hookInputMeta, durationMS int) {
	tctx, cancel := context.WithTimeout(context.Background(), preInvocationTimeoutRecorderBudget)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		recordPreInvocationTimeoutBestEffort(tctx, d, o, m, durationMS)
	}()
	select {
	case <-done:
	case <-tctx.Done():
	}
}

func recordPreInvocationTimeoutBestEffort(tctx context.Context, d Deps, o Options, m hookInputMeta, durationMS int) {
	if tctx.Err() != nil || HooksDisabledForHome(o.Home, m.dir) {
		return
	}
	root, commonDir, ok := guard.FindGitRootFast(m.dir)
	if !ok || HooksDisabled(root) || guard.GuardLedgerFast(root, commonDir, d.GuardProject) != nil {
		return
	}
	st, release := acquireTimeoutRecorderStore(tctx, d, m.storeRef, root)
	if st == nil || st.SQLDB == nil {
		return
	}
	defer release()
	convID := readledger.NormalizeConvID(m.input.ConversationID)
	rawPrompt := StripSandboxPreamble(strings.TrimSpace(m.input.LastUserInput))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	stampedTurn := stampPreInvocationFallbackCtx(tctx, d, st, convID, m.input, rawPrompt)
	var maxTurn int
	_ = st.SQLDB.QueryRowContext(tctx, `SELECT COALESCE(MAX(turn_num), 0) FROM vet_history WHERE conversation_id = ?`, convID).Scan(&maxTurn)
	if tctx.Err() != nil {
		return
	}
	res := vet.Result{Intent: vet.ClassifyIntent(cleanPrompt, 0), Gate: vet.GatePassThrough}
	_ = vet.RecordHistoryCtx(tctx, st.SQLDB, d.now().Unix(), convID, max(maxTurn+1, stampedTurn), rawPrompt, res, nil, vet.TurnMetrics{
		DurationMS: durationMS,
		TimedOut:   true,
	})
}

func acquireTimeoutRecorderStore(tctx context.Context, d Deps, ref *hookTimeoutStoreRef, root string) (*Store, func()) {
	if st, reused := ref.acquire(root); reused {
		return st, ref.release
	}
	if d.OpenShortTimeoutStoreCtx == nil || d.ProjectDB == nil {
		return nil, func() {}
	}
	st := d.OpenShortTimeoutStoreCtx(tctx, d.ProjectDB(root))
	if st == nil {
		return nil, func() {}
	}
	return st, func() { _ = st.CloseErr() }
}

func dispatchHookEvent(ctx context.Context, d Deps, o Options, event, dir string, input HookInput, out io.Writer) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch event {
	case "session-start":
		return HandleSessionStart(ctx, d, o, dir, input, out)
	case "pre-invocation":
		return handlePreInvocation(ctx, d, o, dir, input, out)
	case "pre-tool-use":
		return handlePreToolUse(ctx, d, o, dir, input, out)
	case "post-tool-use":
		return handlePostToolUse(ctx, d, o, dir, input, out)
	default:
		return handleStopHook(ctx, d, o, dir, input, out)
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

// HooksDisabledGlobal reports whether a regular (non-symlinked) no-hooks marker
// exists under <home>/.config/fyr/no-hooks.
func HooksDisabledGlobal(home string) bool {
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

// HookBypassedForEvent reports whether event is bypassed by global or repo no-hooks markers.
func HookBypassedForEvent(home, dir, event string) bool {
	if isRelayProtectedEvent(event) {
		return false
	}
	if HooksDisabledGlobal(home) {
		return true
	}
	return HooksDisabled(dir)
}

// HooksDisabledForHome reports whether global or repo-local no-hooks markers disable hooks.
func HooksDisabledForHome(home, dir string) bool {
	return HooksDisabledGlobal(home) || HooksDisabled(dir)
}

func hasRegularScratchNoHooks(base string) bool {
	if trail.CheckScratchNoSymlinks(base) != nil {
		return false
	}
	fi, err := os.Lstat(filepath.Join(base, ".agents", "scratch", "no-hooks"))
	return err == nil && fi.Mode().IsRegular()
}

// HooksDisabled reports whether dir (or its git checkout root) contains a
// regular (non-symlinked) .agents/scratch/no-hooks marker, used by paired A/B eval scenarios.
func HooksDisabled(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if hasRegularScratchNoHooks(dir) {
		return true
	}
	if root, _, ok := guard.FindGitRootFast(dir); ok && hasRegularScratchNoHooks(root) {
		return true
	}
	return false
}
