package ingest

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
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

// IngestWorkWaves reads wave status snapshots under home into work_waves.
func (d Deps) IngestWorkWaves(home string, log io.Writer) {
	d.IngestWorkWavesWithLabel(home, "", log)
}

// IngestWorkWavesWithLabel reads wave status snapshots under home into work_waves scoped by label.
func (d Deps) IngestWorkWavesWithLabel(home, label string, log io.Writer) {
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
		d.ingestWaveDir(workDir, e.Name(), label, log)
	}
}

func waveIngestSource(statusPath, label string) string {
	if label == "" {
		return statusPath
	}
	return statusPath + "#label=" + label
}

func (d Deps) ingestWaveDir(workDir, dirName, label string, log io.Writer) {
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
	if d.sourceUnchanged(sourceKey, size, mtime) {
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
	if err := d.upsertWaveWorkers(dirName, label, snap, updatedAt); err != nil {
		noteSecondBrain(log, "%s: %v", statusPath, err)
		return
	}
	if err := d.recordIngestState(sourceKey, size, mtime); err != nil {
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

func (d Deps) upsertWaveWorkers(dirName, label string, snap waveSnapshot, updatedAt int64) error {
	if len(snap.Workers) == 0 {
		return nil
	}
	stamp := search.Printable(strings.TrimSpace(snap.Stamp))
	if stamp == "" {
		stamp = search.Printable(strings.TrimSpace(dirName))
	}
	stamp = EncodeWaveStamp(search.Printable(label), stamp)
	var projRoot string
	if strings.TrimSpace(snap.ProjectRoot) != "" {
		projRoot = search.Printable(filepath.Clean(strings.TrimSpace(snap.ProjectRoot)))
	}
	tx, err := d.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range snap.Workers {
		worker := search.Printable(strings.TrimSpace(w.Name))
		if worker == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at, setup_s, agy_s, check_s) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stamp, worker, projRoot, search.Printable(strings.TrimSpace(w.State)), search.Printable(strings.TrimSpace(w.Model)),
			max(0, w.StepCount), max(0, w.Commits), search.Printable(strings.TrimSpace(w.BaseCheck)),
			search.Printable(strings.TrimSpace(w.JudgeModel)), search.Printable(strings.TrimSpace(w.JudgeVerdict)), updatedAt,
			max(0, w.SetupSec), max(0, w.AgySec), max(0, w.CheckSec),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
