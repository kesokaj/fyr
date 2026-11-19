// Package dream is `fyr dream`: the background job that reads every
// registered project's memory, trail, feedback, a numbers-only summary of its
// metrics and the Antigravity transcripts (after ingesting every session's shape
// into the metrics store), asks
// a CLI with no tool granted what should become a rule, a skill or a memory, and
// stores the answer as global `proposal` records (R3's exception, R10, plan
// 2026-09-30-parity-memory-dream; brain plan Phase 4: only projects idle for
// a day, read from their records).
package dream

import (
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// maxPrompt caps what one project sends to the CLI (the plan's Interfaces).
const maxPrompt = 256 * 1024

// Options is one run.
type Options struct {
	Home       string // fyr's home (--home); the child CLI keeps the real environment
	All        bool
	Project    string // --project <dir>
	DryRun     bool
	SkipIngest bool // true when caller (fyr auto) already ingested metrics first
	Out        io.Writer
	Now        func() time.Time
	Projects   func(home string) ([]search.Project, error) // defaultProjects; a test replaces it
	Scopes     func(home string) ([]string, error)         // brain.Scopes: projects whose trail is records; a test replaces it
	// Active says why a project is in use and is not dreamt over yet, ""
	// when it has been idle for idleFor (D22); nil is the real check
	// (activityIn), a test replaces it.
	Active func(home string, p search.Project, now time.Time) string
	// Session reports whether an interactive agy session of the operator's
	// runs, which an --all run needs before it does anything (plan D2, D9);
	// nil is the real check (agySession), a test replaces it.
	Session func() (bool, error)
	// ConversationIdle enables sliding-window conversation evaluation (cutoff = now - ConversationIdle)
	// without whole-project idle blocks (ADR-524). Zero preserves the 24h project idle gate.
	ConversationIdle time.Duration
	// HarnessRepo is the optional resolved harness checkout included in --all targets.
	HarnessRepo string
	// WriteRatings persists validated conversation ratings in realRun (never in DryRun).
	// Nil defaults to writing session_ratings in metrics.db during realRun.
	WriteRatings func(home, project string, rows []metrics.SessionRating) error

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

func defaultProjects(home string) ([]search.Project, error) {
	projects, err := search.ListProjects(home)
	if err != nil {
		return nil, err
	}
	agyWS, err := search.ListAgyWorkspaces(home)
	if err != nil {
		return nil, err
	}
	return append(projects, agyWS...), nil
}

func (o *Options) withDefaults() {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Projects == nil {
		o.Projects = defaultProjects
	}
	if o.Scopes == nil {
		o.Scopes = brain.Scopes
	}
	if o.brainHome == "" {
		o.brainHome = o.Home
	}
	if o.Active == nil {
		bh := o.brainHome
		o.Active = func(home string, p search.Project, now time.Time) string { return activityIn(home, bh, p, now) }
	}
	if o.Session == nil {
		o.Session = func() (bool, error) { return agySession(runtime.GOOS, os.Getuid()) }
	}
}

// Run is `fyr dream`. Exactly one of All and Project is set: cmd/fyr refuses
// any other command line as a usage error, with dream's synopsis, before Run
// is called (ledger item 86); the guard below only keeps another caller from
// a run that would lock, log or write first.
func Run(o Options) error {
	if o.All == (o.Project != "") {
		return &ExitError{2, "fyr dream: exactly one of --all and --project"}
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
	o.withDefaults()
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
// run names one project, or when ConversationIdle uses per-conversation staleness).
func (o Options) inUse(p search.Project) string {
	if !o.All || o.ConversationIdle > 0 {
		return ""
	}
	return o.Active(o.Home, p, o.Now())
}

// activityIn is the real check behind Options.Active: the newest of the
// checkout's last commit, its scope's last record update and its newest
// Antigravity conversation, when that is within idleFor of now. A
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
	if in, err := forDream(brainHome, p.Root, now, now); err == nil && in.Last.After(cut) {
		return "a record updated at " + in.Last.UTC().Format(time.RFC3339)
	}
	if convs, _, err := search.AgyConversationText(home, p.Root, cut); err == nil && len(convs) > 0 {
		return "an Antigravity session at " + convs[0].Modified.UTC().Format(time.RFC3339)
	}
	return ""
}

// absent says why p cannot be dreamt over at all, or "" when it is valid
// (a directory on disk or search.GlobalWorkspace).
func absent(p search.Project) string {
	if p.Root == search.GlobalWorkspace {
		return ""
	}
	if info, err := os.Stat(p.Root); err != nil || !info.IsDir() {
		return "gone"
	}
	if _, _, err := trail.Roots(p.Root); err != nil && !errors.Is(err, trail.ErrNotCheckout) {
		return "invalid git checkout"
	}
	return ""
}

// attempted is when an --all run last started root's CLI call, when that is
// within cooldown of now, and zero otherwise; --project, which the operator
// asked for, ignores it. When ConversationIdle > 0, only failed/incomplete
// attempts trigger the 24h cooldown so newly-stale conversations are rated hourly.
func (o Options) attempted(st state, root string) time.Time {
	if !o.All {
		return time.Time{}
	}
	m := st.Attempts
	if o.ConversationIdle > 0 {
		m = st.FailedAttempts
	}
	t, ok := m[root]
	if !ok || !t.After(o.Now().Add(-cooldown)) {
		return time.Time{}
	}
	return t
}

func (o Options) window(st state, root string) (since, upper, cutoff time.Time) {
	now := o.Now()
	since = o.since(st, root)
	if o.ConversationIdle > 0 {
		cutoff = now.Add(-o.ConversationIdle)
		return since, cutoff, cutoff
	}
	return since, now, time.Time{}
}

func (o Options) since(st state, root string) time.Time {
	if t, ok := st.Projects[root]; ok {
		return t
	}
	return o.Now().Add(-firstLook)
}

// command is the headless call with no tool granted (D4, D7b, D28; ADR-089).
// Antigravity has no no-tools flag: it runs in plan mode from a HOME whose
// settings grant no tool without review, the operator's own when the oracle
// shows that, else a private one that grants nothing (branchFor). Plan mode
// still runs write_file for its own plan under brain/ (the 1.4.12 capture):
// that is not refused, the turn just gives no answer (answer), and the
// prompt's warning keeps it from trying; the prompt goes on stdin
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
