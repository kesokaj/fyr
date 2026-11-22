package brain

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTurnRecordsFiltersAndDoesNotCreateStore(t *testing.T) {
	dir := repo(t)
	emptyHome := t.TempDir()

	// Must not create brain.db when missing.
	if recs, err := ListTurnRecords(Options{Home: emptyHome, Dir: dir}); err != nil || len(recs) != 0 {
		t.Fatalf("missing store ListTurnRecords = (%v, %v), want nil, nil", recs, err)
	}
	if hits, err := SearchTurnRecords(Options{Home: emptyHome, Dir: dir}, []string{"migration"}, 8); err != nil || len(hits) != 0 {
		t.Fatalf("missing store SearchTurnRecords = (%v, %v), want nil, nil", hits, err)
	}
	if _, err := os.Stat(Path(emptyHome)); !os.IsNotExist(err) {
		t.Fatalf("expected missing brain.db not to be created, stat err=%v", err)
	}

	home := t.TempDir()
	bo := Options{Home: home, Dir: dir}
	glob := Options{Home: home, Dir: dir, Global: true}

	// Active turn records that MUST be included.
	openPlanID, err := Add(bo, AddInput{Kind: "plan", Title: "Active migration plan", Body: strings.NewReader("- [ ] Step 1 migration rollback\n")})
	if err != nil {
		t.Fatal(err)
	}
	projMemID, err := MemoryAdd(bo, MemoryEntry{Type: "procedural", Name: "mig-rule", Description: "Always check migration checksums"}, strings.NewReader("Validate migration ledger.\n"))
	if err != nil {
		t.Fatal(err)
	}
	globMemID, err := MemoryAdd(glob, MemoryEntry{Type: "semantic", Name: "global-mig", Description: "Global migration invariant", Global: true}, strings.NewReader("Global migration note.\n"))
	if err != nil {
		t.Fatal(err)
	}
	openBugID, err := Add(bo, AddInput{Kind: "bug", Status: "open", Title: "Migration timeout bug", Body: strings.NewReader("Symptom: timeout during migration.\nRule: keep index fast.\n")})
	if err != nil {
		t.Fatal(err)
	}
	accADRID, err := Add(bo, AddInput{Kind: "adr", Status: "accepted", Title: "Accepted migration ADR", Body: strings.NewReader("Decisions on migration locking.\n")})
	if err != nil {
		t.Fatal(err)
	}

	// Historical/irrelevant records that MUST be excluded.
	closedPlanID, err := Add(bo, AddInput{Kind: "plan", Title: "Done migration plan", Body: strings.NewReader("- [x] Done migration.\n")})
	if err != nil {
		t.Fatal(err)
	}
	fixedBugID, err := Add(bo, AddInput{Kind: "bug", Status: "fixed", Title: "Fixed migration bug", Body: strings.NewReader("Old fixed migration issue.\n")})
	if err != nil {
		t.Fatal(err)
	}
	revID, err := Add(bo, AddInput{Kind: "review", Status: "closed", Title: "Closed migration review", Body: strings.NewReader("Old review about migration.\n")})
	if err != nil {
		t.Fatal(err)
	}
	rejPropID, err := Add(glob, AddInput{Kind: "proposal", Status: "rejected", Title: "Rejected migration proposal", Body: strings.NewReader("Old proposal about migration.\n")})
	if err != nil {
		t.Fatal(err)
	}

	recs, err := ListTurnRecords(bo)
	if err != nil {
		t.Fatalf("ListTurnRecords: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, r := range recs {
		gotIDs[r.ID] = true
	}
	for _, want := range []string{openPlanID, projMemID, globMemID, openBugID, accADRID} {
		if !gotIDs[want] {
			t.Errorf("ListTurnRecords missing active %s (got %v)", want, gotIDs)
		}
	}
	for _, unwanted := range []string{closedPlanID, fixedBugID, revID, rejPropID} {
		if gotIDs[unwanted] {
			t.Errorf("ListTurnRecords unexpectedly included %s", unwanted)
		}
	}

	hits, err := SearchTurnRecords(bo, []string{"migrations"}, 16)
	if err != nil {
		t.Fatalf("SearchTurnRecords: %v", err)
	}
	hitIDs := map[string]bool{}
	for _, h := range hits {
		hitIDs[h.ID] = true
	}
	for _, want := range []string{projMemID, globMemID, openBugID, accADRID} {
		if !hitIDs[want] {
			t.Errorf("SearchTurnRecords missing stemmed active %s (got %v)", want, hitIDs)
		}
	}
	for _, unwanted := range []string{closedPlanID, fixedBugID, revID, rejPropID} {
		if hitIDs[unwanted] {
			t.Errorf("SearchTurnRecords unexpectedly returned non-turn record %s", unwanted)
		}
	}

	// Verify uncheckpointed -wal without -shm triggers openStoreReadOnly fallback
	// without creating -shm or mutating the source WAL directory.
	roDir := Path(home)
	keep, err := sql.Open("sqlite", "file:"+filepath.ToSlash(roDir)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := keep.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := tx.QueryRow(`SELECT count(*) FROM records`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	walPlanID, err := Add(bo, AddInput{Kind: "plan", Title: "WAL snapshot plan", Body: strings.NewReader("- [ ] Step WAL recovery\n")})
	if err != nil {
		t.Fatal(err)
	}
	walHome := t.TempDir()
	walPath := Path(walHome)
	if err := os.MkdirAll(filepath.Dir(walPath), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, suf := range []string{"", "-wal"} {
		data, err := os.ReadFile(roDir + suf)
		if err != nil {
			t.Fatalf("read %s: %v", roDir+suf, err)
		}
		if suf == "-wal" && len(data) == 0 {
			t.Fatal("expected non-empty -wal while read transaction held snapshot")
		}
		if err := os.WriteFile(walPath+suf, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = tx.Rollback()
	_ = keep.Close()

	walRecs, err := ListTurnRecords(Options{Home: walHome, Dir: dir})
	if err != nil {
		t.Fatalf("ListTurnRecords with uncheckpointed -wal fallback: %v", err)
	}
	if !containsRecordID(walRecs, walPlanID) {
		t.Errorf("WAL fallback missed uncheckpointed plan %s; got %d records", walPlanID, len(walRecs))
	}
	if _, err := os.Stat(walPath + "-shm"); !os.IsNotExist(err) {
		t.Errorf("expected no -shm created in walHome, stat err=%v", err)
	}

	// Verify future schema version (> len(migrations)) is refused cleanly.
	futureHome := t.TempDir()
	futurePath := Path(futureHome)
	if err := os.MkdirAll(filepath.Dir(futurePath), 0o755); err != nil {
		t.Fatal(err)
	}
	dbBytes, err := os.ReadFile(roDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(futurePath, dbBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	fdb, err := sql.Open("sqlite", "file:"+filepath.ToSlash(futurePath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	_ = fdb.Close()
	if _, err := ListTurnRecords(Options{Home: futureHome, Dir: dir}); err == nil || !strings.Contains(err.Error(), "schema 99") {
		t.Errorf("ListTurnRecords future schema err = %v, want schema 99 refusal", err)
	}
	if _, err := SearchTurnRecords(Options{Home: futureHome, Dir: dir}, []string{"migration"}, 8); err == nil || !strings.Contains(err.Error(), "schema 99") {
		t.Errorf("SearchTurnRecords future schema err = %v, want schema 99 refusal", err)
	}

	// Verify genuine read-only directory succeeds when -wal is absent.
	if err := os.RemoveAll(roDir + "-wal"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.RemoveAll(roDir + "-shm"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		parent := filepath.Dir(roDir)
		if err := os.Chmod(parent, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	}
	if _, err := ListTurnRecords(bo); err != nil {
		t.Errorf("ListTurnRecords on read-only dir: %v", err)
	}
	if _, err := SearchTurnRecords(bo, []string{"migration"}, 8); err != nil {
		t.Errorf("SearchTurnRecords on read-only dir: %v", err)
	}
	if recs, hits, err := LoadTurnRecordsAndFTS(Options{Home: emptyHome, Dir: dir}, []string{"migration"}, 8); err != nil || len(recs) != 0 || len(hits) != 0 {
		t.Errorf("missing store LoadTurnRecordsAndFTS = (%d, %d, %v), want 0, 0, nil", len(recs), len(hits), err)
	}
	if _, _, err := LoadTurnRecordsAndFTS(Options{Home: futureHome, Dir: dir}, []string{"migration"}, 8); err == nil || !strings.Contains(err.Error(), "schema 99") {
		t.Errorf("LoadTurnRecordsAndFTS future schema err = %v, want schema 99 refusal", err)
	}
	combinedRecs, combinedHits, err := LoadTurnRecordsAndFTS(bo, []string{"migrations"}, 16)
	if err != nil {
		t.Fatalf("LoadTurnRecordsAndFTS: %v", err)
	}
	if !containsRecordID(combinedRecs, openPlanID) || !containsRecordID(combinedRecs, projMemID) {
		t.Errorf("LoadTurnRecordsAndFTS missing expected active records")
	}
	if len(combinedHits) == 0 || combinedHits[0].ID == "" {
		t.Errorf("LoadTurnRecordsAndFTS hits = %+v, want non-empty FTS hits", combinedHits)
	}
}

func containsRecordID(recs []Record, id string) bool {
	for _, r := range recs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func TestTurnRecordsIndexAndFilterWords(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()

	// 1. openStore creates records_scope_kind_status_date idempotently without bumping user_version.
	st, err := openStore(home)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	var idxCount int
	if err := st.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'records_scope_kind_status_date'`).Scan(&idxCount); err != nil {
		st.close()
		t.Fatalf("check index: %v", err)
	}
	if idxCount != 1 {
		st.close()
		t.Fatalf("records_scope_kind_status_date index count = %d, want 1", idxCount)
	}
	ver, err := st.version(st.db)
	if err != nil || ver != len(migrations) {
		st.close()
		t.Fatalf("user_version = %d (err=%v), want %d", ver, err, len(migrations))
	}
	_ = st.close()

	// Re-open must be idempotent.
	st2, err := openStore(home)
	if err != nil {
		t.Fatalf("re-openStore: %v", err)
	}
	defer st2.close()

	// 2. EXPLAIN QUERY PLAN for project and global listTurnQuery must use records_scope_kind_status_date.
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"project", Options{Home: home, Dir: dir}},
		{"global", Options{Home: home, Dir: dir, Global: true}},
	} {
		q, args, ok := listTurnQuery(tc.opts, len(migrations))
		if !ok {
			t.Fatalf("%s: listTurnQuery ok=false", tc.name)
		}
		rows, err := st2.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			t.Fatalf("%s: EXPLAIN QUERY PLAN: %v", tc.name, err)
		}
		var details []string
		usedIdx := 0
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatalf("%s: scan plan: %v", tc.name, err)
			}
			details = append(details, detail)
			if strings.Contains(detail, "SCAN records") {
				t.Errorf("%s: unexpected full table scan in query plan: %s", tc.name, detail)
			}
			if strings.Contains(detail, "USING INDEX records_scope_kind_status_date") {
				usedIdx++
			}
		}
		rows.Close()
		if usedIdx == 0 {
			t.Errorf("%s: query plan never used records_scope_kind_status_date:\n%s", tc.name, strings.Join(details, "\n"))
		}
	}

	// 3. filterTurnFTSWords strips empty, short, numeric, and trivial/stopword inputs,
	//    deduplicates meaningful tokens, and caps at maxTurnFTSWords.
	if got := filterTurnFTSWords(nil); got != nil {
		t.Errorf("filterTurnFTSWords(nil) = %v, want nil", got)
	}
	if got := filterTurnFTSWords([]string{"", "  ", "go", "to", "1234", "the", "and", "fix", "check", "all", "approved", "please", "tests"}); len(got) != 0 {
		t.Errorf("trivial/stopword inputs should filter to empty, got %v", got)
	}
	many := []string{"Migration!", "migration", "SQLite,", "WAL", "hook_deadline", "subagent", "latency", "indexer", "parser", "overflow", "extra"}
	got := filterTurnFTSWords(many)
	if len(got) != maxTurnFTSWords {
		t.Errorf("len(filterTurnFTSWords(many)) = %d (%v), want %d", len(got), got, maxTurnFTSWords)
	}
	if got[0] != "migration" || got[1] != "sqlite" {
		t.Errorf("unexpected normalized words: %v", got)
	}

	// 4. SearchTurnRecords and LoadTurnRecordsAndFTS skip FTS when only trivial/stopword words are passed.
	bo := Options{Home: home, Dir: dir}
	if _, err := Add(bo, AddInput{Kind: "adr", Status: "accepted", Title: "Check tests please", Body: strings.NewReader("Please fix and check all approved tests.\n")}); err != nil {
		t.Fatalf("Add adr: %v", err)
	}
	trivialWords := []string{"please", "fix", "check", "all", "approved", "tests", "123"}
	if hits, err := SearchTurnRecords(bo, trivialWords, 8); err != nil || len(hits) != 0 {
		t.Errorf("SearchTurnRecords(trivial) = (%v, %v), want nil, nil", hits, err)
	}
	if recs, hits, err := LoadTurnRecordsAndFTS(bo, trivialWords, 8); err != nil || len(recs) != 1 || len(hits) != 0 {
		t.Errorf("LoadTurnRecordsAndFTS(trivial) = (recs=%d, hits=%d, err=%v), want 1, 0, nil", len(recs), len(hits), err)
	}
}

func seed1143TurnRecordsT(t *testing.T, bo Options) (planID, hitADRID, hitMemID, oldADRID string) {
	t.Helper()
	var err error
	planID, err = Add(bo, AddInput{Kind: "plan", Title: "Active v910 plan", Body: strings.NewReader("## Phase 2\n- [ ] Step 1 quorum\n")})
	if err != nil {
		t.Fatal(err)
	}
	hitADRID, err = Add(bo, AddInput{Kind: "adr", Status: "accepted", Title: "QuorumSnapshot invariant", Body: strings.NewReader("Always guard QuorumSnapshot state.\n")})
	if err != nil {
		t.Fatal(err)
	}
	hitMemID, err = MemoryAdd(bo, MemoryEntry{Type: "procedural", Name: "quorum-rule", Description: "QuorumSnapshot verify rule"}, strings.NewReader("Check QuorumSnapshot before commit.\n"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := openStore(bo.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	var scope string
	if err := st.db.QueryRow(`SELECT scope FROM records WHERE id = ?`, planID).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance, memory_kind, validity, verified_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'demo', '2026-01-01T00:00:00Z', ?, ?, 1.0, '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	bigADRBody := strings.Repeat("Historical architecture rationale line.\n", 120)
	bigMemBody := strings.Repeat("Background memory context detail.\n", 80)
	oldADRID = "ADR-0001"
	for i := 1; i <= 250; i++ {
		if _, err := stmt.Exec(scope, fmt.Sprintf("ADR-%04d", i), "adr", "accepted", "2025-01-01", fmt.Sprintf("Legacy ADR %d", i), bigADRBody, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 100; i++ {
		if _, err := stmt.Exec(scope, fmt.Sprintf("MEMORY-old-%03d", i), "memory", "closed", "2025-01-01", fmt.Sprintf("Legacy Mem %d", i), bigMemBody, "operator", "semantic"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 790; i++ {
		if _, err := stmt.Exec(scope, fmt.Sprintf("CHANGE-2025-01-01-%04d", i), "change", "closed", "2025-01-01", fmt.Sprintf("Historical change %d", i), bigMemBody, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	return planID, hitADRID, hitMemID, oldADRID
}

func TestTurnRecordsSeeded1143LatencyAndSelectiveBody(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	bo := Options{Home: home, Dir: dir}
	planID, hitADRID, hitMemID, oldADRID := seed1143TurnRecordsT(t, bo)

	// Warm load on 1,143 seeded records must stay well under 120ms and selectively hydrate bodies.
	_, _, err := LoadTurnRecordsAndFTSCtx(context.Background(), bo, []string{"quorumsnapshot"}, 16)
	if err != nil {
		t.Fatalf("warmup LoadTurnRecordsAndFTSCtx: %v", err)
	}
	start := time.Now()
	recs, hits, err := LoadTurnRecordsAndFTSCtx(context.Background(), bo, []string{"quorumsnapshot"}, 16)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("LoadTurnRecordsAndFTSCtx: %v", err)
	}
	if elapsed >= 120*time.Millisecond {
		t.Errorf("warm LoadTurnRecordsAndFTSCtx on 1143 records took %v, want < 120ms", elapsed)
	}
	if len(hits) < 2 {
		t.Fatalf("expected >= 2 FTS hits for quorumsnapshot, got %+v", hits)
	}
	byID := map[string]Record{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	for _, wantBody := range []string{planID, hitADRID, hitMemID} {
		if strings.TrimSpace(byID[wantBody].Body) == "" {
			t.Errorf("expected hydrated Body for %s, got empty", wantBody)
		}
	}
	if byID[oldADRID].ID != oldADRID {
		t.Errorf("expected metadata row for %s", oldADRID)
	}
	if byID[oldADRID].Body != "" {
		t.Errorf("expected selective Body projection to omit full body for non-candidate %s (got %d bytes)", oldADRID, len(byID[oldADRID].Body))
	}

	// Pre-canceled context must abort cleanly without returning records.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := LoadTurnRecordsAndFTSCtx(cctx, bo, []string{"quorumsnapshot"}, 16); err == nil {
		t.Errorf("expected error for canceled context, got nil")
	}
}
