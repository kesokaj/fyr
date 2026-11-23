package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestSD50SearchDBIngestStateCacheAndInvalidation(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(filepath.Join(repo, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sdb := filepath.Join(repo, ".agents", "scratch", "search.db")
	sqliteT(t, sdb,
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT)`,
		`CREATE TABLE read_ledger_stats(conversation_id TEXT, turn_num INT, saved_count INT)`,
		`INSERT INTO vet_history VALUES ('C1', 1, 'TASK', 'PROCEED', 80, 'p1', '', '', 100)`,
	)

	st := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	if _, err := st.db.Exec(`INSERT INTO sessions(id, vendor, project_root) VALUES ('C1', 'agy', ?)`, sub); err != nil {
		t.Fatal(err)
	}
	ingestT(t, st, Options{Home: home})

	var stateSize, stateMtime int64
	if err := st.db.QueryRow(`SELECT size, mtime FROM ingest_state WHERE source = ?`, sdb).Scan(&stateSize, &stateMtime); err != nil || stateSize <= 0 || stateMtime == 0 {
		t.Fatalf("missing search.db ingest_state row: size=%d mtime=%d err=%v", stateSize, stateMtime, err)
	}

	// Mutate destination turn_vets; an unchanged search.db must skip re-upserting.
	if _, err := st.db.Exec(`DELETE FROM turn_vets WHERE conversation_id = 'C1'`); err != nil {
		t.Fatal(err)
	}
	ingestT(t, st, Options{Home: home})
	var cnt int
	if err := st.db.QueryRow(`SELECT count(*) FROM turn_vets WHERE conversation_id = 'C1'`).Scan(&cnt); err != nil || cnt != 0 {
		t.Fatalf("unchanged search.db re-upserted turn_vets: cnt=%d err=%v", cnt, err)
	}

	// Modify search.db and advance mtime to invalidate cache and re-ingest.
	sqliteT(t, sdb, `INSERT INTO vet_history VALUES ('C1', 2, 'TASK', 'PROCEED', 90, 'p2', '', '', 200)`)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(sdb, future, future); err != nil {
		t.Fatal(err)
	}
	ingestT(t, st, Options{Home: home})
	if err := st.db.QueryRow(`SELECT count(*) FROM turn_vets WHERE conversation_id = 'C1'`).Scan(&cnt); err != nil || cnt != 2 {
		t.Fatalf("updated search.db was not re-ingested: cnt=%d err=%v", cnt, err)
	}
}

func TestSD50WaveStatusIngestStateCacheAndInvalidation(t *testing.T) {
	home := t.TempDir()
	waveDir := filepath.Join(home, harness.StateDir, "work", "2026-11-19-100000-1111")
	if err := os.MkdirAll(waveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(waveDir, "status.json")
	writeWave := func(verdict string, steps int) {
		body := `{"stamp":"2026-11-19-100000-1111","project_root":"/repo","updated_at":"2026-11-19T10:00:00Z","workers":[{"name":"w1","state":"` + verdict + `","model":"gemini-4-argon","step_count":` + string(rune('0'+steps)) + `,"commits":1,"base_check":"` + verdict + `","judge_model":"gemini-4-pro","judge_verdict":"` + verdict + `"}]}`
		if err := os.WriteFile(statusPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeWave("PASS", 3)

	st := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, st, Options{Home: home})

	var stateSize int64
	if err := st.db.QueryRow(`SELECT size FROM ingest_state WHERE source = ?`, statusPath).Scan(&stateSize); err != nil || stateSize <= 0 {
		t.Fatalf("missing status.json ingest_state row: size=%d err=%v", stateSize, err)
	}

	// Delete work_waves row; unchanged status.json must skip re-upserting.
	if _, err := st.db.Exec(`DELETE FROM work_waves WHERE stamp = '2026-11-19-100000-1111'`); err != nil {
		t.Fatal(err)
	}
	ingestT(t, st, Options{Home: home})
	var cnt int
	if err := st.db.QueryRow(`SELECT count(*) FROM work_waves WHERE stamp = '2026-11-19-100000-1111'`).Scan(&cnt); err != nil || cnt != 0 {
		t.Fatalf("unchanged status.json re-upserted work_waves: cnt=%d err=%v", cnt, err)
	}

	// Update status.json (same length is also caught via mtime, here both change) and verify re-ingest.
	writeWave("FAIL", 7)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(statusPath, future, future); err != nil {
		t.Fatal(err)
	}
	ingestT(t, st, Options{Home: home})
	var verdict string
	if err := st.db.QueryRow(`SELECT judge_verdict FROM work_waves WHERE stamp = '2026-11-19-100000-1111' AND worker = 'w1'`).Scan(&verdict); err != nil || verdict != "FAIL" {
		t.Fatalf("updated status.json was not re-ingested: verdict=%q err=%v", verdict, err)
	}

	// Labelled wave on same status.json path uses distinct ingest_state source key.
	st.ingestWorkWavesWithLabel(home, "eval=a", nil)
	var lblCnt int
	if err := st.db.QueryRow(`SELECT count(*) FROM work_waves WHERE stamp LIKE '%:2026-11-19-100000-1111'`).Scan(&lblCnt); err != nil || lblCnt != 1 {
		t.Fatalf("labelled wave skipped due to unlabelled ingest_state key: cnt=%d err=%v", lblCnt, err)
	}
}

func TestSD50CopyDevSessionsSkipsExistingSteps(t *testing.T) {
	devHome := t.TempDir()
	devStore := openT(t, Path(devHome))
	if _, err := devStore.db.Exec(`INSERT INTO sessions(id, vendor) VALUES ('S1', 'agy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := devStore.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('S1', 'S1:1', 1000, 'tool', 'Bash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := devStore.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('S1', 'S1:1', 0, 'go test')`); err != nil {
		t.Fatal(err)
	}

	destStore := openT(t, filepath.Join(t.TempDir(), "dest.db"))
	ingestT(t, destStore, Options{Home: t.TempDir(), Source: devHome, Label: "eval=run1"})

	// Delete copied steps and verbs for existing session; second copy must skip re-querying them.
	if _, err := destStore.db.Exec(`DELETE FROM steps WHERE session_id = 'eval=run1:S1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := destStore.db.Exec(`DELETE FROM step_verbs WHERE session_id = 'eval=run1:S1'`); err != nil {
		t.Fatal(err)
	}

	// Add a brand new session S2 with its own step and verb.
	if _, err := devStore.db.Exec(`INSERT INTO sessions(id, vendor) VALUES ('S2', 'agy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := devStore.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('S2', 'S2:1', 2000, 'tool', 'Bash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := devStore.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('S2', 'S2:1', 0, 'git status')`); err != nil {
		t.Fatal(err)
	}

	ingestT(t, destStore, Options{Home: t.TempDir(), Source: devHome, Label: "eval=run1"})

	var s1Steps, s1Verbs, s2Steps, s2Verbs int
	destStore.db.QueryRow(`SELECT count(*) FROM steps WHERE session_id = 'eval=run1:S1'`).Scan(&s1Steps)
	destStore.db.QueryRow(`SELECT count(*) FROM step_verbs WHERE session_id = 'eval=run1:S1'`).Scan(&s1Verbs)
	destStore.db.QueryRow(`SELECT count(*) FROM steps WHERE session_id = 'eval=run1:S2'`).Scan(&s2Steps)
	destStore.db.QueryRow(`SELECT count(*) FROM step_verbs WHERE session_id = 'eval=run1:S2'`).Scan(&s2Verbs)
	if s1Steps != 0 || s1Verbs != 0 {
		t.Fatalf("existing session steps/verbs re-copied despite 0 inserted session rows: steps=%d verbs=%d", s1Steps, s1Verbs)
	}
	if s2Steps != 1 || s2Verbs != 1 {
		t.Fatalf("new session steps/verbs not copied: steps=%d verbs=%d", s2Steps, s2Verbs)
	}
}

func TestV910TurnVetsSuggestedSkillsAndLateSkillRejoin(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sdb := filepath.Join(repo, ".agents", "scratch", "search.db")
	sqliteT(t, sdb,
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, subtask_count INT, directive TEXT, duration_ms INT, timed_out INT, injected_bytes INT, pivots_used_count INT, skills_loaded_count INT, created_at INT)`,
		`INSERT INTO vet_history VALUES ('C-LATE', 1, 'TASK', 'PROCEED', 88, 'fix', '["golang","fyr-bug"]', 'a.go', 1, 'PROCEED', 42, 0, 512, 0, 0, 1791453600)`,
	)
	st := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	if _, err := st.db.Exec(`INSERT INTO sessions(id, vendor, project_root) VALUES ('C-LATE', 'agy', ?)`, repo); err != nil {
		t.Fatal(err)
	}

	// First ingest: search.db is ingested before any SKILL.md read steps arrive.
	res1 := ingestT(t, st, Options{Home: home})
	if res1.TurnVets != 1 || !strings.Contains(res1.Line(), "1 turn_vets added") {
		t.Fatalf("first ingest Result = %+v, line=%q; want TurnVets=1 in Line()", res1, res1.Line())
	}
	var suggested string
	var loaded int
	if err := st.db.QueryRow(`SELECT suggested_skills, skills_loaded_count FROM turn_vets WHERE conversation_id = 'C-LATE' AND turn_num = 1`).Scan(&suggested, &loaded); err != nil {
		t.Fatalf("query turn_vets: %v", err)
	}
	if suggested != "golang,fyr-bug" || loaded != 0 {
		t.Fatalf("initial turn_vets = (suggested=%q, loaded=%d), want ('golang,fyr-bug', 0)", suggested, loaded)
	}

	// Late-arriving skill read step lands in steps while search.db mtime stays unchanged.
	if _, err := st.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, is_error, denied) VALUES ('C-LATE', 'C-LATE:5', 1791453610000, 'tool', 'Read', 'golang', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	res2 := ingestT(t, st, Options{Home: home})
	if res2.TurnVets != 0 {
		t.Fatalf("second ingest TurnVets = %d, want 0", res2.TurnVets)
	}
	if err := st.db.QueryRow(`SELECT skills_loaded_count FROM turn_vets WHERE conversation_id = 'C-LATE' AND turn_num = 1`).Scan(&loaded); err != nil {
		t.Fatalf("re-query turn_vets: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("late skill read did not update skills_loaded_count: got %d, want 1", loaded)
	}
}
