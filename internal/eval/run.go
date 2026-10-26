package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
)

// Options is one invocation of eval.
type Options struct {
	Root     string // project checkout (suite, record scope, default fixture)
	Home     string // fyr's home: sandboxes, stores
	RealHome string // $HOME: the token, the fingerprint
	Fixture  string // the repository each row clones; "" is Root
	Self     string // os.Executable(): the fyr that installs and lists
	Repo     string // the install source tree (D16); "" is the harness embedded in Self
	Out      io.Writer
	Now      func() time.Time
}

// Result is one row's outcome.
type Result struct {
	Row, Verdict string // "PASS" | "FAIL"
	Reasons      []string
	AgyExit      int
	Seconds      int
	Dir          string // the sandbox
}

// The fixed reason strings of a FAIL (Interfaces § Steps, step 16).
const (
	ReasonExists      = "sandbox exists"
	ReasonClone       = "clone"
	ReasonRecords     = "records list"
	ReasonSetup       = "setup failed"
	ReasonInstall     = "install"
	ReasonSettings    = "settings"
	ReasonToken       = "token link"
	ReasonCheck       = "check failed"
	ReasonDenied      = "auto-denied"
	ReasonInvalid     = "invalid allow entry"
	ReasonProcs       = "processes left"
	ReasonRealConfig  = "real config changed"
	ReasonCredentials = "credentials left in the sandbox"
	ReasonInterrupted = "interrupted"
	ReasonReplaced    = "sandbox replaced"
)

// ReasonAgent is the FAIL reason of an agent row whose agent the
// conversation store does not show invoked.
func ReasonAgent(agent string) string {
	return "no invoke_subagent of " + agent + " in the conversation store"
}

// Bounds of one CLI session, variables so a test can shorten them: agy's own
// --print-timeout is 20m, and 25m is the kit's `timeout --foreground` around it.
var (
	agyTimeout = 25 * time.Minute
	stopGrace  = 5 * time.Second
)

const tokenRel = ".gemini/antigravity-cli/antigravity-oauth-token"

// namedGrants is the allow list of a named row (run.sh:212).
var namedGrants = func() []string {
	var out []string
	for _, c := range strings.Fields("git go gofmt cat ls date mkdir pwd grep find wc head tail printf echo cp ln test diff fyr chmod readlink bash python3 sleep govulncheck") {
		out = append(out, "command("+c+")")
	}
	return append(out, "read_file(*)", "write_file(*)")
}()

// wrapperScript is SB/bin/fyr, given SB and the installed binary. fyr's
// flags are last-wins, so a --home of the agent's own would override the one
// pinned here: the wrapper refuses it rather than pass it on.
const wrapperScript = `#!/usr/bin/env bash
for a in "$@"; do
  case "$a" in --home|--home=*|-home|-home=*) echo "fyr: this sandbox pins --home to "%[1]s >&2; exit 2 ;; esac
done
exec %[2]s "$1" --home %[1]s "${@:2}"
`

// SandboxRoot is where eval makes its sandboxes under fyr's home (D8).
func SandboxRoot(home string) string { return filepath.Join(home, harness.StateDir, "eval") }

// Preflight is the part of step 0 that comes before the suite is read
// (D20): root is a checkout, and the sandbox root passes guards a and b on
// its nearest existing ancestor. cmd calls it first; Run calls it again.
func Preflight(o Options) error {
	if err := outsideCheckout(o.Root); err == nil {
		return fmt.Errorf("%s is not a git checkout", o.Root)
	}
	sr := SandboxRoot(o.Home)
	if err := checkRoots(o.RealHome, o.Repo, sr); err != nil {
		return fmt.Errorf("the sandbox root: %v", err)
	}
	if err := outsideCheckout(nearestExisting(sr)); err != nil {
		return fmt.Errorf("the sandbox root %s is inside a git checkout (BUG-106); use a --home outside every checkout", sr)
	}
	return nil
}

// Run refuses (error, no row run) or runs every row and returns its results.
// On ctx's end the current row FAILs interrupted, the rows after it are not
// run, and the rows started are returned with a nil error (D22).
func Run(ctx context.Context, o Options, rows []Row) ([]Result, error) {
	if o.Fixture == "" {
		o.Fixture = o.Root
	}
	if abs, err := filepath.Abs(o.Fixture); err == nil {
		o.Fixture = abs
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := Preflight(o); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("no row to run")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.ID] {
			return nil, fmt.Errorf("row %s is named twice", r.ID)
		}
		seen[r.ID] = true
	}
	tok := filepath.Join(o.RealHome, tokenRel)
	if st, err := os.Lstat(tok); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("no Antigravity login at %s", tok)
	}
	agy, err := exec.LookPath("agy")
	if err != nil {
		return nil, errors.New("agy not found on PATH")
	}
	stamp := o.Now().Format("2006-01-02-150405")
	var results []Result
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		res := runRow(ctx, o, r, agy, tok, filepath.Join(SandboxRoot(o.Home), stamp, r.ID))
		fmt.Fprintln(o.Out, resultLine(res))
		results = append(results, res)
	}
	return results, nil
}

func resultLine(r Result) string {
	v := r.Verdict
	if len(r.Reasons) > 0 {
		v += " (" + strings.Join(r.Reasons, "; ") + ")"
	}
	return printable(fmt.Sprintf("eval: %s  %s  agy exit %d  %ds  %s", r.Row, v, r.AgyExit, r.Seconds, r.Dir))
}

// row is one sandbox's state as the steps fill it.
type row struct {
	o            Options
	r            Row
	dir, app, sb string
	out          io.Writer
	reasons      []string
	pgids        []int
	realBefore   map[string]string
	credsBefore  map[string]credFinding
	linked       bool
	stamp        string // {{STAMP}}: the row's start, %F-%H%M
	// resolved holds dir, app and sb as guard a approved them. The agent can
	// write in dir, so every later step that follows those paths first checks
	// they still resolve here (intact), and check e matches these strings.
	resolved map[string]string
}

func (w *row) fail(reason string) { w.reasons = append(w.reasons, reason) }

func (w *row) note(format string, a ...any) {
	fmt.Fprintln(w.out, printable(fmt.Sprintf("eval: "+w.r.ID+": "+format, a...)))
}

// runRow is steps 1-16 for one row. A failure inside steps 1-8 skips 9-14
// and still runs 15 once the sandbox is eval's own.
func runRow(ctx context.Context, o Options, r Row, agy, tok, dir string) Result {
	t0 := time.Now()
	w := &row{o: o, r: r, dir: dir, app: filepath.Join(dir, "app"), sb: filepath.Join(dir, "home"), out: o.Out, stamp: o.Now().Format("2006-01-02-1504")}
	res := Result{Row: r.ID, AgyExit: -1, Dir: dir}
	finish := func() Result {
		res.Seconds = int(time.Since(t0).Seconds())
		res.Reasons = w.reasons
		res.Verdict = "PASS"
		if len(w.reasons) > 0 {
			res.Verdict = "FAIL"
		}
		return res
	}
	// Step 1: a directory of this invocation, never one that already exists.
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		w.note("%v", err)
		w.fail(ReasonSetup)
		return finish()
	}
	if err := os.Mkdir(dir, 0o700); os.IsExist(err) {
		w.fail(ReasonExists)
		return finish()
	} else if err != nil {
		w.note("%v", err)
		w.fail(ReasonSetup)
		return finish()
	}
	if w.prepare(ctx) {
		res.AgyExit = w.session(ctx, agy, tok)
		w.afterSession(ctx, tok)
	}
	w.exitChecks()
	if ctx.Err() != nil && !w.has(ReasonInterrupted) {
		w.fail(ReasonInterrupted)
	}
	return finish()
}

func (w *row) has(reason string) bool {
	for _, r := range w.reasons {
		if r == reason {
			return true
		}
	}
	return false
}

// prepare is steps 2-8b; false means the row has failed and no CLI runs.
func (w *row) prepare(ctx context.Context) bool {
	// Step 2: the clone, its own toplevel, outside every checkout.
	clone := exec.CommandContext(ctx, "git", "clone", "-q", "--no-hardlinks", "--", w.o.Fixture, w.app)
	clone.Env = withoutGit(os.Environ())
	if out, err := clone.CombinedOutput(); err != nil {
		w.note("git clone: %v: %s", err, strings.TrimSpace(string(out)))
		w.fail(ReasonClone)
		return false
	}
	top := exec.Command("git", "-C", w.app, "rev-parse", "--show-toplevel")
	top.Env = withoutGit(os.Environ())
	gotTop, err := top.Output()
	wantTop, rerr := filepath.EvalSymlinks(w.app)
	if err != nil || rerr != nil || strings.TrimSpace(string(gotTop)) != wantTop {
		w.note("%s is not its own repository", w.app)
		w.fail(ReasonClone)
		return false
	}
	if err := checkRoots(w.o.RealHome, w.o.Repo, w.app, w.sb); err != nil {
		w.note("%v", err)
		w.fail(ReasonClone)
		return false
	}
	if err := outsideCheckout(w.dir); err != nil {
		w.note("%v (BUG-106)", err)
		w.fail(ReasonClone)
		return false
	}
	if err := os.MkdirAll(filepath.Join(w.sb, ".gemini", "antigravity-cli"), 0o700); err != nil {
		w.fail(ReasonSetup)
		return false
	}
	w.resolved = map[string]string{}
	for _, p := range []string{w.dir, w.app, w.sb} {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			w.fail(ReasonSetup)
			return false
		}
		w.resolved[p] = r
	}
	// Step 4: what the trail held before the agent ran; a check counts only
	// the records the run adds (run.sh:52-62).
	before := filepath.Join(w.sb, "records-before.txt")
	var list []byte
	if _, err := os.Stat(filepath.Join(w.app, ".agents", "trail.jsonl")); err == nil {
		cmd := exec.CommandContext(ctx, w.o.Self, "record", "list", "--home", w.sb)
		cmd.Dir = w.app
		cmd.Env = w.fyrEnv()
		var errb strings.Builder
		cmd.Stderr = &errb
		if list, err = cmd.Output(); err != nil {
			w.note("record list: %v: %s", err, strings.TrimSpace(errb.String()))
			w.fail(ReasonRecords)
			return false
		}
	}
	if err := os.WriteFile(before, list, 0o600); err != nil {
		w.fail(ReasonRecords)
		return false
	}
	// Step 5: the row's setup, in the clone.
	if w.r.Setup != "" {
		if !w.bash(ctx, w.r.Setup, w.setupEnv(), "setup.log") {
			w.fail(ReasonSetup)
			return false
		}
	}
	// Step 6: the real config, before anything installs.
	w.realBefore = fingerprint(w.o.RealHome)
	// Step 7: the harness, as `fyr install` gives it to a place (D16), and
	// the wrapper that pins --home on every fyr call the agent makes.
	args := []string{"install", "--home", w.sb}
	if w.o.Repo != "" {
		args = append(args, "--repo", w.o.Repo)
	}
	inst := exec.CommandContext(ctx, w.o.Self, args...)
	inst.Dir = w.sb
	inst.Env = w.fyrEnv()
	log, err := os.Create(filepath.Join(w.dir, "install.log"))
	if err != nil {
		w.fail(ReasonInstall)
		return false
	}
	inst.Stdout, inst.Stderr = log, log
	err = inst.Run()
	log.Close()
	if err != nil {
		w.note("fyr install: %v (see install.log)", err)
		w.fail(ReasonInstall)
		return false
	}
	bin := filepath.Join(w.sb, "bin")
	wrapper := fmt.Sprintf(wrapperScript, shellQuote(w.sb), shellQuote(filepath.Join(w.sb, filepath.FromSlash(harness.BinPath))))
	if err := os.MkdirAll(bin, 0o700); err != nil || os.WriteFile(filepath.Join(bin, "fyr"), []byte(wrapper), 0o755) != nil {
		w.fail(ReasonInstall)
		return false
	}
	// Step 8: the grants, and trust in the clone only.
	allow := namedGrants
	if reason, wide := w.r.Wide(); wide {
		w.note("runs with command(*) in its throwaway clone: %s", reason)
		allow = []string{"command(*)", "read_file(*)", "write_file(*)"}
	}
	settings, _ := json.MarshalIndent(map[string]any{"permissions": map[string]any{"allow": allow}, "trustedWorkspaces": []string{w.app}}, "", " ")
	sp := filepath.Join(w.sb, ".gemini", "antigravity-cli", "settings.json")
	if err := os.WriteFile(sp, settings, 0o600); err != nil {
		w.fail(ReasonSettings)
		return false
	}
	if b, err := os.ReadFile(sp); err != nil || strings.Contains(string(b), "toolPermission") {
		w.fail(ReasonSettings)
		return false
	}
	// Step 8b: the credential baseline, before the token is linked (D17),
	// over the whole sandbox directory: the clone, the CLI's output and the
	// logs are kept as long as the home is.
	w.credsBefore = scanCreds(w.dir)
	return ctx.Err() == nil
}

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
	if w.r.Prompt2 != "" && rc == 0 && ctx.Err() == nil {
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
	out, err := openRegular(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		w.note("%v", err)
		return -1
	}
	defer out.Close()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendErr {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	errf, err := openRegular(errPath, flags)
	if err != nil {
		w.note("%v", err)
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

// afterSession is steps 11-14. A sandbox the session replaced is not
// followed: no removal through it, no check run in it.
func (w *row) afterSession(ctx context.Context, tok string) {
	if !w.intact() {
		return
	}
	w.unlink(tok)
	// Step 12: a denial or a grant the CLI did not take fails the row.
	if n := countLines(filepath.Join(w.dir, "err.txt"), "auto-denied"); n > 0 {
		w.note("auto-denied %d", n)
		w.fail(ReasonDenied)
	}
	if log := newestLog(filepath.Join(w.sb, ".gemini", "antigravity-cli", "log")); log != "" {
		if n := countLines(log, "invalid allow entry"); n > 0 {
			w.note("invalid allow entries %d", n)
			w.fail(ReasonInvalid)
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Step 13: the row's check.
	if !w.bash(ctx, w.r.Check, w.checkEnv(), "check.log") {
		w.fail(ReasonCheck)
	}
	// Step 14: the agent the row names was invoked.
	if w.r.Agent != "" && !invoked(w.sb, w.r.Agent) {
		w.fail(ReasonAgent(w.r.Agent))
	}
}

// unlink is step 11: warn when the token link was replaced or the real token
// changed kind, and remove the link and settings.json by name.
func (w *row) unlink(tok string) {
	if root, err := w.sandboxRoot(); err == nil && w.linked {
		if st, err := root.Lstat(filepath.FromSlash(tokenRel)); err != nil || st.Mode()&os.ModeSymlink == 0 {
			w.note("WARNING the token link was replaced; the real login may be stale")
		}
		root.Close()
	}
	w.removeInSandbox(tokenRel)
	w.removeInSandbox(".gemini/antigravity-cli/settings.json")
	if st, err := os.Lstat(tok); err != nil || !st.Mode().IsRegular() {
		w.note("WARNING %s is no longer a regular file", tok)
	}
}

// exitChecks is step 15: leftover processes, then the real config, then new
// credentials, and last a SIGKILL to every agy group as a sweep.
func (w *row) exitChecks() {
	intact := w.resolved == nil || w.intact()
	if w.linked && intact {
		w.removeInSandbox(tokenRel) // gone even when a step after 9 returned early
	}
	if _, err := os.Stat(w.app); err == nil {
		// The approved strings, never a fresh resolution: a clone the agent
		// turned into a link to the real home must not aim the SIGTERM there.
		roots := []string{}
		for _, p := range []string{w.app, w.sb} {
			if r, ok := w.resolved[p]; ok {
				roots = append(roots, r)
			} else if r, err := filepath.EvalSymlinks(p); err == nil {
				roots = append(roots, r) // before step 2b: no agent has run
			}
		}
		if found := leftoverProcs(w.out, w.sb, roots...); len(found) > 0 {
			w.note("processes left by the run (BUG-163); each was sent SIGTERM: %s", strings.Join(found, ", "))
			w.fail(ReasonProcs)
		}
	}
	if w.realBefore != nil {
		if changed := changedPaths(w.realBefore, fingerprint(w.o.RealHome)); len(changed) > 0 {
			w.note("the real home's watched config changed (BUG-107, BUG-123): %s", strings.Join(changed, ", "))
			w.fail(ReasonRealConfig)
		}
	}
	if w.credsBefore != nil && intact {
		if left := newCreds(w.credsBefore, scanCreds(w.dir)); len(left) > 0 {
			w.note("credentials left in the sandbox (BUG-160): %s", strings.Join(left, ", "))
			w.fail(ReasonCredentials)
		}
	}
	for _, g := range w.pgids {
		syscall.Kill(-g, syscall.SIGKILL)
	}
}

// intact reports whether the sandbox directory, the clone and the sandbox
// home still resolve to what guard a approved, and fails the row once when
// they do not: the agent can replace any of them with a link to the real
// home, and eval removes, scans and ingests through them.
func (w *row) intact() bool {
	for p, want := range w.resolved {
		st, err := os.Lstat(p)
		got, rerr := filepath.EvalSymlinks(p)
		if err != nil || rerr != nil || !st.IsDir() || got != want {
			if !w.has(ReasonReplaced) {
				w.note("%s no longer resolves to %s; nothing is removed, run or scanned through it", p, want)
				w.fail(ReasonReplaced)
			}
			return false
		}
	}
	return true
}

// sandboxRoot opens the sandbox home as an os.Root, which refuses a path
// that a link leads out of the home (the real token sits at the other end of
// one).
func (w *row) sandboxRoot() (*os.Root, error) { return os.OpenRoot(w.sb) }

// removeInSandbox removes rel inside the sandbox home, never through a link
// that leads out of it; one that does fails the row sandbox replaced.
func (w *row) removeInSandbox(rel string) {
	root, err := w.sandboxRoot()
	if err == nil {
		err = root.Remove(filepath.FromSlash(rel))
		root.Close()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		w.note("not removed: %s: %v", rel, err)
		if !w.has(ReasonReplaced) {
			w.fail(ReasonReplaced)
		}
	}
}

// openRegular opens path with flag, refusing a link (O_NOFOLLOW) and
// anything that is not a regular file, without blocking on a FIFO the agent
// left at a name eval reads or writes.
func openRegular(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
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

// checkEnv is run.sh's check environment (:237): env -i, eval's own PATH
// (the wrapper directory is never on it), HOME=SB, and the row's paths.
func (w *row) checkEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + w.sb,
		"SB=" + w.sb,
		"OUT=" + filepath.Join(w.dir, "out.txt"),
		"OUT2=" + filepath.Join(w.dir, "out2.txt"),
		"FIXDIR=" + w.o.Fixture,
		"CLONE=" + w.app,
	}
}

// setupEnv is the check's, plus the git identity run.sh's gi() used.
func (w *row) setupEnv() []string {
	return append(w.checkEnv(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@localhost",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@localhost")
}

// fyrEnv is eval's own environment for the sandbox's fyr calls, minus what
// would choose another harness or repository (D16).
func (w *row) fyrEnv() []string {
	var out []string
	for _, kv := range withoutGit(os.Environ()) {
		if k, _, _ := strings.Cut(kv, "="); k != "FYR_REPO" && k != "FYR_CHILD" {
			out = append(out, kv)
		}
	}
	return out
}

// agyEnv is exactly the allowlist of run.sh:220-222 (D27).
func (w *row) agyEnv() []string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return []string{
		"PATH=" + filepath.Join(w.sb, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TERM=" + envOr("TERM", "dumb"),
		"LANG=" + envOr("LANG", "C.UTF-8"),
		"USER=" + envOr("USER", name),
		"LOGNAME=" + envOr("LOGNAME", name),
		"SHELL=/bin/bash",
		// Read by this box's credential broker, outside this repository: off,
		// so eval behaves here as the kit did (D27).
		"STYRMAN_SKIP_CRED_PULL=1",
		"HOME=" + w.sb,
		"GIT_AUTHOR_NAME=agy-rel", "GIT_AUTHOR_EMAIL=agy-rel@localhost",
		"GIT_COMMITTER_NAME=agy-rel", "GIT_COMMITTER_EMAIL=agy-rel@localhost",
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// countLines is grep -c: the lines of path holding s.
func countLines(path, s string) int {
	f, err := openRegular(path, os.O_RDONLY)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), s) {
			n++
		}
	}
	return n
}

// newestLog is the newest cli-*.log of dir by mtime, or "".
func newestLog(dir string) string {
	logs, _ := filepath.Glob(filepath.Join(dir, "cli-*.log"))
	sort.Slice(logs, func(i, j int) bool {
		a, _ := os.Stat(logs[i])
		b, _ := os.Stat(logs[j])
		return a != nil && b != nil && a.ModTime().After(b.ModTime())
	})
	if len(logs) == 0 {
		return ""
	}
	return logs[0]
}

// shellQuote is s as one single-quoted bash word.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// printable is search.Printable's filter, which eval cannot import (D10):
// control and format characters, the line separators and the ignorables
// become '?', so a row id or a path never forges a line. A test pins the two
// equal.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) ||
			unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return '?'
		}
		return r
	}, s)
}
