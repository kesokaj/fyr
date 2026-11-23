package brain

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMergeMemoryBodyCompactionAndBounds(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)

	// 1. Derived memory merged 5 times: deduplicates and caps Prior sections at <= 2.
	idDer, err := MemoryDraftWithOptions(o, "add", "compacted", "note v0", MemoryDraftOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Duplicate merge of identical text adds no redundant Prior entry.
	if _, err := MemoryDraftWithOptions(o, "merge", idDer, "note v0", MemoryDraftOptions{}); err != nil {
		t.Fatal(err)
	}
	recs, _, err := Get(o, []string{idDer})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(recs[0].Body, "Prior ("); n != 0 {
		t.Errorf("duplicate merge created %d Prior entries, want 0:\n%s", n, recs[0].Body)
	}

	for i := 1; i <= 5; i++ {
		if _, err := MemoryDraftWithOptions(o, "merge", idDer, fmt.Sprintf("note v%d", i), MemoryDraftOptions{}); err != nil {
			t.Fatalf("merge %d: %v", i, err)
		}
	}
	recs, _, err = Get(o, []string{idDer})
	if err != nil {
		t.Fatal(err)
	}
	bodyDer := recs[0].Body
	if n := strings.Count(bodyDer, "Prior ("); n != 2 {
		t.Errorf("after 5 merges Prior count = %d, want 2:\n%s", n, bodyDer)
	}
	if strings.Count(bodyDer, "\n---\n") != 1 {
		t.Errorf("expected single divider, got:\n%s", bodyDer)
	}
	if !strings.Contains(bodyDer, "note v5") || !strings.Contains(bodyDer, "note v4") || !strings.Contains(bodyDer, "note v3") {
		t.Errorf("expected newest main + 2 newest priors (v5, v4, v3), got:\n%s", bodyDer)
	}
	if strings.Contains(bodyDer, "note v0") || strings.Contains(bodyDer, "note v1") {
		t.Errorf("stale priors survived compaction:\n%s", bodyDer)
	}

	// 2. Operator memory merged 5 times: preserves Provenance=="operator", <= 2 priors, AND keeps original operator primary body!
	idOp, err := MemoryAdd(o, MemoryEntry{Type: "procedural", Name: "op-pinned", Description: "sacred rule"}, strings.NewReader("sacred operator text"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := MemoryDraftWithOptions(o, "merge", idOp, fmt.Sprintf("auto refinement %d", i), MemoryDraftOptions{}); err != nil {
			t.Fatalf("operator merge %d: %v", i, err)
		}
	}
	opRecs, _, err := Get(o, []string{idOp})
	if err != nil {
		t.Fatal(err)
	}
	if opRecs[0].Provenance != "operator" {
		t.Errorf("operator Provenance after 5 merges = %q, want operator", opRecs[0].Provenance)
	}
	if n := strings.Count(opRecs[0].Body, "Prior ("); n != 2 {
		t.Errorf("operator Prior count = %d, want 2:\n%s", n, opRecs[0].Body)
	}
	opMain, opPriors := splitMemoryMainAndPriors(opRecs[0].Body)
	if opMain != "sacred operator text" || len(opPriors) != 2 || opPriors[0].prov != "derived" || opPriors[0].text != "auto refinement 5" || opPriors[1].text != "auto refinement 4" {
		t.Errorf("operator primary body or priors drifted across repeated merges:\n%s", opRecs[0].Body)
	}

	// 3. Total length bound <= 2000 bytes and UTF-8 safe even with multi-byte runes.
	longUTF8 := strings.Repeat("αβγδε ", 350)
	merged := mergeMemoryBody(longUTF8, "type: semantic\n\n"+strings.Repeat("ω", 1500), "derived", "semantic")
	if len(merged) > 2000 {
		t.Errorf("len(merged) = %d, want <= 2000", len(merged))
	}
	if !utf8.ValidString(merged) {
		t.Errorf("merged body is not valid UTF-8")
	}
}

func TestStateExportAndImportEndToEnd(t *testing.T) {
	// Source machine A: two repos + global records.
	homeA := t.TempDir()
	dirA1, dirA2 := repo(t), repo(t)
	git(t, dirA1, "remote", "add", "origin", "git@github.com:org/alpha.git")
	git(t, dirA2, "remote", "add", "origin", "https://github.com/org/beta.git")
	const repoAlpha = "git:github.com/org/alpha"
	const repoBeta = "git:github.com/org/beta"

	tOld := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	tMid := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	tNew := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	oA1Old := Options{Home: homeA, Dir: dirA1, Now: func() time.Time { return tOld }}
	oA1Mid := Options{Home: homeA, Dir: dirA1, Now: func() time.Time { return tMid }}
	oA2Mid := Options{Home: homeA, Dir: dirA2, Now: func() time.Time { return tMid }}
	gMid := Options{Home: homeA, Global: true, Now: func() time.Time { return tMid }}

	add(t, oA1Old, "adr", "Alpha architecture", "Immutable event log", "id", "ADR-001")
	add(t, oA1Mid, "bug", "Alpha race condition", "Quasar race in worker pool", "id", "BUG-001")
	add(t, oA2Mid, "bug", "Beta bug", "Beta symptom", "id", "BUG-001")

	if _, err := MemoryAdd(oA1Mid, MemoryEntry{Type: "project", Name: "alpha-op", Description: "op rule", Provenance: "operator"}, strings.NewReader("alpha operator rule")); err != nil {
		t.Fatal(err)
	}
	if _, err := MemoryAdd(oA1Mid, MemoryEntry{Type: "project", Name: "alpha-der", Description: "der rule", Provenance: "derived"}, strings.NewReader("alpha derived v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := MemoryAdd(oA1Mid, MemoryEntry{Type: "semantic", Name: "shared-global", Description: "global mem", Global: true}, strings.NewReader("global memory body")); err != nil {
		t.Fatal(err)
	}
	add(t, gMid, "feedback", "Alpha feedback", "never guess flags", "project", dirA1)
	if _, err := ProposeAll(homeA, dirA1, []Proposal{{Title: "Alpha proposal", Body: "tighten rule"}}, tMid); err != nil {
		t.Fatal(err)
	}

	// Default ExportStateRecords excludes memory, feedback, proposal, and global rows.
	trailOnly, err := ExportStateRecords(homeA, StateExportOpts{})
	if err != nil {
		t.Fatalf("ExportStateRecords default: %v", err)
	}
	if len(trailOnly) != 3 {
		t.Fatalf("len(trailOnly) = %d, want 3: %+v", len(trailOnly), trailOnly)
	}
	for _, r := range trailOnly {
		if unexported[r.Kind] {
			t.Errorf("unexported kind %s leaked into default ExportStateRecords", r.Kind)
		}
	}

	// RepoID + Since filtering on ExportStateRecords.
	alphaSince, err := ExportStateRecords(homeA, StateExportOpts{RepoID: repoAlpha, Since: tMid})
	if err != nil || len(alphaSince) != 1 || alphaSince[0].ID != "BUG-001" {
		t.Fatalf("ExportStateRecords(repoAlpha, Since=tMid) = %+v, %v; want [BUG-001]", alphaSince, err)
	}

	// IncludeMemory with RepoID includes alpha's trail + alpha's memory/feedback/proposal, excluding beta and global-unscoped.
	alphaWithMem, err := ExportStateRecords(homeA, StateExportOpts{RepoID: repoAlpha, IncludeMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaWithMem) != 6 {
		t.Fatalf("len(alphaWithMem) = %d, want 6 (2 trail + 2 mem + 1 fb + 1 prop): %+v", len(alphaWithMem), alphaWithMem)
	}

	// IncludeMemory without RepoID includes global memory too (total 8).
	allWithMem, err := ExportStateRecords(homeA, StateExportOpts{IncludeMemory: true})
	if err != nil || len(allWithMem) != 8 {
		t.Fatalf("len(allWithMem) = %d (%v), want 8", len(allWithMem), err)
	}

	// Destination machine B: different checkout path for repoAlpha!
	homeB := t.TempDir()
	dirB1 := repo(t)
	git(t, dirB1, "remote", "add", "origin", "https://github.com/org/alpha.git")

	// DryRun on non-existent brain.db writes nothing and creates no brain.db file.
	dryStats, err := ImportStateRecords(homeB, allWithMem, StateImportOpts{DryRun: true})
	if err != nil {
		t.Fatalf("ImportStateRecords DryRun: %v", err)
	}
	if dryStats.Total != 3 || dryStats.Inserted != 3 {
		t.Errorf("DryRun without IncludeMemory stats = %+v, want Total=3 Inserted=3", dryStats)
	}
	if _, err := os.Lstat(Path(homeB)); !os.IsNotExist(err) {
		t.Fatalf("DryRun created brain.db on disk: %v", err)
	}

	// Seed dirB1 with an existing operator memory so cross-checkout repo_id mapping + operator guard are exercised.
	oB1Mid := Options{Home: homeB, Dir: dirB1, Now: func() time.Time { return tMid }}
	if _, err := MemoryAdd(oB1Mid, MemoryEntry{Type: "project", Name: "alpha-op", Description: "local sacred", Provenance: "operator"}, strings.NewReader("local operator body on B")); err != nil {
		t.Fatal(err)
	}

	// Defense-in-depth: importing allWithMem with IncludeMemory=false filters out memory/feedback/proposal
	// and any global-scope record (Scope == "" && RepoID == "").
	craftedGlobal := append(append([]Record(nil), allWithMem...), Record{
		ID: "ADR-999-global", Kind: "adr", Status: "accepted", Date: "2026-11-20",
		Title: "Global crafted ADR", Body: "should require --include-memory",
	})
	stNoMem, err := ImportStateRecords(homeB, craftedGlobal, StateImportOpts{RepoID: repoAlpha, IncludeMemory: false})
	if err != nil {
		t.Fatalf("ImportStateRecords(IncludeMemory=false): %v", err)
	}
	if stNoMem.Total != 2 || stNoMem.Inserted != 2 || stNoMem.Skipped != 0 {
		t.Errorf("stNoMem = %+v, want Total=2 Inserted=2", stNoMem)
	}
	stNoMemUnfiltered, err := ImportStateRecords(homeB, []Record{craftedGlobal[len(craftedGlobal)-1]}, StateImportOpts{IncludeMemory: false})
	if err != nil || stNoMemUnfiltered.Total != 0 {
		t.Errorf("global empty-scope record bypassed IncludeMemory=false: %+v, err=%v", stNoMemUnfiltered, err)
	}

	// Re-importing the exact same records is 100% idempotent (Inserted=0, Updated=0, Skipped=2).
	stIdem, err := ImportStateRecords(homeB, allWithMem, StateImportOpts{RepoID: repoAlpha, IncludeMemory: false})
	if err != nil {
		t.Fatal(err)
	}
	if stIdem.Inserted != 0 || stIdem.Updated != 0 || stIdem.Skipped != 2 || stIdem.Conflicts != 0 {
		t.Errorf("idempotent re-import stats = %+v, want Skipped=2", stIdem)
	}

	// Cross-checkout scope mapping + FTS5 trigger on INSERT: Search in dirB1 finds "Quasar"!
	hits, err := Search(oB1Mid, []string{"quasar"}, "", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != "BUG-001" {
		t.Fatalf("FTS5 Search after state import INSERT = %+v, %v; want BUG-001", hits, err)
	}

	// Test operator provenance guard on ImportStateRecords:
	// Incoming derived memory with NEWER timestamp (tNew) targeting MEMORY-alpha-op MUST be rejected,
	// incrementing OperatorPreserved and Conflicts while keeping B's operator body!
	derivedHijack := Record{
		Scope: dirA1, RepoID: repoAlpha, ID: "MEMORY-alpha-op", Kind: "memory", Status: "closed",
		Date: "2026-11-20", Title: "hijacked", Body: "type: semantic\n\nhijacked by derived",
		Provenance: "derived", Updated: tNew.Format(time.RFC3339),
	}
	olderOperator := Record{
		Scope: dirA1, RepoID: repoAlpha, ID: "MEMORY-alpha-op", Kind: "memory", Status: "closed",
		Date: "2026-11-10", Title: "older op", Body: "type: semantic\n\nolder operator text",
		Provenance: "operator", Updated: tOld.Format(time.RFC3339),
	}
	newerBugUpdate := Record{
		Scope: dirA1, RepoID: repoAlpha, ID: "BUG-001", Kind: "bug", Status: "fixed",
		Date: "2026-11-15", Title: "Alpha race fixed", Body: "Supernova fix applied",
		Updated: tNew.Format(time.RFC3339),
	}
	staleADR := Record{
		Scope: dirA1, RepoID: repoAlpha, ID: "ADR-001", Kind: "adr", Status: "accepted",
		Date: "2026-11-01", Title: "Stale ADR", Body: "older text",
		Updated: tOld.Add(-24 * time.Hour).Format(time.RFC3339),
	}
	tieConflictADR := Record{
		Scope: dirA1, RepoID: repoAlpha, ID: "ADR-001", Kind: "adr", Status: "accepted",
		Date: "2026-11-10", Title: "Tied ADR", Body: "divergent text at same timestamp",
		Updated: tOld.Format(time.RFC3339),
	}

	batch := []Record{derivedHijack, olderOperator, newerBugUpdate, staleADR, tieConflictADR}
	dryMerge, err := ImportStateRecords(homeB, batch, StateImportOpts{IncludeMemory: true, DryRun: true})
	if err != nil {
		t.Fatalf("DryRun merge: %v", err)
	}
	realMerge, err := ImportStateRecords(homeB, batch, StateImportOpts{IncludeMemory: true})
	if err != nil {
		t.Fatalf("real merge: %v", err)
	}
	if dryMerge != realMerge {
		t.Errorf("DryRun stats %+v != real stats %+v", dryMerge, realMerge)
	}
	if realMerge.Updated != 1 || realMerge.OperatorPreserved != 2 || realMerge.Conflicts != 3 || realMerge.Skipped != 4 {
		t.Errorf("merge stats = %+v, want Updated=1 OperatorPreserved=2 Conflicts=3 Skipped=4", realMerge)
	}

	// Verify operator memory was preserved intact.
	gotOp, _, err := Get(oB1Mid, []string{"MEMORY-alpha-op", "ADR-001"})
	if err != nil {
		t.Fatal(err)
	}
	if gotOp[0].Provenance != "operator" || !strings.Contains(gotOp[0].Body, "local operator body on B") {
		t.Errorf("operator memory was overwritten: %+v", gotOp[0])
	}
	if gotOp[1].Body != "Immutable event log" {
		t.Errorf("tied/older ADR overwrote existing: %+v", gotOp[1])
	}

	// Verify FTS5 trigger on UPDATE: "Supernova" now hits BUG-001, and old "Quasar" no longer hits!
	hitsNew, err := Search(oB1Mid, []string{"supernova"}, "", 10)
	if err != nil || len(hitsNew) != 1 || hitsNew[0].ID != "BUG-001" || hitsNew[0].Status != "fixed" {
		t.Errorf("FTS5 Search for updated term 'supernova' = %+v, %v; want BUG-001 fixed", hitsNew, err)
	}
	hitsOld, err := Search(oB1Mid, []string{"quasar"}, "", 10)
	if err != nil || len(hitsOld) != 0 {
		t.Errorf("FTS5 Search for replaced term 'quasar' = %+v, %v; want empty", hitsOld, err)
	}

	// Verify an incoming newer operator memory NEVER overwrites local operator memory.
	newerOperator := Record{
		Scope: dirA1, RepoID: repoAlpha, ID: "MEMORY-alpha-op", Kind: "memory", Status: "closed",
		Date: "2026-11-20", Title: "updated sacred", Body: "type: semantic\n\nnewer operator body from A",
		Provenance: "operator", Updated: tNew.Format(time.RFC3339),
	}
	stOpUpd, err := ImportStateRecords(homeB, []Record{newerOperator}, StateImportOpts{IncludeMemory: true})
	if err != nil || stOpUpd.Updated != 0 || stOpUpd.OperatorPreserved != 1 || stOpUpd.Skipped != 1 {
		t.Fatalf("newer operator overwrite stats = %+v, err = %v; want OperatorPreserved=1", stOpUpd, err)
	}
	gotOpAfter, _, err := Get(oB1Mid, []string{"MEMORY-alpha-op"})
	if err != nil || strings.Contains(gotOpAfter[0].Body, "newer operator body from A") || gotOpAfter[0].Provenance != "operator" {
		t.Errorf("local operator memory was overwritten: %+v %v", gotOpAfter, err)
	}

	// Verify newly inserted incoming operator memory downgrades to derived unless TrustOperator is set.
	untrustedOp := Record{
		Scope: dirA1, RepoID: repoAlpha, ID: "MEMORY-alpha-untrusted-op", Kind: "memory", Status: "closed",
		Date: "2026-11-20", Title: "untrusted op", Body: "type: semantic\n\nuntrusted operator import",
		Provenance: "operator", Updated: tNew.Format(time.RFC3339),
	}
	if stIns, err := ImportStateRecords(homeB, []Record{untrustedOp}, StateImportOpts{IncludeMemory: true}); err != nil || stIns.Inserted != 1 {
		t.Fatalf("untrusted operator insert = %+v, err = %v", stIns, err)
	}
	gotDowngraded, _, err := Get(oB1Mid, []string{"MEMORY-alpha-untrusted-op"})
	if err != nil || len(gotDowngraded) != 1 || gotDowngraded[0].Provenance != "derived" {
		t.Fatalf("untrusted incoming operator = %+v, err = %v; want provenance=derived", gotDowngraded, err)
	}
	trustedOp := untrustedOp
	trustedOp.ID = "MEMORY-alpha-trusted-op"
	if stTr, err := ImportStateRecords(homeB, []Record{trustedOp}, StateImportOpts{IncludeMemory: true, TrustOperator: true}); err != nil || stTr.Inserted != 1 {
		t.Fatalf("trusted operator insert = %+v, err = %v", stTr, err)
	}
	gotTrusted, _, err := Get(oB1Mid, []string{"MEMORY-alpha-trusted-op"})
	if err != nil || len(gotTrusted) != 1 || gotTrusted[0].Provenance != "operator" {
		t.Fatalf("trusted incoming operator = %+v, err = %v; want provenance=operator", gotTrusted, err)
	}
}
