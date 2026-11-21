package metrics

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

func vetHistoryColumns(db *sql.DB) (map[string]bool, error) {
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

func buildVetHistoryQuery(cols map[string]bool) string {
	sel := func(col, def string) string {
		if cols[col] {
			return "COALESCE(" + col + ", " + def + ")"
		}
		return def
	}
	return fmt.Sprintf(
		`SELECT %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s FROM vet_history ORDER BY turn_num, created_at, rowid`,
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
	)
}

func readVetHistoryRows(db *sql.DB, root string, ledger map[turnKey]int) ([]turnVetRow, error) {
	var s *Store
	return s.readVetHistoryRows(db, root, ResolveRepoID(root), ledger)
}

func (s *Store) readVetHistoryRows(db *sql.DB, root, repoID string, ledger map[turnKey]int) ([]turnVetRow, error) {
	cols, err := vetHistoryColumns(db)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, nil
	}
	readFiles := readLedgerPathsByConv(db, root)
	loadedSkills := s.readLoadedSkillsByConv()
	rows, err := db.Query(buildVetHistoryQuery(cols))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []turnVetRow
	lastIdx := map[string]int{}
	for rows.Next() {
		r, err := scanVetHistoryRow(rows, root, repoID, cols, ledger, readFiles, loadedSkills)
		if err != nil {
			return nil, err
		}
		lastIdx[r.convID] = len(out)
		out = append(out, r)
	}
	for k, saved := range ledger {
		if idx, ok := lastIdx[k.convID]; ok && saved > 0 {
			out[idx].ledger += saved
		}
	}
	return out, rows.Err()
}

func scanVetHistoryRow(
	rows *sql.Rows,
	root, repoID string,
	cols map[string]bool,
	ledger map[turnKey]int,
	readFiles map[string]map[string]bool,
	loadedSkills map[string]map[string]bool,
) (turnVetRow, error) {
	var convID, intent, gate, rawPrompt, skillsCSV, pivotsCSV, directive string
	var turnNum, score, subtaskCnt, durMS, timedOut, injBytes, pivotsUsed, skillsLoaded int
	var createdAt int64
	if err := rows.Scan(
		&convID, &turnNum, &intent, &gate, &score, &rawPrompt, &skillsCSV, &pivotsCSV, &createdAt,
		&directive, &subtaskCnt, &durMS, &timedOut, &injBytes, &pivotsUsed, &skillsLoaded,
	); err != nil {
		return turnVetRow{}, err
	}
	convID = printable(strings.TrimSpace(convID))
	byp := 0
	if strings.HasPrefix(strings.TrimSpace(rawPrompt), "!") {
		byp = 1
	}
	if timedOut != 0 {
		timedOut = 1
	}
	k := turnKey{convID: convID, turnNum: turnNum}
	saved := ledger[k]
	delete(ledger, k)
	if !cols["pivots_used_count"] && pivotsUsed == 0 {
		pivotsUsed = countMatchedItems(pivotsCSV, readFiles[convID], false)
	}
	if !cols["skills_loaded_count"] && skillsLoaded == 0 {
		skillsLoaded = countMatchedItems(skillsCSV, loadedSkills[convID], true)
	}
	return turnVetRow{
		projectRoot: root, repoID: repoID, convID: convID, turnNum: turnNum,
		intent: clipPrintable(intent, 64), gate: clipPrintable(gate, 64),
		score: min(100, max(0, score)), bypassed: byp,
		skillsCount: countCSVItems(skillsCSV), pivotsCount: countCSVItems(pivotsCSV),
		ledger: max(0, saved), directive: clipPrintable(directive, 128),
		subtaskCount: max(0, subtaskCnt), durationMS: max(0, durMS),
		timedOut: timedOut, injectedBytes: max(0, injBytes),
		pivotsUsedCount: max(0, pivotsUsed), skillsLoadedCount: max(0, skillsLoaded),
		createdAt: createdAt,
	}, nil
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
