package metrics

import (
	"database/sql"
	"io"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/sqliteutil"
)

func (s *Store) readVetHistoryRows(db *sql.DB, root, repoID string, ledger map[turnKey]int) ([]turnVetRow, error) {
	rawRows, err := search.ReadVetHistoryForIngest(db)
	if err != nil || len(rawRows) == 0 {
		return nil, err
	}
	readFiles := readLedgerPathsByConv(db, root)
	loadedSkills := s.readLoadedSkillsByConv()
	out := make([]turnVetRow, 0, len(rawRows))
	lastIdx := map[string]int{}
	for _, raw := range rawRows {
		r := convertVetHistoryIngestRow(raw, root, repoID, ledger, readFiles, loadedSkills)
		lastIdx[r.convID] = len(out)
		out = append(out, r)
	}
	for k, saved := range ledger {
		if idx, ok := lastIdx[k.convID]; ok && saved > 0 {
			out[idx].ledger += saved
		}
	}
	return out, nil
}

func convertVetHistoryIngestRow(
	raw search.VetHistoryIngestRow,
	root, repoID string,
	ledger map[turnKey]int,
	readFiles map[string]map[string]bool,
	loadedSkills map[string]map[string]bool,
) turnVetRow {
	convID := printable(strings.TrimSpace(raw.ConversationID))
	byp := sqliteutil.BoolInt(strings.HasPrefix(strings.TrimSpace(raw.RawPrompt), "!"))
	timedOut := sqliteutil.BoolInt(raw.TimedOut)
	k := turnKey{convID: convID, turnNum: raw.TurnNum}
	saved := ledger[k]
	delete(ledger, k)
	pivotsUsed := raw.PivotsUsedCount
	if !raw.HasPivotsUsedCol && pivotsUsed == 0 {
		pivotsUsed = countMatchedItems(raw.PivotFiles, readFiles[convID], false)
	}
	skillsLoaded := raw.SkillsLoadedCount
	if matched := countMatchedItems(raw.SuggestedSkills, loadedSkills[convID], true); matched > skillsLoaded {
		skillsLoaded = matched
	}
	return turnVetRow{
		projectRoot: root, repoID: repoID, convID: convID, turnNum: raw.TurnNum,
		intent: clipPrintable(raw.Intent, 64), gate: clipPrintable(raw.Gate, 64),
		suggestedSkills: normalizeSuggestedSkillsCSV(raw.SuggestedSkills),
		score:           min(100, max(0, raw.Score)), bypassed: byp,
		skillsCount: countCSVItems(raw.SuggestedSkills), pivotsCount: countCSVItems(raw.PivotFiles),
		ledger: max(0, saved), directive: clipPrintable(raw.Directive, 128),
		subtaskCount: max(0, raw.SubtaskCount), durationMS: max(0, raw.DurationMS),
		timedOut: timedOut, injectedBytes: max(0, raw.InjectedBytes),
		pivotsUsedCount: max(0, pivotsUsed), skillsLoadedCount: max(0, skillsLoaded),
		createdAt: raw.CreatedAt,
	}
}

// maxSuggestedSkillsBytes caps persisted and transferred turn_vets.suggested_skills
// CSV lists across ingest, export, and import, trimmed strictly on comma boundaries.
const maxSuggestedSkillsBytes = 512

func normalizeSuggestedSkillsCSV(s string) string {
	items := parseListItems(s)
	if len(items) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var b strings.Builder
	for _, raw := range items {
		item := clipPrintable(raw, 64)
		low := strings.ToLower(item)
		if item == "" || seen[low] {
			continue
		}
		need := len(item)
		if b.Len() > 0 {
			need++
		}
		if b.Len()+need > maxSuggestedSkillsBytes {
			break
		}
		seen[low] = true
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(item)
	}
	return b.String()
}

func parseListItems(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		item := strings.Trim(strings.TrimSpace(p), "[]\"'")
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func countCSVItems(s string) int {
	return len(parseListItems(s))
}

func countMatchedItems(csv string, set map[string]bool, lower bool) int {
	if len(set) == 0 {
		return 0
	}
	seen := map[string]bool{}
	n := 0
	for _, item := range parseListItems(csv) {
		key := filepath.ToSlash(filepath.Clean(item))
		if lower {
			key = strings.ToLower(item)
		}
		if !seen[key] && set[key] {
			seen[key] = true
			n++
		}
	}
	return n
}

func readLedgerPathsByConv(db *sql.DB, root string) map[string]map[string]bool {
	rows, err := db.Query(`SELECT conversation_id, resolved_path FROM read_ledger`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var cid, p string
		if rows.Scan(&cid, &p) != nil {
			continue
		}
		cid = printable(strings.TrimSpace(cid))
		clean := filepath.Clean(strings.TrimSpace(p))
		if cid == "" || clean == "" {
			continue
		}
		if out[cid] == nil {
			out[cid] = map[string]bool{}
		}
		out[cid][filepath.ToSlash(clean)] = true
		if rel, err := filepath.Rel(root, clean); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			out[cid][filepath.ToSlash(rel)] = true
		}
	}
	return out
}

func (s *Store) readLoadedSkillsByConv() map[string]map[string]bool {
	if s == nil || s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT session_id, LOWER(skill) FROM steps WHERE skill IS NOT NULL AND skill != '' AND COALESCE(is_error, 0) = 0 AND COALESCE(denied, 0) = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var sid, sk string
		if rows.Scan(&sid, &sk) == nil && sid != "" && sk != "" {
			if out[sid] == nil {
				out[sid] = map[string]bool{}
			}
			out[sid][sk] = true
		}
	}
	return out
}

func (s *Store) rootNeedsSuggestedSkillsBackfill(root string) bool {
	if s == nil || s.db == nil {
		return false
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM turn_vets WHERE project_root = ? AND skills_count > 0 AND suggested_skills = '' LIMIT 1`, root).Scan(&one)
	return err == nil
}

type skillLoadedUpdate struct {
	root, convID string
	turnNum      int
	loaded       int
}

// rejoinTurnVetSkillsLoaded recomputes turn_vets.skills_loaded_count against
// metrics.db.steps using the persisted suggested_skills CSV, so late-arriving
// skill read steps update skills_loaded_count even when search.db mtime is
// unchanged in ingest_state (F-039).
func (s *Store) rejoinTurnVetSkillsLoaded(log io.Writer) {
	loadedSkills := s.readLoadedSkillsByConv()
	if len(loadedSkills) == 0 {
		return
	}
	rows, err := s.db.Query(`SELECT project_root, conversation_id, turn_num, suggested_skills, skills_loaded_count FROM turn_vets WHERE suggested_skills != ''`)
	if err != nil {
		return
	}
	var updates []skillLoadedUpdate
	for rows.Next() {
		var root, convID, suggested string
		var turnNum, cur int
		if rows.Scan(&root, &convID, &turnNum, &suggested, &cur) != nil {
			continue
		}
		if matched := countMatchedItems(suggested, loadedSkills[convID], true); matched > cur {
			updates = append(updates, skillLoadedUpdate{root: root, convID: convID, turnNum: turnNum, loaded: matched})
		}
	}
	rows.Close()
	if len(updates) == 0 {
		return
	}
	if err := s.applySkillLoadedUpdates(updates); err != nil {
		noteSecondBrain(log, "turn_vets skills rejoin: %v", err)
	}
}

func (s *Store) applySkillLoadedUpdates(updates []skillLoadedUpdate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, u := range updates {
		if _, err := tx.Exec(
			`UPDATE turn_vets SET skills_loaded_count = ? WHERE project_root = ? AND conversation_id = ? AND turn_num = ?`,
			u.loaded, u.root, u.convID, u.turnNum,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
