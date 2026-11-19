package metrics

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestSD122ToSD129IngestAndSyncFixes(t *testing.T) {
	// SD129: rune-safe truncation in scopedKey.
	nonASCII := strings.Repeat("é", 30)
	if sk := scopedKey(nonASCII, strings.Repeat("x", 200)); !utf8.ValidString(sk) {
		t.Fatalf("scopedKey invalid UTF-8: %q", sk)
	}

	// SD125: readVetHistoryRows deterministic ORDER BY + printable convID in readLedgerSavings.
	sdb := filepath.Join(t.TempDir(), "search.db")
	sqliteT(t, sdb,
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT)`,
		`CREATE TABLE read_ledger_stats(conversation_id TEXT, turn_num INT, saved_count INT)`,
		"INSERT INTO vet_history VALUES ('C1\x1b', 2, 'FIX', 'PROCEED', 80, 'p2', '', '', 200)",
		"INSERT INTO vet_history VALUES ('C1\x1b', 1, 'FIX', 'PROCEED', 70, 'p1', '', '', 100)",
		"INSERT INTO read_ledger_stats VALUES (' C1\x1b ', 99, 5)",
	)
	db, err := sql.Open("sqlite", search.DSN(sdb))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, err := readLedgerSavings(db)
	if err != nil {
		t.Fatalf("readLedgerSavings: %v", err)
	}
	vets, err := readVetHistoryRows(db, "/proj", ledger)
	if err != nil || len(vets) != 2 {
		t.Fatalf("readVetHistoryRows len=%d err=%v", len(vets), err)
	}
	if vets[0].turnNum != 1 || vets[1].turnNum != 2 || vets[1].ledger != 5 || vets[0].convID != "C1?" {
		t.Fatalf("vets order/ledger = %+v", vets)
	}

	// SD126: fixed-point parent root propagation when child precedes parent.
	convs := []agyConv{
		{id: "child", parent: "mid", root: "/home/u/.gemini/antigravity-cli/worktrees/mid/w1"},
		{id: "mid", parent: "root", root: "/home/u/.gemini/antigravity-cli/worktrees/root/w2"},
		{id: "root", root: "/tmp/myrepo"},
	}
	resolveWorktreeRoots(convs, nil)
	if convs[0].root != "/tmp/myrepo" || convs[1].root != "/tmp/myrepo" {
		t.Fatalf("worktree propagation = %+v", convs)
	}

	// SD128: dangling symlink conversation_summaries.db fails instead of silent skip.
	badHome := t.TempDir()
	agyDir := filepath.Join(badHome, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(agyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent-summary-target", filepath.Join(agyDir, "conversation_summaries.db")); err != nil {
		t.Fatal(err)
	}
	st := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	var logBuf bytes.Buffer
	res, err := Ingest(st, Options{Home: badHome}, &logBuf)
	if err != nil || len(res.Failed) != 1 {
		t.Fatalf("dangling symlink Failed=%v err=%v", res.Failed, err)
	}
}

func TestSD122SizeChangeZeroModifiedAndSD124ModelUpdate(t *testing.T) {
	home, root := agyFixture(t)
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	// Set zero timestamp so c.modified == 0, and test size growth & multi-model update.
	sqliteT(t, filepath.Join(dir, "conversation_summaries.db"),
		`DELETE FROM conversation_summaries`,
		`INSERT INTO conversation_summaries VALUES ('CM', '0001-01-01 00:00:00+00:00', '["file://`+root+`"]', '', '')`,
	)
	cdb := filepath.Join(dir, "conversations", "CM.db")
	sqliteT(t, cdb,
		"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
		"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)",
		"INSERT INTO steps VALUES (0, 14, 5, X'', NULL, NULL)",
		"INSERT INTO steps VALUES (1, 15, 5, X'', NULL, NULL)",
	)
	// Write a gen_metadata proto with model=gemini-4-carbon, then add gemini-4-hydrogen.
	db, err := sql.Open("sqlite", search.DSN(cdb))
	if err != nil {
		t.Fatal(err)
	}
	g1 := pbB(1, pbB(19, []byte("gemini-4-carbon")))
	g2 := pbB(1, pbB(19, []byte("gemini-4-hydrogen")))
	if _, err := db.Exec(`INSERT INTO gen_metadata VALUES (0, ?, 1)`, g1); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, st, Options{Home: home})
	var model string
	if err := st.db.QueryRow(`SELECT model FROM sessions WHERE id = 'CM'`).Scan(&model); err != nil || model != "gemini-4-carbon" {
		t.Fatalf("initial model=%q err=%v", model, err)
	}

	// Add second generation while c.modified remains 0: must re-ingest and upgrade model.
	db2, err := sql.Open("sqlite", search.DSN(cdb))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db2.Exec(`INSERT INTO gen_metadata VALUES (1, ?, 1)`, g2); err != nil {
		t.Fatal(err)
	}
	db2.Close()
	ingestT(t, st, Options{Home: home})
	if err := st.db.QueryRow(`SELECT model FROM sessions WHERE id = 'CM'`).Scan(&model); err != nil || model != "gemini-pro-orchestrator" {
		t.Fatalf("upgraded model=%q err=%v, want gemini-pro-orchestrator", model, err)
	}
}

func TestSD132ConcurrentMigrateAndExportCleanup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "race.db")
	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(p)
			if err != nil {
				errs <- err
				return
			}
			_ = s.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Open failed: %v", err)
	}

	// Export removes partial file when exportTable fails.
	st := openT(t, p)
	if _, err := st.db.Exec(`DROP TABLE trials`); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "partial.jsonl")
	if err := st.Export(out); err == nil {
		t.Fatal("expected Export error after dropping table")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("Export left partial file %s: %v", out, err)
	}
}

func TestSD130ToSD139HealthEvalReportAndTrajectoryFixes(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)

	// SD131: primary evals block dev fallback in both Show and ReportEvalsWithDevFallback; o.Label disables both.
	home := t.TempDir()
	mainStore := openT(t, Path(home))
	if err := mainStore.RecordTrial(Trial{
		Run: "main/1", Label: "eval=primary", RowVersion: 1, Kind: "capability", Harness: "v1", Status: "pass",
		StartedAt: now.Add(-time.Hour), Seconds: 5,
	}); err != nil {
		t.Fatal(err)
	}
	seedDevEvalStoreT(t, home, "eval=devonly", "pass", now.Add(-2*time.Hour))
	ev, err := ReportEvalsWithDevFallback(mainStore, home, ShowOptions{Since: since, Now: now})
	if err != nil || len(ev.Rows) != 1 || ev.Rows[0].Row != "primary" {
		t.Fatalf("primary evals should not mix dev rows: %+v err=%v", ev, err)
	}
	lblEv, err := ReportEvalsWithDevFallback(mainStore, home, ShowOptions{Label: "eval=primary", Since: since, Now: now})
	if err != nil || lblEv != nil {
		t.Fatalf("o.Label must disable dev fallback: %+v err=%v", lblEv, err)
	}
	sum, err := mainStore.Show(ShowOptions{Home: home, Since: since, Now: now, AllLabels: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sum.A.PassK["eval=devonly"]; ok {
		t.Fatalf("PassK merged dev evals despite primary eval rows: %+v", sum.A.PassK)
	}

	// SD131 schema < 5 guard in openDevEvalStoreRO.
	oldHome := t.TempDir()
	oldDev := filepath.Join(oldHome, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	if err := os.MkdirAll(filepath.Dir(oldDev), 0o700); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, oldDev, `CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`, `INSERT INTO meta VALUES ('schema', '4')`)
	if _, _, ok := openDevEvalStoreRO(oldHome); ok {
		t.Fatal("openDevEvalStoreRO accepted schema 4")
	}

	// SD133: wave + judge_verdicts deduplication & cross-family Gemini judge classification.
	ts := now.Add(-2 * time.Hour).UnixMilli()
	if _, err := mainStore.db.Exec(`INSERT INTO work_waves(stamp, worker, project_root, state, model, base_check, judge_model, judge_verdict, updated_at) VALUES
		('2026-11-18-100000-1111', 'w1', '/p/a', 'PASS', 'claude-opus-5-5', 'PASS', 'gemini-4-pro', 'PASS', ?),
		('2026-11-18-100000-1111', 'w2', '/p/a', 'FAIL', 'claude-opus-5-5', 'FAIL', 'claude-sonnet-4-6', 'FAIL', ?)`, ts, ts); err != nil {
		t.Fatal(err)
	}
	if err := mainStore.RecordJudgeVerdict(JudgeVerdictRow{
		TS: ts + 10, ProjectRoot: "/p/a", Target: "2026-11-18-100000-1111/w1", Model: "gemini-4-pro", CriterionHash: "c1", Verdict: "PASS",
	}); err != nil {
		t.Fatal(err)
	}
	sumWave, err := mainStore.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if sumWave.F.JudgePassed != 1 || sumWave.F.JudgeFailed != 1 || sumWave.F.JudgeFallback != 1 || sumWave.Health.JudgePassed != 1 {
		t.Fatalf("dedup/fallback judge counts = F:%+v Health:%+v", sumWave.F, sumWave.Health)
	}

	// SD136: redact $HOME in trajectories and ProjectRow, sanitize WriteTrajectoriesText.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	projInHome := filepath.Join(fakeHome, "work", "app")
	if _, err := mainStore.db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES
		('TRAJ1', 'agy', ?, ?, ?, ?)`, projInHome, "m\x1b[2J", ts, ts+60000); err != nil {
		t.Fatal(err)
	}
	trajs, err := mainStore.Trajectories(TrajectoryOptions{})
	if err != nil || len(trajs) == 0 || trajs[0].Project != "~/work/app" {
		t.Fatalf("trajectories project = %+v err=%v", trajs, err)
	}
	var tb bytes.Buffer
	WriteTrajectoriesText(&tb, trajs)
	if strings.Contains(tb.String(), fakeHome) || strings.Contains(tb.String(), "\x1b") {
		t.Fatalf("WriteTrajectoriesText leaked home or control byte: %q", tb.String())
	}

	// SD137: autoStateInWindow branches & AutoModelRuns reconciliation.
	if autoStateInWindow(time.Time{}, "", since, now) {
		t.Fatal("empty lastRun and empty date must return false")
	}
	if !autoStateInWindow(now.Add(-time.Hour), "", since, now) || autoStateInWindow(since.Add(-time.Hour), "", since, now) {
		t.Fatal("lastRun window check wrong")
	}
	if !autoStateInWindow(time.Time{}, "2026-11-18", since, now) || autoStateInWindow(time.Time{}, "bad-date", since, now) {
		t.Fatal("date fallback check wrong")
	}

	// SD138: plot.grid short Day.Date without panic.
	var cb strings.Builder
	newPlot(2, 10).grid(&cb, []Day{{Date: ""}, {Date: "2026"}})

	// SD139: copySQLiteTempParent empty scratchHome guard & cleanupCreatedDirs.
	if _, _, ok := copySQLiteTempParent("", filepath.Join(t.TempDir(), "metrics.db")); ok {
		t.Fatal("expected empty scratchHome with arbitrary dir to fail")
	}
	projScratch := filepath.Join(t.TempDir(), "proj", ".agents", "scratch")
	if err := os.MkdirAll(projScratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if dir, dirs, ok := copySQLiteTempParent("", filepath.Join(projScratch, "search.db")); !ok || dir != projScratch || len(dirs) != 0 {
		t.Fatalf("project scratch check got dir=%q dirs=%v ok=%v", dir, dirs, ok)
	}
	base := t.TempDir()
	d1 := filepath.Join(base, "a")
	d2 := filepath.Join(d1, "b")
	if err := os.MkdirAll(d2, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(base, "nonempty")
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keep, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupCreatedDirs([]string{keep, d1, d2})
	if _, err := os.Stat(d1); !os.IsNotExist(err) {
		t.Fatalf("cleanupCreatedDirs did not remove empty dirs: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanupCreatedDirs removed non-empty dir: %v", err)
	}
}
