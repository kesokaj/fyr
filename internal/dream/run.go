package dream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

const (
	cliTimeout = 10 * time.Minute
	killGrace  = 30 * time.Second
	keepFailed = 30 * 24 * time.Hour
)

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// response is the one JSON object the CLI is asked for.
type response struct {
	Proposals []proposal `json:"proposals"`
	Skills    []draft    `json:"skills"`
}

type proposal struct {
	Title, Kind, Target, File, Text, Evidence string
}

type draft struct {
	Name, Description, Body string
}

// realRun takes the lock, then reads the state and the projects, dreams over
// every changed project and writes what the CLI proposes. Every failure before
// the first project is a run log line, last_run.error where the state can be
// written, and exit 2 (the plan's Interfaces, code review 2026-09-30).
func (o Options) realRun() error {
	conf, err := harness.ConfigDir(o.Home)
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	unlock, held, err := lock(filepath.Join(conf, "dream.lock"))
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	if held {
		fmt.Fprintln(o.Out, "fyr dream: already running")
		return nil
	}
	defer unlock()

	t0 := o.Now()
	logf := logger(filepath.Join(conf, "dream.log"), o.Now)
	st, err := readState(o.Home)
	if err != nil {
		// The state is not rewritten: that would erase the watermarks.
		logf("- run 0 projects, 0 proposals, error " + err.Error())
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	finish := func(n, proposals int, runErr string) error {
		st.LastRun = lastRun{At: t0, Projects: n, Proposals: proposals, Error: runErr}
		line := fmt.Sprintf("- run %d projects, %d proposals", n, proposals)
		if runErr != "" {
			line += ", error " + runErr
		}
		logf(line)
		return saveState(o.Home, st)
	}
	fail := func(msg string) error {
		finish(0, 0, msg)
		return &ExitError{2, "fyr dream: " + msg}
	}

	removeCopies(conf, o.Now())
	prune(filepath.Join(conf, "dream-failed"), t0)
	o.ingestFirst(logf)
	projects, err := o.targets()
	if err != nil {
		return fail(err.Error())
	}
	argv := command()
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fail(fmt.Sprintf("no %s on PATH: the dream job runs the Antigravity CLI", argv[0]))
	}
	agyHomeDir, cleanup, err := agyHome(conf, runtime.GOOS)
	if err != nil {
		return fail(err.Error())
	}
	defer cleanup()
	env := withHome(os.Environ(), agyHomeDir)
	work := filepath.Join(conf, "dream-work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return fail(err.Error())
	}

	failed, total := 0, 0
	var stateErr error
	for _, p := range projects {
		if why := skipReason(p); why != "" {
			logf(p.Root + " skip " + why)
			continue
		}
		c, err := collect(o.Home, p, o.since(st, p.Root), maxPrompt)
		if err != nil {
			failed++
			logf(p.Root + " fail " + scrub(err.Error(), o.Home))
			continue
		}
		if !c.Changed {
			logf(p.Root + " unchanged")
			continue
		}
		stdout, stderr, err := call(argv, c.Prompt, work, env)
		if err != nil {
			failed++
			o.keepFailure(conf, c, t0, stdout, stderr)
			reason := err.Error()
			if hint := agyAuthHint(stdout, stderr); hint != "" {
				reason += ": " + hint
			}
			logf(p.Root + " fail " + reason)
			continue
		}
		text, aerr := answer(stdout)
		resp, ok := parse(text)
		if aerr != nil || !ok {
			failed++
			o.keepFailure(conf, c, t0, stdout, stderr)
			reason := "invalid output"
			if aerr != nil {
				reason = aerr.Error()
			}
			if hint := agyAuthHint(stdout, stderr); hint != "" {
				reason += ": " + hint
			}
			logf(p.Root + " fail " + reason)
			continue
		}
		if err := o.write(conf, c, resp, argv[0], t0); err != nil {
			failed++
			logf(p.Root + " fail " + err.Error())
			continue
		}
		st.Projects[p.Root] = t0
		if err := saveState(o.Home, st); err != nil && stateErr == nil {
			stateErr = err
		}
		total += len(resp.Proposals)
		logf(fmt.Sprintf("%s ok %d proposals", p.Root, len(resp.Proposals)))
	}
	runErr := ""
	if failed > 0 {
		runErr = fmt.Sprintf("%d project(s) failed", failed) // so `status` never reads a run with failures as clean
	}
	if err := finish(len(projects), total, runErr); err != nil && stateErr == nil {
		stateErr = err
	}
	if stateErr != nil {
		return &ExitError{2, "fyr dream: the watermarks could not be saved, so the next run re-sends this one: " + stateErr.Error()}
	}
	if failed > 0 {
		return &ExitError{1, fmt.Sprintf("fyr dream: %d project(s) failed; see %s", failed, filepath.Join(conf, "dream.log"))}
	}
	return nil
}

// call runs the CLI in work with the prompt on stdin as one stream-json
// message. env replaces the child's environment when set (agy's private
// HOME); otherwise the child keeps fyr's own.
func call(argv []string, prompt, work string, env []string) (string, string, error) {
	return callWithin(argv, prompt, work, env, cliTimeout, killGrace)
}

// callWithin is call with its two bounds explicit: timeout for the CLI, and
// grace for how long Run may wait, after the kill, on a grandchild that kept
// the output pipes.
func callWithin(argv []string, prompt, work string, env []string, timeout, grace time.Duration) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	msg, err := json.Marshal(map[string]any{"event": "user", "message": map[string]string{"content": prompt}})
	if err != nil {
		return "", "", err
	}
	stdin := string(msg) + "\n"
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = work
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// The CLI gets its own process group, killed whole at the deadline, on an
	// interrupt, and once the CLI itself is done; Wait gives up on the output
	// pipes after grace. A helper the CLI started (a language or MCP server)
	// kept them open past the kill and held Run, and so dream.lock, for as
	// long as it lived (BUG-169); the same helper after a clean exit turned
	// the run into a failure, and Ctrl-C stopped reaching a CLI outside
	// fyr's own group (the fix's review).
	cmd.WaitDelay = grace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	killGroup := func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Cancel = func() error { killGroup(); return nil }
	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("%s: %v", argv[0], err)
	}
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
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, watched...)
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
	err = cmd.Wait()
	signal.Stop(sigs) // before waited closes: a signal in between still reaches the goroutine
	close(waited)
	<-relayed // a signal taken at the end is acted on before the next project starts
	killGroup()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), errb.String(), fmt.Errorf("%s timed out after %s", argv[0], timeout)
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The CLI exited 0; only a helper it left behind kept the pipes.
		err = nil
	}
	if err != nil {
		return out.String(), errb.String(), fmt.Errorf("%s: %v", argv[0], err)
	}
	return out.String(), errb.String(), nil
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

// answer is the model's text: Antigravity's stream-json ends with a result
// event that carries it as "response". A result that names denied actions is
// no answer.
func answer(stdout string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Event      string `json:"event"`
			StepUpdate struct {
				StepType string `json:"step_type"`
			} `json:"step_update"`
			Result struct {
				Status        string            `json:"status"`
				Response      string            `json:"response"`
				DeniedActions []json.RawMessage `json:"denied_actions"`
			} `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		// A tool step that ran, denied or not, means the answer was not
		// tool-free: no answer (security review of the release range).
		if ev.Event == "step_update" && ev.StepUpdate.StepType == "tool" {
			return "", nil
		}
		if ev.Event != "result" {
			continue
		}
		if ev.Result.Status != "SUCCESS" || len(ev.Result.DeniedActions) > 0 {
			return "", nil
		}
		return ev.Result.Response, nil
	}
	if err := sc.Err(); err != nil {
		// A line over the buffer stopped the scan silently and the failure
		// read "invalid output" (audit 2026-09-30, SD11).
		return "", fmt.Errorf("output line too long or unreadable: %w", err)
	}
	return "", nil
}

// agyHome is a private HOME for one run's Antigravity child: a copy of the
// operator's login and a settings file that grants nothing, so a tool call is
// refused whatever the operator allowed for interactive use (security review
// 2026-09-30, D28). Its conversations stay in it and go with it. The login
// is read from $HOME, the real one, never from --home. On macOS agy keeps the
// login in the Keychain and finds it only through $HOME/Library/Keychains, so
// that folder is linked in and the token file is optional (BUG-166).
func agyHome(conf, goos string) (string, func(), error) {
	real := os.Getenv("HOME")
	if !filepath.IsAbs(real) {
		return "", nil, fmt.Errorf("HOME is %q, not an absolute path: the dream job reads the Antigravity login from it", real)
	}
	src := filepath.Join(real, ".gemini", "antigravity-cli")
	token := filepath.Join(src, "antigravity-oauth-token")
	_, tokenErr := os.Stat(token)
	keychains := ""
	if goos == "darwin" {
		if info, err := os.Stat(filepath.Join(real, "Library", "Keychains")); err == nil && info.IsDir() {
			keychains = filepath.Join(real, "Library", "Keychains")
		}
	}
	if tokenErr != nil && keychains == "" {
		if goos == "darwin" {
			return "", nil, fmt.Errorf("no Antigravity login: neither %s nor a Keychain folder at %s: run agy once and log in", token, filepath.Join(real, "Library", "Keychains"))
		}
		return "", nil, fmt.Errorf("no Antigravity login at %s: run agy once and log in", token)
	}
	home, err := os.MkdirTemp(conf, "dream-agy-home-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(home) }
	dst := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	for _, name := range []string{"antigravity-oauth-token", "installation_id"} {
		optional := name == "installation_id" || keychains != ""
		if err := copyPrivate(filepath.Join(src, name), filepath.Join(dst, name)); err != nil && !(optional && errors.Is(err, os.ErrNotExist)) {
			cleanup()
			return "", nil, err
		}
	}
	if keychains != "" {
		// A link, never a copy: the Keychain stays where it is, and
		// os.RemoveAll removes the link without following it.
		if err := os.MkdirAll(filepath.Join(home, "Library"), 0o700); err != nil {
			cleanup()
			return "", nil, err
		}
		if err := os.Symlink(keychains, filepath.Join(home, "Library", "Keychains")); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	// Nothing allowed, and tool calls set to ask, which a headless run cannot:
	// explicit, never agy's default (security review round 2).
	settings := []byte(`{"toolPermission":"request-review","permissions":{"allow":[]},"trustedWorkspaces":[]}` + "\n")
	if err := os.WriteFile(filepath.Join(dst, "settings.json"), settings, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return home, cleanup, nil
}

func copyPrivate(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", from)
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// agyAuthHint names the one failure "invalid output" hides: agy found no
// login in the private HOME and asked for a browser login nobody can give.
// Only agy's own lines count: a stream-json event (a "{" line) carries model
// text, which may quote the phrase from a transcript.
func agyAuthHint(stdout, stderr string) string {
	found := false
	for _, line := range strings.Split(stdout+"\n"+stderr, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") && strings.Contains(line, "Authentication required") {
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	return "agy is not logged in inside the dream job's private HOME (run agy once and log in; on macOS the login Keychain must be unlocked, so the Mac's user must be logged in)"
}

// withHome is env with HOME replaced.
func withHome(env []string, home string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "HOME=") {
			out = append(out, e)
		}
	}
	return append(out, "HOME="+home)
}

// removeCopies removes what a killed run left in conf: a private agy HOME
// with its login copy, and the unredacted database copies of the readers
// (security review 2026-09-30). A dry run takes no lock and makes copies
// too, so only copies older than an hour, far past any live run's, are
// removed (code review round 2).
func removeCopies(conf string, now time.Time) {
	entries, err := os.ReadDir(conf)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		// metrics-agy-* (Antigravity conversation copies) and dream-metrics-*
		// (a copy of the metrics store) hold the same kind of content.
		if !e.IsDir() || !(strings.HasPrefix(n, "dream-agy-") || strings.HasPrefix(n, "dream-projects-") ||
			strings.HasPrefix(n, "metrics-agy-") || strings.HasPrefix(n, "dream-metrics-")) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(conf, n))
		}
	}
}

// parse takes the first {...} in out that decodes as the response.
func parse(out string) (response, bool) {
	for i := strings.IndexByte(out, '{'); i >= 0; {
		var r response
		// Unknown keys are ignored: one extra key from a chatty model made
		// the answer invalid and the project retried nightly with the same
		// prompt (audit 2026-09-30, SD5). The fields used are validated below.
		dec := json.NewDecoder(strings.NewReader(out[i:]))
		if dec.Decode(&r) == nil && (r.Proposals != nil || r.Skills != nil) {
			return r, true
		}
		next := strings.IndexByte(out[i+1:], '{')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return response{}, false
}

// write renders the proposals: project targets to the project's file,
// harness targets to proposals/dream/harness/ (D23), every valid draft skill
// to its own directory; the model's text is scrubbed again first.
func (o Options) write(conf string, c Collected, r response, cli string, t0 time.Time) error {
	day := t0.Format("2006-01-02")
	dir := filepath.Join(conf, "proposals", "dream")
	var projectP, harnessP []proposal
	for _, p := range r.Proposals {
		if p.Target == "harness" {
			harnessP = append(harnessP, p)
		} else {
			projectP = append(projectP, p)
		}
	}
	var drafts []draft
	var dropped []string
	for _, d := range r.Skills {
		if skillName.MatchString(d.Name) {
			drafts = append(drafts, d)
		} else {
			dropped = append(dropped, d.Name)
		}
	}
	for _, d := range drafts {
		path, err := unique(filepath.Join(dir, "drafts"), day+"-"+c.Name+"-"+d.Name, "")
		if err != nil {
			return err
		}
		desc, _ := json.Marshal(o.clean(d.Description))
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", d.Name, desc, strings.TrimSpace(o.clean(d.Body)))
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := harness.WriteAtomic(filepath.Join(path, "SKILL.md"), []byte(body), 0o600); err != nil {
			return err
		}
	}
	if len(projectP) > 0 || len(drafts) > 0 || len(dropped) > 0 {
		var extra []string
		for _, d := range drafts {
			extra = append(extra, "Draft skill: drafts/"+day+"-"+c.Name+"-"+d.Name+"/SKILL.md")
		}
		for _, n := range dropped {
			extra = append(extra, fmt.Sprintf("Draft skill %q dropped: not a kebab-case name", n))
		}
		if err := o.render(dir, day, c, cli, projectP, extra); err != nil {
			return err
		}
	}
	if len(harnessP) > 0 {
		if err := o.render(filepath.Join(dir, "harness"), day, c, cli, harnessP, nil); err != nil {
			return err
		}
	}
	return nil
}

func (o Options) render(dir, day string, c Collected, cli string, ps []proposal, extra []string) error {
	path, err := unique(dir, day+"-"+c.Name, ".md")
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, in := range c.Inputs {
		counts[in.Kind]++
	}
	var inputs []string
	for _, k := range promptOrder {
		inputs = append(inputs, fmt.Sprintf("%s %d", k, counts[k]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nowner: fyr dream\ndate: %s\nproject: %s\ncli: %s\ninputs: %s\n", day, o.line(c.Root), cli, strings.Join(inputs, ", "))
	if len(c.Dropped) > 0 {
		fmt.Fprintf(&b, "left out: %q\n", o.line(strings.Join(c.Dropped, "; ")))
	}
	fmt.Fprintf(&b, "---\n# Dream proposals — %s   (%s)\n", c.Name, day)
	for i, p := range ps {
		target := "project"
		if p.Target == "harness" {
			target = "harness"
		}
		// The text is indented four spaces, line by line: /retro reads the
		// file by its `### N.` and `**Status:**` lines, and an unfenced
		// text could open a queued section the model never proposed
		// (audit 2026-09-30, SD3).
		fmt.Fprintf(&b, "\n### %d. %s\n**Status:** queued\n**Target:** %s   **Kind:** %s   **File:** %s\n\n%s\n\nEvidence: %s\n",
			i+1, o.line(p.Title), target, o.line(p.Kind), o.line(p.File), indent(strings.TrimSpace(o.clean(p.Text))), o.line(p.Evidence))
	}
	for _, e := range extra {
		b.WriteString("\n" + e + "\n")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return harness.WriteAtomic(path, []byte(b.String()), 0o600)
}

func (o Options) clean(s string) string { return scrub(s, o.Home) }

// indent prefixes every line of s with four spaces.
func indent(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// line is a one-line field: scrubbed, and its line breaks folded, so a model's
// title can never open a second **Status:** line or a fake section in the
// Status-driven queue (security review 2026-09-30).
func (o Options) line(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(o.clean(s))), " ")
}

// unique is dir/base+ext, or with -2, -3 … when that exists.
func unique(dir, base, ext string) (string, error) {
	for n := 1; n < 1000; n++ {
		name := base
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		p := filepath.Join(dir, name+ext)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
	}
	return "", fmt.Errorf("no free name for %s in %s", base, dir)
}

// keepFailure keeps the CLI's output of a failed project, scrubbed, for the
// operator: never in the CLI's working directory (D27).
func (o Options) keepFailure(conf string, c Collected, t0 time.Time, stdout, stderr string) {
	dir := filepath.Join(conf, "dream-failed")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	path, err := unique(dir, t0.Format("2006-01-02")+"-"+c.Name, ".txt")
	if err != nil {
		return
	}
	body := "stdout:\n" + o.clean(stdout) + "\nstderr:\n" + o.clean(stderr) + "\n"
	harness.WriteAtomic(path, []byte(body), 0o600)
}

// prune removes kept failures older than keepFailed.
func prune(dir string, t0 time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && t0.Sub(info.ModTime()) > keepFailed {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// logger appends one timestamped line per call.
func logger(path string, now func() time.Time) func(string) {
	return func(line string) {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		fmt.Fprintf(f, "%s %s\n", now().UTC().Format(time.RFC3339), line)
		f.Close()
	}
}

// writeState writes the state file; a test replaces it with a failing one.
var writeState = func(path string, data []byte) error { return harness.WriteAtomic(path, data, 0o600) }

func saveState(home string, st state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeState(statePath(home), append(data, '\n'))
}

// lock takes an exclusive flock on path without waiting: held is true when
// another run has it. The kernel releases it when that run ends, however it
// ends, so no stale lock can stop the nightly job (code review 2026-09-30).
func lock(path string) (unlock func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	f.Truncate(0)
	fmt.Fprint(f, os.Getpid())
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, false, nil
}
