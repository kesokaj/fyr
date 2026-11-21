package metrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// JSONScorecard is the structured Section 0 Scorecard in RenderJSONReport.
type JSONScorecard struct {
	Sessions                int                `json:"sessions"`
	Turns                   int                `json:"turns"`
	ToolCalls               int                `json:"tool_calls"`
	PeakContext             int64              `json:"peak_context"`
	KneeRatePct             *float64           `json:"knee_rate_pct"`
	OverTargetPct           *float64           `json:"over_target_pct"`
	SearchGrepRatio         *float64           `json:"search_grep_ratio"`
	RepeatReadsPct          *float64           `json:"repeat_reads_pct"`
	RedundantReads          int                `json:"redundant_reads"`
	BlockedLedgerSave       int                `json:"blocked_ledger_saves"`
	ClampSaves              int                `json:"clamp_saves"`
	BashReadSkips           int                `json:"bash_read_bypasses"`
	PreInvocationTimeoutPct *float64           `json:"pre_invocation_timeout_pct"`
	PreInvocationP50MS      *float64           `json:"pre_invocation_p50_ms"`
	PreInvocationP95MS      *float64           `json:"pre_invocation_p95_ms"`
	ToolErrorRatePct        *float64           `json:"tool_error_rate_pct"`
	RetryLoops              int                `json:"retry_loops"`
	GuardedDowngrades       int                `json:"guarded_downgrades"`
	Deltas                  *WindowDeltas      `json:"deltas,omitempty"`
	TopSignals              []DiagnosticSignal `json:"top_signals"`
}

// JSONAutonomy is the structured Background Autonomy panel in RenderJSONReport.
type JSONAutonomy struct {
	DreamRuns           BackgroundKindStats `json:"dream_runs"`
	AutoRuns            BackgroundKindStats `json:"auto_runs"`
	RatingsCount        int                 `json:"ratings_count"`
	MeanOutcomeScore    *float64            `json:"mean_outcome_score"`
	MeanTokenEfficiency *float64            `json:"mean_token_efficiency"`
	KeptAutoBranches    int                 `json:"kept_auto_branches"`
	ProposalsQueued     int                 `json:"proposals_queued"`
	ProposalsClosed     int                 `json:"proposals_closed"`
}

// JSONReport is the JSON payload written by RenderJSONReport and WriteJSONReport.
type JSONReport struct {
	GeneratedAt string `json:"generated_at"`
	*Summary
	Scorecard          JSONScorecard `json:"scorecard"`
	BackgroundAutonomy JSONAutonomy  `json:"background_autonomy"`
	Evals              *EvalRows     `json:"evals,omitempty"`
}

// RenderJSONReport writes the JSON usage report for sum and evals to w.
func RenderJSONReport(w io.Writer, sum *Summary, evals *EvalRows, now time.Time) error {
	if sum == nil {
		sum = &Summary{}
	}
	var ev *EvalRows
	if evals != nil && len(evals.Rows) > 0 {
		ev = evals
	}
	doc := JSONReport{
		GeneratedAt:        now.UTC().Format(time.RFC3339),
		Summary:            sum,
		Scorecard:          buildJSONScorecard(sum),
		BackgroundAutonomy: buildJSONAutonomy(sum),
		Evals:              ev,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// WriteJSONReport renders sum and evals as JSON to file (mode 0600, O_EXCL);
// an existing file is refused without overwriting.
func WriteJSONReport(sum *Summary, evals *EvalRows, file string, now time.Time) error {
	var page bytes.Buffer
	if err := RenderJSONReport(&page, sum, evals, now); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite %s", file)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(page.Bytes()); err != nil {
		f.Close()
		os.Remove(file)
		return err
	}
	return f.Close()
}

func buildJSONScorecard(s *Summary) JSONScorecard {
	sc := JSONScorecard{
		Sessions:                s.Sessions["agy"],
		Turns:                   s.B.Turns["agy"],
		ToolCalls:               s.B.ToolCalls["agy"],
		PeakContext:             s.B.PeakContext["agy"],
		SearchGrepRatio:         s.C.SearchGrepRatio["agy"],
		RedundantReads:          s.C.RedundantReads["agy"],
		BlockedLedgerSave:       s.E.LedgerSaved + s.Health.HookBlocksLedger,
		ClampSaves:              s.Health.ClampedSaves,
		BashReadSkips:           s.C.BashReadSkips["agy"],
		PreInvocationTimeoutPct: s.E.TimeoutRatePct,
		PreInvocationP50MS:      s.E.P50DurationMS,
		PreInvocationP95MS:      s.E.P95DurationMS,
		RetryLoops:              s.D.RetryLoops["agy"],
		GuardedDowngrades:       s.Health.GuardedDowngrades,
		Deltas:                  s.Deltas,
		TopSignals:              []DiagnosticSignal{},
	}
	if v, ok := kneePct(s); ok {
		sc.KneeRatePct = &v
	}
	if v, ok := overTargetPct(s); ok {
		sc.OverTargetPct = &v
	}
	if pct, _, _, ok := repeatReadsCounts(s); ok {
		sc.RepeatReadsPct = &pct
	}
	if r := s.D.ToolErrorRate["agy"]; r != nil {
		ep := round4(*r * 100.0)
		sc.ToolErrorRatePct = &ep
	}
	if s.Diagnostics != nil && len(s.Diagnostics.Signals) > 0 {
		n := min(5, len(s.Diagnostics.Signals))
		sc.TopSignals = append(sc.TopSignals, s.Diagnostics.Signals[:n]...)
	}
	return sc
}

func buildJSONAutonomy(s *Summary) JSONAutonomy {
	eff := s.A.MeanTokenEfficiency
	return JSONAutonomy{
		DreamRuns:           s.Health.DreamRuns,
		AutoRuns:            s.Health.AutoRuns,
		RatingsCount:        s.A.RatingsCount,
		MeanOutcomeScore:    s.A.MeanOutcomeScore,
		MeanTokenEfficiency: eff,
		KeptAutoBranches:    s.Health.KeptAutoBranches,
		ProposalsQueued:     s.Health.ProposalsQueued,
		ProposalsClosed:     s.Health.ProposalsClosed,
	}
}
