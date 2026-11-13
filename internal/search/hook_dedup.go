package search

import (
	"strings"
	"time"
)

const hookDedupSentinelLine = -4

// markSessionSentinelOnce records key in read_ledger (start_line = -4, isolated
// from read/write/co-activity rows) for (conversationID, generationID) and
// returns true only on the first sighting in that compaction generation.
func markSessionSentinelOnce(st *store, convID string, genID, turnNum int, key string) bool {
	if st == nil || convID == "" || key == "" {
		return true
	}
	var exists int
	err := st.db.QueryRow(
		`SELECT 1 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? LIMIT 1`,
		convID, genID, key, hookDedupSentinelLine,
	).Scan(&exists)
	if err == nil {
		return false
	}
	_, _ = st.db.Exec(
		`INSERT OR IGNORE INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at)
		 VALUES (?, ?, ?, ?, ?, '', ?, 1, ?)`,
		convID, genID, key, hookDedupSentinelLine, hookDedupSentinelLine, max(1, turnNum), time.Now().Unix(),
	)
	return true
}

// filterSessionDedupTurn strips records and skills already injected earlier in
// the same compaction generation so Turn 2+ avoids redundant boilerplate while
// automatically re-priming after compaction increments generationID (ADR-521).
func filterSessionDedupTurn(st *store, convID string, genID, turnNum int, res *VetResult, brainCtx *TurnBrainContext) {
	if st == nil || convID == "" || res == nil || brainCtx == nil {
		return
	}
	if turnNum <= 1 {
		seedTurnDedupKeys(st, convID, genID, turnNum, res, brainCtx)
		return
	}
	brainCtx.Memories = dedupRecords(st, convID, genID, turnNum, brainCtx.Memories, nil)
	brainCtx.Bugs = dedupRecords(st, convID, genID, turnNum, brainCtx.Bugs, nil)
	brainCtx.ADRs = dedupRecords(st, convID, genID, turnNum, brainCtx.ADRs, res.Conflicts)
	if brainCtx.ActivePlan != nil && brainCtx.ActivePlan.ID != "" {
		if !markSessionSentinelOnce(st, convID, genID, turnNum, "__vet_rec__:"+brainCtx.ActivePlan.ID) {
			brainCtx.ActivePlan = nil
		}
	}
	res.MatchedRecords = collectMatchedRecords(*brainCtx)
	if len(res.Skills) > 0 {
		var kept []SkillMeta
		for _, s := range res.Skills {
			if markSessionSentinelOnce(st, convID, genID, turnNum, "__vet_skill__:"+s.Name) {
				kept = append(kept, s)
			}
		}
		res.Skills = kept
	}
}

func seedTurnDedupKeys(st *store, convID string, genID, turnNum int, res *VetResult, brainCtx *TurnBrainContext) {
	for _, r := range collectMatchedRecords(*brainCtx) {
		if r.ID != "" {
			_ = markSessionSentinelOnce(st, convID, genID, turnNum, "__vet_rec__:"+r.ID)
		}
	}
	for _, s := range res.Skills {
		if s.Name != "" {
			_ = markSessionSentinelOnce(st, convID, genID, turnNum, "__vet_skill__:"+s.Name)
		}
	}
}

func dedupRecords(st *store, convID string, genID, turnNum int, recs []MatchedRecord, conflicts []string) []MatchedRecord {
	if len(recs) == 0 {
		return nil
	}
	var kept []MatchedRecord
	for _, r := range recs {
		if r.ID == "" {
			continue
		}
		if hasRecordConflict(r.ID, conflicts) || markSessionSentinelOnce(st, convID, genID, turnNum, "__vet_rec__:"+r.ID) {
			kept = append(kept, r)
		}
	}
	return kept
}

func hasRecordConflict(id string, conflicts []string) bool {
	if id == "" || len(conflicts) == 0 {
		return false
	}
	for _, c := range conflicts {
		if strings.Contains(c, id) {
			return true
		}
	}
	return false
}
