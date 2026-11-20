package dream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

// The kinds, in prompt order. The metrics summary is dropped first when the
// prompt is over the cap (ADR-055a), then the dated kinds together, newest
// first (ADR-069), then skills; memory is always sent whole (D21).
const (
	kindMemory     = "memory"
	kindFeedback   = "feedback"
	kindTrail      = "trail"
	kindSkill      = "skill"
	kindTranscript = "transcript"
	kindMetrics    = "metrics"
)

var promptOrder = []string{kindMemory, kindFeedback, kindTrail, kindSkill, kindMetrics, kindTranscript}

// collectWindow collects project inputs in (since, upper], where upper is cutoff
// when non-zero (sliding-window stale conversations), else now.
func collectWindow(home, brainHome string, p search.Project, since, cutoff, now time.Time, limit int) (Collected, error) {
	// The name is the checkout's base name, which may hold a line break: it
	// is folded once here, for the prompt, the proposals header and the file
	// names alike (audit 2026-10-25, SD15).
	c := &Collected{Root: p.Root, Name: oneLine(p.Name)}
	if !cutoff.IsZero() && !cutoff.After(since) {
		return *c, nil
	}
	add := c.adder(since)
	upper := now
	if !cutoff.IsZero() {
		upper = cutoff
	}
	if err := addRecords(brainHome, p.Root, since, upper, add); err != nil {
		return *c, err
	}
	return c.finishWindow(home, brainHome, p, since, cutoff, upper, limit, add)
}

// finishWindow adds skills, metrics summary, Antigravity conversations, and
// optional PLAN-*/HANDOFF-* context, then scrubs, fits, and renders the prompt.
func (c *Collected) finishWindow(home, brainHome string, p search.Project, since, cutoff, upper time.Time, limit int, add func(kind, label, text string, t time.Time)) (Collected, error) {
	for _, s := range skills(home) {
		add(kindSkill, "skill "+s.name, s.name+": "+s.description, time.Time{})
	}
	// The project's agent metrics, numbers only, for this window and the one
	// before it (R3's addendum, ADR-055a). A store that cannot be read is
	// named as left out; it never fails the project.
	if sum, err := metricsSummary(home, p.Root, since, upper); err != nil {
		c.Dropped = append(c.Dropped, "metrics: "+err.Error())
	} else {
		add(kindMetrics, "metrics", sum, time.Time{})
	}
	addHiddenCouplingHints(home, p.Root, add)
	// An Antigravity read error fails the project, so its watermark stays and
	// the conversations are read again next time, never skipped for good
	// (code review 2026-09-30).
	agy, leftOut, err := search.AgyConversationTextInWindow(home, p.Root, since, cutoff)
	if err != nil {
		return *c, fmt.Errorf("reading Antigravity's conversations: %w", err)
	}
	for _, a := range agy {
		add(kindTranscript, "antigravity "+a.ID, a.Text, a.Modified)
	}
	// A conversation under the root that is not the project's own, a nested
	// checkout's above all, is named and never sent (ledger item 77).
	for _, l := range leftOut {
		c.Dropped = append(c.Dropped, kindTranscript+" antigravity "+l)
	}
	if c.Changed {
		addOptionalPlanAndHandoff(brainHome, p.Root, since, upper, c, add)
	}
	// Labels too: a record's ID is project text and can hold a secret's
	// shape (SD31 of the 2026-10-27 audit).
	for i := range c.Inputs {
		c.Inputs[i].Text = scrub(c.Inputs[i].Text, home)
		c.Inputs[i].Label = scrub(c.Inputs[i].Label, home)
	}
	for i := range c.Dropped {
		c.Dropped[i] = scrub(c.Dropped[i], home)
	}
	if err := c.fit(limit); err != nil {
		return *c, err
	}
	c.Prompt = render(*c)
	return *c, nil
}

// scrub is what every input passes before it leaves fyr: the
// redactor's needles (internal/redact), and the home path written as ~.
func scrub(text, home string) string {
	text = redact.Redact(text)
	if home != "" && home != "/" {
		text = strings.ReplaceAll(text, strings.TrimSuffix(home, "/"), "~")
	}
	return text
}

func readFile(path string) (string, time.Time) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", time.Time{}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}
	}
	return string(body), info.ModTime()
}

type skill struct{ name, description string }

// skills are the installed skills' names and descriptions, from the
// Antigravity CLI's skill directory.
func skills(home string) []skill {
	dir := filepath.Join(home, harness.AgyRoot, "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []skill
	for _, e := range entries {
		body, _ := readFile(filepath.Join(dir, e.Name(), "SKILL.md"))
		// The installer's own reader: a block-scalar description is folded,
		// where a key-line read returned its `>` (project review 2026-10-09).
		fm := harness.Frontmatter{}
		if front, _, err := harness.Split(body); err == nil {
			fm = harness.Parse(front)
		}
		name, desc := fm["name"], fm["description"]
		if name == "" {
			name = e.Name()
		}
		if desc != "" {
			out = append(out, skill{name, desc})
		}
	}
	return out
}

// forDream is brain.ForDream; a test replaces it.
var forDream = brain.ForDream

// recordTime is when a record last changed.
func recordTime(r brain.Record) time.Time {
	t, _ := time.Parse(time.RFC3339, r.Updated)
	return t
}

// redactFeedback redacts a feedback record's body as the JSON line it holds;
// an indented body is compacted to one line first.
func redactFeedback(body string) string {
	var b bytes.Buffer
	if json.Compact(&b, []byte(body)) == nil {
		body = b.String()
	}
	return strings.TrimRight(redact.RedactJSONL(body+"\n"), "\n")
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
