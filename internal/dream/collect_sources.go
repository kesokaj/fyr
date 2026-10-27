package dream

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/memory"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// adder returns collectIn's add: one input, unless its text is blank, and
// Changed when it is news after since.
func (c *Collected) adder(since time.Time) func(kind, label, text string, t time.Time) {
	return func(kind, label, text string, t time.Time) {
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
}

// addRecords adds a migrated project's records from the brain store under
// brainHome: memory, the global feedback about it, and its trail.
func addRecords(brainHome, root string, since, now time.Time, add func(kind, label, text string, t time.Time)) error {
	// The run's clock, not the wall's, so a test clock reaches "never a
	// record dated after now" (SD32 of the 2026-10-27 audit).
	in, err := forDream(brainHome, root, since, now)
	if err != nil {
		return fmt.Errorf("reading the brain store: %w", err)
	}
	for _, r := range in.Memory {
		add(kindMemory, r.ID, r.Title+"\n"+r.Body, recordTime(r))
	}
	for _, r := range in.Feedback {
		// A feedback record's body is the old log's JSON line: redacted as
		// one, so a secret behind a JSON escape (`\u0026sig=`) is caught on
		// the decoded line, as the log's lines are (security review of the
		// dream over records, finding 1).
		add(kindFeedback, r.ID, redactFeedback(r.Body), recordTime(r))
	}
	for _, r := range in.Trail {
		add(kindTrail, r.ID, r.Title+"\n"+r.Body, recordTime(r))
	}
	return nil
}

// addFiles adds a project still on files: its memory store and its trail
// records newer than since.
func (c *Collected) addFiles(p search.Project, since time.Time, add func(kind, label, text string, t time.Time)) {
	if store, err := memory.Store(p.Root); err != nil {
		c.Dropped = append(c.Dropped, "memory: "+err.Error())
	} else {
		for _, f := range mdFiles(store) {
			body, t := readFile(f)
			add(kindMemory, "memory/"+filepath.Base(f), body, t)
		}
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
}
