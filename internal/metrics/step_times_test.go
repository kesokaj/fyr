package metrics

import (
	"bytes"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	probeMain      = "6a0066be-3958-4af9-9319-a4544694a20f"
	probeChild     = "c3882232-59f6-4709-b3cf-f7feec3e998e"
	probeCancelled = "c321274c-a20a-4366-b7ff-dcb2a91da5e0"
	probeHeadless  = "4315b739-38a8-4df5-9f4a-bfef0ecd0635"
)

// probeFixture materializes the text SQL dumps in testdata/probe/ into an
// Antigravity home directory under t.TempDir().
func probeFixture(t *testing.T, onlyIDs ...string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	loadSQL(t, filepath.Join(dir, "conversation_summaries.db"), filepath.Join("testdata", "probe", "conversation_summaries.sql"))
	if len(onlyIDs) > 0 {
		keep := "'" + strings.Join(onlyIDs, "','") + "'"
		sqliteT(t, filepath.Join(dir, "conversation_summaries.db"),
			`DELETE FROM conversation_summaries WHERE conversation_id NOT IN (`+keep+`)`)
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "probe", "conversations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			id := strings.TrimSuffix(e.Name(), ".sql")
			loadSQL(t, filepath.Join(dir, "conversations", id+".db"), filepath.Join("testdata", "probe", "conversations", e.Name()))
		}
	}
	return home
}

func loadSQL(t *testing.T, dbPath, sqlPath string) {
	t.Helper()
	b, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatal(err)
	}
	sqliteT(t, dbPath, string(b))
}

func readProbeStep(t *testing.T, home, id string, idx int) (typ, status int, meta []byte) {
	t.Helper()
	p := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", id+".db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT step_type, status, metadata FROM steps WHERE idx = ?`, idx).Scan(&typ, &status, &meta); err != nil {
		t.Fatalf("%s idx=%d: %v", id, idx, err)
	}
	return typ, status, meta
}

// TestStepTimes pins H1-H6 on the timing probe conversations recorded on
// macOS (agy 1.4.12) in testdata/probe/, then checks that ingest stores
// started_ms, ended_ms and wait_ms and that show splits model, tool, wait
// and idle minutes.
func TestStepTimes(t *testing.T) {
	home := probeFixture(t)
	checkProbeHypotheses(t, home)
	checkProbeIngestAndShow(t, home)
}

func checkProbeHypotheses(t *testing.T, home string) {
	t.Helper()
	// H1: pre-approved sleep 10 tool step (idx=2): t6 == t7 < t8, t8-t6 = 10,048 ms, no status 9.
	_, st2, m2 := readProbeStep(t, home, probeMain, 2)
	f2 := fieldMap(m2)
	if st2 != 3 || f2.timeMS(6) != f2.timeMS(7) || f2.timeMS(8)-f2.timeMS(6) != 10048 || waitMS(m2, f2.timeMS(8)) != 0 {
		t.Fatalf("H1 sleep 10: status=%d t6=%d t7=%d t8=%d wait=%d", st2, f2.timeMS(6), f2.timeMS(7), f2.timeMS(8), waitMS(m2, f2.timeMS(8)))
	}
	// H2: dialog-approved command (idx=6, counted 5.12 s pause): status 9 in field 26 gives wait_ms = 5,145 ms, tool = 33 ms.
	_, st6, m6 := readProbeStep(t, home, probeMain, 6)
	f6 := fieldMap(m6)
	w6 := waitMS(m6, f6.timeMS(8))
	if st6 != 3 || f6.timeMS(8)-f6.timeMS(6) != 5178 || w6 != 5145 || (f6.timeMS(8)-f6.timeMS(6))-w6 != 33 {
		t.Fatalf("H2 dialog approved: status=%d dur=%d wait=%d", st6, f6.timeMS(8)-f6.timeMS(6), w6)
	}
	// H3: assistant reply step (idx=1): t1 (created) < t6 (streaming start, status 8) < t7 == t8 (completed, status 3).
	_, st1, m1 := readProbeStep(t, home, probeMain, 1)
	f1 := fieldMap(m1)
	if st1 != 3 || !(f1.timeMS(1) < f1.timeMS(6) && f1.timeMS(6) < f1.timeMS(7) && f1.timeMS(7) == f1.timeMS(8)) || f1.timeMS(8)-f1.timeMS(1) != 5798 {
		t.Fatalf("H3 reply: t1=%d t6=%d t7=%d t8=%d", f1.timeMS(1), f1.timeMS(6), f1.timeMS(7), f1.timeMS(8))
	}
	// H4: step cancelled while waiting at the dialog (idx=6 of probeCancelled): status = 6 (agyCancelled), wait_ms = 88,928 ms.
	_, stC, mC := readProbeStep(t, home, probeCancelled, 6)
	fC := fieldMap(mC)
	if stC != agyCancelled || waitMS(mC, fC.timeMS(8)) != 88928 {
		t.Fatalf("H4 cancelled dialog: status=%d wait=%d", stC, waitMS(mC, fC.timeMS(8)))
	}
	// H5 & H6: headless agy -p (probeHeadless): sleep 1 has wait 0; unapproved command fails (status 7) after 1 ms in status 9.
	_, stP2, mP2 := readProbeStep(t, home, probeHeadless, 2)
	_, stP4, mP4 := readProbeStep(t, home, probeHeadless, 4)
	fP2, fP4 := fieldMap(mP2), fieldMap(mP4)
	if stP2 != 3 || fP2.timeMS(8)-fP2.timeMS(6) != 1440 || waitMS(mP2, fP2.timeMS(8)) != 0 {
		t.Fatalf("H6 headless sleep 1: status=%d dur=%d wait=%d", stP2, fP2.timeMS(8)-fP2.timeMS(6), waitMS(mP2, fP2.timeMS(8)))
	}
	if stP4 != agyFailed || waitMS(mP4, fP4.timeMS(8)) != 1 {
		t.Fatalf("H5/H6 headless unapproved: status=%d wait=%d", stP4, waitMS(mP4, fP4.timeMS(8)))
	}
}

func checkProbeIngestAndShow(t *testing.T, home string) {
	t.Helper()
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: home})

	var got string
	err := s.db.QueryRow(`SELECT
		(SELECT started_ms || '/' || ended_ms || '/' || wait_ms FROM steps WHERE ext_id = ? || ':2') || ',' ||
		(SELECT started_ms || '/' || ended_ms || '/' || wait_ms FROM steps WHERE ext_id = ? || ':6') || ',' ||
		(SELECT ts || '/' || started_ms || '/' || ended_ms || '/' || wait_ms FROM steps WHERE ext_id = ? || ':1') || ',' ||
		(SELECT is_error || '/' || wait_ms FROM steps WHERE ext_id = ? || ':6') || ',' ||
		(SELECT is_error || '/' || wait_ms FROM steps WHERE ext_id = ? || ':4')`,
		probeMain, probeMain, probeMain, probeCancelled, probeHeadless).Scan(&got)
	want := "1793730638268/1793730648316/0,1793730664234/1793730669412/5145,1793730632472/1793730635440/1793730638270/0,0/88928,1/1"
	if err != nil || got != want {
		t.Fatalf("stored step times:\n got %q\nwant %q\nerr=%v", got, want, err)
	}

	// Ingest the main interactive probe and its subagent alone to check the
	// exact session time split without the cancelled and headless sessions.
	mainHome := probeFixture(t, probeMain, probeChild)
	sMain := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, sMain, Options{Home: mainHome})
	sum, err := sMain.Show(ShowOptions{Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	// Main session (6a0066be): model 35,253 ms, tool 10,120 ms, wait 5,145 ms, idle 5,229 ms;
	// child subagent (c3882232) runs inside the parent's wall time and is excluded.
	for name, pair := range map[string][2]float64{
		"model_min": {sum.B.ModelMin["agy"], 35253.0 / 60000},
		"tool_min":  {sum.B.ToolMin["agy"], 10120.0 / 60000},
		"wait_min":  {sum.B.WaitMin["agy"], 5145.0 / 60000},
		"idle_min":  {sum.B.IdleMin["agy"], 5229.0 / 60000},
	} {
		if math.Abs(pair[0]-pair[1]) > 1e-6 {
			t.Errorf("%s = %.6f, want %.6f", name, pair[0], pair[1])
		}
	}
	var text bytes.Buffer
	sum.WriteText(&text)
	if !strings.Contains(text.String(), "time model/tool/wait/idle m  0.6 / 0.2 / 0.1 / 0.1") {
		t.Errorf("WriteText lacks expected time line:\n%s", text.String())
	}
	n := sum.Numbers()
	if math.Abs(n["B.wait_min.agy"]-5145.0/60000) > 1e-6 {
		t.Errorf("Numbers B.wait_min.agy = %v, want %v", n["B.wait_min.agy"], 5145.0/60000)
	}
}
