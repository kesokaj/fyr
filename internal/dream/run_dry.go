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
func (o Options) dryRun(projects []search.Project, st state) error {
	if err := checkAgy(runtime.GOOS); err != nil {
		return &ExitError{2, "fyr dream: " + err.Error()}
	}
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
