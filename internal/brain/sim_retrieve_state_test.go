package brain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSIMRetrieve(t *testing.T) {
	home := t.TempDir()
	now := fixed

	// Create 4 distinct problems, each matching a different retrieval tier:
	// P1: Tier 4 only (FTS5 term "quasar").
	pFTS, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:   "quasar lexical tokenizer drift",
		What:        "fts attempt",
		EvalVerdict: "kept",
		FieldStatus: "recovered",
		Now:         now.AddDate(0, 0, -45),
	})
	if err != nil {
		t.Fatal(err)
	}
	// P2: Tier 3 only (recent failed within 30d).
	pFail, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:   "recent regression failure mode",
		What:        "recent not_kept attempt",
		EvalVerdict: "not_kept",
		FieldStatus: "n/a",
		Now:         now.AddDate(0, 0, -5),
	})
	if err != nil {
		t.Fatal(err)
	}
	// P3: Tier 2 (target_paths prefix match).
	pPath, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:   "skill prompt path issue",
		Files:       []string{"skills/fyr-bug/SKILL.md"},
		What:        "path match attempt",
		EvalVerdict: "kept",
		FieldStatus: "pending_data",
		Now:         now.AddDate(0, 0, -40),
	})
	if err != nil {
		t.Fatal(err)
	}
	// P4: Tier 1 (exact signal_id match).
	pSig, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:   "exact signal root cause",
		SignalID:    "sig:global:bash_read_bypass",
		What:        "signal match attempt",
		EvalVerdict: "kept",
		FieldStatus: "pending_data",
		Now:         now.AddDate(0, 0, -40),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Verify strict 4-tier priority order: Tier 1 < Tier 2 < Tier 3 < Tier 4.
	sl, err := RetrieveSIMSlice(home, SIMQuery{
		Signals:     []string{"sig:global:bash_read_bypass"},
		TargetPaths: []string{"skills/fyr-bug/"},
		Terms:       []string{"quasar"},
		Now:         now,
	})
	if err != nil {
		t.Fatalf("RetrieveSIMSlice: %v", err)
	}
	if sl.ProblemCount != 4 || sl.Truncated {
		t.Fatalf("RetrieveSIMSlice counts = %+v, want ProblemCount=4 Truncated=false", sl)
	}
	idxSig := strings.Index(sl.Text, pSig.ProblemID)
	idxPath := strings.Index(sl.Text, pPath.ProblemID)
	idxFail := strings.Index(sl.Text, pFail.ProblemID)
	idxFTS := strings.Index(sl.Text, pFTS.ProblemID)
	if idxSig < 0 || !(idxSig < idxPath && idxPath < idxFail && idxFail < idxFTS) {
		t.Fatalf("wrong tier priority order (sig=%d path=%d fail=%d fts=%d) in:\n%s", idxSig, idxPath, idxFail, idxFTS, sl.Text)
	}

	// 2. Rollup > 4 attempts into 1-line summary.
	pRoll, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:   "rollup history problem",
		SignalID:    "sig:global:rollup",
		What:        "att-1",
		EvalVerdict: "kept",
		FieldStatus: "recovered",
		Now:         now.Add(-6 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	specs := []struct {
		v, f string
	}{
		{"not_kept", "n/a"},
		{"kept", "reverted"},
		{"not_kept", "n/a"},
		{"kept", "pending_data"},
		{"not_kept", "n/a"},
	}
	for i, sp := range specs {
		if _, err := RecordSIMAttempt(home, SIMRecordInput{
			ProblemID:   pRoll.ProblemID,
			What:        fmt.Sprintf("att-%d", i+2),
			EvalVerdict: sp.v,
			FieldStatus: sp.f,
			Now:         now.Add(time.Duration(i-5) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	rollSlice, err := RetrieveSIMSlice(home, SIMQuery{
		Signals:          []string{"sig:global:rollup"},
		RecentFailedDays: 0,
		MaxProblems:      1,
		Now:              now,
	})
	if err != nil {
		t.Fatalf("RetrieveSIMSlice rollup: %v", err)
	}
	if !strings.Contains(rollSlice.Text, "+2 earlier attempts:") {
		t.Fatalf("missing rollup line in:\n%s", rollSlice.Text)
	}
	if strings.Contains(rollSlice.Text, "att-1") || strings.Contains(rollSlice.Text, "att-2") {
		t.Fatalf("older attempts should be rolled up, not listed individually:\n%s", rollSlice.Text)
	}
	if !strings.Contains(rollSlice.Text, "att-6") {
		t.Fatalf("newest attempt att-6 missing:\n%s", rollSlice.Text)
	}

	// 3. FTS5 injection safety: nasty operators/quotes/NULs never error or corrupt DB.
	injSlice, err := RetrieveSIMSlice(home, SIMQuery{
		Terms: []string{
			`" OR NOT * ( ) ^ : NEAR/3; DROP TABLE sim_problems;--`,
			"",
			"***",
			"bad\x00term",
			`quasar" OR "missing`,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("RetrieveSIMSlice FTS5 injection: %v", err)
	}
	_ = injSlice

	// 4. Redaction & home-path scrubbing on every rendered line.
	secretToken := "gh" + "p_1234567890abcdef1234567890abcdef1234"
	if _, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause: "leaked secret " + secretToken + " in " + home,
		SignalID:  "sig:global:secret_test",
		What:      "pass" + "word=supersecretvalue in " + home,
		Now:       now,
	}); err != nil {
		t.Fatal(err)
	}
	redSlice, err := RetrieveSIMSlice(home, SIMQuery{
		Signals:     []string{"sig:global:secret_test"},
		MaxProblems: 1,
		Now:         now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(redSlice.Text, secretToken) || strings.Contains(redSlice.Text, "supersecretvalue") || strings.Contains(redSlice.Text, home) {
		t.Fatalf("unredacted secret or home path leaked:\n%s", redSlice.Text)
	}
	if !strings.Contains(redSlice.Text, "[redacted by fyr]") {
		t.Fatalf("expected redaction marker in:\n%s", redSlice.Text)
	}

	// 5. Hard <= 6 KiB default byte cap and MaxProblems=8 cap.
	bigHome := t.TempDir()
	for i := 0; i < 25; i++ {
		if _, err := RecordSIMAttempt(bigHome, SIMRecordInput{
			RootCause:   fmt.Sprintf("large problem %02d %s", i, strings.Repeat("word ", 30)),
			SignalID:    "sig:global:big",
			What:        fmt.Sprintf("attempt summary %02d %s", i, strings.Repeat("detail ", 45)),
			EvalVerdict: "not_kept",
			Now:         now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	bigSlice, err := RetrieveSIMSlice(bigHome, SIMQuery{
		Signals: []string{"sig:global:big"},
		Now:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bigSlice.Text) > 6144 {
		t.Fatalf("len(bigSlice.Text) = %d, want <= 6144", len(bigSlice.Text))
	}
	if bigSlice.ProblemCount > 8 || !bigSlice.Truncated || !utf8.ValidString(bigSlice.Text) {
		t.Fatalf("bigSlice bounds = (problems=%d truncated=%v validUTF8=%v)", bigSlice.ProblemCount, bigSlice.Truncated, utf8.ValidString(bigSlice.Text))
	}

	// 6. Alias deduplication across tiers + tiny UTF-8 byte cap.
	aliasHome := t.TempDir()
	a1, _ := RecordSIMAttempt(aliasHome, SIMRecordInput{RootCause: "αβγ alias old", SignalID: "sig:global:dup", What: "old", Now: now})
	a2, _ := RecordSIMAttempt(aliasHome, SIMRecordInput{RootCause: "αβγ alias canonical", SignalID: "sig:global:dup", What: "canon", Now: now.Add(time.Minute)})
	if _, err := MergeSIMProblem(aliasHome, a1.ProblemID, a2.ProblemID, "derived", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	dedup, err := RetrieveSIMSlice(aliasHome, SIMQuery{Signals: []string{"sig:global:dup"}, Terms: []string{"alias"}, Now: now.Add(3 * time.Minute)})
	if err != nil || dedup.ProblemCount != 1 || strings.Count(dedup.Text, a2.ProblemID) != 1 {
		t.Fatalf("alias dedup slice = (%+v, %v), want 1 canonical %s", dedup, err, a2.ProblemID)
	}
	tiny, err := RetrieveSIMSlice(aliasHome, SIMQuery{Signals: []string{"sig:global:dup"}, MaxBytes: 15, Now: now.Add(3 * time.Minute)})
	if err != nil || len(tiny.Text) > 15 || !utf8.ValidString(tiny.Text) || !tiny.Truncated {
		t.Fatalf("tiny slice = (%+v, %v), want <=15 valid UTF-8 truncated", tiny, err)
	}
}

func TestSIMStateIO(t *testing.T) {
	homeA, homeB := t.TempDir(), t.TempDir()
	t1 := fixed
	t2 := fixed.Add(2 * time.Hour)

	// Populate homeA with problems, attempts, FieldModels, and an alias.
	p1, err := RecordSIMAttempt(homeA, SIMRecordInput{
		RootCause:      "portable alpha root cause",
		MechanismClass: "threshold",
		SignalID:       "sig:global:alpha",
		SourceKind:     "auto_harness_edit",
		ProposalID:     "PROPOSAL-A1",
		CommitSHA:      "1111111abcdef",
		Files:          []string{"skills/fyr-bug/SKILL.md"},
		What:           "portable attempt 1",
		EvalVerdict:    "kept",
		FieldStatus:    "pending_data",
		FieldMetric:    "alpha_rate",
		FieldBaseline:  floatPtr(0.5),
		Provenance:     "derived",
		Now:            t1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSIMAttemptFieldOutcome(homeA, SIMFieldUpdate{
		AttemptID:     p1.AttemptID,
		FieldStatus:   "recovered",
		FieldObserved: floatPtr(0.1),
		FieldModels:   []SIMModelStatus{{Model: "gemini-4-neon-xhigh", Status: "recovered", Sessions: 4}},
		Now:           t2,
	}); err != nil {
		t.Fatal(err)
	}
	p2, err := RecordSIMAttempt(homeA, SIMRecordInput{
		RootCause:  "portable beta merged into alpha",
		What:       "beta attempt",
		Provenance: "derived",
		Now:        t1.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MergeSIMProblem(homeA, p2.ProblemID, p1.ProblemID, "derived", t2); err != nil {
		t.Fatal(err)
	}

	// 1. Export full state from homeA.
	exp, err := ExportStateSIM(homeA, time.Time{})
	if err != nil {
		t.Fatalf("ExportStateSIM: %v", err)
	}
	if len(exp.Problems) != 2 || len(exp.Attempts) != 2 || len(exp.Aliases) != 1 {
		t.Fatalf("ExportStateSIM counts = %d/%d/%d, want 2/2/1", len(exp.Problems), len(exp.Attempts), len(exp.Aliases))
	}

	// 2. DryRun on empty homeB reports inserts without creating brain.db.
	dryStats, err := ImportStateSIM(homeB, exp, SIMStateImportOpts{DryRun: true})
	if err != nil {
		t.Fatalf("ImportStateSIM dry-run: %v", err)
	}
	if dryStats.Total != 5 || dryStats.Inserted != 5 {
		t.Fatalf("dry-run stats = %+v, want Total=5 Inserted=5", dryStats)
	}
	if _, err := os.Stat(Path(homeB)); !os.IsNotExist(err) {
		t.Fatalf("dry-run created brain.db: %v", err)
	}

	// 3. Real import into homeB + FTS5 search verification.
	realStats, err := ImportStateSIM(homeB, exp, SIMStateImportOpts{})
	if err != nil {
		t.Fatalf("ImportStateSIM: %v", err)
	}
	if realStats != dryStats {
		t.Fatalf("realStats = %+v, want %+v", realStats, dryStats)
	}
	sl, err := RetrieveSIMSlice(homeB, SIMQuery{Terms: []string{"portable"}, Now: t2})
	if err != nil || sl.ProblemCount == 0 || !strings.Contains(sl.Text, p1.ProblemID) {
		t.Fatalf("RetrieveSIMSlice after import (sim_fts) = (%+v, %v)", sl, err)
	}

	// 4. Re-import is strictly idempotent (zero inserts/updates).
	idemStats, err := ImportStateSIM(homeB, exp, SIMStateImportOpts{})
	if err != nil {
		t.Fatalf("idempotent ImportStateSIM: %v", err)
	}
	if idemStats.Inserted != 0 || idemStats.Updated != 0 || idemStats.Skipped != 5 {
		t.Fatalf("idempotent stats = %+v, want Inserted=0 Updated=0 Skipped=5", idemStats)
	}

	// 5. Local operator provenance is preserved unconditionally!
	homeC := t.TempDir()
	opLocal, err := RecordSIMAttempt(homeC, SIMRecordInput{
		RootCause:  "local operator truth",
		What:       "local operator note",
		Provenance: "operator",
		Now:        t1,
	})
	if err != nil || opLocal.ProblemID != p1.ProblemID {
		t.Fatalf("seed local operator on homeC: (%+v, %v)", opLocal, err)
	}
	// Make incoming P1 even newer AND marked operator — local operator still wins unconditionally!
	incConflict := exp
	incConflict.Problems = append([]SIMProblem(nil), exp.Problems...)
	incConflict.Problems[0].RootCause = "incoming overwrite attempt"
	incConflict.Problems[0].Provenance = "operator"
	incConflict.Problems[0].UpdatedAt = t2.Add(24 * time.Hour).Format(time.RFC3339)

	conflictStats, err := ImportStateSIM(homeC, incConflict, SIMStateImportOpts{})
	if err != nil {
		t.Fatalf("ImportStateSIM operator conflict: %v", err)
	}
	if conflictStats.OperatorPreserved < 1 || conflictStats.Conflicts < 1 {
		t.Fatalf("conflictStats = %+v, want OperatorPreserved>=1 Conflicts>=1", conflictStats)
	}
	detC, err := GetSIMProblem(homeC, opLocal.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if detC.Problem.RootCause != "local operator truth" || detC.Problem.Provenance != "operator" {
		t.Fatalf("local operator problem was overwritten: %+v", detC.Problem)
	}

	// 6. ExportStateSIM with since filter.
	sinceExp, err := ExportStateSIM(homeA, t2.Add(time.Hour))
	if err != nil {
		t.Fatalf("ExportStateSIM future since: %v", err)
	}
	if len(sinceExp.Problems) != 0 || len(sinceExp.Attempts) != 0 || len(sinceExp.Aliases) != 0 {
		t.Fatalf("ExportStateSIM future since = %+v, want empty", sinceExp)
	}

	// 7. Updating a derived problem via ImportStateSIM updates sim_fts; empty non-nil slices stay idempotent.
	updExp := exp
	updExp.Problems = append([]SIMProblem(nil), exp.Problems...)
	updExp.Problems[0].RootCause = "nebula refreshed root cause"
	updExp.Problems[0].NormRootCause = ""
	updExp.Problems[0].UpdatedAt = t2.Add(4 * time.Hour).Format(time.RFC3339)
	updExp.Attempts = append([]SIMAttempt(nil), exp.Attempts...)
	updExp.Attempts[0].PredictedRows = []string{}
	updStats, err := ImportStateSIM(homeB, updExp, SIMStateImportOpts{})
	if err != nil || updStats.Updated != 1 || updStats.Inserted != 0 {
		t.Fatalf("update ImportStateSIM = (%+v, %v), want Updated=1 Inserted=0", updStats, err)
	}
	ftsNew, err := RetrieveSIMSlice(homeB, SIMQuery{Terms: []string{"nebula"}, Now: t2.Add(5 * time.Hour)})
	if err != nil || ftsNew.ProblemCount == 0 || !strings.Contains(ftsNew.Text, p1.ProblemID) {
		t.Fatalf("sim_fts after update import = (%+v, %v), want %s", ftsNew, err, p1.ProblemID)
	}

	// 8. Malformed import & alias cycle refused cleanly before writing.
	badExp := SIMStateExport{
		Problems: []SIMProblem{{ID: "BAD-ID", RootCause: "x", Status: "active", Provenance: "derived", OpenedAt: t1.Format(time.RFC3339), UpdatedAt: t1.Format(time.RFC3339)}},
	}
	if _, err := ImportStateSIM(t.TempDir(), badExp, SIMStateImportOpts{}); !IsRefusal(err) {
		t.Fatalf("bad ImportStateSIM = %v, want refusal", err)
	}
	cycExp := SIMStateExport{
		Aliases: []SIMAlias{
			{OldID: "SIM-P001", TargetID: "SIM-P002", MergedAt: t1.Format(time.RFC3339)},
			{OldID: "SIM-P002", TargetID: "SIM-P001", MergedAt: t1.Format(time.RFC3339)},
		},
	}
	if _, err := ImportStateSIM(t.TempDir(), cycExp, SIMStateImportOpts{DryRun: true}); !IsRefusal(err) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle ImportStateSIM dry-run = %v, want cycle refusal", err)
	}
	if _, err := ImportStateSIM(t.TempDir(), cycExp, SIMStateImportOpts{}); !IsRefusal(err) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle ImportStateSIM = %v, want cycle refusal", err)
	}

	// 9. Security & reviewer hardening: forged norm_root_cause recomputed, bad RFC3339/control chars/negative counts refused,
	// full attempt column update stays idempotent, alias chains flatten, and --since keeps pre-since alias closure.
	hardHome := t.TempDir()
	forgeExp := SIMStateExport{
		Problems: []SIMProblem{{
			ID: "SIM-P001", RootCause: "real root cause", NormRootCause: "forged hijacked key",
			Status: "active", Provenance: "derived",
			OpenedAt: t1.Format(time.RFC3339), UpdatedAt: t1.Format(time.RFC3339),
		}},
		Attempts: []SIMAttempt{{
			ProblemID: "SIM-P001", SourceKind: "auto_harness_edit", ProposalID: "PROPOSAL-1",
			CommitSHA: "abcdef1", What: "v1", PolicyModel: "model-old", EvalVerdict: "kept",
			FieldStatus: "pending_data", Provenance: "derived",
			CreatedAt: t1.Format(time.RFC3339), UpdatedAt: t1.Format(time.RFC3339),
		}},
	}
	if _, err := ImportStateSIM(hardHome, forgeExp, SIMStateImportOpts{}); err != nil {
		t.Fatalf("import forged norm_root_cause: %v", err)
	}
	gotHard, err := GetSIMProblem(hardHome, "SIM-P001")
	if err != nil || gotHard.Problem.NormRootCause != "real root cause" {
		t.Fatalf("forged norm_root_cause was trusted: %+v err=%v", gotHard.Problem, err)
	}
	// Update non-Stage-A/B columns (PolicyModel, What, Provenance) -> persists & re-imports cleanly with 0 conflicts.
	updAttExp := forgeExp
	updAttExp.Attempts = []SIMAttempt{forgeExp.Attempts[0]}
	updAttExp.Attempts[0].PolicyModel = "model-new"
	updAttExp.Attempts[0].What = "v2 updated"
	updAttExp.Attempts[0].Provenance = "operator"
	updAttExp.Attempts[0].UpdatedAt = t2.Format(time.RFC3339)
	if st1, err := ImportStateSIM(hardHome, updAttExp, SIMStateImportOpts{}); err != nil || st1.Updated != 1 {
		t.Fatalf("attempt full-column update = (%+v, %v), want Updated=1", st1, err)
	}
	if st2, err := ImportStateSIM(hardHome, updAttExp, SIMStateImportOpts{}); err != nil || st2.Conflicts != 0 || st2.Updated != 0 {
		t.Fatalf("re-import after attempt update = (%+v, %v), want 0 conflicts/updates", st2, err)
	}
	gotHard2, _ := GetSIMProblem(hardHome, "SIM-P001")
	if len(gotHard2.Attempts) != 1 || gotHard2.Attempts[0].PolicyModel != "model-new" || gotHard2.Attempts[0].What != "v2 updated" || gotHard2.Attempts[0].Provenance != "operator" {
		t.Fatalf("attempt columns not persisted: %+v", gotHard2.Attempts)
	}
	// Invalid RFC3339 timestamp & control chars / oversize metadata refused.
	badTS := SIMStateExport{Problems: []SIMProblem{{ID: "SIM-P001", RootCause: "x", Status: "active", Provenance: "derived", OpenedAt: "\x1b[31m2026", UpdatedAt: t1.Format(time.RFC3339)}}}
	if _, err := ImportStateSIM(t.TempDir(), badTS, SIMStateImportOpts{}); !IsRefusal(err) {
		t.Fatalf("bad RFC3339 OpenedAt = %v, want refusal", err)
	}
	if _, err := RecordSIMAttempt(t.TempDir(), SIMRecordInput{RootCause: "x", What: "y", SignalID: "sig:\x1b[2J", Now: t1}); err == nil {
		t.Fatal("RecordSIMAttempt accepted control char in SignalID")
	}
	if _, err := RecordSIMAttempt(t.TempDir(), SIMRecordInput{RootCause: "x", What: "y", PolicyModel: strings.Repeat("m", maxSIMMetaBytes+1), Now: t1}); err == nil {
		t.Fatal("RecordSIMAttempt accepted oversize PolicyModel")
	}
	if _, err := UpdateSIMAttemptFieldOutcome(hardHome, SIMFieldUpdate{AttemptID: gotHard2.Attempts[0].ID, FieldStatus: "recovered", FieldMetric: "bad\x1bmetric", Now: t2}); err == nil {
		t.Fatal("UpdateSIMAttemptFieldOutcome accepted control char in FieldMetric")
	}
	// --since export keeps pre-since alias edges & target problem when an aliased attempt is updated after since.
	sinceHome := t.TempDir()
	sp1, _ := RecordSIMAttempt(sinceHome, SIMRecordInput{RootCause: "old merged prob", What: "a1", EvalVerdict: "kept", FieldStatus: "pending_data", Now: t1})
	sp2, _ := RecordSIMAttempt(sinceHome, SIMRecordInput{RootCause: "canon target prob", What: "a2", Now: t1.Add(time.Minute)})
	if _, err := MergeSIMProblem(sinceHome, sp1.ProblemID, sp2.ProblemID, "derived", t1.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSIMAttemptFieldOutcome(sinceHome, SIMFieldUpdate{AttemptID: sp1.AttemptID, FieldStatus: "recovered", Now: t2}); err != nil {
		t.Fatal(err)
	}
	incSince, err := ExportStateSIM(sinceHome, t2.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(incSince.Aliases) != 1 || len(incSince.Problems) != 2 {
		t.Fatalf("--since export missed alias closure: probs=%d aliases=%d atts=%d", len(incSince.Problems), len(incSince.Aliases), len(incSince.Attempts))
	}
	// redactSIMLine strips ANSI control chars.
	if got := redactSIMLine("hello\x1b[31mworld", ""); strings.Contains(got, "\x1b") {
		t.Fatalf("redactSIMLine left ESC byte: %q", got)
	}
}

func TestSIMIsolation(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)

	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# AGENTS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PRD.md"), []byte("# PRD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TODO.md"), []byte("## Now\n- [ ] Task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	add(t, o, "bug", "normal project bug", "ordinary symptom")
	if _, err := MemoryAdd(o, MemoryEntry{Type: "project", Name: "task-mem", Description: "project note"}, strings.NewReader("normal memory")); err != nil {
		t.Fatal(err)
	}

	// Capture SnapshotScopes digest BEFORE writing any SIM data.
	scratch := t.TempDir()
	snapBefore, ok := SnapshotScopes(Path(home), scratch, []string{dir})
	if !ok || snapBefore == "" {
		t.Fatalf("SnapshotScopes before SIM = (%q, %v)", snapBefore, ok)
	}

	// Populate SIM ledger with distinctive markers.
	const markerRC = "SIM_ISOLATION_MARKER_ROOT_CAUSE"
	const markerWhat = "SIM_ISOLATION_MARKER_ATTEMPT_WHAT"
	simRes, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:      markerRC,
		MechanismClass: "prompt_guidance",
		SignalID:       "sig:global:isolation_marker",
		Files:          []string{"skills/fyr-auto/SKILL.md"},
		What:           markerWhat,
		EvalVerdict:    "not_kept",
		Provenance:     "operator",
		Now:            fixed,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. SnapshotScopes MUST NOT change when SIM tables change.
	snapAfter, ok := SnapshotScopes(Path(home), scratch, []string{dir})
	if !ok || snapAfter != snapBefore {
		t.Fatalf("SnapshotScopes changed after SIM write: before=%s after=%s", snapBefore, snapAfter)
	}

	// 2. Resume MUST emit zero SIM markers.
	resText, err := Resume(o)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for _, m := range []string{simRes.ProblemID, markerRC, markerWhat} {
		if strings.Contains(resText, m) {
			t.Errorf("Resume leaked SIM marker %q:\n%s", m, resText)
		}
	}

	// 3. ListTurnRecordsCtx and SearchTurnRecordsCtx MUST emit zero SIM rows.
	turnRecs, err := ListTurnRecordsCtx(context.Background(), o)
	if err != nil {
		t.Fatalf("ListTurnRecordsCtx: %v", err)
	}
	for _, r := range turnRecs {
		if strings.Contains(r.ID+r.Title+r.Body, markerRC) || strings.HasPrefix(r.ID, "SIM-P") {
			t.Errorf("ListTurnRecordsCtx leaked SIM row: %+v", r)
		}
	}
	turnHits, err := SearchTurnRecordsCtx(context.Background(), o, []string{markerRC, "isolation"}, 10)
	if err != nil {
		t.Fatalf("SearchTurnRecordsCtx: %v", err)
	}
	if len(turnHits) != 0 {
		t.Errorf("SearchTurnRecordsCtx leaked SIM hits: %+v", turnHits)
	}

	// 4. Export (.agents/trail.jsonl) MUST emit zero SIM rows.
	if _, err := Export(o); err != nil {
		t.Fatalf("Export: %v", err)
	}
	trail := trailText(t, dir)
	for _, m := range []string{simRes.ProblemID, markerRC, markerWhat} {
		if strings.Contains(trail, m) {
			t.Errorf("trail.jsonl leaked SIM marker %q:\n%s", m, trail)
		}
	}

	// 5. MemoryShow MUST emit zero SIM markers.
	memOut, err := MemoryShow(o)
	if err != nil {
		t.Fatalf("MemoryShow: %v", err)
	}
	for _, m := range []string{simRes.ProblemID, markerRC, markerWhat} {
		if strings.Contains(memOut, m) {
			t.Errorf("MemoryShow leaked SIM marker %q:\n%s", m, memOut)
		}
	}
}
