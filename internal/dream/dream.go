// Package dream is `fyr dream`: the background job that reads every
// indexed project's memory, trail, feedback, a numbers-only summary of its
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
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/memory"
	"github.com/kesokaj/fyr/internal/metrics"
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

	brainHome string // where the brain store is read: Home, or a dry run's copy
}

// idleFor is how long a project must have been left alone before a dream
// reads it (brain plan D22): no commit, no record update, no Antigravity
// session in that window.
const idleFor = 24 * time.Hour

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
}

// firstLook is how far back a project's first dream reads.
const firstLook = 14 * 24 * time.Hour

// Run is `fyr dream`.
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
	// A usage mistake is refused before anything is locked, logged or read.
	switch {
	case o.All && o.Project != "":
		return &ExitError{2, "fyr dream: --all or --project <dir>, not both"}
	case !o.All && o.Project == "":
		return &ExitError{2, "usage: fyr dream --all | --project <dir> [--dry-run] | --install-timer | --remove-timer"}
	}
	if !o.DryRun {
		return o.realRun()
	}
	projects, err := o.targets()
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	st, err := readState(o.Home)
	if err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	return o.dryRun(projects, st)
}

// targets is --project's one checkout, or for --all every project the file
// index registered (a trail still on files) and every project scope the
// brain store holds (a trail of records: such a checkout no longer registers
// in the file index); Run has already refused both and neither.
func (o Options) targets() ([]search.Project, error) {
	switch {
	case o.Project != "":
		abs, err := filepath.Abs(o.Project)
		if err != nil {
			return nil, err
		}
		root, err := memory.Root(abs)
		if err != nil {
			return nil, err
		}
		return []search.Project{{Root: root, Name: filepath.Base(root), DocsRoot: ".agents/docs"}}, nil
	}
	projects, err := o.Projects(o.Home)
	if err != nil {
		return nil, err
	}
	scopesOf := o.Scopes
	if scopesOf == nil {
		scopesOf = brain.Scopes
	}
	scopes, err := scopesOf(o.Home)
	if err != nil {
		return nil, err
	}
	// One checkout, however reached (a linked worktree's path, a symlinked
	// parent), is one target: its records would otherwise be sent twice
	// (security review of the dream over records, finding 6).
	seen := map[string]bool{}
	var out []search.Project
	for _, p := range projects {
		if k := projectKey(p.Root); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	for _, s := range scopes {
		if k := projectKey(s); !seen[k] {
			seen[k] = true
			out = append(out, search.Project{Root: s, Name: filepath.Base(s)})
		}
	}
	return out, nil
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
	if convs, err := search.AgyConversationText(home, p.Root, cut); err == nil && len(convs) > 0 {
		return "an Antigravity session at " + convs[0].Modified.UTC().Format(time.RFC3339)
	}
	return ""
}

// skipReason says why a project is not dreamt over, or "" when it is.
func skipReason(p search.Project) string {
	if info, err := os.Stat(p.Root); err != nil || !info.IsDir() {
		return "gone"
	}
	if _, err := memory.Root(p.Root); err != nil {
		return "not a git checkout"
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

func (o Options) since(st state, root string) time.Time {
	if t, ok := st.Projects[root]; ok {
		return t
	}
	return o.Now().Add(-firstLook)
}

// shownDropped is how many dropped inputs a dry run lists by name; the rest
// are counted (audit 2026-10-25, SD-S2: 34 lines on one box).
const shownDropped = 5

// dryRun prints what a run would collect and run, and writes, locks and
// advances nothing.
func (o Options) dryRun(projects []search.Project, st state) error {
	fmt.Fprintf(o.Out, "[dry-run] would ingest metrics into %s\n", metrics.Path(o.Home))
	cli := append(command(), "(prompt on stdin as one stream-json message; HOME a private copy with no grants)")
	for _, p := range projects {
		if why := skipReason(p); why != "" {
			fmt.Fprintf(o.Out, "[dry-run] %s: skip (%s)\n", p.Root, why)
			continue
		}
		// The idle rule is the nightly run's (--all); a dream asked for one
		// project runs now.
		if why := o.inUse(p); why != "" {
			fmt.Fprintf(o.Out, "[dry-run] %s: skip (in use within %s: %s)\n", p.Root, idleFor, why)
			continue
		}
		c, err := collectIn(o.Home, o.brainHome, p, o.since(st, p.Root), maxPrompt)
		if err != nil {
			fmt.Fprintf(o.Out, "[dry-run] %s: would fail (%v)\n", p.Root, err)
			continue
		}
		if !c.Changed {
			fmt.Fprintf(o.Out, "[dry-run] %s: idle, unchanged since %s\n", p.Root, o.since(st, p.Root).Format(time.RFC3339))
			continue
		}
		counts := map[string]int{}
		for _, in := range c.Inputs {
			counts[in.Kind]++
		}
		var parts []string
		for _, k := range promptOrder {
			parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
		}
		fmt.Fprintf(o.Out, "[dry-run] %s: would dream (idle %s, changed since %s): %s; %d bytes; would run: %s\n", p.Root, idleFor, o.since(st, p.Root).Format(time.RFC3339), strings.Join(parts, ", "), len(c.Prompt), strings.Join(cli, " "))
		shown := c.Dropped
		if len(shown) > shownDropped {
			fmt.Fprintf(o.Out, "[dry-run]   left out: %d inputs, the first %d below\n", len(shown), shownDropped)
			shown = shown[:shownDropped]
		}
		for _, d := range shown {
			fmt.Fprintf(o.Out, "[dry-run]   left out: %s\n", oneLine(d))
		}
		if rest := len(c.Dropped) - len(shown); rest > 0 {
			fmt.Fprintf(o.Out, "[dry-run]   left out: and %d more\n", rest)
		}
		if o.Project != "" {
			fmt.Fprintf(o.Out, "[dry-run] prompt:\n%s\n", c.Prompt)
		}
	}
	return nil
}

// command is the tool-free headless call (D4, D7b, D28). Antigravity has no
// no-tools flag: it runs in plan mode from a private HOME whose settings
// grant nothing, so any tool call is refused, and the prompt's opening
// warning keeps it from trying; the prompt goes on stdin as one stream-json
// message, never on the command line (Linux caps one argument at 128 KiB,
// and argv is readable in /proc).
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
	if TimerInstalled(home) {
		timer = "timer installed"
	}
	// Every branch below ends in timer, so the legacy note reaches them all.
	if len(LegacyTimerFiles(home)) > 0 {
		// A dry run, with this home named: the bare command removes the
		// timer of the home it defaults to, which is the real one.
		timer += "; legacy timer files under " + home + ": fyr dream --remove-timer --dry-run --home " + shellQuote(home) + " prints the cleanup steps"
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
// directory laid out as a home, its brain.db and companions copied in (under
// ~/.config/fyr beside the live one, swept by removeCopies when a run dies;
// in the system's temporary directory when there is no store yet), removed
// by cleanup.
func brainCopy(home string) (string, func(), error) {
	live := brain.Path(home)
	parent := ""
	if _, err := os.Lstat(live); err == nil {
		parent = filepath.Dir(live)
	}
	tmp, err := os.MkdirTemp(parent, "dream-brain-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if parent != "" {
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
