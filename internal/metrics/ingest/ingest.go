package ingest

import (
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const trialTextLimit = 120

// Options are one ingest's inputs (spec § Verbs). Ingest reads the
// Antigravity CLI's conversations only; rows an older ingest read from Claude
// Code stay in the store as history (plan fyr D7).
type Options struct {
	Home string
	// Source is a sandbox home; Label (KEY=VALUE) is required with it, and
	// Outcome ("pass", "fail", "error" or "") is stamped with it (D11).
	Source, Label, Outcome string
	// Run is the trial the Source's sessions belong to (trials.run), written
	// on each session at insert and never changed after; "" leaves it NULL.
	Run string
	// BrainHome is an optional alternate home whose brain.db is also read when
	// joining trail and feedback events (F-012).
	BrainHome string
}

// Result counts one ingest.
type Result struct {
	Sessions, Steps, Events, TurnVets int
	Failed                            []string
}

// Deps provides store handles and parent-package callbacks without cycles.
type Deps struct {
	DB                      *sql.DB
	StorePath               string
	MaxSchemaVersion        int
	ResolveRepoID           func(string) string
	ResolveMetricsHome      func(home, storePath string) string
	ResolveWorktreeRoots    func(convs []AgyConv, db *sql.DB, changed map[string]bool)
	IsAgyWorktreePath       func(string) bool
	IsUsageProbeSummary     func(root, title, preview string) bool
	JoinEventsWithBrainHome func(home, brainHome string) ([]string, error)
	IsRealDirChain          func(base string, parts ...string) bool
}

// Line is the summary ingest prints.
func (r Result) Line() string {
	if r.TurnVets > 0 {
		return fmt.Sprintf("metrics: %d sessions, %d steps, %d events, %d turn_vets added; %d sources failed",
			r.Sessions, r.Steps, r.Events, r.TurnVets, len(r.Failed))
	}
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
	if o.Outcome != "" && o.Outcome != "pass" && o.Outcome != "fail" && o.Outcome != "error" {
		return fmt.Errorf("--outcome %q: want pass, fail or error", o.Outcome)
	}
	if (o.Outcome != "" || o.Label != "") && o.Source == "" {
		return fmt.Errorf("--label and --outcome are for a --source run")
	}
	return o.validateRun()
}

func (o Options) validateRun() error {
	switch {
	case o.Run == "":
		return nil
	case o.Source == "":
		return fmt.Errorf("run is for a --source run")
	case search.Printable(o.Run) != o.Run || len(o.Run) > trialTextLimit:
		return fmt.Errorf("run %q: not printable or over %d bytes", search.Printable(o.Run), trialTextLimit)
	}
	return nil
}

func (o Options) agyRoot() string {
	if o.Source != "" {
		return filepath.Join(o.Source, ".gemini", "antigravity-cli")
	}
	return filepath.Join(o.Home, ".gemini", "antigravity-cli")
}

func (o Options) dreamWork() []string {
	out := harness.DreamWorkPaths(o.Home)
	if o.Source != "" {
		out = append(out, harness.DreamWorkPaths(o.Source)...)
	}
	return out
}

// Ingest reads every transcript the options name into deps.DB.
func Ingest(deps Deps, o Options, log io.Writer) (Result, error) {
	var r Result
	if err := o.validateRun(); err != nil {
		return r, fmt.Errorf("metrics: %w", err)
	}
	before, err := deps.counts()
	if err != nil {
		return r, err
	}
	deps.ingestAgy(o, &r, log)
	if o.Source != "" && o.Label != "" && o.Run == "" {
		deps.ingestDevHome(o, &r, log)
	}
	deps.stampOutcome(o, &r, log)
	deps.joinEvents(o, &r, log)
	if o.Source == "" {
		deps.ingestSecondBrainAndWaves(o.Home, log)
	} else {
		deps.ingestSourceSecondBrain(o, log)
	}
	after, err := deps.counts()
	if err != nil {
		return r, err
	}
	r.Sessions, r.Steps, r.Events, r.TurnVets = after[0]-before[0], after[1]-before[1], after[2]-before[2], after[3]-before[3]
	return r, nil
}

func (d Deps) stampOutcome(o Options, r *Result, log io.Writer) {
	if o.Outcome == "" {
		return
	}
	if _, err := d.DB.Exec(`UPDATE sessions SET outcome = ? WHERE label = ? AND outcome IS NULL`, o.Outcome, o.Label); err != nil {
		r.Failed = append(r.Failed, "outcome")
		fmt.Fprintln(log, search.Printable(fmt.Sprintf("metrics: outcome: %v", err)))
	}
}

func (d Deps) joinEvents(o Options, r *Result, log io.Writer) {
	if d.JoinEventsWithBrainHome == nil {
		return
	}
	notes, err := d.JoinEventsWithBrainHome(o.Home, o.BrainHome)
	if err != nil {
		r.Failed = append(r.Failed, "events")
		fmt.Fprintln(log, search.Printable(fmt.Sprintf("metrics: events: %v", err)))
	}
	for _, n := range notes {
		fmt.Fprintln(log, search.Printable("metrics: note: "+n))
	}
}

func (d Deps) counts() ([4]int, error) {
	var c [4]int
	for i, t := range []string{"sessions", "steps", "events"} {
		if err := d.DB.QueryRow(`SELECT count(*) FROM ` + t).Scan(&c[i]); err != nil {
			return c, err
		}
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM turn_vets`).Scan(&c[3]); err != nil && !strings.Contains(err.Error(), "no such table") {
		return c, err
	}
	return c, nil
}

func (d Deps) salt() string {
	var v string
	_ = d.DB.QueryRow(`SELECT value FROM meta WHERE key = 'salt'`).Scan(&v)
	return v
}

func (d Deps) resolveRepoID(root string) string {
	if d.ResolveRepoID == nil {
		return ""
	}
	return d.ResolveRepoID(root)
}

func clipIngestText(s string, maxLen int) string {
	return trail.TruncateUTF8(search.Printable(strings.TrimSpace(s)), maxLen)
}

func metricsStorePath(home string) string {
	return filepath.Join(home, harness.StateDir, "metrics.db")
}
