package dream

import (
	"fmt"
	"strings"
	"time"
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

// addRecords adds a project's records from the brain store under
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
		// A feedback record's body is a JSON line: redacted as one, so a
		// secret behind a JSON escape (`\u0026sig=`) is caught on the decoded
		// line (security review of the dream over records, finding 1).
		add(kindFeedback, r.ID, redactFeedback(r.Body), recordTime(r))
	}
	for _, r := range in.Trail {
		add(kindTrail, r.ID, r.Title+"\n"+r.Body, recordTime(r))
	}
	return nil
}
