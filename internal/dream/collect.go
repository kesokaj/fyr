package dream

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/styrman-harness/internal/harness"
	"github.com/kesokaj/styrman-harness/internal/memory"
	"github.com/kesokaj/styrman-harness/internal/redact"
	"github.com/kesokaj/styrman-harness/internal/search"
	"github.com/kesokaj/styrman-harness/internal/trail"
)

// The kinds, in prompt order. The metrics summary is dropped first when the
// prompt is over the cap (ADR-055a), then transcripts, then the trail, then
// feedback, then skills; memory is always sent whole (D21).
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

// collect gathers what project p would send: every memory file, and the
// feedback, trail and transcripts after since, with the installed skills.
// Changed is true when anything but the skills is newer than since.
func collect(home string, p search.Project, since time.Time, limit int) (Collected, error) {
	c := Collected{Root: p.Root, Name: p.Name}
	add := func(kind, label, text string, t time.Time) {
		if strings.TrimSpace(text) == "" {
			return
		}
		c.Inputs = append(c.Inputs, Input{Kind: kind, Label: label, Text: text, Time: t})
		// Skills and the metrics summary are context, never news: neither
		// makes a project worth dreaming over by itself.
		if kind != kindSkill && kind != kindMetrics && t.After(since) {
			c.Changed = true
		}
	}

	if store, err := memory.Store(p.Root); err != nil {
		c.Dropped = append(c.Dropped, "memory: "+err.Error())
	} else {
		for _, f := range mdFiles(store) {
			body, t := readFile(f)
			add(kindMemory, "memory/"+filepath.Base(f), body, t)
		}
	}

	for _, e := range feedback(home, p.Root, since) {
		add(kindFeedback, "feedback.jsonl", e.text, e.ts)
	}

	docs := docsOf(p)
	dates, modified := trail.Dates(p.Root, docs)
	for _, dir := range trailDirs {
		// A symlinked docs root or record directory is never followed: a
		// repository could commit one pointing anywhere (security review
		// 2026-09-30).
		if linked(p.Root, docs, dir) {
			c.Dropped = append(c.Dropped, filepath.Join(docs, dir)+": a symlink on the way, not followed")
			continue
		}
		for _, f := range mdFiles(filepath.Join(p.Root, docs, dir)) {
			body, t := readFile(f)
			// A tracked, unmodified record is dated by its newest commit:
			// its mtime is the clone's or the checkout's, and by mtime a
			// whole trail looked new and evicted the transcripts (audit
			// 2026-09-30, SD4). An untracked or modified file keeps its mtime.
			if rel, err := filepath.Rel(p.Root, f); err == nil {
				if gt, ok := dates[filepath.ToSlash(rel)]; ok && !modified[filepath.ToSlash(rel)] {
					t = gt
				}
			}
			if t.After(since) {
				add(kindTrail, filepath.Join(dir, filepath.Base(f)), body, t)
			}
		}
	}

	for _, s := range skills(home) {
		add(kindSkill, "skill "+s.name, s.name+": "+s.description, time.Time{})
	}

	// The project's agent metrics, numbers only, for this window and the one
	// before it (R3's addendum, ADR-055a). A store that cannot be read is
	// named as left out; it never fails the project.
	if sum, err := metricsSummary(home, p.Root, since, time.Now()); err != nil {
		c.Dropped = append(c.Dropped, "metrics: "+err.Error())
	} else {
		add(kindMetrics, "metrics", sum, time.Time{})
	}

	// An Antigravity read error fails the project, so its watermark stays and
	// the conversations are read again next time, never skipped for good
	// (code review 2026-09-30).
	agy, err := search.AgyConversationText(home, p.Root, since)
	if err != nil {
		return c, fmt.Errorf("reading Antigravity's conversations: %w", err)
	}
	for _, a := range agy {
		add(kindTranscript, "antigravity "+a.ID, a.Text, a.Modified)
	}

	for i := range c.Inputs {
		c.Inputs[i].Text = scrub(c.Inputs[i].Text, home)
	}
	for i := range c.Dropped {
		c.Dropped[i] = scrub(c.Dropped[i], home)
	}
	if err := c.fit(limit); err != nil {
		return c, err
	}
	c.Prompt = prompt(c)
	return c, nil
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

// scrub is what every input passes before it leaves sh-agent: the
// redactor's needles (internal/redact), and the home path written as ~.
func scrub(text, home string) string {
	text = redact.Redact(text)
	if home != "" && home != "/" {
		text = strings.ReplaceAll(text, strings.TrimSuffix(home, "/"), "~")
	}
	return text
}

// fit drops inputs until the prompt is under limit: the metrics summary,
// then transcripts oldest first, then trail files oldest first, then
// feedback, then skills (D21, ADR-055a). A
// project whose memory alone is over the limit fails.
func (c *Collected) fit(limit int) error {
	for _, kind := range []string{kindMetrics, kindTranscript, kindTrail, kindFeedback, kindSkill} {
		for len(prompt(*c)) > limit {
			i := oldest(c.Inputs, kind)
			if i < 0 {
				break
			}
			c.Dropped = append(c.Dropped, fmt.Sprintf("%s %s (over the %d KB cap)", c.Inputs[i].Kind, c.Inputs[i].Label, limit/1024))
			c.Inputs = append(c.Inputs[:i], c.Inputs[i+1:]...)
		}
	}
	if len(prompt(*c)) > limit {
		return errors.New("memory over cap")
	}
	return nil
}

func oldest(in []Input, kind string) int {
	best := -1
	for i, x := range in {
		if x.Kind == kind && (best < 0 || x.Time.Before(in[best].Time)) {
			best = i
		}
	}
	return best
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
func feedback(home, root string, since time.Time) []entry {
	f, err := os.Open(filepath.Join(home, ".config", "sh-agent", "feedback.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
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
	return out
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
