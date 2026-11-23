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
	projectRoot, repoID, convID, intent, gate, directive       string
	suggestedSkills                                            string
	turnNum, score, bypassed, skillsCount, pivotsCount, ledger int
	subtaskCount, durationMS, timedOut, injectedBytes          int
	pivotsUsedCount, skillsLoadedCount                         int
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
	s.ingestSearchDBRoots(s.candidateProjectRoots(home, log), log)
	s.rejoinTurnVetSkillsLoaded(log)
}

func (s *Store) ingestSourceSecondBrain(o Options, log io.Writer) {
	if s == nil || s.db == nil {
		return
	}
	s.ingestSearchDBRoots(s.sourceProjectRoots(o), log)
	s.rejoinTurnVetSkillsLoaded(log)
}

func (s *Store) ingestSearchDBRoots(roots []string, log io.Writer) {
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

func (s *Store) sourceProjectRoots(o Options) []string {
	var rows *sql.Rows
	var err error
	switch {
	case o.Run != "":
		rows, err = s.db.Query(`SELECT DISTINCT project_root FROM sessions WHERE run = ? AND project_root IS NOT NULL AND project_root <> ''`, o.Run)
	case o.Label != "":
		rows, err = s.db.Query(`SELECT DISTINCT project_root FROM sessions WHERE label = ? AND project_root IS NOT NULL AND project_root <> ''`, o.Label)
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	var sessionRoots []string
	for rows.Next() {
		var r string
		if rows.Scan(&r) == nil {
			sessionRoots = append(sessionRoots, r)
		}
	}
	rows.Close()
	seen := map[string]bool{}
	for _, r := range sessionRoots {
		s.addCandidateRoot(seen, r)
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
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
	if !isRealDirChain(root, ".agents", "scratch") {
		return
	}
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
	if s.sourceUnchanged(dbPath, size, mtime) && !s.rootNeedsSuggestedSkillsBackfill(filepath.Clean(root)) {
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
	cleanRoot := filepath.Clean(root)
	repoID := ResolveRepoID(cleanRoot)
	vets, err := s.readVetHistoryRows(db, cleanRoot, repoID, ledger)
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

func (s *Store) upsertTurnVets(rows []turnVetRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const q = `INSERT OR REPLACE INTO turn_vets(
		project_root, repo_id, conversation_id, turn_num, intent, gate, score, bypassed,
		skills_count, pivots_count, ledger_saved, directive, subtask_count, duration_ms,
		timed_out, injected_bytes, pivots_used_count, skills_loaded_count, suggested_skills, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, r := range rows {
		if _, err := tx.Exec(
			q, r.projectRoot, r.repoID, r.convID, r.turnNum, r.intent, r.gate, r.score, r.bypassed,
			r.skillsCount, r.pivotsCount, r.ledger, r.directive, r.subtaskCount, r.durationMS,
			r.timedOut, r.injectedBytes, r.pivotsUsedCount, r.skillsLoadedCount, r.suggestedSkills, r.createdAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
