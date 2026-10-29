// Package dream is `fyr dream`: the background job that reads every
// registered project's memory, trail, feedback, a numbers-only summary of its
// metrics and the Antigravity transcripts (after ingesting every session's shape
// into the metrics store), asks
// a CLI with no tools what should become a rule, a skill or a memory, and
// stores the answer as global `proposal` records (R3's exception, R10, plan
// 2026-09-30-parity-memory-dream; brain plan Phase 4: only projects idle for
// a day, read from their records).
package dream

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/memory"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// maxPrompt caps what one project sends to the CLI (the plan's Interfaces).
const maxPrompt = 256 * 1024

// Options is one run.
type Options struct {
	Home     string // fyr's home (--home); the child CLI keeps the real environment
	All      bool
	Project  string // --project <dir>
	DryRun   bool
	Out      io.Writer
	Now      func() time.Time
	Projects func(home string) ([]search.Project, error) // search.ListProjects; a test replaces it
	Scopes   func(home string) ([]string, error)         // brain.Scopes: projects whose trail is records; a test replaces it
	// Active says why a project is in use and is not dreamt over yet, ""
	// when it has been idle for idleFor (D22); nil is the real check
	// (activity), a test replaces it.
	Active func(home string, p search.Project, now time.Time) string
	// Session reports whether an interactive agy session of the operator's
	// runs, which an --all run needs before it does anything (plan D2, D9);
	// nil is the real check (agySession), a test replaces it.
	Session func() (bool, error)

	brainHome string // where the brain store is read: Home, or a dry run's copy
}

// idleFor is how long a project must have been left alone before a dream
// reads it (brain plan D22): no commit, no record update, no Antigravity
// session in that window.
const idleFor = 24 * time.Hour

// cooldown is how long an --all run leaves a project alone after its CLI
// call started, success or failure (visible-dream plan D10): a failed
// project keeps its watermark, so an hourly timer would otherwise try it
// every hour, each a call of up to cliTimeout.
const cooldown = 24 * time.Hour

// Input is one piece of what a project sends.
type Input struct {
	Kind, Label, Text string
	Time              time.Time
}

// Collected is what one project would send.
type Collected struct {
	Root, Name string
	Changed    bool
	Inputs     []Input
	Dropped    []string
	Prompt     string
	// Through is just before the oldest dated input the cap dropped, or zero
	// when it dropped none: the watermark a successful run stores (ADR-069).
	Through time.Time
}

// firstLook is how far back a project's first dream reads.
const firstLook = 14 * 24 * time.Hour

// Run is `fyr dream`. Exactly one of All and Project is set: cmd/fyr refuses
// any other command line as a usage error, with dream's synopsis, before Run
// is called (ledger item 86); the guard below only keeps another caller from
// a run that would lock, log or write first.
func Run(o Options) error {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Projects == nil {
		o.Projects = search.ListProjects
	}
	if o.Scopes == nil {
		o.Scopes = brain.Scopes
	}
	if o.DryRun && o.brainHome == "" {
		// A dry run reads a copy of the brain store: opening the live one
		// migrates it, loads trail.jsonl into it, or creates it (security
		// review of the dream over records, finding 5).
		tmp, cleanup, err := brainCopy(o.Home)
		if err != nil {
			return &ExitError{2, "fyr dream: " + err.Error()}
		}
		defer cleanup()
		o.brainHome = tmp
	}
	if o.brainHome == "" {
		o.brainHome = o.Home
	}
	if o.Active == nil {
		bh := o.brainHome
		o.Active = func(home string, p search.Project, now time.Time) string { return activityIn(home, bh, p, now) }
	}
	if o.All == (o.Project != "") {
		return &ExitError{2, "fyr dream: exactly one of --all and --project"}
	}
	if o.Session == nil {
		o.Session = func() (bool, error) { return agySession(runtime.GOOS, os.Getuid()) }
	}
	if !o.DryRun {
		return o.realRun()
	}
	projects, skips, err := o.targets()
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	st, err := readState(o.Home)
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	return o.dryRun(projects, skips, st)
}

// inUse is why p is not idle yet under --all, "" when it is (or when the
// run names one project).
func (o Options) inUse(p search.Project) string {
	if !o.All {
		return ""
	}
	return o.Active(o.Home, p, o.Now())
}

// activity is the real check behind Options.Active: the newest of the
// checkout's last commit, its scope's last record update and its newest
// Antigravity conversation, when that is within idleFor of now.
func activity(home string, p search.Project, now time.Time) string {
	return activityIn(home, home, p, now)
}

// activityIn is activity with the brain store read from brainHome. A
// record dated after now is clock skew or a planted line, never a sign of
// use (security review of the dream over records, finding 4). The commit
// is read through trail.Log, which refuses a partial clone on a git that
// would fetch through the clone's own config (finding 2).
func activityIn(home, brainHome string, p search.Project, now time.Time) string {
	cut := now.Add(-idleFor)
	if out, err := trail.Log(p.Root, "-1", "--format=%ct"); err == nil {
		if sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
			if t := time.Unix(sec, 0); t.After(cut) {
				return "a commit at " + t.UTC().Format(time.RFC3339)
			}
		}
	}
	if migrated(p.Root) {
		if in, err := forDream(brainHome, p.Root, now, now); err == nil && in.Last.After(cut) {
			return "a record updated at " + in.Last.UTC().Format(time.RFC3339)
		}
	}
	if convs, _, err := search.AgyConversationText(home, p.Root, cut); err == nil && len(convs) > 0 {
		return "an Antigravity session at " + convs[0].Modified.UTC().Format(time.RFC3339)
	}
	return ""
}

// skipReason says why a project is not dreamt over, or "" when it is.
func skipReason(p search.Project) string {
	if why := absent(p); why != "" {
		return why
	}
	if migrated(p.Root) {
		return ""
	}
	docs := docsOf(p)
	if info, err := os.Stat(filepath.Join(p.Root, docs)); err != nil || !info.IsDir() {
		return "no " + docs
	}
	return ""
}

// absent says why p is no checkout to dream over at all, or "" when it is
// one.
func absent(p search.Project) string {
	if info, err := os.Stat(p.Root); err != nil || !info.IsDir() {
		return "gone"
	}
	if _, err := memory.Root(p.Root); err != nil {
		return "not a git checkout"
	}
	return ""
}

// attempted is when an --all run last started root's CLI call, when that is
// within cooldown of now, and zero otherwise; --project, which the operator
// asked for, ignores it. An attempt dated after now (a clock set back) holds
// until the clock passes it plus cooldown, toward not dreaming.
func (o Options) attempted(st state, root string) time.Time {
	t, ok := st.Attempts[root]
	if !o.All || !ok || !t.After(o.Now().Add(-cooldown)) {
		return time.Time{}
	}
	return t
}

func (o Options) since(st state, root string) time.Time {
	if t, ok := st.Projects[root]; ok {
		return t
	}
	return o.Now().Add(-firstLook)
}

// command is the tool-free headless call (D4, D7b, D28). Antigravity has no
// no-tools flag: it runs in plan mode from a HOME whose settings grant no
// tool without review, the operator's own when the oracle shows that, else a
// private one that grants nothing (branchFor), so any tool call is refused,
// and the prompt's warning keeps it from trying; the prompt goes on stdin
// as one stream-json message, never on the command line (Linux caps one
// argument at 128 KiB, and argv is readable in /proc).
func command() []string {
	return []string{"agy", "--input-format", "stream-json", "--output-format", "stream-json", "--mode", "plan", "-p", ""}
}

// ExitError is a run's exit status: 1 when a project failed, 2 when the run
// failed before its first project.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// StatusLine is `fyr status`'s dream line.
func StatusLine(home string) string {
	timer := "timer not installed"
	switch {
	case timerStale(home, runtime.GOOS):
		// Named with --home under a home that is not the real one: the bare
		// commands act on the home they default to (BUG-149's class).
		flag := ""
		if unit, _ := UnitFor(home); unit != unitName {
			flag = " --home " + shellQuote(home)
		}
		timer = "timer installed, older schedule: fyr dream --remove-timer" + flag + ", then fyr dream --install-timer" + flag
	case TimerInstalled(home):
		timer = "timer installed"
	}
	st, err := readState(home)
	if err != nil {
		return "state unreadable: " + err.Error() + "; " + timer
	}
	r := st.LastRun
	switch {
	case r.At.IsZero():
		return "never run; " + timer
	case r.Error != "":
		return fmt.Sprintf("last run %s failed: %s; %s", r.At.Format("2006-01-02"), r.Error, timer)
	}
	return fmt.Sprintf("last run %s: %d projects, %d proposals; %s", r.At.Format("2006-01-02"), r.Projects, r.Proposals, timer)
}

// TimerInstalled reports whether a dream timer unit is on disk in home.
func TimerInstalled(home string) bool {
	for _, p := range []string{linuxTimerPath(home), macPlistPath(home)} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

func linuxTimerPath(home string) string {
	unit, _ := UnitFor(home)
	return filepath.Join(home, ".config", "systemd", "user", unit+".timer")
}

func macPlistPath(home string) string {
	_, plist := UnitFor(home)
	return filepath.Join(home, "Library", "LaunchAgents", plist+".plist")
}

// projectKey is the checkout a path names: the main worktree's root when it
// is one, else the path with its links resolved.
func projectKey(root string) string {
	if main, _, err := trail.Roots(root); err == nil {
		return main
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		return r
	}
	return filepath.Clean(root)
}

// brainCopy is a private copy of home's brain store for a dry run: a
// directory laid out as a home, its brain.db and companions copied in, made
// under ~/.config/fyr whenever that directory exists (swept by removeCopies
// when a run dies), else in the system's temporary directory: a dry run
// never creates ~/.config/fyr, which beside an old ~/.config/sh-agent would
// stop the crossing's migration. Removed by cleanup.
func brainCopy(home string) (string, func(), error) {
	live := brain.Path(home)
	parent, store := "", false
	if info, err := os.Lstat(filepath.Dir(live)); err == nil && info.IsDir() {
		parent = filepath.Dir(live)
	}
	if _, err := os.Lstat(live); err == nil && parent != "" {
		store = true
	}
	tmp, err := os.MkdirTemp(parent, "dream-brain-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if store {
		dst := filepath.Dir(brain.Path(tmp))
		if err := os.MkdirAll(dst, 0o700); err != nil {
			cleanup()
			return "", nil, err
		}
		if _, err := search.CopyDB(parent, filepath.Base(live), dst); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return tmp, cleanup, nil
}
