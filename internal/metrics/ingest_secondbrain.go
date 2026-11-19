package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

type turnKey struct {
	convID  string
	turnNum int
}

type turnVetRow struct {
	projectRoot, convID, intent, gate                          string
	turnNum, score, bypassed, skillsCount, pivotsCount, ledger int
	createdAt                                                  int64
}

func ingestSecondBrainAndWaves(s *Store, home string, log io.Writer) {
	if s == nil || s.db == nil || strings.TrimSpace(home) == "" {
		return
	}
	s.ingestSecondBrain(home, log)
	s.ingestWorkWaves(home, log)
}

func noteSecondBrain(log io.Writer, format string, args ...any) {
	if log == nil {
		return
	}
	fmt.Fprintln(log, printable("metrics: note: "+fmt.Sprintf(format, args...)))
}

func (s *Store) ingestSecondBrain(home string, log io.Writer) {
	roots := s.candidateProjectRoots(home, log)
	if len(roots) == 0 {
		return
	}
	var tmp string
	defer func() {
		if tmp != "" {
			os.RemoveAll(tmp)
		}
	}()
	tempDir := func() (string, error) {
		if tmp == "" {
			d, err := os.MkdirTemp(filepath.Dir(s.path), "metrics-sb-")
			if err != nil {
				return "", err
			}
			tmp = d
		}
		return tmp, nil
	}
	for i, root := range roots {
		s.ingestProjectSearchDB(root, i, tempDir, log)
	}
}

func (s *Store) candidateProjectRoots(home string, log io.Writer) []string {
	seen := map[string]bool{}
	rows, err := s.db.Query(`SELECT DISTINCT project_root FROM sessions WHERE project_root IS NOT NULL AND project_root <> ''`)
	if err == nil {
		var sessionRoots []string
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				sessionRoots = append(sessionRoots, r)
			}
		}
		rows.Close()
		for _, r := range sessionRoots {
			s.addCandidateRoot(seen, r)
		}
	}
	projs, perr := search.ListProjects(home)
	if perr != nil {
		noteSecondBrain(log, "global search.db: %v", perr)
	}
	for _, p := range projs {
		s.addCandidateRoot(seen, p.Root)
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func (s *Store) addCandidateRoot(seen map[string]bool, r string) {
	r = strings.TrimSpace(r)
	if r == "" {
		return
	}
	clean := filepath.Clean(r)
	seen[clean] = true
	fi, err := os.Stat(clean)
	if err != nil || !fi.IsDir() {
		return
	}
	anc, isSelf := findAncestorGitRoot(clean)
	if anc == "" || isSelf || seen[anc] || s.isSearchDBUnchanged(anc) {
		return
	}
	if top := trail.Toplevel(clean); top != "" {
		seen[filepath.Clean(top)] = true
	}
}

func hasGitEntry(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

func findAncestorGitRoot(dir string) (string, bool) {
	if hasGitEntry(dir) {
		return dir, true
	}
	cur := filepath.Dir(dir)
	for cur != dir {
		if hasGitEntry(cur) {
			return cur, false
		}
		dir = cur
		cur = filepath.Dir(dir)
	}
	return "", false
}

func (s *Store) isSearchDBUnchanged(root string) bool {
	dbPath := filepath.Join(root, ".agents", "scratch", "search.db")
	fi, err := os.Lstat(dbPath)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	size, mtime := searchDBSignature(dbPath, fi)
	return s.sourceUnchanged(dbPath, size, mtime)
}

func searchDBSignature(dbPath string, fi os.FileInfo) (int64, int64) {
	size := fi.Size()
	mtime := fi.ModTime().UnixNano()
	if wfi, err := os.Lstat(dbPath + "-wal"); err == nil && wfi.Mode().IsRegular() && wfi.Size() > 0 {
		size += wfi.Size()
		if wm := wfi.ModTime().UnixNano(); wm > mtime {
			mtime = wm
		}
	}
	return size, mtime
}

func (s *Store) sourceUnchanged(source string, size, mtime int64) bool {
	var prevSize, prevMtime int64
	err := s.db.QueryRow(`SELECT size, mtime FROM ingest_state WHERE source = ?`, source).Scan(&prevSize, &prevMtime)
	return err == nil && prevSize == size && prevMtime == mtime
}

func (s *Store) recordIngestState(source string, size, mtime int64) error {
	_, err := s.db.Exec(
		`INSERT INTO ingest_state(source, size, mtime, offset) VALUES (?, ?, ?, 0)
		ON CONFLICT(source) DO UPDATE SET size = excluded.size, mtime = excluded.mtime`,
		source, size, mtime,
	)
	return err
}

func (s *Store) ingestProjectSearchDB(root string, idx int, tempDir func() (string, error), log io.Writer) {
	dbPath := filepath.Join(root, ".agents", "scratch", "search.db")
	fi, err := os.Lstat(dbPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
		return
	}
	if !fi.Mode().IsRegular() {
		noteSecondBrain(log, "%s: not a regular file", dbPath)
		return
	}
	size, mtime := searchDBSignature(dbPath, fi)
	if s.sourceUnchanged(dbPath, size, mtime) {
		return
	}
	baseTmp, err := tempDir()
	if err != nil {
		noteSecondBrain(log, "second-brain temp dir: %v", err)
		return
	}
	tmpDir := filepath.Join(baseTmp, strconv.Itoa(idx))
	if err := s.syncProjectSearchDB(root, dbPath, tmpDir); err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
		return
	}
	if err := s.recordIngestState(dbPath, size, mtime); err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
	}
}

func (s *Store) syncProjectSearchDB(root, dbPath, tmpDir string) error {
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	db, err := search.OpenCopy(filepath.Dir(dbPath), filepath.Base(dbPath), tmpDir)
	if err != nil {
		return err
	}
	defer db.Close()
	ledger, err := readLedgerSavings(db)
	if err != nil {
		return err
	}
	vets, err := readVetHistoryRows(db, filepath.Clean(root), ledger)
	if err != nil {
		return err
	}
	return s.upsertTurnVets(vets)
}

func readLedgerSavings(db *sql.DB) (map[turnKey]int, error) {
	rows, err := db.Query(`SELECT conversation_id, turn_num, COALESCE(SUM(saved_count), 0) FROM read_ledger_stats GROUP BY conversation_id, turn_num`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return map[turnKey]int{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := map[turnKey]int{}
	for rows.Next() {
		var convID string
		var turnNum, saved int
		if err := rows.Scan(&convID, &turnNum, &saved); err != nil {
			return nil, err
		}
		if saved > 0 {
			out[turnKey{convID: printable(strings.TrimSpace(convID)), turnNum: turnNum}] += saved
		}
	}
	return out, rows.Err()
}

func readVetHistoryRows(db *sql.DB, root string, ledger map[turnKey]int) ([]turnVetRow, error) {
	rows, err := db.Query(`SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, suggested_skills, pivot_files, created_at FROM vet_history ORDER BY turn_num, created_at, rowid`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []turnVetRow
	lastIdx := map[string]int{}
	for rows.Next() {
		var convID, intent, gate, rawPrompt, skillsCSV, pivotsCSV string
		var turnNum, score int
		var createdAt int64
		if err := rows.Scan(&convID, &turnNum, &intent, &gate, &score, &rawPrompt, &skillsCSV, &pivotsCSV, &createdAt); err != nil {
			return nil, err
		}
		convID = printable(strings.TrimSpace(convID))
		bypassed := 0
		if strings.HasPrefix(strings.TrimSpace(rawPrompt), "!") {
			bypassed = 1
		}
		k := turnKey{convID: convID, turnNum: turnNum}
		saved := ledger[k]
		delete(ledger, k)
		lastIdx[convID] = len(out)
		out = append(out, turnVetRow{
			projectRoot: root, convID: convID, turnNum: turnNum,
			intent: printable(strings.TrimSpace(intent)), gate: printable(strings.TrimSpace(gate)),
			score: min(100, max(0, score)), bypassed: bypassed,
			skillsCount: countCSVItems(skillsCSV), pivotsCount: countCSVItems(pivotsCSV),
			ledger: max(0, saved), createdAt: createdAt,
		})
	}
	for k, saved := range ledger {
		if idx, ok := lastIdx[k.convID]; ok && saved > 0 {
			out[idx].ledger += saved
		}
	}
	return out, rows.Err()
}

func countCSVItems(s string) int {
	n := 0
	for _, p := range strings.Split(s, ",") {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}

func (s *Store) upsertTurnVets(rows []turnVetRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, bypassed, skills_count, pivots_count, ledger_saved, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.projectRoot, r.convID, r.turnNum, r.intent, r.gate, r.score, r.bypassed, r.skillsCount, r.pivotsCount, r.ledger, r.createdAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
