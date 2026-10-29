package metrics

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// Options are one ingest's inputs (spec § Verbs). Ingest reads the
// Antigravity CLI's conversations only; rows an older ingest read from Claude
// Code stay in the store as history (plan fyr D7).
type Options struct {
	Home string
	// Source is a sandbox home; Label (KEY=VALUE) is required with it, and
	// Outcome ("pass", "fail" or "") is stamped with it (D11).
	Source, Label, Outcome string
}

// Result counts one ingest.
type Result struct {
	Sessions, Steps, Events int
	Failed                  []string
}

// Line is the summary ingest prints.
func (r Result) Line() string {
	// No skipped-lines count: it belonged to the removed transcript-line
	// reader and was never incremented (SD50 of the 2026-10-27 audit).
	return fmt.Sprintf("metrics: %d sessions, %d steps, %d events added; %d sources failed",
		r.Sessions, r.Steps, r.Events, len(r.Failed))
}

// Validate is the usage check: exit 2 when it fails.
func (o Options) Validate() error {
	if o.Source != "" && o.Label == "" {
		return fmt.Errorf("--source needs --label KEY=VALUE")
	}
	if o.Label != "" {
		k, v, ok := strings.Cut(o.Label, "=")
		if !ok || k == "" || v == "" {
			return fmt.Errorf("--label %q: want KEY=VALUE", o.Label)
		}
	}
	if o.Outcome != "" && o.Outcome != "pass" && o.Outcome != "fail" {
		return fmt.Errorf("--outcome %q: want pass or fail", o.Outcome)
	}
	if (o.Outcome != "" || o.Label != "") && o.Source == "" {
		return fmt.Errorf("--label and --outcome are for a --source run")
	}
	return nil
}

func (o Options) agyRoot() string {
	if o.Source != "" {
		return filepath.Join(o.Source, ".gemini", "antigravity-cli")
	}
	return filepath.Join(o.Home, ".gemini", "antigravity-cli")
}

// dreamWork is the dream's working directory in every form for the home
// whose store is read and for --home, when they differ: a conversation there
// is the dream's own, never a session (visible dream plan D5, D15).
func (o Options) dreamWork() []string {
	out := harness.DreamWorkPaths(o.Home)
	if o.Source != "" {
		out = append(out, harness.DreamWorkPaths(o.Source)...)
	}
	return out
}

// Ingest reads every transcript the options name into s. A source that fails
// is named in Result.Failed and the rest are still read; the error is only for
// a store that cannot be written at all.
func Ingest(s *Store, o Options, log io.Writer) (Result, error) {
	var r Result
	before, err := s.counts()
	if err != nil {
		return r, err
	}
	s.ingestAgy(o, &r, log)
	// A kit learns its run's outcome after the run (accept.py): the label's
	// sessions with no outcome yet get this one, though no transcript changed.
	// An earlier run, stamped after its own acceptance, keeps its outcome.
	if o.Outcome != "" {
		if _, err := s.db.Exec(`UPDATE sessions SET outcome = ? WHERE label = ? AND outcome IS NULL`, o.Outcome, o.Label); err != nil {
			r.Failed = append(r.Failed, "outcome")
			fmt.Fprintln(log, printable(fmt.Sprintf("metrics: outcome: %v", err)))
		}
	}
	// Events come from the real home's projects and its global feedback
	// records, also for a --source run: its sessions' project roots are where
	// git and the trail are.
	notes, err := s.joinEvents(o.Home)
	if err != nil {
		r.Failed = append(r.Failed, "events")
		fmt.Fprintln(log, printable(fmt.Sprintf("metrics: events: %v", err)))
	}
	for _, n := range notes {
		fmt.Fprintln(log, printable("metrics: note: "+n))
	}
	after, err := s.counts()
	if err != nil {
		return r, err
	}
	r.Sessions, r.Steps, r.Events = after[0]-before[0], after[1]-before[1], after[2]-before[2]
	return r, nil
}

func (s *Store) counts() ([3]int, error) {
	var c [3]int
	for i, t := range []string{"sessions", "steps", "events"} {
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + t).Scan(&c[i]); err != nil {
			return c, err
		}
	}
	return c, nil
}

// printable is search's filter (BUG-152: by Unicode class, so control,
// format, line- and paragraph-separator characters all become '?'): a path
// or a project root ingest prints comes from transcripts, which a --source
// sandbox writes.
func printable(s string) string { return search.Printable(s) }
