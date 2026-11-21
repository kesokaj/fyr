package metrics

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

// SanitizedMetricsExport carries portable, secret-free metrics rows filtered by
// repo_id and optional since window (ADR-531). It never includes meta.salt,
// steps.cmd_hash, ingest_state, or host filesystem project_root paths.
type SanitizedMetricsExport struct {
	Schema         int                `json:"schema"`
	RepoID         string             `json:"repo_id,omitempty"`
	SinceMS        int64              `json:"since_ms,omitempty"`
	Sessions       []SanitizedSession `json:"sessions,omitempty"`
	Steps          []SanitizedStep    `json:"steps,omitempty"`
	Events         []SanitizedEvent   `json:"events,omitempty"`
	Trials         []SanitizedTrial   `json:"trials,omitempty"`
	TurnVets       []SanitizedTurnVet `json:"turn_vets,omitempty"`
	SessionRatings []SessionRating    `json:"session_ratings,omitempty"`
	JudgeVerdicts  []JudgeVerdictRow  `json:"judge_verdicts,omitempty"`
	BackgroundRuns []BackgroundRunRow `json:"background_runs,omitempty"`
}

// SanitizedSession is one portable sessions row without local project_root.
type SanitizedSession struct {
	ID         string   `json:"id"`
	Vendor     string   `json:"vendor,omitempty"`
	RepoID     string   `json:"repo_id,omitempty"`
	ParentID   string   `json:"parent_id,omitempty"`
	AgentType  string   `json:"agent_type,omitempty"`
	Model      string   `json:"model,omitempty"`
	Label      string   `json:"label,omitempty"`
	Outcome    string   `json:"outcome,omitempty"`
	Run        string   `json:"run,omitempty"`
	StartedAt  *int64   `json:"started_at,omitempty"`
	EndedAt    *int64   `json:"ended_at,omitempty"`
	CostUSD    *float64 `json:"cost_usd,omitempty"`
	DurationMS *int64   `json:"duration_ms,omitempty"`
}

// SanitizedStep is one portable steps row with cmd_hash stripped.
type SanitizedStep struct {
	SessionID  string `json:"session_id"`
	ExtID      string `json:"ext_id"`
	TS         *int64 `json:"ts,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Tool       string `json:"tool,omitempty"`
	Skill      string `json:"skill,omitempty"`
	TargetKind string `json:"target_kind,omitempty"`
	IsError    *int64 `json:"is_error,omitempty"`
	Denied     *int64 `json:"denied,omitempty"`
	TokIn      *int64 `json:"tok_in,omitempty"`
	TokOut     *int64 `json:"tok_out,omitempty"`
	CacheRead  *int64 `json:"cache_read,omitempty"`
	CacheWrite *int64 `json:"cache_write,omitempty"`
	Context    *int64 `json:"context,omitempty"`
	StartedMS  *int64 `json:"started_ms,omitempty"`
	EndedMS    *int64 `json:"ended_ms,omitempty"`
	WaitMS     *int64 `json:"wait_ms,omitempty"`
}

// SanitizedEvent is one portable events row.
type SanitizedEvent struct {
	SessionID string `json:"session_id"`
	TS        int64  `json:"ts"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Value     string `json:"value,omitempty"`
}

// SanitizedTrial is one portable trials row.
type SanitizedTrial struct {
	Run            string `json:"run"`
	Label          string `json:"label"`
	RowVersion     int    `json:"row_version,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Cluster        string `json:"cluster,omitempty"`
	Expect         string `json:"expect,omitempty"`
	Harness        string `json:"harness"`
	CLI            string `json:"cli,omitempty"`
	Status         string `json:"status"`
	Cause          string `json:"cause"`
	StartedAt      int64  `json:"started_at"`
	Seconds        int64  `json:"seconds"`
	SessionSeconds *int64 `json:"session_seconds,omitempty"`
	SetupSeconds   *int64 `json:"setup_seconds,omitempty"`
	CheckSeconds   *int64 `json:"check_seconds,omitempty"`
}

// SanitizedTurnVet is one portable turn_vets row without local project_root.
type SanitizedTurnVet struct {
	RepoID            string `json:"repo_id,omitempty"`
	ConversationID    string `json:"conversation_id"`
	TurnNum           int    `json:"turn_num"`
	Intent            string `json:"intent"`
	Gate              string `json:"gate"`
	Score             int    `json:"score"`
	Bypassed          int    `json:"bypassed,omitempty"`
	SkillsCount       int    `json:"skills_count,omitempty"`
	PivotsCount       int    `json:"pivots_count,omitempty"`
	LedgerSaved       int    `json:"ledger_saved,omitempty"`
	Directive         string `json:"directive,omitempty"`
	SubtaskCount      int    `json:"subtask_count,omitempty"`
	DurationMS        int    `json:"duration_ms,omitempty"`
	TimedOut          int    `json:"timed_out,omitempty"`
	InjectedBytes     int    `json:"injected_bytes,omitempty"`
	PivotsUsedCount   int    `json:"pivots_used_count,omitempty"`
	SkillsLoadedCount int    `json:"skills_loaded_count,omitempty"`
	CreatedAt         int64  `json:"created_at"`
}

// ExportSanitizedState exports portable metrics rows from s, stripping salts,
// command hashes, secrets, and local paths, filtered by repoID and optional since.
func ExportSanitizedState(s *Store, repoID string, since time.Time) (SanitizedMetricsExport, error) {
	if s == nil || s.db == nil {
		return SanitizedMetricsExport{}, errors.New("metrics: nil store")
	}
	repoID = strings.TrimSpace(repoID)
	var sinceMS int64
	if !since.IsZero() {
		sinceMS = since.UnixMilli()
	}
	out := SanitizedMetricsExport{Schema: len(migrations), RepoID: repoID, SinceMS: sinceMS}
	sess, sids, err := exportSanitizedSessions(s.db, repoID, sinceMS)
	if err != nil {
		return SanitizedMetricsExport{}, err
	}
	out.Sessions = sess
	if out.Steps, err = exportSanitizedSteps(s.db, sids); err != nil {
		return SanitizedMetricsExport{}, err
	}
	if out.Events, err = exportSanitizedEvents(s.db, sids); err != nil {
		return SanitizedMetricsExport{}, err
	}
	return populateRemainingExport(&out, s, repoID, since, sinceMS)
}

func populateRemainingExport(out *SanitizedMetricsExport, s *Store, repoID string, since time.Time, sinceMS int64) (SanitizedMetricsExport, error) {
	var err error
	if out.Trials, err = exportSanitizedTrials(s.db, repoID, sinceMS); err != nil {
		return SanitizedMetricsExport{}, err
	}
	if out.TurnVets, err = exportSanitizedTurnVets(s.db, repoID, sinceMS); err != nil {
		return SanitizedMetricsExport{}, err
	}
	if out.SessionRatings, err = exportSanitizedRatings(s.db, repoID, sinceMS); err != nil {
		return SanitizedMetricsExport{}, err
	}
	if out.JudgeVerdicts, err = exportSanitizedVerdicts(s.db, repoID, sinceMS); err != nil {
		return SanitizedMetricsExport{}, err
	}
	if repoID == "" {
		runs, err := s.QueryBackgroundRuns(since, time.Time{})
		if err != nil {
			return SanitizedMetricsExport{}, err
		}
		for i := range runs {
			runs[i].ID = 0
		}
		out.BackgroundRuns = runs
	}
	return *out, nil
}

func exportSanitizedSessions(db *sql.DB, repoID string, sinceMS int64) ([]SanitizedSession, map[string]bool, error) {
	var conds []string
	var args []any
	if repoID != "" {
		conds = append(conds, `repo_id = ?`)
		args = append(args, repoID)
	}
	if sinceMS > 0 {
		conds = append(conds, `(COALESCE(ended_at, started_at, 0) >= ? OR (run IS NOT NULL AND run IN (SELECT run FROM trials WHERE started_at >= ?)))`)
		args = append(args, sinceMS, sinceMS)
	}
	q := `SELECT id, COALESCE(vendor, ''), COALESCE(repo_id, ''), COALESCE(parent_id, ''), COALESCE(agent_type, ''), COALESCE(model, ''), COALESCE(label, ''), COALESCE(outcome, ''), COALESCE(run, ''), started_at, ended_at, cost_usd, duration_ms FROM sessions`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY id`
	return scanSanitizedSessions(db, q, args)
}

func scanSanitizedSessions(db *sql.DB, q string, args []any) ([]SanitizedSession, map[string]bool, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []SanitizedSession
	sids := map[string]bool{}
	for rows.Next() {
		var r SanitizedSession
		var st, en, dur sql.NullInt64
		var cost sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.Vendor, &r.RepoID, &r.ParentID, &r.AgentType, &r.Model, &r.Label, &r.Outcome, &r.Run, &st, &en, &cost, &dur); err != nil {
			return nil, nil, err
		}
		r.ID = clipSanitizedText(r.ID, 128)
		if r.ID == "" {
			continue
		}
		r.Vendor = clipSanitizedText(r.Vendor, 32)
		r.ParentID = clipSanitizedText(r.ParentID, 128)
		r.AgentType = clipSanitizedText(r.AgentType, 64)
		r.Model = clipSanitizedText(r.Model, 128)
		r.Label = clipSanitizedText(r.Label, trialText)
		r.Outcome = clipSanitizedText(r.Outcome, 32)
		r.Run = clipSanitizedText(r.Run, trialText)
		r.StartedAt, r.EndedAt, r.DurationMS = ptrInt64(st), ptrInt64(en), ptrInt64(dur)
		if cost.Valid {
			v := cost.Float64
			r.CostUSD = &v
		}
		sids[r.ID] = true
		out = append(out, r)
	}
	return out, sids, rows.Err()
}

func exportSanitizedSteps(db *sql.DB, sids map[string]bool) ([]SanitizedStep, error) {
	if len(sids) == 0 {
		return nil, nil
	}
	rows, err := db.Query(`SELECT session_id, ext_id, ts, COALESCE(kind, ''), COALESCE(tool, ''), COALESCE(skill, ''), COALESCE(target_kind, ''), is_error, denied, tok_in, tok_out, cache_read, cache_write, context, started_ms, ended_ms, wait_ms FROM steps ORDER BY session_id, ext_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SanitizedStep
	for rows.Next() {
		var r SanitizedStep
		var ts, isErr, den, tIn, tOut, cR, cW, ctx, sMS, eMS, wMS sql.NullInt64
		if err := rows.Scan(&r.SessionID, &r.ExtID, &ts, &r.Kind, &r.Tool, &r.Skill, &r.TargetKind, &isErr, &den, &tIn, &tOut, &cR, &cW, &ctx, &sMS, &eMS, &wMS); err != nil {
			return nil, err
		}
		if !sids[r.SessionID] {
			continue
		}
		r.SessionID = clipSanitizedText(r.SessionID, 128)
		r.ExtID = clipSanitizedText(r.ExtID, 160)
		r.Kind = clipSanitizedText(r.Kind, 64)
		r.Tool = clipSanitizedText(r.Tool, 64)
		r.Skill = clipSanitizedText(r.Skill, 128)
		r.TargetKind = clipSanitizedText(r.TargetKind, 64)
		r.TS, r.IsError, r.Denied = ptrInt64(ts), ptrInt64(isErr), ptrInt64(den)
		r.TokIn, r.TokOut, r.CacheRead, r.CacheWrite = ptrInt64(tIn), ptrInt64(tOut), ptrInt64(cR), ptrInt64(cW)
		r.Context, r.StartedMS, r.EndedMS, r.WaitMS = ptrInt64(ctx), ptrInt64(sMS), ptrInt64(eMS), ptrInt64(wMS)
		out = append(out, r)
	}
	return out, rows.Err()
}

func exportSanitizedEvents(db *sql.DB, sids map[string]bool) ([]SanitizedEvent, error) {
	if len(sids) == 0 {
		return nil, nil
	}
	rows, err := db.Query(`SELECT session_id, COALESCE(ts, 0), COALESCE(kind, ''), COALESCE(ref, ''), COALESCE(value, '') FROM events ORDER BY session_id, kind, ref`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SanitizedEvent
	for rows.Next() {
		var r SanitizedEvent
		if err := rows.Scan(&r.SessionID, &r.TS, &r.Kind, &r.Ref, &r.Value); err != nil {
			return nil, err
		}
		if !sids[r.SessionID] {
			continue
		}
		r.Kind = clipSanitizedText(r.Kind, 64)
		r.Ref = clipSanitizedText(r.Ref, 256)
		r.Value = clipSanitizedText(r.Value, 256)
		out = append(out, r)
	}
	return out, rows.Err()
}

func exportSanitizedTrials(db *sql.DB, repoID string, sinceMS int64) ([]SanitizedTrial, error) {
	q := `SELECT DISTINCT t.run, t.label, COALESCE(t.row_version, 0), COALESCE(t.kind, ''), COALESCE(t.cluster, ''), COALESCE(t.expect, ''), t.harness, COALESCE(t.cli, ''), t.status, t.cause, t.started_at, t.seconds, t.session_seconds, t.setup_seconds, t.check_seconds FROM trials t`
	var conds []string
	var args []any
	if repoID != "" {
		q += ` JOIN sessions s ON s.run = t.run`
		conds = append(conds, `s.repo_id = ?`)
		args = append(args, repoID)
	}
	if sinceMS > 0 {
		conds = append(conds, `t.started_at >= ?`)
		args = append(args, sinceMS)
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY t.started_at, t.run`
	return scanSanitizedTrials(db, q, args)
}

func scanSanitizedTrials(db *sql.DB, q string, args []any) ([]SanitizedTrial, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SanitizedTrial
	for rows.Next() {
		var r SanitizedTrial
		var sesS, setS, chkS sql.NullInt64
		if err := rows.Scan(&r.Run, &r.Label, &r.RowVersion, &r.Kind, &r.Cluster, &r.Expect, &r.Harness, &r.CLI, &r.Status, &r.Cause, &r.StartedAt, &r.Seconds, &sesS, &setS, &chkS); err != nil {
			return nil, err
		}
		r.Run = clipSanitizedText(r.Run, trialText)
		r.Label = clipSanitizedText(r.Label, trialText)
		r.Kind = clipSanitizedText(r.Kind, 64)
		r.Cluster = clipSanitizedText(r.Cluster, 128)
		r.Expect = clipSanitizedText(r.Expect, trialText)
		r.Harness = clipSanitizedText(r.Harness, trialText)
		r.CLI = clipSanitizedText(r.CLI, trialText)
		r.Status = clipSanitizedText(r.Status, 32)
		r.Cause = clipSanitizedText(r.Cause, 256)
		r.SessionSeconds, r.SetupSeconds, r.CheckSeconds = ptrInt64(sesS), ptrInt64(setS), ptrInt64(chkS)
		if r.Run != "" {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func exportSanitizedTurnVets(db *sql.DB, repoID string, sinceMS int64) ([]SanitizedTurnVet, error) {
	var conds []string
	var args []any
	if repoID != "" {
		conds = append(conds, `repo_id = ?`)
		args = append(args, repoID)
	}
	if sinceMS > 0 {
		tsExpr := `(CASE WHEN created_at >= 100000000 AND created_at < 100000000000 THEN created_at * 1000 ELSE created_at END)`
		conds = append(conds, tsExpr+` >= ?`)
		args = append(args, sinceMS)
	}
	q := `SELECT COALESCE(repo_id, ''), conversation_id, turn_num, intent, gate, score, bypassed, skills_count, pivots_count, ledger_saved, COALESCE(directive, ''), COALESCE(subtask_count, 0), COALESCE(duration_ms, 0), COALESCE(timed_out, 0), COALESCE(injected_bytes, 0), COALESCE(pivots_used_count, 0), COALESCE(skills_loaded_count, 0), created_at FROM turn_vets`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY conversation_id, turn_num`
	return scanSanitizedTurnVets(db, q, args)
}

func scanSanitizedTurnVets(db *sql.DB, q string, args []any) ([]SanitizedTurnVet, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SanitizedTurnVet
	for rows.Next() {
		var r SanitizedTurnVet
		if err := rows.Scan(&r.RepoID, &r.ConversationID, &r.TurnNum, &r.Intent, &r.Gate, &r.Score, &r.Bypassed, &r.SkillsCount, &r.PivotsCount, &r.LedgerSaved, &r.Directive, &r.SubtaskCount, &r.DurationMS, &r.TimedOut, &r.InjectedBytes, &r.PivotsUsedCount, &r.SkillsLoadedCount, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.ConversationID = clipSanitizedText(r.ConversationID, 128)
		r.Intent = clipSanitizedText(r.Intent, 64)
		r.Gate = clipSanitizedText(r.Gate, 64)
		r.Directive = clipSanitizedText(r.Directive, 128)
		if r.ConversationID != "" {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func exportSanitizedRatings(db *sql.DB, repoID string, sinceMS int64) ([]SessionRating, error) {
	var conds []string
	var args []any
	if repoID != "" {
		conds = append(conds, `(repo_id = ? OR (repo_id = '' AND session_id IN (SELECT id FROM sessions WHERE repo_id = ?)))`)
		args = append(args, repoID, repoID)
	}
	if sinceMS > 0 {
		conds = append(conds, evalTimeMSExpr+` >= ?`)
		args = append(args, sinceMS)
	}
	q := `SELECT session_id, project_root, COALESCE(repo_id, ''), evaluated_at, outcome_score, token_efficiency, plan_alignment, skill_action, target_skill, summary FROM session_ratings`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY session_id`
	return scanSanitizedRatings(db, q, args, repoID)
}

func scanSanitizedRatings(db *sql.DB, q string, args []any, filterRepoID string) ([]SessionRating, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRating
	for rows.Next() {
		var r SessionRating
		if err := rows.Scan(&r.SessionID, &r.ProjectRoot, &r.RepoID, &r.EvaluatedAt, &r.OutcomeScore, &r.TokenEfficiency, &r.PlanAlignment, &r.SkillAction, &r.TargetSkill, &r.Summary); err != nil {
			return nil, err
		}
		if r.ProjectRoot != search.GlobalWorkspace {
			r.ProjectRoot = ""
		}
		if r.RepoID == "" && filterRepoID != "" {
			r.RepoID = filterRepoID
		}
		r.SessionID = clipSanitizedText(r.SessionID, 128)
		if r.SessionID == "" {
			continue
		}
		r.SkillAction = clipSanitizedText(r.SkillAction, 64)
		r.TargetSkill = clipSanitizedText(r.TargetSkill, 200)
		r.Summary = clipSanitizedText(r.Summary, 400)
		out = append(out, r)
	}
	return out, rows.Err()
}

func exportSanitizedVerdicts(db *sql.DB, repoID string, sinceMS int64) ([]JudgeVerdictRow, error) {
	var conds []string
	var args []any
	if repoID != "" {
		conds = append(conds, `repo_id = ?`)
		args = append(args, repoID)
	}
	if sinceMS > 0 {
		conds = append(conds, verdictTSMSExpr+` >= ?`)
		args = append(args, sinceMS)
	}
	q := `SELECT ts, COALESCE(repo_id, ''), target, model, COALESCE(worker_model, ''), COALESCE(worker_session_id, ''), fallback_reason, criterion_hash, verdict FROM judge_verdicts`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY ts, id`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JudgeVerdictRow
	for rows.Next() {
		var r JudgeVerdictRow
		if err := rows.Scan(&r.TS, &r.RepoID, &r.Target, &r.Model, &r.WorkerModel, &r.WorkerSessionID, &r.FallbackReason, &r.CriterionHash, &r.Verdict); err != nil {
			return nil, err
		}
		r.Target = clipSanitizedText(r.Target, 256)
		r.Model = clipSanitizedText(r.Model, 128)
		r.WorkerModel = clipSanitizedText(r.WorkerModel, 128)
		r.WorkerSessionID = clipSanitizedText(r.WorkerSessionID, 128)
		r.FallbackReason = clipSanitizedText(r.FallbackReason, 400)
		r.Verdict = clipSanitizedText(r.Verdict, 32)
		out = append(out, r)
	}
	return out, rows.Err()
}

func ptrInt64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
