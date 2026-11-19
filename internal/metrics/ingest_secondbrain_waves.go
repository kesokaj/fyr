package metrics

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

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
	SetupSec     int    `json:"setup_s"`
	AgySec       int    `json:"agy_s"`
	CheckSec     int    `json:"check_s"`
	BaseCheck    string `json:"base_check"`
	JudgeModel   string `json:"judge_model"`
	JudgeVerdict string `json:"judge_verdict"`
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

func waveIngestSource(statusPath, label string) string {
	if label == "" {
		return statusPath
	}
	return statusPath + "#label=" + label
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
	sourceKey := waveIngestSource(statusPath, label)
	size, mtime := fi.Size(), fi.ModTime().UnixNano()
	if s.sourceUnchanged(sourceKey, size, mtime) {
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
		return
	}
	if err := s.recordIngestState(sourceKey, size, mtime); err != nil {
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
			`INSERT OR REPLACE INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at, setup_s, agy_s, check_s) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stamp, worker, projRoot, printable(strings.TrimSpace(w.State)), printable(strings.TrimSpace(w.Model)),
			max(0, w.StepCount), max(0, w.Commits), printable(strings.TrimSpace(w.BaseCheck)),
			printable(strings.TrimSpace(w.JudgeModel)), printable(strings.TrimSpace(w.JudgeVerdict)), updatedAt,
			max(0, w.SetupSec), max(0, w.AgySec), max(0, w.CheckSec),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
