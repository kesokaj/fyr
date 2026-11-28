package brain

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }

func buildV4Store(t *testing.T, dir, home string) string {
	t.Helper()
	p := Path(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 4`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records(scope, repo_id, id, kind, status, date, title, body, project, provenance, memory_kind, validity, verified_at, updated)
		VALUES(?, 'git:example/repo', 'BUG-001', 'bug', 'open', '2026-11-20', 'v4 bug', 'existing row', '', '', '', 1.0, '', '2026-11-20T10:00:00Z')`, dir); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func readUserVersion(t *testing.T, dbPath string) int {
	t.Helper()
	ro, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var uv int
	if err := ro.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	return uv
}

func TestSIMMigration(t *testing.T) {
	t.Parallel()
	dir, home := repo(t), t.TempDir()
	dbPath := buildV4Store(t, dir, home)

	// 1. Read-only SIM callers tolerate a pre-v5 store without mutating it.
	if list, err := ListSIMProblems(home, SIMListFilter{}); err != nil || len(list) != 0 {
		t.Fatalf("ListSIMProblems on v4 = (%+v, %v), want empty nil", list, err)
	}
	if _, err := GetSIMProblem(home, "SIM-P001"); !IsRefusal(err) {
		t.Fatalf("GetSIMProblem on v4 = %v, want refusal", err)
	}
	if att, err := FindSIMAttemptByProposalOrSHA(home, "PROPOSAL-1", "abcdef1"); err != nil || att != nil {
		t.Fatalf("FindSIMAttemptByProposalOrSHA on v4 = (%+v, %v), want nil nil", att, err)
	}
	if syncs, err := FindSIMAttemptsForFieldSync(home); err != nil || len(syncs) != 0 {
		t.Fatalf("FindSIMAttemptsForFieldSync on v4 = (%+v, %v), want empty nil", syncs, err)
	}
	if sl, err := RetrieveSIMSlice(home, SIMQuery{Terms: []string{"bypass"}}); err != nil || sl.Text != "" || sl.ProblemCount != 0 {
		t.Fatalf("RetrieveSIMSlice on v4 = (%+v, %v), want empty nil", sl, err)
	}
	if exp, err := ExportStateSIM(home, time.Time{}); err != nil || len(exp.Problems) != 0 || len(exp.Attempts) != 0 || len(exp.Aliases) != 0 {
		t.Fatalf("ExportStateSIM on v4 = (%+v, %v), want empty nil", exp, err)
	}
	if st, err := ImportStateSIM(home, SIMStateExport{
		Problems: []SIMProblem{{ID: "SIM-P001", RootCause: "dry run check", Status: "active", Provenance: "derived", OpenedAt: fixed.Format(time.RFC3339), UpdatedAt: fixed.Format(time.RFC3339)}},
	}, SIMStateImportOpts{DryRun: true}); err != nil || st.Inserted != 1 {
		t.Fatalf("ImportStateSIM dry-run on v4 = (%+v, %v), want Inserted=1 nil", st, err)
	}
	if uv := readUserVersion(t, dbPath); uv != 4 {
		t.Fatalf("user_version after read-only v4 calls = %d, want 4", uv)
	}

	// 2. Write migrates v4 -> v5 and creates all SIM tables, indexes, triggers, and FTS5.
	res, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:      "premature bash read bypass on large files",
		MechanismClass: "threshold",
		SignalID:       "sig:global:bash_read_bypass",
		SourceKind:     "auto_harness_edit",
		Files:          []string{"skills/fyr-bug/SKILL.md"},
		What:           "clarify ranged read guidance",
		EvalVerdict:    "kept",
		FieldStatus:    "pending_data",
		Provenance:     "derived",
		Now:            fixed,
	})
	if err != nil {
		t.Fatalf("RecordSIMAttempt migrating v4->v5: %v", err)
	}
	if res.ProblemID != "SIM-P001" || !res.CreatedProblem {
		t.Fatalf("RecordSIMAttempt result = %+v, want SIM-P001 created", res)
	}
	if uv := readUserVersion(t, dbPath); uv != 5 {
		t.Fatalf("user_version after v4->v5 migration = %d, want 5", uv)
	}

	// Existing v4 record survived migration untouched.
	recs, missing, err := Get(opts(dir, home), []string{"BUG-001"})
	if err != nil || len(missing) != 0 || len(recs) != 1 || recs[0].Title != "v4 bug" {
		t.Fatalf("v4 record after migration: recs=%+v missing=%v err=%v", recs, missing, err)
	}

	// 3. VACUUM preserves sim_fts integrity (explicit pid INTEGER PRIMARY KEY).
	rw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range []struct{ typ, name string }{
		{"table", "sim_problems"},
		{"table", "sim_aliases"},
		{"table", "sim_attempts"},
		{"table", "sim_fts"},
		{"index", "sim_attempts_problem"},
		{"index", "sim_problems_signal"},
		{"trigger", "sim_problems_ai"},
		{"trigger", "sim_problems_ad"},
		{"trigger", "sim_problems_au"},
	} {
		var n int
		if err := rw.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?`, obj.typ, obj.name).Scan(&n); err != nil || n != 1 {
			t.Errorf("sqlite_master %s %s: n=%d err=%v", obj.typ, obj.name, n, err)
		}
	}
	if _, err := rw.Exec(`VACUUM`); err != nil {
		t.Fatalf("VACUUM: %v", err)
	}
	if _, err := rw.Exec(`INSERT INTO sim_fts(sim_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("sim_fts integrity-check after VACUUM: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	sl, err := RetrieveSIMSlice(home, SIMQuery{Terms: []string{"bypass"}, Now: fixed})
	if err != nil || sl.ProblemCount != 1 || !strings.Contains(sl.Text, "SIM-P001") {
		t.Fatalf("RetrieveSIMSlice after VACUUM = (%+v, %v), want SIM-P001", sl, err)
	}

	// 4. Newer schema (>5) is refused cleanly.
	futureHome := t.TempDir()
	fp := Path(futureHome)
	if err := os.MkdirAll(filepath.Dir(fp), 0o700); err != nil {
		t.Fatal(err)
	}
	fdb, err := sql.Open("sqlite", "file:"+fp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	fdb.Close()
	if _, err := ListSIMProblems(futureHome, SIMListFilter{}); !IsRefusal(err) {
		t.Fatalf("ListSIMProblems on schema 99 = %v, want refusal", err)
	}
}

func TestSIMLedger(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	t1 := fixed
	t2 := fixed.Add(1 * time.Hour)
	t3 := fixed.Add(2 * time.Hour)

	// 1. Validation must-fail cases (usage errors).
	badInputs := []struct {
		name string
		in   SIMRecordInput
	}{
		{"empty what", SIMRecordInput{RootCause: "rc"}},
		{"what too long", SIMRecordInput{RootCause: "rc", What: strings.Repeat("a", 401)}},
		{"what control char", SIMRecordInput{RootCause: "rc", What: "bad\x01text"}},
		{"missing problem and root cause", SIMRecordInput{What: "did something"}},
		{"bad problem id", SIMRecordInput{ProblemID: "SIM-1", What: "x"}},
		{"root cause too long", SIMRecordInput{RootCause: strings.Repeat("r", 241), What: "x"}},
		{"root cause punctuation only", SIMRecordInput{RootCause: "---", What: "x"}},
		{"bad mechanism", SIMRecordInput{RootCause: "rc", MechanismClass: "magic", What: "x"}},
		{"bad problem status merged", SIMRecordInput{RootCause: "rc", ProblemStatus: "merged", What: "x"}},
		{"bad source kind", SIMRecordInput{RootCause: "rc", SourceKind: "cron", What: "x"}},
		{"bad verdict", SIMRecordInput{RootCause: "rc", EvalVerdict: "maybe", What: "x"}},
		{"bad field status", SIMRecordInput{RootCause: "rc", FieldStatus: "broken", What: "x"}},
		{"bad provenance", SIMRecordInput{RootCause: "rc", Provenance: "unknown", What: "x"}},
		{"short commit sha", SIMRecordInput{RootCause: "rc", CommitSHA: "abc123", What: "x"}},
		{"non-hex commit sha", SIMRecordInput{RootCause: "rc", CommitSHA: "zzzzzzz", What: "x"}},
	}
	for _, tc := range badInputs {
		if _, err := RecordSIMAttempt(home, tc.in); !IsUsage(err) {
			t.Errorf("%s: RecordSIMAttempt err = %v, want usage error", tc.name, err)
		}
	}
	if _, err := ListSIMProblems(home, SIMListFilter{Status: "bogus"}); !IsUsage(err) {
		t.Errorf("ListSIMProblems bogus status = %v, want usage error", err)
	}
	if _, err := ListSIMProblems(home, SIMListFilter{Limit: -1}); !IsUsage(err) {
		t.Errorf("ListSIMProblems negative limit = %v, want usage error", err)
	}

	// 2. Deterministic norm_root_cause merge.
	r1, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:        "Premature bash read bypass on large files",
		MechanismClass:   "threshold",
		SignalID:         "sig:global:bash_read_bypass",
		SourceKind:       "auto_harness_edit",
		ProposalID:       "PROPOSAL-2026-11-22-01",
		CommitSHA:        "1234567890abcdef1234567890abcdef12345678",
		ParentSHA:        "abcdef1234567",
		HarnessVersion:   "v9.9.0",
		ProposerModel:    "gemini-4-neon-xhigh",
		PolicyModel:      "gemini-4-neon-xhigh",
		Files:            []string{"skills/fyr-bug/SKILL.md"},
		What:             "attempt one",
		PredictedRows:    []string{"read-ledger-dedup"},
		PredictedSignals: []string{"sig:global:bash_read_bypass"},
		EvalVerdict:      "not_kept",
		MissedRows:       []string{"read-ledger-dedup"},
		EvalPassed:       1,
		EvalTotal:        2,
		FieldStatus:      "n/a",
		Provenance:       "derived",
		Now:              t1,
	})
	if err != nil || !r1.CreatedProblem || r1.ProblemID != "SIM-P001" {
		t.Fatalf("r1 = (%+v, %v)", r1, err)
	}

	r2, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:     "  premature   bash-read  bypass on large files. ",
		SignalID:      "sig:global:bash_read_bypass",
		SourceKind:    "auto_harness_edit",
		ProposalID:    "PROPOSAL-2026-11-22-02",
		CommitSHA:     "fedcba9876543210fedcba9876543210fedcba98",
		PolicyModel:   "gemini-4-neon-xhigh",
		Files:         []string{"skills/fyr-bug/SKILL.md", "skills/fyr-auto/SKILL.md"},
		What:          "attempt two kept",
		EvalVerdict:   "kept",
		FixedRows:     []string{"read-ledger-dedup"},
		EvalPassed:    2,
		EvalTotal:     2,
		FieldStatus:   "pending_data",
		FieldMetric:   "bash_read_bypass_rate",
		FieldBaseline: floatPtr(0.42),
		Provenance:    "derived",
		Now:           t2,
	})
	if err != nil || r2.CreatedProblem || r2.ProblemID != "SIM-P001" {
		t.Fatalf("r2 norm merge = (%+v, %v), want existing SIM-P001", r2, err)
	}

	// 3. FindSIMAttemptByProposalOrSHA: exact proposal first, then >=7-hex prefix match.
	foundByProp, err := FindSIMAttemptByProposalOrSHA(home, "PROPOSAL-2026-11-22-01", "fedcba9")
	if err != nil || foundByProp == nil || foundByProp.ID != r1.AttemptID {
		t.Fatalf("FindSIMAttemptByProposalOrSHA proposal precedence = (%+v, %v), want attempt %d", foundByProp, err, r1.AttemptID)
	}
	foundByShortSHA, err := FindSIMAttemptByProposalOrSHA(home, "", "fedcba9")
	if err != nil || foundByShortSHA == nil || foundByShortSHA.ID != r2.AttemptID {
		t.Fatalf("FindSIMAttemptByProposalOrSHA short SHA = (%+v, %v), want attempt %d", foundByShortSHA, err, r2.AttemptID)
	}
	foundByTooShort, err := FindSIMAttemptByProposalOrSHA(home, "", "fedcba")
	if err != nil || foundByTooShort != nil {
		t.Fatalf("FindSIMAttemptByProposalOrSHA 6-hex = (%+v, %v), want nil", foundByTooShort, err)
	}

	// 4. FindSIMAttemptsForFieldSync returns kept/pending_data, excludes not_kept.
	syncs, err := FindSIMAttemptsForFieldSync(home)
	if err != nil || len(syncs) != 1 || syncs[0].ID != r2.AttemptID {
		t.Fatalf("FindSIMAttemptsForFieldSync = (%+v, %v), want [%d]", syncs, err, r2.AttemptID)
	}

	// 5. UpdateSIMAttemptFieldOutcome: SIMModelStatus round-trip + derived status transitions + reverted guard.
	models := []SIMModelStatus{
		{Model: "gemini-4-neon-xhigh", Status: "recovered", Sessions: 5},
		{Model: "gemini-4-flash", Status: "pending_data", Sessions: 1},
	}
	ok, err := UpdateSIMAttemptFieldOutcome(home, SIMFieldUpdate{
		CommitSHA:     "fedcba987",
		FieldStatus:   "recovered",
		FieldObserved: floatPtr(0.05),
		FieldModels:   models,
		Now:           t3,
	})
	if err != nil || !ok {
		t.Fatalf("UpdateSIMAttemptFieldOutcome recovered = (%v, %v)", ok, err)
	}
	det, err := GetSIMProblem(home, "SIM-P001")
	if err != nil {
		t.Fatalf("GetSIMProblem: %v", err)
	}
	if det.Problem.Status != "resolved" {
		t.Fatalf("derived problem status after recovered = %q, want resolved", det.Problem.Status)
	}
	if len(det.Attempts) != 2 || len(det.Attempts[0].FieldModels) != 2 || det.Attempts[0].FieldModels[0] != models[0] {
		t.Fatalf("FieldModels round-trip = %+v, want %+v", det.Attempts[0].FieldModels, models)
	}
	if det.Attempts[0].FieldBaseline == nil || *det.Attempts[0].FieldBaseline != 0.42 || det.Attempts[0].FieldObserved == nil || *det.Attempts[0].FieldObserved != 0.05 {
		t.Fatalf("FieldBaseline/Observed = %v / %v", det.Attempts[0].FieldBaseline, det.Attempts[0].FieldObserved)
	}

	// Reopen derived problem on still_regressing, then revert and verify reverted cannot be overwritten.
	ok, err = UpdateSIMAttemptFieldOutcome(home, SIMFieldUpdate{
		ProposalID:  "PROPOSAL-2026-11-22-02",
		FieldStatus: "still_regressing",
		Now:         t3.Add(time.Minute),
	})
	if err != nil || !ok {
		t.Fatalf("UpdateSIMAttemptFieldOutcome still_regressing = (%v, %v)", ok, err)
	}
	det, _ = GetSIMProblem(home, "SIM-P001")
	if det.Problem.Status != "active" {
		t.Fatalf("derived problem status after still_regressing = %q, want active", det.Problem.Status)
	}

	// Mark resolved again, then reverted -> reopens to active AND locks field_status='reverted'.
	_, _ = UpdateSIMAttemptFieldOutcome(home, SIMFieldUpdate{AttemptID: r2.AttemptID, FieldStatus: "recovered", Now: t3.Add(2 * time.Minute)})
	ok, err = UpdateSIMAttemptFieldOutcome(home, SIMFieldUpdate{AttemptID: r2.AttemptID, FieldStatus: "reverted", Now: t3.Add(3 * time.Minute)})
	if err != nil || !ok {
		t.Fatalf("UpdateSIMAttemptFieldOutcome reverted = (%v, %v)", ok, err)
	}
	det, _ = GetSIMProblem(home, "SIM-P001")
	if det.Problem.Status != "active" || det.Attempts[0].FieldStatus != "reverted" {
		t.Fatalf("after reverted: status=%q field=%q", det.Problem.Status, det.Attempts[0].FieldStatus)
	}
	// Attempting to overwrite reverted with recovered MUST NOT overwrite!
	ok, err = UpdateSIMAttemptFieldOutcome(home, SIMFieldUpdate{AttemptID: r2.AttemptID, FieldStatus: "recovered", Now: t3.Add(4 * time.Minute)})
	if err != nil || ok {
		t.Fatalf("overwrite reverted = (%v, %v), want (false, nil)", ok, err)
	}
	det, _ = GetSIMProblem(home, "SIM-P001")
	if det.Attempts[0].FieldStatus != "reverted" {
		t.Fatalf("reverted field_status was overwritten to %q", det.Attempts[0].FieldStatus)
	}
	// Reverted attempt is excluded from FindSIMAttemptsForFieldSync.
	syncs, err = FindSIMAttemptsForFieldSync(home)
	if err != nil || len(syncs) != 0 {
		t.Fatalf("FindSIMAttemptsForFieldSync after revert = (%+v, %v), want empty", syncs, err)
	}

	// 6. Backwards-compatible []string unmarshal for field_models_json (SQLite + Go JSON).
	rw, err := sql.Open("sqlite", "file:"+Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Exec(`UPDATE sim_attempts SET field_models_json = '["legacy-model-a","legacy-model-b"]' WHERE id = ?`, r1.AttemptID); err != nil {
		t.Fatal(err)
	}
	rw.Close()
	det, err = GetSIMProblem(home, "SIM-P001")
	if err != nil {
		t.Fatal(err)
	}
	legacy := det.Attempts[1]
	if len(legacy.FieldModels) != 2 || legacy.FieldModels[0].Model != "legacy-model-a" || legacy.FieldModels[1].Model != "legacy-model-b" {
		t.Fatalf("legacy []string DB decode = %+v", legacy.FieldModels)
	}
	var jsonAtt SIMAttempt
	if err := json.Unmarshal([]byte(`{"id":1,"problem_id":"SIM-P001","source_kind":"operator_commit","what":"x","eval_verdict":"none","field_status":"n/a","field_models":["m-old"],"provenance":"derived","created_at":"2026-11-22T00:00:00Z","updated_at":"2026-11-22T00:00:00Z"}`), &jsonAtt); err != nil {
		t.Fatalf("json.Unmarshal legacy []string field_models: %v", err)
	}
	if len(jsonAtt.FieldModels) != 1 || jsonAtt.FieldModels[0].Model != "m-old" {
		t.Fatalf("jsonAtt.FieldModels = %+v", jsonAtt.FieldModels)
	}

	// 7. ID allocation > 999 (safe against SQL MAX(id) lexicographic bug).
	rw2, err := sql.Open("sqlite", "file:"+Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw2.Exec(`INSERT INTO sim_problems(id, root_cause, norm_root_cause, status, provenance, opened_at, updated_at)
		VALUES('SIM-P999', 'high number seed', 'high number seed', 'active', 'derived', ?, ?)`, t1.Format(time.RFC3339), t1.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	rw2.Close()
	r1000, err := RecordSIMAttempt(home, SIMRecordInput{RootCause: "problem one thousand", What: "w", Now: t3})
	if err != nil || r1000.ProblemID != "SIM-P1000" {
		t.Fatalf("ID after SIM-P999 = (%+v, %v), want SIM-P1000", r1000, err)
	}
	r1001, err := RecordSIMAttempt(home, SIMRecordInput{RootCause: "problem one thousand one", What: "w", Now: t3})
	if err != nil || r1001.ProblemID != "SIM-P1001" {
		t.Fatalf("ID after SIM-P1000 = (%+v, %v), want SIM-P1001", r1001, err)
	}

	// 8. ListSIMProblems path prefix boundary & signal filters.
	_, err = RecordSIMAttempt(home, SIMRecordInput{
		RootCause: "sibling path problem",
		Files:     []string{"skills/fyr-bug2/SKILL.md"},
		What:      "sibling",
		Now:       t3,
	})
	if err != nil {
		t.Fatal(err)
	}
	byPath, err := ListSIMProblems(home, SIMListFilter{PathPrefix: "skills/fyr-bug"})
	if err != nil || len(byPath) != 1 || byPath[0].Problem.ID != "SIM-P001" || byPath[0].AttemptCount != 2 {
		t.Fatalf("ListSIMProblems path prefix = (%+v, %v), want only SIM-P001 with 2 attempts", byPath, err)
	}

	// 9. Full-SHA query matching short-stored SHA & missing DB UpdateSIMAttemptFieldOutcome no-op.
	shortRes, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:   "short sha stored case",
		CommitSHA:   "deadbee",
		What:        "short sha stored",
		EvalVerdict: "kept",
		FieldStatus: "pending_data",
		Provenance:  "derived",
		Now:         t3,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundLong, err := FindSIMAttemptByProposalOrSHA(home, "", "deadbeef0123456789abcdef0123456789abcdef")
	if err != nil || foundLong == nil || foundLong.ID != shortRes.AttemptID {
		t.Fatalf("full-SHA lookup against short stored SHA = (%+v, %v), want %d", foundLong, err, shortRes.AttemptID)
	}
	ok, err = UpdateSIMAttemptFieldOutcome(home, SIMFieldUpdate{
		CommitSHA:   "deadbeef0123456789abcdef0123456789abcdef",
		FieldStatus: "recovered",
		Now:         t3.Add(time.Minute),
	})
	if err != nil || !ok {
		t.Fatalf("full-SHA update against short stored SHA = (%v, %v)", ok, err)
	}
	// Reopen resolved derived problem via not_kept RecordSIMAttempt.
	if _, err := RecordSIMAttempt(home, SIMRecordInput{
		ProblemID:   shortRes.ProblemID,
		What:        "follow-up failed attempt",
		EvalVerdict: "not_kept",
		Provenance:  "derived",
		Now:         t3.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	detReopen, _ := GetSIMProblem(home, shortRes.ProblemID)
	if detReopen.Problem.Status != "active" {
		t.Fatalf("derived problem status after not_kept on resolved = %q, want active", detReopen.Problem.Status)
	}
	emptyHome := t.TempDir()
	if ok, err := UpdateSIMAttemptFieldOutcome(emptyHome, SIMFieldUpdate{CommitSHA: "deadbee", FieldStatus: "recovered"}); err != nil || ok {
		t.Fatalf("UpdateSIMAttemptFieldOutcome on empty home = (%v, %v), want (false, nil)", ok, err)
	}
	if _, err := os.Stat(Path(emptyHome)); !os.IsNotExist(err) {
		t.Fatalf("UpdateSIMAttemptFieldOutcome created brain.db on empty home: %v", err)
	}
}
