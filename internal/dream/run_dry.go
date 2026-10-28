package dream

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

// shownDropped is how many dropped inputs a dry run lists by name; the rest
// are counted (audit 2026-10-25, SD-S2: 34 lines on one box).
const shownDropped = 5

// dryRun prints what a run would collect and run, and writes, locks and
// advances nothing.
func (o Options) dryRun(projects []search.Project, skips []skipped, st state) error {
	if err := o.drySession(); err != nil {
		return err
	}
	if err := checkAgy(runtime.GOOS); err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
	fmt.Fprintf(o.Out, "[dry-run] would ingest metrics into %s\n", metrics.Path(o.Home))
	cli := append(command(), "(prompt on stdin as one stream-json message; HOME a private copy with no grants)")
	for _, s := range skips {
		fmt.Fprintf(o.Out, "[dry-run] %s: skip (%s)\n", s.root, s.why)
	}
	for _, p := range projects {
		if why := o.drySkip(p, st); why != "" {
			fmt.Fprintf(o.Out, "[dry-run] %s: skip (%s)\n", p.Root, why)
			continue
		}
		c, err := collectIn(o.Home, o.brainHome, p, o.since(st, p.Root), o.Now(), maxPrompt)
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
		o.printDropped(c.Dropped)
		if o.Project != "" {
			fmt.Fprintf(o.Out, "[dry-run] prompt:\n%s\n", c.Prompt)
		}
	}
	return nil
}

// drySession is the real --all run's first step, said: whether an agy
// session runs, and a check that errors exits 2 as that one does.
func (o Options) drySession() error {
	if !o.All {
		return nil
	}
	on, err := o.Session()
	switch {
	case err != nil:
		return &ExitError{2, "fyr dream: " + err.Error()}
	case on:
		fmt.Fprintln(o.Out, "[dry-run] agy session: yes")
	default:
		fmt.Fprintln(o.Out, "[dry-run] agy session: no (a real --all run would stop here)")
	}
	return nil
}

// drySkip is why the real run would skip p, "" when it would not.
func (o Options) drySkip(p search.Project, st state) string {
	if why := skipReason(p); why != "" {
		return why
	}
	if t := o.attempted(st, p.Root); !t.IsZero() {
		return "attempted " + t.UTC().Format(time.RFC3339)
	}
	// The idle rule is the timer's run's (--all); a dream asked for one
	// project runs now.
	if why := o.inUse(p); why != "" {
		return fmt.Sprintf("in use within %s: %s", idleFor, why)
	}
	return ""
}

// printDropped lists a dry run's left-out inputs: the first shownDropped by
// name, the rest counted.
func (o Options) printDropped(dropped []string) {
	shown := dropped
	if len(shown) > shownDropped {
		fmt.Fprintf(o.Out, "[dry-run]   left out: %d inputs, the first %d below\n", len(shown), shownDropped)
		shown = shown[:shownDropped]
	}
	for _, d := range shown {
		fmt.Fprintf(o.Out, "[dry-run]   left out: %s\n", oneLine(d))
	}
	if rest := len(dropped) - len(shown); rest > 0 {
		fmt.Fprintf(o.Out, "[dry-run]   left out: and %d more\n", rest)
	}
}
