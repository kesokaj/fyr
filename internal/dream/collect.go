package dream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// trailDirs are the records a dream reads under a project's docs root.
var trailDirs = []string{"bugs", "reviews", "retros", "changes", "handoffs"}

// collect gathers what project p would send: its memory, and the feedback,
// trail and transcripts after since, with the installed skills. A project
// whose trail is records (`.agents/trail.jsonl`) is read from the brain
// store: its memory records, its bug, review, retro, change and handoff
// records and the global feedback records about it (brain plan Phase 4); a
// project still on files is read as before. Changed is true when anything
// but the skills is newer than since.
func collect(home string, p search.Project, since, now time.Time, limit int) (Collected, error) {
	return collectIn(home, home, p, since, now, limit)
}

// collectIn is collect with the brain store read from brainHome: the live
// one (home) in a run, a copy in a dry run, which writes nothing. now is
// the run's clock.
func collectIn(home, brainHome string, p search.Project, since, now time.Time, limit int) (Collected, error) {
	// The name is the checkout's base name, which may hold a line break: it
	// is folded once here, for the prompt, the proposals header and the file
	// names alike (audit 2026-10-25, SD15).
	c := &Collected{Root: p.Root, Name: oneLine(p.Name)}
	add := c.adder(since)

	fb, fbNote := feedback(home, p.Root, since)
	if fbNote != "" {
		c.Dropped = append(c.Dropped, fbNote)
	}
	for _, e := range fb {
		add(kindFeedback, "feedback.jsonl", e.text, e.ts)
	}

	if migrated(p.Root) {
		if err := addRecords(brainHome, p.Root, since, now, add); err != nil {
			return *c, err
		}
		return c.finish(home, p, since, now, limit, add)
	}
	c.addFiles(p, since, add)
	return c.finish(home, p, since, now, limit, add)
}

// finish adds what every project sends whatever its trail — the skills, the
// metrics summary and the Antigravity conversations — then scrubs, fits and
// renders the prompt.
func (c *Collected) finish(home string, p search.Project, since, now time.Time, limit int, add func(kind, label, text string, t time.Time)) (Collected, error) {
	for _, s := range skills(home) {
		add(kindSkill, "skill "+s.name, s.name+": "+s.description, time.Time{})
	}

	// The project's agent metrics, numbers only, for this window and the one
	// before it (R3's addendum, ADR-055a). A store that cannot be read is
	// named as left out; it never fails the project.
	if sum, err := metricsSummary(home, p.Root, since, now); err != nil {
		c.Dropped = append(c.Dropped, "metrics: "+err.Error())
	} else {
		add(kindMetrics, "metrics", sum, time.Time{})
	}

	// An Antigravity read error fails the project, so its watermark stays and
	// the conversations are read again next time, never skipped for good
	// (code review 2026-09-30).
	agy, err := search.AgyConversationText(home, p.Root, since)
	if err != nil {
		return *c, fmt.Errorf("reading Antigravity's conversations: %w", err)
	}
	for _, a := range agy {
		add(kindTranscript, "antigravity "+a.ID, a.Text, a.Modified)
	}

	// Labels too: a trail file's name is project text and can hold a
	// secret's shape (SD31 of the 2026-10-27 audit).
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

// linked reports whether any directory from root down to docs/dir is a
// symlink: .agents, .agents/docs and the record directory alike (code
// review round 2).
func linked(root, docs, dir string) bool {
	p := root
	for _, part := range strings.Split(filepath.ToSlash(filepath.Join(docs, dir)), "/") {
		p = filepath.Join(p, part)
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
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

// mdFiles lists the .md files directly in dir, by name, never through a
// pattern built from a path (the golang skill's glob rule).
func mdFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
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

type entry struct {
	ts   time.Time
	text string
}

// feedback is the feedback log's lines for root after since, each as it is
// in the file.
func feedback(home, root string, since time.Time) ([]entry, string) {
	f, err := openRegular(filepath.Join(home, harness.StateDir, "feedback.jsonl"), true)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ""
		}
		return nil, "feedback.jsonl: " + err.Error()
	}
	defer f.Close()
	var out []entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		var e struct {
			TS      string `json:"ts"`
			Project string `json:"project"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Project != root {
			continue
		}
		ts, err := time.Parse(time.RFC3339, e.TS)
		if err != nil || !ts.After(since) {
			continue
		}
		// Redacted as a JSON line, so a secret behind a JSON escape is caught
		// on the decoded line; scrub runs the raw needles again later.
		out = append(out, entry{ts: ts, text: strings.TrimRight(redact.RedactJSONL(sc.Text()+"\n"), "\n")})
	}
	// An overlong line ends the read: the prompt names it as left out, so
	// the later corrections are not dropped in silence (SD59b).
	if err := sc.Err(); err != nil {
		return out, fmt.Sprintf("feedback.jsonl: line %d and after (%v)", n+1, err)
	}
	return out, ""
}

// docsOf is the project's docs root as the index recorded it, or the
// scaffold's default.
func docsOf(p search.Project) string {
	if p.DocsRoot != "" {
		return p.DocsRoot
	}
	return ".agents/docs"
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

// migrated reports whether root's trail is records: a committed
// `.agents/trail.jsonl` (brain plan D16's line; an Lstat, so a link counts and
// the brain store refuses it when read).
func migrated(root string) bool {
	_, err := os.Lstat(filepath.Join(root, ".agents", "trail.jsonl"))
	return err == nil
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
