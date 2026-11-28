package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// VetHistoryIngestRow is one row read from a search.db vet_history table for
// downstream metrics ingest, carrying raw CSV fields and optional-column
// presence across pre-v10, v10, and v11/v12 search.db schemas.
type VetHistoryIngestRow struct {
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

// ReadVetHistoryForIngest reads all vet_history rows from db in deterministic
// turn_num, created_at, rowid order, defaulting columns absent in older schemas.
// A nil db or missing vet_history table returns nil, nil.
func ReadVetHistoryForIngest(db *sql.DB) ([]VetHistoryIngestRow, error) {
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
	var out []VetHistoryIngestRow
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

func scanVetHistoryIngestRow(rows *sql.Rows, hasPivotsUsed, hasSkillsLoaded bool) (VetHistoryIngestRow, error) {
	var r VetHistoryIngestRow
	var timedOut int
	if err := rows.Scan(
		&r.ConversationID, &r.TurnNum, &r.Intent, &r.Gate, &r.Score,
		&r.RawPrompt, &r.SuggestedSkills, &r.PivotFiles, &r.CreatedAt,
		&r.Directive, &r.SubtaskCount, &r.DurationMS, &timedOut,
		&r.InjectedBytes, &r.PivotsUsedCount, &r.SkillsLoadedCount,
	); err != nil {
		return VetHistoryIngestRow{}, err
	}
	r.TimedOut = timedOut != 0
	r.HasPivotsUsedCol = hasPivotsUsed
	r.HasSkillsLoadedCol = hasSkillsLoaded
	return r, nil
}

type vetTurnMetrics struct {
	durationMS    int
	timedOut      bool
	injectedBytes int
}

const vetHistoryCols = `conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, duration_ms, timed_out, injected_bytes, created_at`

func (st *store) recordVetHistory(convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string) error {
	return st.recordVetHistoryWithMetrics(convID, turnNum, rawPrompt, res, pivotFiles, vetTurnMetrics{})
}

func (st *store) recordVetHistoryWithMetrics(convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string, m vetTurnMetrics) error {
	return st.recordVetHistoryWithMetricsCtx(context.Background(), convID, turnNum, rawPrompt, res, pivotFiles, m)
}

func (st *store) recordVetHistoryWithMetricsCtx(ctx context.Context, convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string, m vetTurnMetrics) error {
	if st == nil || st.db == nil || ctx.Err() != nil {
		return ctx.Err()
	}
	convID = normalizeLedgerConvID(convID)
	if turnNum <= 0 {
		turnNum = 1
	}
	trimmed := strings.TrimSpace(rawPrompt)
	now := nowFunc().Unix()
	if st.collapseDuplicateApprovalCtx(ctx, convID, turnNum, trimmed, res, m, now) {
		return nil
	}
	var skillNames []string
	for _, s := range res.Skills {
		if s.Name != "" {
			skillNames = append(skillNames, s.Name)
		}
	}
	_, err := st.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO vet_history(`+vetHistoryCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		convID, turnNum, string(res.Intent), string(res.Gate), res.Score, trimmed,
		res.ImprovedPrompt, strings.Join(res.PresentAnchors, ","), strings.Join(res.MissingAnchors, ","),
		strings.Join(skillNames, ","), strings.Join(pivotFiles, ","), len(res.Subtasks),
		preInvocationDirective(res), max(0, m.durationMS), boolInt(m.timedOut), max(0, m.injectedBytes), now,
	)
	return err
}

func (st *store) collapseDuplicateApprovalCtx(ctx context.Context, convID string, turnNum int, trimmed string, res VetResult, m vetTurnMetrics, now int64) bool {
	if ctx.Err() != nil || res.Intent != IntentApproval || res.Bypassed || m.timedOut || !isShortApprovalDirective(trimmed) {
		return false
	}
	prev, err := st.lastVetHistoryCtx(ctx, convID, turnNum)
	if err != nil || prev == nil || prev.Intent != IntentApproval || prev.TimedOut {
		return false
	}
	if !strings.EqualFold(collapseOneLine(prev.RawPrompt, 0), collapseOneLine(trimmed, 0)) {
		return false
	}
	_, _ = st.db.ExecContext(ctx,
		`UPDATE vet_history SET duration_ms = ?, injected_bytes = ?, created_at = ? WHERE conversation_id = ? AND turn_num = ?`,
		max(0, m.durationMS), max(0, m.injectedBytes), now, convID, prev.TurnNum,
	)
	return true
}

func isShortApprovalDirective(prompt string) bool {
	return prompt != "" && len(prompt) <= 80 && len(strings.Fields(prompt)) <= 8
}

func (st *store) lastVetHistory(convID string, beforeTurn int) (*VetHistoryRow, error) {
	return st.lastVetHistoryCtx(context.Background(), convID, beforeTurn)
}

func (st *store) lastVetHistoryCtx(ctx context.Context, convID string, beforeTurn int) (*VetHistoryRow, error) {
	if st == nil || st.db == nil {
		return nil, nil
	}
	convID = normalizeLedgerConvID(convID)
	query := `SELECT ` + vetHistoryCols + ` FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT 1`
	args := []any{convID}
	if beforeTurn > 0 {
		query = `SELECT ` + vetHistoryCols + ` FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT 1`
		args = []any{convID, beforeTurn}
	}
	row, err := scanVetHistoryRow(st.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

const recentSuggestedSkillsWindow = 3

func (st *store) recentSuggestedSkills(convID string, beforeTurn int) (map[string]bool, []string, error) {
	if st == nil || st.db == nil {
		return nil, nil, nil
	}
	convID = normalizeLedgerConvID(convID)
	query := `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT ?`
	args := []any{convID, recentSuggestedSkillsWindow}
	if beforeTurn > 0 {
		query = `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT ?`
		args = []any{convID, beforeTurn, recentSuggestedSkillsWindow}
	}
	rows, err := st.db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	skills := map[string]bool{}
	pivotsSeen := map[string]bool{}
	var pivots []string
	for rows.Next() {
		var sCSV, pCSV string
		if err := rows.Scan(&sCSV, &pCSV); err != nil {
			continue
		}
		for _, s := range splitCSV(sCSV) {
			skills[s] = true
		}
		for _, p := range splitCSV(pCSV) {
			if !pivotsSeen[p] {
				pivotsSeen[p] = true
				pivots = append(pivots, p)
			}
		}
	}
	return skills, pivots, rows.Err()
}

func (st *store) listConversationVetHistory(convID string, limit int) ([]VetHistoryRow, error) {
	if st == nil || st.db == nil {
		return nil, nil
	}
	convID = normalizeLedgerConvID(convID)
	if limit <= 0 {
		limit = 50
	}
	rows, err := st.db.Query(
		`SELECT `+vetHistoryCols+` FROM vet_history WHERE conversation_id = ? ORDER BY turn_num ASC LIMIT ?`,
		convID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VetHistoryRow
	for rows.Next() {
		r, err := scanVetHistoryRow(rows)
		if err != nil {
			return out, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVetHistoryRow(sc rowScanner) (*VetHistoryRow, error) {
	var r VetHistoryRow
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
	r.PresentAnchors = splitCSV(presentCSV)
	r.MissingAnchors = splitCSV(missingCSV)
	r.SuggestedSkills = splitCSV(skillsCSV)
	r.PivotFiles = splitCSV(pivotsCSV)
	return &r, nil
}

func splitCSV(s string) []string {
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
