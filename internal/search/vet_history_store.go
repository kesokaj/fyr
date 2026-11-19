package search

import (
	"database/sql"
	"errors"
	"strings"
)

func (st *store) recordVetHistory(convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string) error {
	if st == nil || st.db == nil {
		return nil
	}
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	if turnNum <= 0 {
		turnNum = 1
	}
	var skillNames []string
	for _, s := range res.Skills {
		if s.Name != "" {
			skillNames = append(skillNames, s.Name)
		}
	}
	_, err := st.db.Exec(
		`INSERT OR REPLACE INTO vet_history(conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		convID,
		turnNum,
		string(res.Intent),
		string(res.Gate),
		res.Score,
		strings.TrimSpace(rawPrompt),
		res.ImprovedPrompt,
		strings.Join(res.PresentAnchors, ","),
		strings.Join(res.MissingAnchors, ","),
		strings.Join(skillNames, ","),
		strings.Join(pivotFiles, ","),
		len(res.Subtasks),
		preInvocationDirective(res),
		nowFunc().Unix(),
	)
	return err
}

func (st *store) lastVetHistory(convID string, beforeTurn int) (*VetHistoryRow, error) {
	if st == nil || st.db == nil {
		return nil, nil
	}
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	query := `SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, created_at FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT 1`
	args := []any{convID}
	if beforeTurn > 0 {
		query = `SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, created_at FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT 1`
		args = []any{convID, beforeTurn}
	}
	row, err := scanVetHistoryRow(st.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

func (st *store) recentSuggestedSkills(convID string, beforeTurn, window int) (map[string]bool, []string, error) {
	if st == nil || st.db == nil {
		return nil, nil, nil
	}
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	if window <= 0 {
		window = 3
	}
	query := `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT ?`
	args := []any{convID, window}
	if beforeTurn > 0 {
		query = `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT ?`
		args = []any{convID, beforeTurn, window}
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
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := st.db.Query(
		`SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, created_at FROM vet_history WHERE conversation_id = ? ORDER BY turn_num ASC LIMIT ?`,
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
	if err := sc.Scan(
		&r.ConversationID, &r.TurnNum, &intent, &gate, &r.Score,
		&r.RawPrompt, &r.ImprovedPrompt, &presentCSV, &missingCSV,
		&skillsCSV, &pivotsCSV, &r.SubtaskCount, &r.Directive, &r.CreatedAt,
	); err != nil {
		return nil, err
	}
	r.Intent = IntentMode(intent)
	r.Gate = VetGate(gate)
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
