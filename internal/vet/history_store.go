package vet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/sqliteutil"
)

// HistoryIngestRow is one row read from a search.db vet_history table for
// downstream metrics ingest, carrying raw CSV fields and optional-column
// presence across pre-v10, v10, and v11/v12 search.db schemas.
type HistoryIngestRow struct {
	ConversationID     string
	TurnNum            int
	Intent             string
	Gate               string
	Score              int
	RawPrompt          string
	SuggestedSkills    string
	PivotFiles         string
	CreatedAt          int64
	Directive          string
	SubtaskCount       int
	DurationMS         int
	TimedOut           bool
	InjectedBytes      int
	PivotsUsedCount    int
	SkillsLoadedCount  int
	HasPivotsUsedCol   bool
	HasSkillsLoadedCol bool
}

// ReadHistoryForIngest reads all vet_history rows from db in deterministic
// turn_num, created_at, rowid order, defaulting columns absent in older schemas.
// A nil db or missing vet_history table returns nil, nil.
func ReadHistoryForIngest(db *sql.DB) ([]HistoryIngestRow, error) {
	if db == nil {
		return nil, nil
	}
	cols, err := vetHistoryTableCols(db)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, nil
	}
	rows, err := db.Query(buildVetHistoryIngestQuery(cols))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hasPivots := cols["pivots_used_count"]
	hasSkills := cols["skills_loaded_count"]
	var out []HistoryIngestRow
	for rows.Next() {
		r, err := scanVetHistoryIngestRow(rows, hasPivots, hasSkills)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func vetHistoryTableCols(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(vet_history)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return cols, rows.Err()
}

func buildVetHistoryIngestQuery(cols map[string]bool) string {
	sel := func(col, def string) string {
		if cols[col] {
			return "COALESCE(" + col + ", " + def + ")"
		}
		return def
	}
	orderTurn := "0"
	if cols["turn_num"] {
		orderTurn = "turn_num"
	}
	orderCreated := "0"
	if cols["created_at"] {
		orderCreated = "created_at"
	}
	return fmt.Sprintf(
		`SELECT %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s FROM vet_history ORDER BY %s, %s, rowid`,
		sel("conversation_id", "''"),
		sel("turn_num", "0"),
		sel("intent", "''"),
		sel("gate", "''"),
		sel("score", "0"),
		sel("raw_prompt", "''"),
		sel("suggested_skills", "''"),
		sel("pivot_files", "''"),
		sel("created_at", "0"),
		sel("directive", "''"),
		sel("subtask_count", "0"),
		sel("duration_ms", "0"),
		sel("timed_out", "0"),
		sel("injected_bytes", "0"),
		sel("pivots_used_count", "0"),
		sel("skills_loaded_count", "0"),
		orderTurn,
		orderCreated,
	)
}

func scanVetHistoryIngestRow(rows *sql.Rows, hasPivotsUsed, hasSkillsLoaded bool) (HistoryIngestRow, error) {
	var r HistoryIngestRow
	var timedOut int
	if err := rows.Scan(
		&r.ConversationID, &r.TurnNum, &r.Intent, &r.Gate, &r.Score,
		&r.RawPrompt, &r.SuggestedSkills, &r.PivotFiles, &r.CreatedAt,
		&r.Directive, &r.SubtaskCount, &r.DurationMS, &timedOut,
		&r.InjectedBytes, &r.PivotsUsedCount, &r.SkillsLoadedCount,
	); err != nil {
		return HistoryIngestRow{}, err
	}
	r.TimedOut = timedOut != 0
	r.HasPivotsUsedCol = hasPivotsUsed
	r.HasSkillsLoadedCol = hasSkillsLoaded
	return r, nil
}

// TurnMetrics records timing and payload stats for one pre-invocation turn.
type TurnMetrics struct {
	DurationMS    int
	TimedOut      bool
	InjectedBytes int
}

const vetHistoryCols = `conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, duration_ms, timed_out, injected_bytes, created_at`

// RecordHistoryCtx persists one turn's vet result and metrics into vet_history.
func RecordHistoryCtx(ctx context.Context, db *sql.DB, nowUnix int64, convID string, turnNum int, rawPrompt string, res Result, pivotFiles []string, m TurnMetrics) error {
	if db == nil || ctx.Err() != nil {
		return ctx.Err()
	}
	convID = readledger.NormalizeConvID(convID)
	if turnNum <= 0 {
		turnNum = 1
	}
	trimmed := strings.TrimSpace(rawPrompt)
	if collapseDuplicateApprovalCtx(ctx, db, convID, turnNum, trimmed, res, m, nowUnix) {
		return nil
	}
	var skillNames []string
	for _, s := range res.Skills {
		if s.Name != "" {
			skillNames = append(skillNames, s.Name)
		}
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR REPLACE INTO vet_history(`+vetHistoryCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		convID, turnNum, string(res.Intent), string(res.Gate), res.Score, trimmed,
		res.ImprovedPrompt, strings.Join(res.PresentAnchors, ","), strings.Join(res.MissingAnchors, ","),
		strings.Join(skillNames, ","), strings.Join(pivotFiles, ","), len(res.Subtasks),
		Directive(res), max(0, m.DurationMS), sqliteutil.BoolInt(m.TimedOut), max(0, m.InjectedBytes), nowUnix,
	)
	return err
}

func collapseDuplicateApprovalCtx(ctx context.Context, db *sql.DB, convID string, turnNum int, trimmed string, res Result, m TurnMetrics, now int64) bool {
	if ctx.Err() != nil || res.Intent != IntentApproval || res.Bypassed || m.TimedOut || !isShortApprovalDirective(trimmed) {
		return false
	}
	prev, err := LastHistoryCtx(ctx, db, convID, turnNum)
	if err != nil || prev == nil || prev.Intent != IntentApproval || prev.TimedOut {
		return false
	}
	if !strings.EqualFold(CollapseOneLine(prev.RawPrompt, 0), CollapseOneLine(trimmed, 0)) {
		return false
	}
	_, _ = db.ExecContext(ctx,
		`UPDATE vet_history SET duration_ms = ?, injected_bytes = ?, created_at = ? WHERE conversation_id = ? AND turn_num = ?`,
		max(0, m.DurationMS), max(0, m.InjectedBytes), now, convID, prev.TurnNum,
	)
	return true
}

func isShortApprovalDirective(prompt string) bool {
	return prompt != "" && len(prompt) <= 80 && len(strings.Fields(prompt)) <= 8
}

// LastHistoryCtx returns the latest vet_history row for convID (optionally strictly before beforeTurn).
func LastHistoryCtx(ctx context.Context, db *sql.DB, convID string, beforeTurn int) (*HistoryRow, error) {
	if db == nil {
		return nil, nil
	}
	convID = readledger.NormalizeConvID(convID)
	query := `SELECT ` + vetHistoryCols + ` FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT 1`
	args := []any{convID}
	if beforeTurn > 0 {
		query = `SELECT ` + vetHistoryCols + ` FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT 1`
		args = []any{convID, beforeTurn}
	}
	row, err := scanVetHistoryRow(db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

const recentSuggestedSkillsWindow = 3

// RecentSuggestedSkills collects skills and pivot files suggested in the last 3 turns of convID.
func RecentSuggestedSkills(db *sql.DB, convID string, beforeTurn int) (map[string]bool, []string, error) {
	if db == nil {
		return nil, nil, nil
	}
	convID = readledger.NormalizeConvID(convID)
	query := `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT ?`
	args := []any{convID, recentSuggestedSkillsWindow}
	if beforeTurn > 0 {
		query = `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT ?`
		args = []any{convID, beforeTurn, recentSuggestedSkillsWindow}
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	return collectRecentSkillsAndPivots(rows)
}

func collectRecentSkillsAndPivots(rows *sql.Rows) (map[string]bool, []string, error) {
	skills := map[string]bool{}
	pivotsSeen := map[string]bool{}
	var pivots []string
	for rows.Next() {
		var sCSV, pCSV string
		if err := rows.Scan(&sCSV, &pCSV); err != nil {
			continue
		}
		for _, s := range splitVetCSV(sCSV) {
			skills[s] = true
		}
		for _, p := range splitVetCSV(pCSV) {
			if !pivotsSeen[p] {
				pivotsSeen[p] = true
				pivots = append(pivots, p)
			}
		}
	}
	return skills, pivots, rows.Err()
}

// ListConversationHistory returns up to limit vet_history rows for convID ordered by turn_num ASC.
func ListConversationHistory(db *sql.DB, convID string, limit int) ([]HistoryRow, error) {
	if db == nil {
		return nil, nil
	}
	convID = readledger.NormalizeConvID(convID)
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(
		`SELECT `+vetHistoryCols+` FROM vet_history WHERE conversation_id = ? ORDER BY turn_num ASC LIMIT ?`,
		convID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		r, err := scanVetHistoryRow(rows)
		if err != nil {
			return out, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

type vetRowScanner interface {
	Scan(dest ...any) error
}

func scanVetHistoryRow(sc vetRowScanner) (*HistoryRow, error) {
	var r HistoryRow
	var intent, gate, presentCSV, missingCSV, skillsCSV, pivotsCSV string
	var timedOut int
	if err := sc.Scan(
		&r.ConversationID, &r.TurnNum, &intent, &gate, &r.Score,
		&r.RawPrompt, &r.ImprovedPrompt, &presentCSV, &missingCSV,
		&skillsCSV, &pivotsCSV, &r.SubtaskCount, &r.Directive,
		&r.DurationMS, &timedOut, &r.InjectedBytes, &r.CreatedAt,
	); err != nil {
		return nil, err
	}
	r.Intent = IntentMode(intent)
	r.Gate = VetGate(gate)
	r.TimedOut = timedOut != 0
	r.PresentAnchors = splitVetCSV(presentCSV)
	r.MissingAnchors = splitVetCSV(missingCSV)
	r.SuggestedSkills = splitVetCSV(skillsCSV)
	r.PivotFiles = splitVetCSV(pivotsCSV)
	return &r, nil
}

func splitVetCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
