package watch

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

func liveAgyProcs() func() ([]ProcInfo, error) {
	return func() ([]ProcInfo, error) {
		return []ProcInfo{
			{PID: 101, PPID: 1, State: "S", Wchan: "0", Cmdline: "/usr/local/bin/agy --mode code", ElapsedSec: 20},
		}, nil
	}
}

func seedMetricsSessions(t *testing.T, home string, rows [][2]any) {
	t.Helper()
	dbPath := filepath.Join(home, harness.StateDir, "metrics.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", search.DSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions(id TEXT PRIMARY KEY, vendor TEXT, started_at INTEGER, ended_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES (?, 'agy', 1000, ?)`, r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
}

type richMetaRow struct {
	id, role, agent string
	turns, tools    int
	updatedAt       int64
}

func seedRichSearchMeta(t *testing.T, dir string, padMB int, rows []richMetaRow) string {
	t.Helper()
	sdbPath := filepath.Join(dir, ".agents", "scratch", "search.db")
	if err := os.MkdirAll(filepath.Dir(sdbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", search.DSN(sdbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE session_meta(conversation_id TEXT PRIMARY KEY, generation_id INT, last_transcript_bytes INT, turn_count INT, est_tokens INT, agent_role TEXT DEFAULT '', agent_name TEXT DEFAULT '', tool_calls INT DEFAULT 0, updated_at INT DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO session_meta VALUES (?, 0, 1000, ?, 250, ?, ?, ?, ?)`, r.id, r.turns, r.role, r.agent, r.tools, r.updatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if padMB > 0 {
		if _, err := db.Exec(`CREATE TABLE pad_blobs(id INTEGER PRIMARY KEY, data BLOB)`); err != nil {
			t.Fatal(err)
		}
		chunk := bytes.Repeat([]byte("A"), 1<<20)
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		stmt, err := tx.Prepare(`INSERT INTO pad_blobs(data) VALUES (?)`)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < padMB; i++ {
			if _, err := stmt.Exec(chunk); err != nil {
				t.Fatal(err)
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
	}
	return sdbPath
}

func hookMetaCopyCounter(t *testing.T) *int {
	t.Helper()
	var count int
	prev := openMetaCopy
	openMetaCopy = func(dir, name, tmp string) (*sql.DB, error) {
		count++
		return prev(dir, name, tmp)
	}
	t.Cleanup(func() { openMetaCopy = prev })
	return &count
}

func testV910ReadOnly40MB(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	home, dir := t.TempDir(), t.TempDir()
	seedWatchDBs(t, home, dir, []convSpec{
		{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
		{id: "sub-40mb", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
	}, nil)
	sdbPath := seedRichSearchMeta(t, dir, 41, []richMetaRow{
		{id: "sub-40mb", role: "reviewer", agent: "reviewer", turns: 32, tools: 32, updatedAt: now.Add(-30 * time.Second).Unix()},
	})
	fi, err := os.Stat(sdbPath)
	if err != nil || fi.Size() < 40<<20 {
		t.Fatalf("synthetic search.db size = %d (err=%v), want >= 40 MiB", fi.Size(), err)
	}

	// Verify openReadOnlySQLite enforces PRAGMA query_only=ON (writes must fail).
	roDB, err := openReadOnlySQLite(sdbPath)
	if err != nil {
		t.Fatalf("openReadOnlySQLite: %v", err)
	}
	if _, wErr := roDB.Exec(`INSERT INTO session_meta(conversation_id, turn_count) VALUES ('write-fail', 1)`); wErr == nil {
		t.Error("expected write on PRAGMA query_only=ON connection to fail")
	}
	_ = roDB.Close()

	copies := hookMetaCopyCounter(t)
	start := time.Now()
	rep, err := Check(context.Background(), Options{
		Home:       home,
		Dir:        dir,
		Now:        func() time.Time { return now },
		ProcLister: liveAgyProcs(),
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Check on 40MB search.db: %v", err)
	}
	if *copies != 0 {
		t.Fatalf("40MB search.db invoked OpenCopy %d times, want 0 (direct RO open)", *copies)
	}
	if !hasWatchFinding(rep, kindBudgetNearLimit, "sub-40mb") {
		t.Fatalf("missing BUDGET_NEAR_LIMIT for sub-40mb: %+v", rep.Findings)
	}
	t.Logf("watch.Check on %d MiB search.db completed in %v (budget %v, copies=%d)", fi.Size()>>20, elapsed, watch40MBReadBudget, *copies)
	if elapsed >= watch40MBReadBudget {
		t.Errorf("watch.Check on 40MB search.db took %v, want < %v (direct RO open without copy)", elapsed, watch40MBReadBudget)
	}
	assertNoWatchTempDirs(t, home)
}

func testV910ReadOnlyFallbackAndRefusal(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

	t.Run("busy_lock_falls_back_to_open_copy", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
			{id: "sub-locked", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
		}, nil)
		sdbPath := filepath.Join(dir, ".agents", "scratch", "search.db")
		if err := os.MkdirAll(filepath.Dir(sdbPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// Rollback journal mode + BEGIN EXCLUSIVE causes mode=ro readers to hit SQLITE_BUSY.
		lockDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(sdbPath)+"?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(50)")
		if err != nil {
			t.Fatal(err)
		}
		defer lockDB.Close()
		lockDB.SetMaxOpenConns(1)
		if _, err := lockDB.Exec(`CREATE TABLE session_meta(conversation_id TEXT PRIMARY KEY, turn_count INT, tool_calls INT, agent_role TEXT)`); err != nil {
			t.Fatal(err)
		}
		if _, err := lockDB.Exec(`INSERT INTO session_meta VALUES ('sub-locked', 34, 34, 'reviewer')`); err != nil {
			t.Fatal(err)
		}
		if _, err := lockDB.Exec(`BEGIN EXCLUSIVE`); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = lockDB.Exec(`ROLLBACK`) }()

		copies := hookMetaCopyCounter(t)
		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check under EXCLUSIVE lock: %v", err)
		}
		if *copies != 1 {
			t.Errorf("expected 1 OpenCopy fallback under SQLITE_BUSY, got %d", *copies)
		}
		if !hasWatchFinding(rep, kindBudgetNearLimit, "sub-locked") {
			t.Errorf("missing BUDGET_NEAR_LIMIT after OpenCopy fallback: %+v", rep.Findings)
		}
	})

	t.Run("cantopen_wal_in_ro_dir_falls_back_to_open_copy", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses 0500 directory permission checks")
		}
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
			{id: "sub-cantopen", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
		}, nil)
		sdbPath := seedRichSearchMeta(t, dir, 0, []richMetaRow{
			{id: "sub-cantopen", role: "reviewer", agent: "reviewer", turns: 35, tools: 35, updatedAt: now.Add(-30 * time.Second).Unix()},
		})
		// Leave a WAL-mode DB without -shm inside a 0500 dir so mode=ro fails with SQLITE_CANTOPEN.
		_ = os.Remove(sdbPath + "-shm")
		scratchDir := filepath.Dir(sdbPath)
		if err := os.Chmod(scratchDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(scratchDir, 0o755) })

		copies := hookMetaCopyCounter(t)
		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check under RO WAL dir: %v", err)
		}
		if *copies != 1 {
			t.Errorf("expected 1 OpenCopy fallback under SQLITE_CANTOPEN, got %d", *copies)
		}
		if !hasWatchFinding(rep, kindBudgetNearLimit, "sub-cantopen") {
			t.Errorf("missing BUDGET_NEAR_LIMIT after SQLITE_CANTOPEN fallback: %+v", rep.Findings)
		}
	})

	t.Run("corrupt_db_no_fallback_and_symlink_refused", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		corruptPath := filepath.Join(dir, ".agents", "scratch", "search.db")
		mustWriteWatchFile(t, corruptPath, "not a sqlite database at all", now)
		copies := hookMetaCopyCounter(t)
		if _, err := Check(context.Background(), Options{
			Home: home, Dir: dir, Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
		}); err == nil {
			t.Fatal("expected error for corrupt search.db")
		}
		if *copies != 0 {
			t.Errorf("corrupt search.db should NOT invoke OpenCopy fallback, got %d", *copies)
		}

		realTarget := filepath.Join(t.TempDir(), "other.db")
		mustWriteWatchFile(t, realTarget, "x", now)
		_ = os.Remove(corruptPath)
		if err := os.Symlink(realTarget, corruptPath); err != nil {
			t.Fatal(err)
		}
		_, err := Check(context.Background(), Options{
			Home: home, Dir: dir, Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
		})
		if !errors.Is(err, search.ErrNotRegular) {
			t.Fatalf("symlinked search.db err = %v, want search.ErrNotRegular", err)
		}
		if *copies != 0 {
			t.Errorf("symlinked search.db should NOT invoke OpenCopy, got %d", *copies)
		}
		for _, tc := range []struct {
			code int
			want bool
		}{
			{sqliteBusyCode, true},
			{sqliteLockedCode, true},
			{sqliteCantOpenCode, true},
			{sqliteReadonlyDirectory, true},
			{11, false}, // SQLITE_CORRUPT
			{26, false}, // SQLITE_NOTADB
		} {
			if got := isSQLiteROFallbackErr(fakeSQLiteErr(tc.code)); got != tc.want {
				t.Errorf("isSQLiteROFallbackErr(code=%d) = %v, want %v", tc.code, got, tc.want)
			}
		}
	})
}

type fakeSQLiteErr int

func (e fakeSQLiteErr) Error() string { return "sqlite err" }
func (e fakeSQLiteErr) Code() int     { return int(e) }

func testV910LivenessFilters(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

	t.Run("ended_in_metrics_db_skipped", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
			{id: "sub-ended-int", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
			{id: "sub-ended-str", ws: dir, agent: "security", parent: "lead", mod: now.Add(-10 * time.Minute)},
		}, map[string]int{
			"sub-ended-int": 36,
			"sub-ended-str": 36,
		})
		seedMetricsSessionsWithOutcome(t, home, [][3]any{
			{"sub-ended-int", "pass", now.Add(-30 * time.Second).UnixMilli()},
			{"sub-ended-str", "", "2026-11-23T07:50:00Z"},
		})
		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if !rep.OK || len(rep.Findings) != 0 {
			t.Fatalf("ended sessions in metrics.db should return ok=true, got %+v", rep)
		}
		repConv, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			ConvID:     "sub-ended-int",
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil || !repConv.OK {
			t.Fatalf("explicit --conv on session with terminal outcome should return ok=true, got rep=%+v err=%v", repConv, err)
		}
	})

	t.Run("stale_outside_active_window_skipped", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
			{id: "sub-stale-6m", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-6 * time.Minute), transcript: "{\"content\":\"done\"}\n"},
			{id: "sub-stale-2h", ws: dir, agent: "security", parent: "lead", mod: now.Add(-2 * time.Hour)},
		}, map[string]int{
			"sub-stale-6m": 38,
			"sub-stale-2h": 40,
		})
		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if !rep.OK || len(rep.Findings) != 0 {
			t.Fatalf("stale sessions outside 300s window should return ok=true, got %+v", rep)
		}
		repWide, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			IdleSec:    200,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check wide window: %v", err)
		}
		if !hasWatchFinding(repWide, kindBudgetNearLimit, "sub-stale-6m") || hasWatchFinding(repWide, kindBudgetNearLimit, "sub-stale-2h") {
			t.Fatalf("IdleSec=200 window (400s) findings = %+v, want only sub-stale-6m", repWide.Findings)
		}
	})

	t.Run("updated_at_column_staleness", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedRichSearchMeta(t, dir, 0, []richMetaRow{
			{id: "sub-old-upd", role: "reviewer", agent: "reviewer", turns: 38, tools: 38, updatedAt: now.Add(-10 * time.Minute).Unix()},
			{id: "sub-new-upd", role: "reviewer", agent: "reviewer", turns: 35, tools: 35, updatedAt: now.Add(-2 * time.Minute).Unix()},
		})
		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if hasWatchFinding(rep, kindBudgetNearLimit, "sub-old-upd") || !hasWatchFinding(rep, kindBudgetNearLimit, "sub-new-upd") {
			t.Fatalf("updated_at filter findings = %+v, want only sub-new-upd", rep.Findings)
		}
	})

	t.Run("no_live_agy_proc_skipped", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
			{id: "sub-dead-host", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
		}, map[string]int{"sub-dead-host": 38})

		for _, tc := range []struct {
			name  string
			procs []ProcInfo
		}{
			{"only_bash", []ProcInfo{{PID: 101, PPID: 1, State: "S", Wchan: "0", Cmdline: "/bin/bash", ElapsedSec: 10}}},
			{"zombie_agy", []ProcInfo{{PID: 102, PPID: 1, State: "Z", Wchan: "0", Cmdline: "/usr/local/bin/agy", ElapsedSec: 10}}},
			{"empty_inspectable", []ProcInfo{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rep, err := Check(context.Background(), Options{
					Home:       home,
					Dir:        dir,
					Now:        func() time.Time { return now },
					ProcLister: func() ([]ProcInfo, error) { return tc.procs, nil },
				})
				if err != nil || !rep.OK {
					t.Fatalf("%s: expected ok=true with no live agy proc, got rep=%+v err=%v", tc.name, rep, err)
				}
			})
		}
	})

	t.Run("live_active_over_budget_flagged", func(t *testing.T) {
		home, dir := t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
			{id: "sub-live-null", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
			{id: "sub-live-zero", ws: dir, agent: "security", parent: "lead", mod: now.Add(-1 * time.Minute)},
		}, map[string]int{
			"sub-live-null": 32,
			"sub-live-zero": 35,
		})
		seedMetricsSessions(t, home, [][2]any{
			{"sub-live-null", nil},
			{"sub-live-zero", 0},
		})
		rep, err := Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if rep.OK || !hasWatchFinding(rep, kindBudgetNearLimit, "sub-live-null") || !hasWatchFinding(rep, kindBudgetNearLimit, "sub-live-zero") {
			t.Fatalf("expected BUDGET_NEAR_LIMIT for live active subagents, got %+v", rep)
		}
	})
}

func testV910AlignedRoleCaps(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	home, dir := t.TempDir(), t.TempDir()
	convs := []convSpec{{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)}}
	var metaRows []richMetaRow
	add := func(id, role, agent string, calls int) {
		convs = append(convs, convSpec{id: id, ws: dir, agent: agent, parent: "lead", mod: now.Add(-1 * time.Minute)})
		metaRows = append(metaRows, richMetaRow{id: id, role: role, agent: agent, turns: calls, tools: calls, updatedAt: now.Add(-30 * time.Second).Unix()})
	}
	add("b-ok", "builder-p12", "builder-p12", 45)
	add("b-hi", "builder_x", "builder_x", 96)
	add("c-ok", "coder_1", "coder_1", 45)
	add("s-hi", "self", "self", 96)
	add("rev-hi", "reviewer", "", 32)
	add("judge-hi", "judge", "judge", 32)
	add("unk-hi", "custom-audit", "custom-audit", 32)
	add("unk-ok", "unknown-role", "unknown-role", 31)
	convs = append(convs, convSpec{id: "main-big", ws: dir, mod: now.Add(-1 * time.Minute)})
	metaRows = append(metaRows, richMetaRow{id: "main-big", role: "main", turns: 200, tools: 200, updatedAt: now.Add(-30 * time.Second).Unix()})

	seedWatchDBs(t, home, dir, convs, nil)
	seedRichSearchMeta(t, dir, 0, metaRows)

	rep, err := Check(context.Background(), Options{
		Home:       home,
		Dir:        dir,
		Now:        func() time.Time { return now },
		ProcLister: liveAgyProcs(),
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, want := range []string{"b-hi", "s-hi", "rev-hi", "judge-hi", "unk-hi"} {
		if !hasWatchFinding(rep, kindBudgetNearLimit, want) {
			t.Errorf("missing BUDGET_NEAR_LIMIT for %s: %+v", want, rep.Findings)
		}
	}
	for _, noWant := range []string{"b-ok", "c-ok", "unk-ok", "main-big"} {
		if hasWatchFinding(rep, kindBudgetNearLimit, noWant) {
			t.Errorf("unexpected BUDGET_NEAR_LIMIT for %s: %+v", noWant, rep.Findings)
		}
	}
	for _, r := range []string{"self", "builder", "builder-p12", "builder_x", "coder", "coder_1", "reviewer", "security", "verifier", "explorer", "dreamer", "documentation", "research", "judge", "custom-audit", "unknown-role"} {
		_, wantCap := search.SubagentBudgetCap(r)
		_, gotCap, ok := resolveSubagentBudget("conv-"+r, r, "", "lead", nil, false)
		if !ok || gotCap != wantCap {
			t.Errorf("resolveSubagentBudget(%q) = cap %d ok=%v, want %d ok=true", r, gotCap, ok, wantCap)
		}
	}
}

func testV910OpenSSHPrompts(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	home, dir := t.TempDir(), t.TempDir()
	mustMatch := []convSpec{
		{id: "ssh-yesno-tr", ws: dir, mod: now.Add(-1 * time.Minute), transcript: `{"step_index":2,"status":"RUNNING","content":"Are you sure you want to continue connecting (yes/no)?"}` + "\n"},
		{id: "ssh-fp-tr", ws: dir, mod: now.Add(-1 * time.Minute), transcript: `{"step_index":2,"status":"RUNNING","content":"Are you sure you want to continue connecting (yes/no/[fingerprint])? "}` + "\n"},
		{id: "ssh-yesno-step", ws: dir, mod: now.Add(-1 * time.Minute), stepOutputs: map[string]string{"1": "Are you sure you want to continue connecting (yes/no)? "}},
		{id: "ssh-fp-step", ws: dir, mod: now.Add(-1 * time.Minute), stepOutputs: map[string]string{"1": "Are you sure you want to continue connecting (yes/no/[fingerprint])? "}},
		{id: "yn-bracket", ws: dir, mod: now.Add(-1 * time.Minute), transcript: `{"content":"Overwrite config? [y/N]"}` + "\n"},
		{id: "yn-paren", ws: dir, mod: now.Add(-1 * time.Minute), transcript: `{"content":"Proceed with cleanup? (y/n)"}` + "\n"},
		{id: "pw-prompt", ws: dir, mod: now.Add(-1 * time.Minute), transcript: "{\"content\":\"Enter pa" + "ssword:\"}\n"},
	}
	mustSkip := []convSpec{
		{id: "benign-ok", ws: dir, mod: now.Add(-1 * time.Minute), transcript: `{"content":"git push complete, everything up-to-date"}` + "\n"},
		{id: "benign-prose", ws: dir, mod: now.Add(-1 * time.Minute), transcript: `{"content":"option accepts (yes/no) values without prompt"}` + "\n"},
		{id: "benign-fp", ws: dir, mod: now.Add(-1 * time.Minute), transcript: `{"content":"ED25519 key fingerprint is SHA256:abc123456"}` + "\n"},
	}
	seedWatchDBs(t, home, dir, append(mustMatch, mustSkip...), nil)
	rep, err := Check(context.Background(), Options{
		Home:       home,
		Dir:        dir,
		Now:        func() time.Time { return now },
		ProcLister: liveAgyProcs(),
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, tc := range mustMatch {
		if !hasWatchFinding(rep, kindInteractiveWait, tc.id) {
			t.Errorf("expected INTERACTIVE_WAIT for %s, got %+v", tc.id, rep.Findings)
		}
	}
	for _, tc := range mustSkip {
		if hasWatchFinding(rep, kindInteractiveWait, tc.id) {
			t.Errorf("unexpected INTERACTIVE_WAIT false positive for %s: %+v", tc.id, rep.Findings)
		}
	}
}

func TestV910WatchNoDeadlockWithAuxTables(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	home, dir := t.TempDir(), t.TempDir()
	seedWatchDBs(t, home, dir, []convSpec{
		{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
		{id: "sub-aux", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
	}, nil)
	sdbPath := seedRichSearchMeta(t, dir, 0, []richMetaRow{
		{id: "sub-aux", role: "reviewer", agent: "reviewer", turns: 35, tools: 35, updatedAt: now.Add(-20 * time.Second).Unix()},
	})
	db, err := sql.Open("sqlite", search.DSN(sdbPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE watchdog_ring(conversation_id TEXT, ts INTEGER)`,
		`CREATE TABLE vet_history(conversation_id TEXT, created_at INTEGER)`,
		`CREATE TABLE read_ledger(conversation_id TEXT, updated_at INTEGER)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	ts := now.Add(-10 * time.Second).Unix()
	if _, err := db.Exec(`INSERT INTO vet_history VALUES ('sub-aux', ?)`, ts); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	done := make(chan error, 1)
	var rep Report
	go func() {
		var checkErr error
		rep, checkErr = Check(context.Background(), Options{
			Home:       home,
			Dir:        dir,
			Now:        func() time.Time { return now },
			ProcLister: liveAgyProcs(),
		})
		done <- checkErr
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Check with aux tables: %v", err)
		}
		if !hasWatchFinding(rep, kindBudgetNearLimit, "sub-aux") {
			t.Errorf("expected BUDGET_NEAR_LIMIT for sub-aux, got %+v", rep.Findings)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch.Check deadlocked on SetMaxOpenConns(1) with aux tables open during rows.Next")
	}
}

func seedMetricsSessionsWithOutcome(t *testing.T, home string, rows [][3]any) {
	t.Helper()
	dbPath := filepath.Join(home, harness.StateDir, "metrics.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", search.DSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions(id TEXT PRIMARY KEY, vendor TEXT, outcome TEXT, started_at INTEGER, ended_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO sessions(id, vendor, outcome, started_at, ended_at) VALUES (?, 'agy', ?, 1000, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestV910SecuritySymlinkScratchAndMetricsStateDir(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

	t.Run("symlinked_scratch_dir_refused", func(t *testing.T) {
		home, dir, extDir := t.TempDir(), t.TempDir(), t.TempDir()
		seedRichSearchMeta(t, extDir, 0, []richMetaRow{
			{id: "sub-ext", role: "reviewer", agent: "reviewer", turns: 35, tools: 35, updatedAt: now.Add(-10 * time.Second).Unix()},
		})
		if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(extDir, ".agents", "scratch"), filepath.Join(dir, ".agents", "scratch")); err != nil {
			t.Fatal(err)
		}
		copies := hookMetaCopyCounter(t)
		if _, err := Check(context.Background(), Options{
			Home: home, Dir: dir, Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
		}); err == nil {
			t.Fatal("expected symlinked .agents/scratch to be refused")
		}
		if *copies != 0 {
			t.Errorf("symlinked .agents/scratch invoked OpenCopy %d times, want 0", *copies)
		}
	})

	t.Run("symlinked_scratch_without_searchdb_refused_before_artifacts", func(t *testing.T) {
		home, dir, extDir := t.TempDir(), t.TempDir(), t.TempDir()
		extArt := filepath.Join(extDir, "agents", "stuck.md")
		mustWriteWatchFile(t, extArt, "# Stuck\n## Status: IN PROGRESS\n", now.Add(-5*time.Minute))
		if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(extDir, filepath.Join(dir, ".agents", "scratch")); err != nil {
			t.Fatal(err)
		}
		_, err := Check(context.Background(), Options{
			Home: home, Dir: dir, Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
		})
		if !errors.Is(err, trail.ErrStampSymlink) {
			t.Fatalf("symlinked .agents/scratch without search.db err = %v, want trail.ErrStampSymlink", err)
		}
	})

	t.Run("symlinked_agents_dir_refused", func(t *testing.T) {
		home, dir, extAgents := t.TempDir(), t.TempDir(), t.TempDir()
		mustWriteWatchFile(t, filepath.Join(extAgents, "stuck.md"), "# Stuck\n## Status: IN PROGRESS\n", now.Add(-5*time.Minute))
		if err := os.MkdirAll(filepath.Join(dir, ".agents", "scratch"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(extAgents, filepath.Join(dir, ".agents", "scratch", "agents")); err != nil {
			t.Fatal(err)
		}
		checked, findings, artErr := inspectArtifactsChecked(dir, 90, now)
		if checked != 0 || len(findings) != 0 || !errors.Is(artErr, trail.ErrStampSymlink) {
			t.Errorf("inspectArtifactsChecked on symlinked agents dir = (checked=%d, findings=%+v, err=%v), want (0, nil, trail.ErrStampSymlink)", checked, findings, artErr)
		}
		_, err := Check(context.Background(), Options{
			Home: home, Dir: dir, Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
		})
		if !errors.Is(err, trail.ErrStampSymlink) {
			t.Fatalf("Check with symlinked .agents/scratch/agents err = %v, want trail.ErrStampSymlink", err)
		}
	})

	t.Run("symlinked_metrics_state_dir_ignored", func(t *testing.T) {
		home, dir, extHome := t.TempDir(), t.TempDir(), t.TempDir()
		seedWatchDBs(t, home, dir, []convSpec{
			{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
			{id: "sub-sym-metrics", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
		}, map[string]int{"sub-sym-metrics": 36})
		seedMetricsSessionsWithOutcome(t, extHome, [][3]any{
			{"sub-sym-metrics", "pass", now.Add(-10 * time.Minute).UnixMilli()},
		})
		if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(extHome, harness.StateDir), filepath.Join(home, harness.StateDir)); err != nil {
			t.Fatal(err)
		}
		if lk := openMetricsEndedLookup(home); lk != nil {
			lk.close()
			t.Fatal("openMetricsEndedLookup followed symlinked .config/fyr")
		}
		rep, err := Check(context.Background(), Options{
			Home: home, Dir: dir, Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
		})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if !hasWatchFinding(rep, kindBudgetNearLimit, "sub-sym-metrics") {
			t.Fatalf("symlinked metrics.db suppressed BUDGET_NEAR_LIMIT: %+v", rep.Findings)
		}
	})
}

func TestV910MetricsEndedAtLiveSessionStillFires(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	home, dir := t.TempDir(), t.TempDir()
	seedWatchDBs(t, home, dir, []convSpec{
		{id: "lead", ws: dir, mod: now.Add(-1 * time.Minute)},
		{id: "sub-live-recent-ended", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
		{id: "sub-live-newer-act", ws: dir, agent: "security", parent: "lead", mod: now.Add(-30 * time.Second)},
		{id: "sub-old-no-outcome", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-15 * time.Minute)},
		{id: "sub-terminal-outcome", ws: dir, agent: "reviewer", parent: "lead", mod: now.Add(-1 * time.Minute)},
	}, nil)
	seedRichSearchMeta(t, dir, 0, []richMetaRow{
		{id: "sub-live-recent-ended", role: "reviewer", agent: "reviewer", turns: 36, tools: 36, updatedAt: now.Add(-30 * time.Second).Unix()},
		{id: "sub-live-newer-act", role: "security", agent: "security", turns: 36, tools: 36, updatedAt: now.Add(-20 * time.Second).Unix()},
		{id: "sub-old-no-outcome", role: "reviewer", agent: "reviewer", turns: 36, tools: 36, updatedAt: now.Add(-15 * time.Minute).Unix()},
		{id: "sub-terminal-outcome", role: "reviewer", agent: "reviewer", turns: 36, tools: 36, updatedAt: now.Add(-30 * time.Second).Unix()},
	})
	seedMetricsSessionsWithOutcome(t, home, [][3]any{
		{"sub-live-recent-ended", "", now.Add(-30 * time.Second).UnixMilli()},
		{"sub-live-newer-act", "", now.Add(-10 * time.Minute).UnixMilli()},
		{"sub-old-no-outcome", "", now.Add(-12 * time.Minute).UnixMilli()},
		{"sub-terminal-outcome", "pass", now.Add(-30 * time.Second).UnixMilli()},
	})

	rep, err := Check(context.Background(), Options{
		Home: home, Dir: dir, Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, want := range []string{"sub-live-recent-ended", "sub-live-newer-act"} {
		if !hasWatchFinding(rep, kindBudgetNearLimit, want) {
			t.Errorf("live session %s missing BUDGET_NEAR_LIMIT: %+v", want, rep.Findings)
		}
	}
	for _, skip := range []string{"sub-old-no-outcome", "sub-terminal-outcome"} {
		if hasWatchFinding(rep, kindBudgetNearLimit, skip) {
			t.Errorf("ended session %s unexpectedly flagged: %+v", skip, rep.Findings)
		}
	}

	// Explicit --conv must NOT suppress on ended_at alone, only on terminal outcome.
	repConvLive, err := Check(context.Background(), Options{
		Home: home, Dir: dir, ConvID: "sub-old-no-outcome", Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
	})
	if err != nil || !hasWatchFinding(repConvLive, kindBudgetNearLimit, "sub-old-no-outcome") {
		t.Errorf("explicit --conv without terminal outcome should still flag: rep=%+v err=%v", repConvLive, err)
	}
	repConvDone, err := Check(context.Background(), Options{
		Home: home, Dir: dir, ConvID: "sub-terminal-outcome", Now: func() time.Time { return now }, ProcLister: liveAgyProcs(),
	})
	if err != nil || !repConvDone.OK {
		t.Errorf("explicit --conv with terminal outcome should be ok=true: rep=%+v err=%v", repConvDone, err)
	}
}
