package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
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

func brainDBExists(home string) bool {
	if home == "" {
		return false
	}
	_, err := os.Lstat(brain.Path(home))
	return err == nil
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
	if len(in.Feedback) == 0 && root != "" && root != search.GlobalWorkspace && brainDBExists(brainHome) {
		if _, _, err := trail.Roots(root); errors.Is(err, trail.ErrNotCheckout) {
			addNonGitFeedback(brainHome, root, since, now, add)
		}
	}
	return nil
}

func addNonGitFeedback(brainHome, root string, since, now time.Time, add func(kind, label, text string, t time.Time)) {
	recs, err := brain.List(brain.Options{Home: brainHome, Global: true}, brain.ListFilter{Kind: "feedback", Project: root})
	if err != nil {
		return
	}
	for _, r := range recs {
		t := recordTime(r)
		if t.After(since) && !t.After(now) {
			add(kindFeedback, r.ID, redactFeedback(r.Body), t)
		}
	}
}

func addOptionalPlanAndHandoff(brainHome, root string, since, upper time.Time, c *Collected, add func(kind, label, text string, t time.Time)) {
	if !brainDBExists(brainHome) || root == "" || root == search.GlobalWorkspace {
		return
	}
	seen := map[string]bool{}
	for _, in := range c.Inputs {
		if in.Kind == kindTrail {
			seen[in.Label] = true
		}
	}
	o := brain.Options{Home: brainHome, Dir: root}
	if plans, err := brain.List(o, brain.ListFilter{Kind: "plan"}); err == nil {
		addPlanContext(plans, since, upper, seen, add)
	}
	if handoffs, err := brain.List(o, brain.ListFilter{Kind: "handoff"}); err == nil {
		for _, r := range handoffs {
			if seen[r.ID] {
				break
			}
			seen[r.ID] = true
			add(kindTrail, r.ID, r.Title+"\n"+r.Body, contextTime(r, since, upper))
			break
		}
	}
}

func addPlanContext(plans []brain.Record, since, upper time.Time, seen map[string]bool, add func(kind, label, text string, t time.Time)) {
	added := 0
	for _, r := range plans {
		if r.Status != "open" || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		add(kindTrail, r.ID, r.Title+"\n"+r.Body, contextTime(r, since, upper))
		added++
		if added >= 3 {
			return
		}
	}
	if added == 0 && len(plans) > 0 && !seen[plans[0].ID] {
		seen[plans[0].ID] = true
		add(kindTrail, plans[0].ID, plans[0].Title+"\n"+plans[0].Body, contextTime(plans[0], since, upper))
	}
}

func contextTime(_ brain.Record, _, _ time.Time) time.Time {
	return time.Time{}
}

// addHiddenCouplingHints reads project-local redacted co-activity pairs with
// zero AST edge from a copy of <root>/.agents/scratch/search.db (never touching
// live search.db or writing paths to metrics.db).
func addHiddenCouplingHints(home, root string, add func(kind, label, text string, t time.Time)) {
	if root == "" || root == search.GlobalWorkspace {
		return
	}
	if err := trail.CheckScratchNoSymlinks(root); err != nil {
		return
	}
	scratchDir := filepath.Join(root, ".agents", "scratch")
	info, err := os.Lstat(filepath.Join(scratchDir, "search.db"))
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	parent := ""
	if home != "" {
		if conf := filepath.Dir(brain.Path(home)); conf != "" {
			if st, err := os.Lstat(conf); err == nil && st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
				parent = conf
			}
		}
	}
	tmp, err := os.MkdirTemp(parent, "dream-coupling-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	copied, err := search.CopyDB(scratchDir, "search.db", tmp)
	if err != nil {
		return
	}
	hints, err := search.ReadHiddenCouplingHints(copied, root, 3, 10)
	if err != nil || len(hints) == 0 {
		return
	}
	if b, err := json.MarshalIndent(hints, "", " "); err == nil {
		add(kindTrail, "hidden-coupling", string(b), time.Time{})
	}
}
