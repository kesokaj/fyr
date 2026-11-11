package metrics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
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

type waveSnapshot struct {
	Stamp       string               `json:"stamp"`
	ProjectRoot string               `json:"project_root"`
	UpdatedAt   string               `json:"updated_at"`
	Workers     []waveWorkerSnapshot `json:"workers"`
}

type waveWorkerSnapshot struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Model        string `json:"model"`
	StepCount    int    `json:"step_count"`
	Commits      int    `json:"commits"`
	BaseCheck    string `json:"base_check"`
	JudgeModel   string `json:"judge_model"`
	JudgeVerdict string `json:"judge_verdict"`
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
	tmp, err := os.MkdirTemp(filepath.Dir(s.path), "metrics-sb-")
	if err != nil {
		noteSecondBrain(log, "second-brain temp dir: %v", err)
		return
	}
	defer os.RemoveAll(tmp)
	for i, root := range roots {
		s.ingestProjectSearchDB(root, filepath.Join(tmp, strconv.Itoa(i)), log)
	}
}

func (s *Store) candidateProjectRoots(home string, log io.Writer) []string {
	seen := map[string]bool{}
	add := func(r string) {
		r = strings.TrimSpace(r)
		if r == "" {
			return
		}
		clean := filepath.Clean(r)
		seen[clean] = true
		if fi, err := os.Stat(clean); err == nil && fi.IsDir() {
			if top := trail.Toplevel(clean); top != "" {
				seen[filepath.Clean(top)] = true
			}
		}
	}
	rows, err := s.db.Query(`SELECT DISTINCT project_root FROM sessions WHERE project_root IS NOT NULL AND project_root <> ''`)
	if err == nil {
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				add(r)
			}
		}
		rows.Close()
	}
	projs, perr := search.ListProjects(home)
	if perr != nil {
		noteSecondBrain(log, "global search.db: %v", perr)
	}
	for _, p := range projs {
		add(p.Root)
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func (s *Store) ingestProjectSearchDB(root, tmpDir string, log io.Writer) {
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
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
		return
	}
	db, err := search.OpenCopy(filepath.Dir(dbPath), filepath.Base(dbPath), tmpDir)
	if err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
		return
	}
	defer db.Close()
	ledger, err := readLedgerSavings(db)
	if err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
		return
	}
	vets, err := readVetHistoryRows(db, filepath.Clean(root), ledger)
	if err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
		return
	}
	if err := s.upsertTurnVets(vets); err != nil {
		noteSecondBrain(log, "%s: %v", dbPath, err)
	}
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
			out[turnKey{convID: strings.TrimSpace(convID), turnNum: turnNum}] += saved
		}
	}
	return out, rows.Err()
}

func readVetHistoryRows(db *sql.DB, root string, ledger map[turnKey]int) ([]turnVetRow, error) {
	rows, err := db.Query(`SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, suggested_skills, pivot_files, created_at FROM vet_history`)
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

func (s *Store) ingestWorkWaves(home string, log io.Writer) {
	s.ingestWorkWavesWithLabel(home, "", log)
}

func (s *Store) ingestWorkWavesWithLabel(home, label string, log io.Writer) {
	workDir := filepath.Join(home, harness.StateDir, "work")
	entries, err := os.ReadDir(workDir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		noteSecondBrain(log, "%s: %v", workDir, err)
		return
	}
	for _, e := range entries {
		if !e.Type().IsDir() {
			continue
		}
		s.ingestWaveDir(workDir, e.Name(), label, log)
	}
}

func (s *Store) ingestWaveDir(workDir, dirName, label string, log io.Writer) {
	statusPath := filepath.Join(workDir, dirName, "status.json")
	fi, err := os.Lstat(statusPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		noteSecondBrain(log, "%s: %v", statusPath, err)
		return
	}
	if !fi.Mode().IsRegular() || fi.Size() > 4<<20 {
		noteSecondBrain(log, "%s: not a regular file or exceeds 4 MiB", statusPath)
		return
	}
	data, err := os.ReadFile(statusPath)
	if err != nil {
		noteSecondBrain(log, "%s: %v", statusPath, err)
		return
	}
	var snap waveSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		noteSecondBrain(log, "%s: %v", statusPath, err)
		return
	}
	updatedAt := parseWaveUpdatedAt(snap.UpdatedAt, fi.ModTime())
	if err := s.upsertWaveWorkers(dirName, label, snap, updatedAt); err != nil {
		noteSecondBrain(log, "%s: %v", statusPath, err)
	}
}

func parseWaveUpdatedAt(raw string, fallback time.Time) int64 {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, raw); err == nil && t.Unix() >= 0 {
			return t.Unix()
		}
	}
	if fallback.Unix() >= 0 {
		return fallback.Unix()
	}
	return 0
}

func (s *Store) upsertWaveWorkers(dirName, label string, snap waveSnapshot, updatedAt int64) error {
	if len(snap.Workers) == 0 {
		return nil
	}
	stamp := printable(strings.TrimSpace(snap.Stamp))
	if stamp == "" {
		stamp = printable(strings.TrimSpace(dirName))
	}
	stamp = encodeWaveStamp(printable(label), stamp)
	var projRoot string
	if strings.TrimSpace(snap.ProjectRoot) != "" {
		projRoot = printable(filepath.Clean(strings.TrimSpace(snap.ProjectRoot)))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range snap.Workers {
		worker := printable(strings.TrimSpace(w.Name))
		if worker == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stamp, worker, projRoot, printable(strings.TrimSpace(w.State)), printable(strings.TrimSpace(w.Model)),
			max(0, w.StepCount), max(0, w.Commits), printable(strings.TrimSpace(w.BaseCheck)),
			printable(strings.TrimSpace(w.JudgeModel)), printable(strings.TrimSpace(w.JudgeVerdict)), updatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
