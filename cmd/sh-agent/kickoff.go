package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kesokaj/styrman-harness/internal/trail"
)

// runKickoff prints the newest handoff's `## Kickoff` block for the checkout
// the shell is in, so one command starts the next session: `claude "$(sh-agent
// kickoff)"` or `agy -i "$(sh-agent kickoff)"` (plan 2026-10-23-session-sized-work
// D28, ADR-058). It reads no home and no harness tree (its global flags are
// refused in run). Nothing queued is a plain error: main prints `sh-agent:
// kickoff: nothing queued…` on stderr and exits 1, and stdout stays empty, so
// `$(…)` hands the CLI an empty prompt and `while k=$(sh-agent kickoff); do …;
// done` ends (D29).
func runKickoff(c *ctx) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	// The checkout's top level, so the verb works from a subdirectory; outside
	// a checkout Toplevel returns the directory itself, which then has no
	// docs root and queues nothing.
	root := trail.Toplevel(wd)
	docs := trail.DocsRoot(root)
	if docs == "" {
		return errors.New("kickoff: nothing queued: no docs root (.agents/docs or docs) under " + root)
	}
	dir := filepath.Join(docs, "handoffs")
	name, err := newestHandoff(filepath.Join(root, dir))
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("kickoff: nothing queued: no handoff in " + dir)
	}
	text, err := os.ReadFile(filepath.Join(root, dir, name))
	if err != nil {
		return err
	}
	block, ok := kickoffBlock(string(text))
	switch {
	case !ok:
		return fmt.Errorf("kickoff: nothing queued: %s has no ## Kickoff section", filepath.Join(dir, name))
	case block == "":
		return fmt.Errorf("kickoff: nothing queued: the Kickoff of %s queues nothing", filepath.Join(dir, name))
	}
	fmt.Fprint(c.out, block)
	return nil
}

// handoffStamp is the handoff file name the handoff skill writes,
// `$(date +%F-%H%M)-<slug>.md`, and the older `YYYY-MM-DD-<slug>.md`.
var handoffStamp = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:-(\d{4}))?-.*\.md$`)

// newestHandoff names the handoff in dir with the greatest stamp (date, then
// HHMM; an older name without HHMM counts as 0000 that day; two with the same
// stamp are ordered by name), "" when dir has none or does not exist. A file
// whose name carries no stamp is not a handoff.
func newestHandoff(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	best, bestKey := "", ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := handoffStamp.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		hhmm := m[2]
		if hhmm == "" {
			hhmm = "0000"
		}
		key := m[1] + hhmm
		if key > bestKey || (key == bestKey && e.Name() > best) {
			best, bestKey = e.Name(), key
		}
	}
	return best, nil
}

// kickoffBlock returns the kickoff under the handoff's `## Kickoff` heading:
// the text inside the section's first fenced block, fence lines removed, or,
// when the section has no fence, the section's own text — a model that wrote
// the kickoff as prose instead of the template's block still queued it, and
// the operator's shell gets what was queued (plan D33). Trailing blank lines
// are dropped and the result ends in one newline. found is false when the
// heading is absent. A section that is empty or holds the nothing-queued
// line (it starts with "Nothing queued") is found with an empty block. Fences
// are tracked from the top of the file, so a `## Kickoff` line quoted inside
// another block is not the heading.
func kickoffBlock(text string) (block string, found bool) {
	var (
		section bool     // inside the ## Kickoff section
		open    string   // the fence line that is open, "" outside a fence
		body    []string // the fenced block's lines
		prose   []string // the section's unfenced lines, used when no fence comes
	)
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if open != "" {
			if t == open {
				if section {
					return trimBlock(body), true
				}
				open = ""
				continue
			}
			if section {
				body = append(body, l)
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			open = t[:len(t)-len(strings.TrimLeft(t, "`"))]
			continue
		}
		if strings.HasPrefix(t, "## ") {
			if section {
				return proseBlock(prose), true // the next section began before any fence
			}
			section = t == "## Kickoff"
			found = found || section
			continue
		}
		if section {
			prose = append(prose, l)
		}
	}
	if section && open != "" {
		return trimBlock(body), true // the fence never closed: print what it holds
	}
	if section {
		return proseBlock(prose), true
	}
	return "", found
}

// proseBlock is an unfenced Kickoff section: its text, or "" when it is empty
// or is the nothing-queued line.
func proseBlock(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 || strings.HasPrefix(strings.ToLower(strings.TrimSpace(lines[0])), "nothing queued") {
		return ""
	}
	return trimBlock(lines)
}

func trimBlock(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
