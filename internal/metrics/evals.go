package metrics

import (
	"sort"
	"strings"
	"time"
)

// EvalOptions filter `fyr metrics evals` (SPEC-fyr-metrics § Interfaces):
// the window the trials started in, and one harness key or all.
type EvalOptions struct {
	Since   time.Time // zero: 30 days before Now
	Now     time.Time // zero: time.Now()
	Harness string    // "": every key; else the trials whose harness is exactly this
}

// EvalRows is the per-row view of the suite's trials (the eval-metrics plan
// § Interfaces, `fyr metrics evals`): one line per row and row version with
// a trial in the window, then the suite. It never gates (R3).
type EvalRows struct {
	Window  map[string]string `json:"window"`
	Harness string            `json:"harness,omitempty"`
	Rows    []EvalRow         `json:"rows"`
	Suite   EvalSuite         `json:"suite"`
}

// EvalRow is one row at one version. A rate a row cannot have (no graded
// trial, pass^3 below n = 3, no session behind a mean) is absent, never a
// number; the text prints n/a or -.
type EvalRow struct {
	Row      string      `json:"row"`
	Version  int         `json:"version,omitempty"` // 0: not known at the run (before the suite's version column)
	N        int         `json:"n"`
	C        int         `json:"c"`
	Errors   int         `json:"errors"`
	PassAt1  *float64    `json:"pass_at_1,omitempty"`
	PassPow3 *float64    `json:"pass_pow_3,omitempty"`
	Wilson95 *[2]float64 `json:"wilson95,omitempty"`
	Flip     int         `json:"flip"`
	Means    *EvalMeans  `json:"means,omitempty"`
	Last     string      `json:"last"`  // the UTC day of the newest trial in the window
	Flags    []string    `json:"flags"` // saturated, broken, unstable or history; empty for none
}

// EvalMeans are the efficiency numbers of a row's trials, each a mean over
// their main sessions; ToolErrorRate is pooled over the tool calls, and
// absent with none; SessionSeconds is over the sessions that carry both
// ends.
type EvalMeans struct {
	Sessions       int      `json:"sessions"`
	Turns          float64  `json:"turns"`
	ToolCalls      float64  `json:"tool_calls"`
	TokensIn       float64  `json:"tokens_in"`
	TokensOut      float64  `json:"tokens_out"`
	TokensCache    float64  `json:"tokens_cache"`
	SessionSeconds *float64 `json:"session_seconds,omitempty"`
	ActiveSeconds  float64  `json:"active_seconds"`
	ToolErrorRate  *float64 `json:"tool_error_rate,omitempty"`
	Retries        float64  `json:"retries"`
	PeakContext    float64  `json:"peak_context"`
}

// EvalSuite is the suite over the rows with a graded trial: the pass rate
// is the mean of each row's c/n with its Wilson 95 % interval over rows,
// C_out Rabanser 2026's outcome consistency, and the trajectory similarity
// the mean over rows with two or more main sessions of their pairwise
// SeqSimilarity.
type EvalSuite struct {
	Rows             int         `json:"rows"`
	Trials           int         `json:"trials"`
	Errors           int         `json:"errors"`
	PassRate         *float64    `json:"pass_rate,omitempty"`
	Wilson95         *[2]float64 `json:"wilson95,omitempty"`
	COut             *float64    `json:"c_out,omitempty"`
	Similarity       *float64    `json:"trajectory_similarity,omitempty"`
	SimilarityOfRows int         `json:"trajectory_rows"`
}

// evalPrefix is the label every suite row's trials and sessions carry.
const evalPrefix = "eval="

// rowKey is a row at one version, the view's line.
type rowKey struct {
	label   string
	version int
}

// evalTrial is a trial as the view reads it, or a session no trial claims
// (stored before migration 4), which counts as one trial under harness
// unknown with itself as its only session.
type evalTrial struct {
	key          rowKey
	run, harness string
	status       string
	start        int64
	sessionID    string // the unclaimed session, "" for a trial
}

// Evals computes the per-row view for the options' window.
func (s *Store) Evals(o EvalOptions) (*EvalRows, error) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	since := o.Since
	if since.IsZero() {
		since = now.AddDate(0, 0, -30)
	}
	all, err := s.evalTrials()
	if err != nil {
		return nil, err
	}
	flags := historyFlags(all)
	out := &EvalRows{Window: map[string]string{"since": since.UTC().Format("2006-01-02"), "until": now.UTC().Format("2006-01-02")},
		Harness: o.Harness, Rows: []EvalRow{}}
	lines := map[rowKey]*rowAcc{}
	for _, t := range all {
		if t.start < since.UnixMilli() || t.start >= now.UnixMilli() || (o.Harness != "" && t.harness != o.Harness) {
			continue
		}
		a := lines[t.key]
		if a == nil {
			a = &rowAcc{seen: map[string]bool{}}
			lines[t.key] = a
		}
		a.count(t)
	}
	if err := s.addSessions(lines); err != nil {
		return nil, err
	}
	keys := make([]rowKey, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].label != keys[j].label {
			return keys[i].label < keys[j].label
		}
		return keys[i].version < keys[j].version
	})
	for _, k := range keys {
		out.Rows = append(out.Rows, lines[k].row(k, flags[k]))
	}
	out.Suite = suiteOf(out.Rows, lines, keys)
	return out, nil
}

// evalTrials reads every suite trial in the store, then every main session
// of a suite label with an outcome that no trial claims, whatever the
// window: the flags read the whole history.
func (s *Store) evalTrials() ([]evalTrial, error) {
	claimed, err := s.trialRuns()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT label, COALESCE(row_version, 0), run, harness, status, started_at
		FROM trials WHERE substr(label, 1, length(?)) = ? ORDER BY started_at, run`, evalPrefix, evalPrefix)
	if err != nil {
		return nil, err
	}
	var out []evalTrial
	for rows.Next() {
		var t evalTrial
		if err := rows.Scan(&t.key.label, &t.key.version, &t.run, &t.harness, &t.status, &t.start); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sr, err := s.db.Query(`SELECT id, label, outcome, COALESCE(run, ''), COALESCE(started_at, ended_at, 0) FROM sessions
		WHERE parent_id IS NULL AND outcome IS NOT NULL AND outcome != '' AND substr(label, 1, length(?)) = ?
		ORDER BY started_at, id`, evalPrefix, evalPrefix)
	if err != nil {
		return nil, err
	}
	defer sr.Close()
	for sr.Next() {
		var t evalTrial
		if err := sr.Scan(&t.sessionID, &t.key.label, &t.status, &t.run, &t.start); err != nil {
			return nil, err
		}
		if t.run != "" && claimed[t.run] {
			continue
		}
		t.harness = "unknown"
		out = append(out, t)
	}
	return out, sr.Err()
}

// keyCount is one harness key's graded trials of a row and the passed ones.
type keyCount struct{ n, c int }

// knownCounts is each row's graded trials per harness key other than
// unknown, and every row with a graded trial under any key.
func knownCounts(all []evalTrial) (known map[rowKey]map[string]*keyCount, graded map[rowKey]bool) {
	known, graded = map[rowKey]map[string]*keyCount{}, map[rowKey]bool{}
	for _, t := range all {
		if t.status != "pass" && t.status != "fail" {
			continue
		}
		graded[t.key] = true
		if t.harness == "unknown" {
			continue
		}
		if known[t.key] == nil {
			known[t.key] = map[string]*keyCount{}
		}
		c := known[t.key][t.harness]
		if c == nil {
			c = &keyCount{}
			known[t.key][t.harness] = c
		}
		c.n++
		if t.status == "pass" {
			c.c++
		}
	}
	return known, graded
}

// historyFlags is each row's flags from its graded trials under a known
// harness key, over the whole store (PLAN.md D16): unstable when one key
// both passed and failed; broken when none passed; saturated when none
// failed and two keys have three or more; history when the row has no
// graded trial under a known key at all.
func historyFlags(all []evalTrial) map[rowKey][]string {
	known, graded := knownCounts(all)
	out := map[rowKey][]string{}
	for k := range graded {
		keys := known[k]
		if len(keys) == 0 {
			out[k] = []string{"history"}
			continue
		}
		full, passed, failed, unstable := 0, 0, false, false
		for _, c := range keys {
			passed += c.c
			failed = failed || c.c < c.n
			unstable = unstable || (0 < c.c && c.c < c.n)
			if c.c == c.n && c.n >= 3 {
				full++
			}
		}
		switch {
		case unstable:
			out[k] = []string{"unstable"}
		case passed == 0:
			out[k] = []string{"broken"}
		case !failed && full >= 2:
			out[k] = []string{"saturated"}
		}
	}
	return out
}

// rowName is a label's row id, the label without its eval= prefix.
func rowName(label string) string { return strings.TrimPrefix(label, evalPrefix) }
