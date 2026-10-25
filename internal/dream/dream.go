// Package dream is `fyr dream`: the background job that reads every
// indexed project's memory, trail, feedback, a numbers-only summary of its
// metrics and the Antigravity transcripts (after ingesting every session's shape
// into the metrics store), asks
// a CLI with no tools what should become a rule, a skill or a memory, and
// writes the answer as proposals under ~/.config/fyr/proposals/dream/
// (R3's exception, R10, plan 2026-09-30-parity-memory-dream).
package dream

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/memory"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
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
}

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

// targets is --project's one checkout, or every indexed project for --all;
// Run has already refused both and neither.
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
	return o.Projects(o.Home)
}

// skipReason says why a project is not dreamt over, or "" when it is.
func skipReason(p search.Project) string {
	if info, err := os.Stat(p.Root); err != nil || !info.IsDir() {
		return "gone"
	}
	if _, err := memory.Root(p.Root); err != nil {
		return "not a git checkout"
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
		c, err := collect(o.Home, p, o.since(st, p.Root), maxPrompt)
		if err != nil {
			fmt.Fprintf(o.Out, "[dry-run] %s: would fail (%v)\n", p.Root, err)
			continue
		}
		if !c.Changed {
			fmt.Fprintf(o.Out, "[dry-run] %s: unchanged since %s\n", p.Root, o.since(st, p.Root).Format(time.RFC3339))
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
		fmt.Fprintf(o.Out, "[dry-run] %s: %s; %d bytes; would run: %s\n", p.Root, strings.Join(parts, ", "), len(c.Prompt), strings.Join(cli, " "))
		for _, d := range c.Dropped {
			fmt.Fprintf(o.Out, "[dry-run]   left out: %s\n", oneLine(d))
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
