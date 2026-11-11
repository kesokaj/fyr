package metrics

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestWorkWavesStageSecondsSchema7 verifies Phase 7:
//  1. Migration 7 upgrades a schema-6 store in place without losing rows,
//     defaulting existing work_waves rows to setup_s=0, agy_s=0, check_s=0.
//  2. ingestWorkWaves persists setup_s, agy_s and check_s from status.json.
//  3. copyDevWorkWaves copies setup_s, agy_s and check_s from schema-7 dev
//     stores while accepting schema-6 dev stores cleanly.
//  4. Store.ModelEvidence uses setup_s + agy_s + check_s for WorkMeanSeconds.
func TestWorkWavesStageSecondsSchema7(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metrics.db")

	// 1. Create a schema-6 database with an existing work_waves row, then upgrade to schema 7.
	saved := migrations
	if len(saved) < 7 {
		t.Fatalf("len(migrations) = %d, want >= 7", len(saved))
	}
	migrations = saved[:6]
	s6, err := Open(dbPath)
	if err != nil {
		migrations = saved
		t.Fatalf("open schema 6: %v", err)
	}
	if _, err := s6.db.Exec(
		`INSERT INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, updated_at) VALUES ('2026-11-10-010000-1', 'old-w', '/repo', 'PASS', 'gemini-4-carbon-high', 4, 1, 1762800000)`,
	); err != nil {
		s6.Close()
		migrations = saved
		t.Fatalf("insert schema-6 row: %v", err)
	}
	s6.Close()
	migrations = saved

	s7 := openT(t, dbPath)
	if got := s7.meta(t, "schema"); got != "7" {
		t.Fatalf("schema = %q, want 7", got)
	}
	var set0, agy0, chk0 int
	if err := s7.db.QueryRow(`SELECT setup_s, agy_s, check_s FROM work_waves WHERE worker = 'old-w'`).Scan(&set0, &agy0, &chk0); err != nil {
		t.Fatalf("query upgraded row: %v", err)
	}
	if set0 != 0 || agy0 != 0 || chk0 != 0 {
		t.Errorf("upgraded row stage seconds = (%d, %d, %d), want (0, 0, 0)", set0, agy0, chk0)
	}

	// 2. Ingest status.json with setup_s, agy_s, check_s into schema 7.
	home := filepath.Join(dir, "home")
	waveDir := filepath.Join(home, harness.StateDir, "work", "2026-11-11-120000-42")
	if err := os.MkdirAll(waveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 5, 0, 0, time.UTC)
	statusJSON := `{
  "stamp": "2026-11-11-120000-42",
  "project_root": "/repo",
  "updated_at": "` + now.Format(time.RFC3339) + `",
  "workers": [
    {
      "name": "p7-a",
      "state": "PASS",
      "model": "gemini-4-carbon-high",
      "step_count": 12,
      "commits": 1,
      "setup_s": 8,
      "agy_s": 95,
      "check_s": 17,
      "base_check": "PASS",
      "judge_model": "claude-opus-5-5-high",
      "judge_verdict": "PASS"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(waveDir, "status.json"), []byte(statusJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	s7.ingestWorkWaves(home, &logBuf)

	var set1, agy1, chk1 int
	if err := s7.db.QueryRow(`SELECT setup_s, agy_s, check_s FROM work_waves WHERE stamp = '2026-11-11-120000-42' AND worker = 'p7-a'`).Scan(&set1, &agy1, &chk1); err != nil {
		t.Fatalf("query ingested row: %v (log: %s)", err, logBuf.String())
	}
	if set1 != 8 || agy1 != 95 || chk1 != 17 {
		t.Fatalf("ingested stage seconds = (%d, %d, %d), want (8, 95, 17)", set1, agy1, chk1)
	}

	// 3. Copy from a schema-7 dev store preserves setup_s, agy_s, check_s.
	devPath := filepath.Join(dir, "dev-metrics.db")
	devStore := openT(t, devPath)
	if _, err := devStore.db.Exec(
		`INSERT INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, setup_s, agy_s, check_s, updated_at) VALUES ('2026-11-11-130000-99', 'p7-dev', '/repo', 'PASS', 'gemini-4-carbon-high', 6, 1, 5, 40, 15, ?)`,
		now.Unix(),
	); err != nil {
		t.Fatal(err)
	}
	devStore.Close()
	tmpCopy := filepath.Join(dir, "tmp-copy")
	if err := os.MkdirAll(tmpCopy, 0o700); err != nil {
		t.Fatal(err)
	}
	s7.copyDevStoreTables(devPath, tmpCopy, "fyr-work", &logBuf)

	var set2, agy2, chk2 int
	if err := s7.db.QueryRow(`SELECT setup_s, agy_s, check_s FROM work_waves WHERE worker = 'p7-dev'`).Scan(&set2, &agy2, &chk2); err != nil {
		t.Fatalf("query copied dev row: %v (log: %s)", err, logBuf.String())
	}
	if set2 != 5 || agy2 != 40 || chk2 != 15 {
		t.Fatalf("copied dev stage seconds = (%d, %d, %d), want (5, 40, 15)", set2, agy2, chk2)
	}

	// Schema-6 dev store (without stage columns) copies cleanly with (0, 0, 0).
	dev6Path := filepath.Join(dir, "dev6-metrics.db")
	migrations = saved[:6]
	dev6, err := Open(dev6Path)
	if err != nil {
		migrations = saved
		t.Fatalf("open dev6: %v", err)
	}
	if _, err := dev6.db.Exec(
		`INSERT INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, updated_at) VALUES ('2026-11-10-020000-8', 'v6-dev', '/repo', 'PASS', 'gemini-3.8-flash-high', 3, 1, ?)`,
		now.Unix(),
	); err != nil {
		dev6.Close()
		migrations = saved
		t.Fatal(err)
	}
	dev6.Close()
	migrations = saved
	s7.copyDevStoreTables(dev6Path, tmpCopy, "fyr-work", &logBuf)
	var set6, agy6, chk6 int
	if err := s7.db.QueryRow(`SELECT setup_s, agy_s, check_s FROM work_waves WHERE worker = 'v6-dev'`).Scan(&set6, &agy6, &chk6); err != nil || set6 != 0 || agy6 != 0 || chk6 != 0 {
		t.Fatalf("copied v6 dev row = (%d, %d, %d), err=%v; want (0, 0, 0)", set6, agy6, chk6, err)
	}

	// 4. ModelEvidence averages (8+95+17)=120s and (5+40+15)=60s -> 90s across the 2 timed runs!
	ev, err := s7.ModelEvidence(now.Add(-24*time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ModelEvidence: %v", err)
	}
	me := ev["gemini-4-carbon-high"]
	if me.WorkRuns != 2 || me.WorkPass != 2 || me.WorkMeanSeconds != 90 {
		t.Errorf("ModelEvidence = %+v, want WorkRuns=2 WorkPass=2 WorkMeanSeconds=90", me)
	}
}
