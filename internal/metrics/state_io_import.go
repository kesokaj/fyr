package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// StateImportCounts summarizes rows merged by ImportSanitizedState.
type StateImportCounts struct {
	Sessions       int `json:"sessions"`
	Steps          int `json:"steps"`
	Events         int `json:"events"`
	Trials         int `json:"trials"`
	TurnVets       int `json:"turn_vets"`
	SessionRatings int `json:"session_ratings"`
	JudgeVerdicts  int `json:"judge_verdicts"`
	BackgroundRuns int `json:"background_runs"`
	Inserted       int `json:"inserted"`
	Updated        int `json:"updated"`
	Skipped        int `json:"skipped"`
}

// ImportSanitizedState merges data idempotently into s inside a single
// transaction; when dryRun is true, counts are computed and rolled back.
func ImportSanitizedState(s *Store, data SanitizedMetricsExport, dryRun bool) (StateImportCounts, error) {
	if s == nil || s.db == nil {
		return StateImportCounts{}, errors.New("metrics: nil store")
	}
	if data.Schema > len(migrations) {
		return StateImportCounts{}, fmt.Errorf("export schema %d is newer than this binary (%d): %w", data.Schema, len(migrations), ErrNewerSchema)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return StateImportCounts{}, err
	}
	defer tx.Rollback()
	var c StateImportCounts
	if err := importCoreTablesTx(tx, data, &c); err != nil {
		return StateImportCounts{}, err
	}
	if err := importLedgerTablesTx(tx, data, &c); err != nil {
		return StateImportCounts{}, err
	}
	if dryRun {
		return c, tx.Rollback()
	}
	return c, tx.Commit()
}

func importCoreTablesTx(tx *sql.Tx, data SanitizedMetricsExport, c *StateImportCounts) error {
	for _, r := range data.Sessions {
		if err := importOneSessionTx(tx, r, c); err != nil {
			return err
		}
	}
	for _, r := range data.Steps {
		if err := importOneStepTx(tx, r, c); err != nil {
			return err
		}
	}
	for _, r := range data.Events {
		if err := importOneEventTx(tx, r, c); err != nil {
			return err
		}
	}
	for _, r := range data.Trials {
		if err := importOneTrialTx(tx, r, c); err != nil {
			return err
		}
	}
	return nil
}

func importLedgerTablesTx(tx *sql.Tx, data SanitizedMetricsExport, c *StateImportCounts) error {
	for _, r := range data.TurnVets {
		if err := importOneTurnVetTx(tx, r, c); err != nil {
			return err
		}
	}
	for _, r := range data.SessionRatings {
		if err := importOneRatingTx(tx, r, c); err != nil {
			return err
		}
	}
	for _, r := range data.JudgeVerdicts {
		if err := importOneVerdictTx(tx, r, c); err != nil {
			return err
		}
	}
	for _, r := range data.BackgroundRuns {
		if err := importOneBackgroundRunTx(tx, r, c); err != nil {
			return err
		}
	}
	return nil
}

func importOneSessionTx(tx *sql.Tx, r SanitizedSession, c *StateImportCounts) error {
	sid := clipSanitizedText(r.ID, 128)
	if sid == "" {
		c.Skipped++
		return nil
	}
	repoID := clipSanitizedText(r.RepoID, 256)
	res, err := tx.Exec(`INSERT OR IGNORE INTO sessions(id, vendor, repo_id, parent_id, agent_type, model, label, outcome, run, started_at, ended_at, cost_usd, duration_ms)
		VALUES (?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)`,
		sid, clipSanitizedText(r.Vendor, 32), repoID, clipSanitizedText(r.ParentID, 128),
		identifier(r.AgentType), identifier(r.Model), clipSanitizedText(r.Label, trialText),
		clipSanitizedText(r.Outcome, 32), clipSanitizedText(r.Run, trialText),
		optInt64Val(r.StartedAt), optInt64Val(r.EndedAt), optFloat64Val(r.CostUSD), optInt64Val(r.DurationMS))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.Sessions++
		c.Inserted++
		return nil
	}
	return backfillExistingSessionTx(tx, sid, repoID, r, c)
}

func backfillExistingSessionTx(tx *sql.Tx, sid, repoID string, r SanitizedSession, c *StateImportCounts) error {
	res, err := tx.Exec(`UPDATE sessions SET
		repo_id = CASE WHEN repo_id = '' AND ? != '' THEN ? ELSE repo_id END,
		model = COALESCE(NULLIF(model, ''), NULLIF(?, '')),
		outcome = COALESCE(NULLIF(outcome, ''), NULLIF(?, '')),
		run = COALESCE(NULLIF(run, ''), NULLIF(?, ''))
		WHERE id = ? AND ((repo_id = '' AND ? != '') OR ((model IS NULL OR model = '') AND ? != '') OR ((outcome IS NULL OR outcome = '') AND ? != '') OR ((run IS NULL OR run = '') AND ? != ''))`,
		repoID, repoID, identifier(r.Model), clipSanitizedText(r.Outcome, 32), clipSanitizedText(r.Run, trialText),
		sid, repoID, identifier(r.Model), clipSanitizedText(r.Outcome, 32), clipSanitizedText(r.Run, trialText))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.Sessions++
		c.Updated++
	} else {
		c.Skipped++
	}
	return nil
}

func importOneStepTx(tx *sql.Tx, r SanitizedStep, c *StateImportCounts) error {
	sid := clipSanitizedText(r.SessionID, 128)
	ext := clipSanitizedText(r.ExtID, 160)
	if sid == "" || ext == "" {
		c.Skipped++
		return nil
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO steps(session_id, ext_id, ts, kind, tool, skill, target_kind, is_error, denied, tok_in, tok_out, cache_read, cache_write, context, started_ms, ended_ms, wait_ms)
		VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sid, ext, optInt64Val(r.TS), clipSanitizedText(r.Kind, 32), clipSanitizedText(r.Tool, 64),
		identifier(r.Skill), clipSanitizedText(r.TargetKind, 32), optInt64Val(r.IsError), optInt64Val(r.Denied),
		optInt64Val(r.TokIn), optInt64Val(r.TokOut), optInt64Val(r.CacheRead), optInt64Val(r.CacheWrite),
		optInt64Val(r.Context), optInt64Val(r.StartedMS), optInt64Val(r.EndedMS), optInt64Val(r.WaitMS))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.Steps++
		c.Inserted++
	} else {
		c.Skipped++
	}
	return nil
}

func importOneEventTx(tx *sql.Tx, r SanitizedEvent, c *StateImportCounts) error {
	sid := clipSanitizedText(r.SessionID, 128)
	kind := clipSanitizedText(r.Kind, 64)
	ref := clipSanitizedText(r.Ref, 256)
	if sid == "" || kind == "" {
		c.Skipped++
		return nil
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO events(session_id, ts, kind, ref, value) VALUES (?, ?, ?, ?, ?)`,
		sid, r.TS, kind, ref, clipSanitizedText(r.Value, 256))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.Events++
		c.Inserted++
	} else {
		c.Skipped++
	}
	return nil
}

func importOneTrialTx(tx *sql.Tx, r SanitizedTrial, c *StateImportCounts) error {
	tr := Trial{
		Run: clipSanitizedText(r.Run, trialText), Label: clipSanitizedText(r.Label, trialText),
		RowVersion: r.RowVersion, Kind: clipSanitizedText(r.Kind, trialText),
		Cluster: clipSanitizedText(r.Cluster, trialText), Expect: clipSanitizedText(r.Expect, trialText),
		Harness: clipSanitizedText(r.Harness, trialText), CLI: clipSanitizedText(r.CLI, trialText),
		Status: strings.ToLower(strings.TrimSpace(r.Status)), Cause: clipSanitizedText(r.Cause, trialText),
		StartedAt: time.UnixMilli(r.StartedAt), Seconds: r.Seconds,
		SessionSeconds: r.SessionSeconds, SetupSeconds: r.SetupSeconds, CheckSeconds: r.CheckSeconds,
	}
	if tr.validate() != nil {
		c.Skipped++
		return nil
	}
	var exists int
	if tx.QueryRow(`SELECT 1 FROM trials WHERE run = ?`, tr.Run).Scan(&exists) == nil {
		c.Skipped++
		return nil
	}
	if err := insertTrialTx(tx, tr); err != nil {
		return err
	}
	c.Trials++
	c.Inserted++
	return nil
}

func importOneTurnVetTx(tx *sql.Tx, r SanitizedTurnVet, c *StateImportCounts) error {
	convID := clipSanitizedText(r.ConversationID, 128)
	if convID == "" {
		c.Skipped++
		return nil
	}
	repoID := clipSanitizedText(r.RepoID, 256)
	pkRoot := repoID
	if pkRoot == "" {
		pkRoot = "imported"
	}
	var exists int
	if tx.QueryRow(`SELECT 1 FROM turn_vets WHERE (project_root = ? OR (repo_id != '' AND repo_id = ?)) AND conversation_id = ? AND turn_num = ? LIMIT 1`,
		pkRoot, repoID, convID, r.TurnNum).Scan(&exists) == nil {
		c.Skipped++
		return nil
	}
	_, err := tx.Exec(`INSERT INTO turn_vets(project_root, repo_id, conversation_id, turn_num, intent, gate, score, bypassed, skills_count, pivots_count, ledger_saved, directive, subtask_count, duration_ms, timed_out, injected_bytes, pivots_used_count, skills_loaded_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pkRoot, repoID, convID, r.TurnNum, clipSanitizedText(r.Intent, 64), clipSanitizedText(r.Gate, 64),
		min(100, max(0, r.Score)), boolInt(r.Bypassed), max(0, r.SkillsCount), max(0, r.PivotsCount),
		max(0, r.LedgerSaved), clipSanitizedText(r.Directive, 128), max(0, r.SubtaskCount),
		max(0, r.DurationMS), boolInt(r.TimedOut), max(0, r.InjectedBytes),
		max(0, r.PivotsUsedCount), max(0, r.SkillsLoadedCount), r.CreatedAt)
	if err != nil {
		return err
	}
	c.TurnVets++
	c.Inserted++
	return nil
}

func importOneRatingTx(tx *sql.Tx, r SessionRating, c *StateImportCounts) error {
	r.ProjectRoot = ""
	r.Summary = clipSanitizedText(r.Summary, 400)
	r.TargetSkill = clipSanitizedText(r.TargetSkill, 200)
	norm, err := normalizeSessionRating(r)
	if err != nil {
		c.Skipped++
		return nil
	}
	var curEval int64
	err = tx.QueryRow(`SELECT evaluated_at FROM session_ratings WHERE session_id = ?`, norm.SessionID).Scan(&curEval)
	if errors.Is(err, sql.ErrNoRows) {
		if err := upsertOneSessionRating(tx, norm); err != nil {
			return err
		}
		c.SessionRatings++
		c.Inserted++
		return nil
	}
	if err != nil {
		return err
	}
	if normalizeMilli(norm.EvaluatedAt) <= normalizeMilli(curEval) {
		c.Skipped++
		return nil
	}
	if _, err := tx.Exec(`UPDATE session_ratings SET repo_id = CASE WHEN ? != '' THEN ? ELSE repo_id END, evaluated_at = ?, outcome_score = ?, token_efficiency = ?, plan_alignment = ?, skill_action = ?, target_skill = ?, summary = ? WHERE session_id = ?`,
		norm.RepoID, norm.RepoID, norm.EvaluatedAt, norm.OutcomeScore, norm.TokenEfficiency, norm.PlanAlignment, norm.SkillAction, norm.TargetSkill, norm.Summary, norm.SessionID); err != nil {
		return err
	}
	c.SessionRatings++
	c.Updated++
	return nil
}

func importOneVerdictTx(tx *sql.Tx, r JudgeVerdictRow, c *StateImportCounts) error {
	r.ProjectRoot = ""
	r.Target = clipSanitizedText(r.Target, 256)
	r.FallbackReason = clipSanitizedText(r.FallbackReason, 400)
	norm, err := normalizeJudgeVerdictRow(r)
	if err != nil {
		c.Skipped++
		return nil
	}
	var exists int
	if tx.QueryRow(`SELECT 1 FROM judge_verdicts WHERE ts = ? AND repo_id = ? AND target = ? AND model = ? AND worker_model = ? AND worker_session_id = ? AND criterion_hash = ? AND verdict = ? LIMIT 1`,
		norm.TS, norm.RepoID, norm.Target, norm.Model, norm.WorkerModel, norm.WorkerSessionID, norm.CriterionHash, norm.Verdict).Scan(&exists) == nil {
		c.Skipped++
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO judge_verdicts(ts, project_root, repo_id, target, model, worker_model, worker_session_id, fallback_reason, criterion_hash, verdict) VALUES (?, '', ?, ?, ?, ?, ?, ?, ?, ?)`,
		norm.TS, norm.RepoID, norm.Target, norm.Model, norm.WorkerModel, norm.WorkerSessionID, norm.FallbackReason, norm.CriterionHash, norm.Verdict); err != nil {
		return err
	}
	c.JudgeVerdicts++
	c.Inserted++
	return nil
}

func importOneBackgroundRunTx(tx *sql.Tx, r BackgroundRunRow, c *StateImportCounts) error {
	norm, err := normalizeBackgroundRunRow(r)
	if err != nil {
		c.Skipped++
		return nil
	}
	var exists int
	if tx.QueryRow(`SELECT 1 FROM background_runs WHERE ts = ? AND kind = ? AND outcome = ? AND duration_ms = ? LIMIT 1`,
		norm.TS, norm.Kind, norm.Outcome, norm.DurationMS).Scan(&exists) == nil {
		c.Skipped++
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO background_runs(ts, kind, outcome, duration_ms, projects_checked, proposals_written, ratings_written, worker_ran, applied) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		norm.TS, norm.Kind, norm.Outcome, norm.DurationMS, norm.ProjectsChecked, norm.ProposalsWritten, norm.RatingsWritten, norm.WorkerRan, norm.Applied); err != nil {
		return err
	}
	c.BackgroundRuns++
	c.Inserted++
	return nil
}

func optInt64Val(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func optFloat64Val(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolInt(v int) int {
	if v != 0 {
		return 1
	}
	return 0
}
