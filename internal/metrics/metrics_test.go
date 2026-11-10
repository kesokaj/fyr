package metrics

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestReadEfficiencyTelemetryAndHashedPaths(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(root, "internal", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, filepath.Join(dir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('C_READ', '2026-10-08 10:05:00+00:00', '["file://`+root+`"]', '', '')`,
	)

	secretFile := filepath.Join(root, "internal", "core", "secret_"+canary+".go")
	secondFile := filepath.Join(root, "internal", "core", "other.go")
	const t0 = 1791367200
	steps := [][]any{
		{14, 3, agyStepMeta(t0, "", nil), nil},
		// 1. Full read of secretFile -> f:<hash>
		{132, 3, agyStepMeta(t0+1, "read_file", map[string]any{"Path": secretFile}), nil},
		// 2. Redundant ranged read of secretFile (same session, no edit) -> r:<hash>, redundant=1
		{132, 3, agyStepMeta(t0+2, "read_file", map[string]any{"Path": secretFile, "StartLine": 10, "EndLine": 40}), nil},
		// 3. Edit secretFile -> f:<hash>, resets seen read state for secretFile
		{132, 3, agyStepMeta(t0+3, "edit_file", map[string]any{"Path": secretFile}), nil},
		// 4. Ranged read of secretFile after edit -> r:<hash>, NOT redundant!
		{132, 3, agyStepMeta(t0+4, "read_file", map[string]any{"Path": secretFile, "StartLine": 1, "EndLine": 25}), nil},
		// 5. Ranged read of secondFile -> r:<hash>, NOT redundant!
		{132, 3, agyStepMeta(t0+5, "view_file", map[string]any{"AbsolutePath": secondFile, "StartLine": 5, "EndLine": 15}), nil},
		// 6. Bash fyr search + grep for Search : Grep ratio
		{132, 3, agyStepMeta(t0+6, "run_command", map[string]any{"CommandLine": "fyr search TargetAlpha"}), nil},
		{132, 3, agyStepMeta(t0+7, "grep_search", map[string]any{"Query": "TargetAlpha"}), nil},
	}

	convDB := filepath.Join(dir, "conversations", "C_READ.db")
	sqliteT(t, convDB,
		"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
		"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	db, err := sql.Open("sqlite", convDB)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, ?, ?)`, i, s[0], s[1], s[2], s[3], []byte("payload "+canary)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	storeDir := t.TempDir()
	s := openT(t, filepath.Join(storeDir, "metrics.db"))
	ingestT(t, s, Options{Home: home})

	// Verify cmd_hash prefixes ("f:" for full read and edit, "r:" for ranged reads) and no raw paths.
	rows, err := s.db.Query(`SELECT tool, cmd_hash FROM steps WHERE session_id = 'C_READ' AND tool IN ('Read', 'Edit') ORDER BY ext_id`)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for rows.Next() {
		var tool, h sql.NullString
		if err := rows.Scan(&tool, &h); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, tool.String+":"+h.String)
	}
	rows.Close()
	if len(hashes) != 5 {
		t.Fatalf("expected 5 Read/Edit steps, got %v", hashes)
	}
	if !strings.HasPrefix(hashes[0], "Read:f:") {
		t.Errorf("step 1 should be full read Read:f:<hash>, got %s", hashes[0])
	}
	if !strings.HasPrefix(hashes[1], "Read:r:") {
		t.Errorf("step 2 should be ranged read Read:r:<hash>, got %s", hashes[1])
	}
	if !strings.HasPrefix(hashes[2], "Edit:f:") {
		t.Errorf("step 3 should be Edit:f:<hash>, got %s", hashes[2])
	}
	if !strings.HasPrefix(hashes[3], "Read:r:") {
		t.Errorf("step 4 should be ranged read Read:r:<hash>, got %s", hashes[3])
	}
	if !strings.HasPrefix(hashes[4], "Read:r:") {
		t.Errorf("step 5 should be ranged read Read:r:<hash>, got %s", hashes[4])
	}
	// Verify the underlying path hash of steps 1, 2, 3, 4 is identical.
	baseHash := strings.TrimPrefix(hashes[0], "Read:f:")
	if strings.TrimPrefix(hashes[1], "Read:r:") != baseHash || strings.TrimPrefix(hashes[2], "Edit:f:") != baseHash {
		t.Errorf("expected identical normalized path hash across Read/Edit of same file, got %v", hashes[:4])
	}

	// Verify canary never appears in metrics.db.
	rawDB, err := os.ReadFile(filepath.Join(storeDir, "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawDB, []byte(canary)) {
		t.Errorf("metrics.db contains raw path canary %s", canary)
	}

	sum, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Total Read calls = 4 (1 full + 3 ranged -> 75.0% ranged).
	// Redundant reads = 1 (step 2 was redundant; step 4 followed an Edit so it was not redundant).
	if got := sum.C.RedundantReads["agy"]; got != 1 {
		t.Errorf("RedundantReads[agy] = %d, want 1", got)
	}
	if pct := sum.C.RangedReadPct["agy"]; pct == nil || *pct != 75.0 {
		t.Errorf("RangedReadPct[agy] = %v, want 75.0", pct)
	}
	if ratio := sum.C.SearchGrepRatio["agy"]; ratio == nil || *ratio != 1.0 {
		t.Errorf("SearchGrepRatio[agy] = %v, want 1.0", ratio)
	}
	if retries := sum.D.RetryLoops["agy"]; retries != 0 {
		t.Errorf("RetryLoops[agy] = %d, want 0 (file reads must not count as Bash retry loops)", retries)
	}

	var textOut bytes.Buffer
	sum.WriteText(&textOut)
	txt := textOut.String()
	for _, want := range []string{"redundant reads", "ranged read %", "75.0%", "search : grep", "1.000"} {
		if !strings.Contains(txt, want) {
			t.Errorf("WriteText output missing %q in:\n%s", want, txt)
		}
	}
}

func TestSecondBrainAndWorkWaveIngestAndShow(t *testing.T) {
	home := t.TempDir()
	rootA := filepath.Join(t.TempDir(), "proj-alpha")
	rootB := filepath.Join(t.TempDir(), "proj-beta")
	rootCorrupt := filepath.Join(t.TempDir(), "proj-corrupt")
	for _, r := range []string{rootA, rootB, rootCorrupt} {
		if err := os.MkdirAll(filepath.Join(r, ".agents", "scratch"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	const t0 = 1791367200 // 2026-10-07T10:00:00Z
	// Seed Antigravity conversation summary for rootA so rootA is discovered from sessions.
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(agyDir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, filepath.Join(agyDir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('C_ALPHA', '2026-10-08 10:05:00+00:00', '["file://`+rootA+`"]', '', '')`,
	)
	convDB := filepath.Join(agyDir, "conversations", "C_ALPHA.db")
	sqliteT(t, convDB,
		"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
		"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)",
	)
	db, err := sql.Open("sqlite", convDB)
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range [][]any{
		{14, 3, agyStepMeta(t0, "", nil), nil},
		{132, 3, agyStepMeta(t0+60, "run_command", map[string]any{"CommandLine": "fyr search TargetAlpha"}), nil},
		{132, 3, agyStepMeta(t0+120, "grep_search", map[string]any{"Query": "TargetAlpha"}), nil},
	} {
		if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, ?, ?)`, i, st[0], st[1], st[2], st[3], []byte("payload")); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	// Seed global ~/.config/fyr/search.db with rootB and rootCorrupt so they are discovered from projects.
	globalSearchDB := filepath.Join(home, harness.StateDir, "search.db")
	if err := os.MkdirAll(filepath.Dir(globalSearchDB), 0o700); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, globalSearchDB,
		`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO meta VALUES ('schema', '5')`,
		`CREATE TABLE projects(root TEXT PRIMARY KEY, name TEXT, last_seen INTEGER)`,
		`INSERT INTO projects VALUES ('`+rootB+`', 'proj-beta', 1791367200)`,
		`INSERT INTO projects VALUES ('`+rootCorrupt+`', 'proj-corrupt', 1791367200)`,
	)

	// Seed rootA/.agents/scratch/search.db with vet_history (including raw_prompt with canary and "!" bypass) and read_ledger.
	sqliteT(t, filepath.Join(rootA, ".agents", "scratch", "search.db"),
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, improved_prompt TEXT, present_anchors TEXT, missing_anchors TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT, PRIMARY KEY(conversation_id, turn_num))`,
		`CREATE TABLE read_ledger(conversation_id TEXT, generation_id INT, resolved_path TEXT, start_line INT, end_line INT, sha256 TEXT, turn_num INT, hit_count INT, updated_at INT, PRIMARY KEY(conversation_id, generation_id, resolved_path, start_line, end_line))`,
		`INSERT INTO vet_history VALUES ('C_ALPHA', 1, 'FEATURE', 'VET_FIRST', 40, 'design `+canary+`', 'improved `+canary+`', 'target', 'scope', 'fyr-feature,golang', 'internal/a.go,internal/b.go', 1791367200)`,
		`INSERT INTO vet_history VALUES ('C_ALPHA', 2, 'TASK', 'PROCEED', 100, '  ! bypass `+canary+`', '', 'target,goal', '', 'golang', 'internal/a.go', 1791367260)`,
		`INSERT INTO read_ledger VALUES ('C_ALPHA', 0, '/a.go', 1, 50, 'sha1', 1, 3, 1791367210)`,
		`INSERT INTO read_ledger VALUES ('C_ALPHA', 0, '/b.go', 1, 20, 'sha2', 1, 1, 1791367215)`,
		`INSERT INTO read_ledger VALUES ('C_ALPHA', 0, '/c.go', 1, 30, 'sha3', 2, 2, 1791367270)`,
	)

	// Seed rootB/.agents/scratch/search.db with vet_history.
	sqliteT(t, filepath.Join(rootB, ".agents", "scratch", "search.db"),
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, improved_prompt TEXT, present_anchors TEXT, missing_anchors TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT, PRIMARY KEY(conversation_id, turn_num))`,
		`CREATE TABLE read_ledger(conversation_id TEXT, generation_id INT, resolved_path TEXT, start_line INT, end_line INT, sha256 TEXT, turn_num INT, hit_count INT, updated_at INT, PRIMARY KEY(conversation_id, generation_id, resolved_path, start_line, end_line))`,
		`INSERT INTO vet_history VALUES ('C_BETA', 1, 'TASK', 'ENRICH_AND_PROCEED', 70, 'fix bug', 'improved', 'target', '', '', 'cmd/main.go', 1791367300)`,
	)

	// Seed corrupt search.db in rootCorrupt to verify non-fatal note handling.
	if err := os.WriteFile(filepath.Join(rootCorrupt, ".agents", "scratch", "search.db"), []byte("not a sqlite db"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed two valid fyr work waves and one corrupt status.json under ~/.config/fyr/work/.
	wave1Dir := filepath.Join(home, harness.StateDir, "work", "2026-10-08-100000-1111")
	wave2Dir := filepath.Join(home, harness.StateDir, "work", "2026-10-08-110000-2222")
	waveBadDir := filepath.Join(home, harness.StateDir, "work", "2026-10-08-120000-bad")
	for _, d := range []string{wave1Dir, wave2Dir, waveBadDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wave1Dir, "status.json"), []byte(`{
		"stamp": "2026-10-08-100000-1111",
		"project_root": "`+rootA+`",
		"updated_at": "2026-10-08T10:00:00Z",
		"workers": [
			{"name": "p1", "state": "PASS", "model": "gemini-4-argon", "step_count": 5, "commits": 2, "base_check": "PASS", "judge_model": "claude-opus-5-5", "judge_verdict": "PASS"},
			{"name": "p2", "state": "FAIL", "model": "gemini-4-argon", "step_count": 3, "commits": 0, "base_check": "FAIL", "judge_model": "gemini-4-argon", "judge_verdict": "FAIL"}
		]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wave2Dir, "status.json"), []byte(`{
		"stamp": "2026-10-08-110000-2222",
		"project_root": "`+rootB+`",
		"updated_at": "2026-10-08T11:00:00Z",
		"workers": [
			{"name": "w1", "state": "PASS", "model": "gemini-3.8-flash", "step_count": 2, "commits": 1, "base_check": "PASS", "judge_model": "claude-sonnet-4-6", "judge_verdict": "PASS"}
		]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(waveBadDir, "status.json"), []byte(`{broken json`), 0o600); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(t.TempDir(), "metrics.db")
	s := openT(t, storePath)
	var logBuf bytes.Buffer
	res, err := Ingest(s, Options{Home: home}, &logBuf)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("expected 0 failed sources (corrupt second-brain/wave files must be notes only), got %v", res.Failed)
	}
	if !strings.Contains(logBuf.String(), "metrics: note:") {
		t.Errorf("expected metrics: note: for corrupt search.db / status.json, got:\n%s", logBuf.String())
	}

	// Verify raw_prompt / improved_prompt canary never reaches metrics.db.
	rawDB, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawDB, []byte(canary)) {
		t.Fatalf("metrics.db leaked raw prompt canary %s", canary)
	}

	// Verify global Show aggregations (Summary.E, Summary.F, Summary.Projects).
	win := ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	}
	sum, err := s.Show(win)
	if err != nil {
		t.Fatalf("Show global: %v", err)
	}
	if sum.E.TurnVets != 3 || sum.E.MeanVetScore == nil || *sum.E.MeanVetScore != 70.0 ||
		sum.E.VetFirstTurns != 1 || sum.E.BypassedTurns != 1 || sum.E.PivotsSeeded != 4 || sum.E.LedgerSaved != 3 {
		t.Errorf("unexpected global Summary.E: %+v", sum.E)
	}
	if sum.F.Waves != 2 || sum.F.Workers != 3 || sum.F.Passed != 2 || sum.F.Failed != 1 ||
		sum.F.BaseCheckPassed != 2 || sum.F.BaseCheckFailed != 1 ||
		sum.F.JudgePassed != 2 || sum.F.JudgeFailed != 1 || sum.F.JudgeFallback != 1 {
		t.Errorf("unexpected global Summary.F: %+v", sum.F)
	}
	if len(sum.Projects) != 2 {
		t.Fatalf("expected 2 ProjectRows in global Show, got %+v", sum.Projects)
	}
	if sum.Projects[0].Project != filepath.Clean(rootA) || sum.Projects[0].Sessions != 1 ||
		sum.Projects[0].MeanVetScore == nil || *sum.Projects[0].MeanVetScore != 70.0 {
		t.Errorf("unexpected Projects[0]: %+v", sum.Projects[0])
	}

	// Verify --project Show filters Summary.E and Summary.F to rootA and omits Summary.Projects.
	projSum, err := s.Show(ShowOptions{
		Project: rootA,
		Since:   win.Since,
		Now:     win.Now,
	})
	if err != nil {
		t.Fatalf("Show project: %v", err)
	}
	if projSum.E.TurnVets != 2 || projSum.E.MeanVetScore == nil || *projSum.E.MeanVetScore != 70.0 ||
		projSum.E.PivotsSeeded != 3 || projSum.E.LedgerSaved != 3 {
		t.Errorf("unexpected project Summary.E: %+v", projSum.E)
	}
	if projSum.F.Waves != 1 || projSum.F.Workers != 2 || projSum.F.Passed != 1 || projSum.F.Failed != 1 || projSum.F.JudgeFallback != 1 {
		t.Errorf("unexpected project Summary.F: %+v", projSum.F)
	}
	if len(projSum.Projects) != 0 {
		t.Errorf("expected empty Projects in --project mode, got %+v", projSum.Projects)
	}
}
