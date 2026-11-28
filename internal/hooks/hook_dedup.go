package hooks

import (
	"database/sql"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/vet"
)

const hookDedupSentinelLine = -4

// MarkSessionSentinelOnce records sentinel in read_ledger (start_line = -4, isolated
// from read/write/co-activity rows) for (conversationID, generationID) and
// returns true only on the first sighting in that compaction generation.
func MarkSessionSentinelOnce(db *sql.DB, convID string, genID, turnNum int, sentinel string) bool {
	if db == nil || convID == "" || sentinel == "" {
		return true
	}
	var exists int
	err := db.QueryRow(
		`SELECT 1 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? LIMIT 1`,
		convID, genID, sentinel, hookDedupSentinelLine,
	).Scan(&exists)
	if err == nil {
		return false
	}
	_, _ = db.Exec(
		`INSERT OR IGNORE INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at)
		 VALUES (?, ?, ?, ?, ?, '', ?, 1, ?)`,
		convID, genID, sentinel, hookDedupSentinelLine, hookDedupSentinelLine, max(1, turnNum), time.Now().Unix(),
	)
	return true
}

// filterSessionDedupTurn strips records and skills already injected earlier in
// the same compaction generation so Turn 2+ avoids redundant boilerplate while
// automatically re-priming after compaction increments generationID (ADR-521).
func filterSessionDedupTurn(db *sql.DB, convID string, genID, turnNum int, res *vet.Result, brainCtx *vet.TurnBrainContext) {
	if db == nil || convID == "" || res == nil || brainCtx == nil {
		return
	}
	if turnNum <= 1 {
		seedTurnDedupKeys(db, convID, genID, turnNum, res, brainCtx)
		return
	}
	brainCtx.Memories = dedupRecords(db, convID, genID, turnNum, brainCtx.Memories, nil)
	brainCtx.Bugs = dedupRecords(db, convID, genID, turnNum, brainCtx.Bugs, nil)
	brainCtx.ADRs = dedupRecords(db, convID, genID, turnNum, brainCtx.ADRs, res.Conflicts)
	if brainCtx.ActivePlan != nil && brainCtx.ActivePlan.ID != "" {
		if !MarkSessionSentinelOnce(db, convID, genID, turnNum, "__vet_rec__:"+brainCtx.ActivePlan.ID) {
			brainCtx.ActivePlan = nil
		}
	}
	res.MatchedRecords = vet.CollectMatchedRecords(*brainCtx)
	if len(res.Skills) > 0 {
		var kept []vet.SkillMeta
		for _, s := range res.Skills {
			if s.Name != "" && MarkSessionSentinelOnce(db, convID, genID, turnNum, "__vet_skill__:"+s.Name) {
				kept = append(kept, s)
			}
		}
		res.Skills = kept
	}
}

func seedTurnDedupKeys(db *sql.DB, convID string, genID, turnNum int, res *vet.Result, brainCtx *vet.TurnBrainContext) {
	for _, r := range vet.CollectMatchedRecords(*brainCtx) {
		if r.ID != "" {
			_ = MarkSessionSentinelOnce(db, convID, genID, turnNum, "__vet_rec__:"+r.ID)
		}
	}
	for _, s := range res.Skills {
		if s.Name != "" {
			_ = MarkSessionSentinelOnce(db, convID, genID, turnNum, "__vet_skill__:"+s.Name)
		}
	}
}

func dedupRecords(db *sql.DB, convID string, genID, turnNum int, recs []vet.MatchedRecord, conflicts []string) []vet.MatchedRecord {
	if len(recs) == 0 {
		return nil
	}
	var kept []vet.MatchedRecord
	for _, r := range recs {
		if r.ID == "" {
			continue
		}
		if hasRecordConflict(r.ID, conflicts) || MarkSessionSentinelOnce(db, convID, genID, turnNum, "__vet_rec__:"+r.ID) {
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
