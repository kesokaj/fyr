package metrics

import ()

// Summary is `show --json` (spec § Output). A metric the CLI cannot produce
// is nil for its column and named in Notes, never 0.
type Summary struct {
	Window map[string]string `json:"window"`
	// Columns are the summary's columns: "agy", then "claude" only when the
	// window holds historical rows.
	Columns   []string                  `json:"columns"`
	Sessions  map[string]int            `json:"sessions"`
	Subagents map[string]map[string]int `json:"subagents"`
	A         Outcome                   `json:"A"`
	B         Efficiency                `json:"B"`
	C         Adherence                 `json:"C"`
	D         Reliability               `json:"D"`
	E         SecondBrain               `json:"E"`
	F         WorkSummary               `json:"F"`
	Projects  []ProjectRow              `json:"projects,omitempty"`
	Daily     []Day                     `json:"daily"`
	Notes     []string                  `json:"notes"`

	accs map[string]*acc
	// seen holds the vendor and id of every tool call and message already
	// counted: a resumed or forked session replays earlier history into a
	// new transcript, and the replay is not new work.
	seen           map[string]bool
	readCalls      map[string]int
	rangedReads    map[string]int
	redundantReads map[string]int
}

type Outcome struct {
	DoDClosed      map[string]Closed `json:"dod_closed"`
	Verifier       map[string]int    `json:"verifier"`
	ReviewFindings map[string]int    `json:"review_findings"`
	EscapedBugs    int               `json:"escaped_bugs"`
	Corrections    int               `json:"corrections"`
	PassK          map[string]PassK  `json:"pass_k"`
}

type Closed struct {
	Closed    int `json:"closed"`
	Ambiguous int `json:"ambiguous"`
	Of        int `json:"of"`
}

// PassK is A6 for one label (spec § Metric definitions): the keys the
// first consumers read, k, passed and pass_k (1 when all k passed), and
// per trial (the eval-metrics plan D2, D3) n graded trials, c passed, the
// error trials, pass@k and pass^k for k in 1, 3, 5 up to n, the Wilson 95 %
// interval of c of n and flip, 1 when the label both passed and failed.
// Rates are rounded to four decimals.
type PassK struct {
	K        int                `json:"k"`
	Passed   int                `json:"passed"`
	PassK    int                `json:"pass_k"`
	N        int                `json:"n"`
	C        int                `json:"c"`
	Errors   int                `json:"errors"`
	PassAt   map[string]float64 `json:"pass_at,omitempty"`
	PassPow  map[string]float64 `json:"pass_pow,omitempty"`
	Wilson95 [2]float64         `json:"wilson95"`
	Flip     int                `json:"flip"`
}

type Tokens struct {
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type Efficiency struct {
	ToolCalls       map[string]int            `json:"tool_calls"`
	ToolCallsByTool map[string]map[string]int `json:"tool_calls_by_tool"`
	Turns           map[string]int            `json:"turns"`
	Tokens          map[string]*Tokens        `json:"tokens"`
	CostUSD         map[string]*float64       `json:"cost_usd"`
	WallMin         map[string]float64        `json:"wall_min"`
	ActiveMin       map[string]float64        `json:"active_min"`
	ModelMin        map[string]float64        `json:"model_min"`
	ToolMin         map[string]float64        `json:"tool_min"`
	WaitMin         map[string]float64        `json:"wait_min"`
	IdleMin         map[string]float64        `json:"idle_min"`
	PeakContext     map[string]int64          `json:"peak_context"`
	Compactions     map[string]*int           `json:"compactions"`
	// ContextTarget is the target the next two fields are measured against.
	ContextTarget int64 `json:"context_target"`
	// SessionsOverContextTarget counts the main sessions whose peak passed
	// the target; nil, named in Notes, for a vendor with no main session in
	// the window that carries a context value (spec B3).
	SessionsOverContextTarget map[string]*int `json:"sessions_over_context_target"`
	// SessionPeaks is every main session's peak context, largest first,
	// for the vendors that have one; no content, no path (plan D6, D16).
	SessionPeaks map[string][]SessionPeak `json:"session_peaks"`
}

// SessionPeak is one main session's peak context: the session's id, the UTC
// day it started and the largest context one of its model calls saw.
type SessionPeak struct {
	Session string `json:"session"`
	Date    string `json:"date"`
	Peak    int64  `json:"peak"`
}

type Adherence struct {
	SQLiteVerbs     map[string]int            `json:"sqlite_verbs"`
	GrepCalls       map[string]int            `json:"grep_calls"`
	SearchGrepRatio map[string]*float64       `json:"search_grep_ratio"`
	RedundantReads  map[string]int            `json:"redundant_reads"`
	RangedReadPct   map[string]*float64       `json:"ranged_read_pct"`
	Forbidden       map[string]int            `json:"forbidden"`
	FirstReads      map[string]int            `json:"first_reads"`
	Skills          map[string]map[string]int `json:"skills"`
}

type Reliability struct {
	ToolErrorRate     map[string]*float64       `json:"tool_error_rate"`
	Denials           map[string]*int           `json:"denials"`
	Interrupts        map[string]*int           `json:"interrupts"`
	RetryLoops        map[string]int            `json:"retry_loops"`
	SubagentToolCalls map[string]map[string]int `json:"subagent_tool_calls"`
}

// SecondBrain aggregates PreInvocation vetting and PreToolUse read-ledger
// savings from turn_vets (schema 6).
type SecondBrain struct {
	TurnVets      int      `json:"turn_vets"`
	MeanVetScore  *float64 `json:"mean_vet_score"`
	VetFirstTurns int      `json:"vet_first_turns"`
	BypassedTurns int      `json:"bypassed_turns"`
	PivotsSeeded  int      `json:"pivots_seeded"`
	LedgerSaved   int      `json:"ledger_saved"`
}

// WorkSummary aggregates fyr work wave, Base-Check, and cross-family Judge
// verdicts from work_waves (schema 6).
type WorkSummary struct {
	Waves           int `json:"waves"`
	Workers         int `json:"workers"`
	Passed          int `json:"passed"`
	Failed          int `json:"failed"`
	BaseCheckPassed int `json:"base_check_passed"`
	BaseCheckFailed int `json:"base_check_failed"`
	JudgePassed     int `json:"judge_passed"`
	JudgeFailed     int `json:"judge_failed"`
	JudgeFallback   int `json:"judge_fallback"`
}

// ProjectRow summarizes one project's activity for the global report's
// per-project breakdown.
type ProjectRow struct {
	Project         string   `json:"project"`
	Sessions        int      `json:"sessions"`
	ActiveMin       float64  `json:"active_min"`
	Tokens          int64    `json:"tokens"`
	SearchGrepRatio *float64 `json:"search_grep_ratio"`
	MeanVetScore    *float64 `json:"mean_vet_score"`
	Corrections     int      `json:"corrections"`
	EscapedBugs     int      `json:"escaped_bugs"`
	LastActive      string   `json:"last_active"`
}

// Day is one calendar day (UTC) of the window, per vendor: the report's
// trend charts read these.
type Day struct {
	Date        string             `json:"date"`
	Sessions    map[string]int     `json:"sessions"`
	ToolCalls   map[string]int     `json:"tool_calls"`
	Tokens      map[string]int64   `json:"tokens"`
	CostUSD     map[string]float64 `json:"cost_usd"`
	SQLiteVerbs map[string]int     `json:"sqlite_verbs"`
	GrepCalls   map[string]int     `json:"grep_calls"`
	Errors      map[string]int     `json:"errors"`
}

type sess struct {
	id, vendor, root, parent, agent, label, outcome, run string
	start, end                                           int64
	cost                                                 *float64
}

type stp struct {
	ext, kind, tool, skill, kindOf, hash string
	ts, started, ended, wait             int64
	isErr, denied                        bool
	in, out, cr, cw, ctx                 int64
	verbs                                []string
}
