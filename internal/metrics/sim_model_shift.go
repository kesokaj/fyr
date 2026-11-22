package metrics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

const (
	ruleModelShiftAudit   = "model_shift_audit"
	sigModelShiftAudit    = "sig:global:model_shift_audit"
	scopeOperatorRule     = "operator_rule"
	severityInfo          = "info"
	minModelShiftSessions = 3
	maxModelShiftAuditIDs = 5
)

func detectModelShiftAudit(s *Store, home, repoRoot string, nowUTC time.Time) *DigestAction {
	if s == nil || s.db == nil || strings.TrimSpace(repoRoot) != "" {
		return nil
	}
	model, topCount, total7d, ok := dominantNewModel(s, nowUTC)
	if !ok {
		return nil
	}
	staleIDs := staleSIMWorkarounds(home, model, maxModelShiftAuditIDs)
	summary := fmt.Sprintf("new dominant model %s (%d/%d 7d sessions, 0 in prior 14d)", model, topCount, total7d)
	action := fmt.Sprintf("Review harness model workarounds against %s via /fyr-retro --harness.", model)
	if len(staleIDs) > 0 {
		joined := strings.Join(staleIDs, ", ")
		summary = fmt.Sprintf("%s; audit active SIM workarounds: %s", summary, joined)
		action = fmt.Sprintf("Audit active SIM workarounds (%s) against %s via /fyr-retro --harness.", joined, model)
	}
	return &DigestAction{
		SignalID:          sigModelShiftAudit,
		Severity:          severityInfo,
		Rule:              ruleModelShiftAudit,
		ActionScope:       scopeOperatorRule,
		RecommendedAction: printable(action),
		Summary:           printable(summary),
	}
}

func dominantNewModel(s *Store, nowUTC time.Time) (string, int, int, bool) {
	since7 := nowUTC.Add(-7 * 24 * time.Hour)
	since21 := since7.Add(-14 * 24 * time.Hour)
	counts, total7d, err := modelCountsInWindow(s, since7.UnixMilli(), nowUTC.UnixMilli(), true)
	if err != nil || total7d < minModelShiftSessions {
		return "", 0, 0, false
	}
	topModel, topCount := pickDominantModel(counts)
	if topModel == "" || topCount < minModelShiftSessions || topCount*2 <= total7d {
		return "", 0, 0, false
	}
	priorCounts, _, err := modelCountsInWindow(s, since21.UnixMilli(), since7.UnixMilli(), false)
	if err != nil || priorCounts[topModel] > 0 {
		return "", 0, 0, false
	}
	return topModel, topCount, total7d, true
}

func pickDominantModel(counts map[string]int) (string, int) {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best, bestN := "", 0
	for _, k := range keys {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best, bestN
}

func modelCountsInWindow(s *Store, startMS, endMS int64, inclusiveEnd bool) (map[string]int, int, error) {
	endOp := "<"
	if inclusiveEnd {
		endOp = "<="
	}
	rows, err := s.db.Query(`SELECT TRIM(model), COUNT(*)
		FROM sessions
		WHERE vendor = 'agy'
			AND COALESCE(label, '') = ''
			AND COALESCE(parent_id, '') = ''
			AND TRIM(COALESCE(model, '')) != ''
			AND COALESCE(started_at, ended_at, 0) >= ?
			AND COALESCE(started_at, ended_at, 0) `+endOp+` ?
		GROUP BY TRIM(model)`, startMS, endMS)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	counts, total := map[string]int{}, 0
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			return nil, 0, err
		}
		if c := clipPrintable(m, trialText); c != "" && n > 0 {
			counts[c] += n
			total += n
		}
	}
	return counts, total, rows.Err()
}

func staleSIMWorkarounds(home, newModel string, limit int) []string {
	if strings.TrimSpace(home) == "" || strings.TrimSpace(newModel) == "" || limit <= 0 {
		return nil
	}
	db, cleanup, ok := openCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return nil
	}
	defer cleanup()
	if !brainHasSIMTables(db) {
		return nil
	}
	candidates, err := loadActiveSIMWorkarounds(db)
	if err != nil {
		return nil
	}
	var out []string
	for _, cand := range candidates {
		if len(out) >= limit {
			break
		}
		hasOlder, hasNew := simProblemModelCoverage(db, cand.id, cand.openedModel, newModel)
		if hasOlder && !hasNew {
			out = append(out, cand.id)
		}
	}
	return out
}

type simWorkaroundCand struct {
	id, openedModel string
}

func loadActiveSIMWorkarounds(db *sql.DB) ([]simWorkaroundCand, error) {
	rows, err := db.Query(`SELECT id, COALESCE(opened_model, '')
		FROM sim_problems
		WHERE status = 'active'
			AND mechanism_class IN ('model_workaround', 'prompt_guidance')
		ORDER BY CASE WHEN mechanism_class = 'model_workaround' THEN 0 ELSE 1 END, updated_at DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []simWorkaroundCand
	for rows.Next() {
		var id, openedModel string
		if rows.Scan(&id, &openedModel) != nil {
			continue
		}
		id = clipPrintable(id, 64)
		if strings.HasPrefix(id, "SIM-P") {
			out = append(out, simWorkaroundCand{id: id, openedModel: openedModel})
		}
	}
	return out, rows.Err()
}

func brainHasSIMTables(db *sql.DB) bool {
	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err == nil && ver > 0 && ver < 5 {
		return false
	}
	var cnt int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('sim_problems', 'sim_attempts')`).Scan(&cnt)
	return err == nil && cnt == 2
}

func simProblemModelCoverage(db *sql.DB, probID, openedModel, newModel string) (hasOlder, hasNew bool) {
	if om := strings.TrimSpace(openedModel); om != "" {
		if strings.EqualFold(om, newModel) {
			hasNew = true
		} else {
			hasOlder = true
		}
	}
	rows, err := db.Query(`SELECT COALESCE(policy_model, ''), COALESCE(eval_verdict, ''), COALESCE(field_models_json, '') FROM sim_attempts WHERE problem_id = ?`, probID)
	if err != nil {
		return hasOlder, hasNew
	}
	defer rows.Close()
	for rows.Next() {
		var pm, verdict, fmJSON string
		if rows.Scan(&pm, &verdict, &fmJSON) != nil {
			continue
		}
		if pm = strings.TrimSpace(pm); pm != "" {
			if strings.EqualFold(pm, newModel) {
				if !strings.EqualFold(strings.TrimSpace(verdict), "skipped") {
					hasNew = true
				}
			} else {
				hasOlder = true
			}
		}
		fo, fn := fieldModelsCoverage(fmJSON, newModel)
		hasOlder = hasOlder || fo
		hasNew = hasNew || fn
	}
	return hasOlder, hasNew
}

func fieldModelsCoverage(raw, newModel string) (hasOlder, hasNew bool) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "[]" {
		return false, false
	}
	var items []ModelFieldStatus
	if json.Unmarshal([]byte(raw), &items) != nil {
		return false, false
	}
	for _, item := range items {
		m := strings.TrimSpace(item.Model)
		if m == "" {
			continue
		}
		if strings.EqualFold(m, newModel) {
			if item.Status != "" && item.Status != StatusPendingData {
				hasNew = true
			}
		} else {
			hasOlder = true
		}
	}
	return hasOlder, hasNew
}
