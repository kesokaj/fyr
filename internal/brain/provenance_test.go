package brain

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A version-1 brain.db migrated forward gains the provenance column, and
// every memory and feedback record stored before the migration reads as
// unknown, never operator (D12).
func TestProvenanceMigrationSetsUnknown(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	p := Path(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ scope, id, kind, status, title, body, proj string }{
		{dir, "MEMORY-old-mem", "memory", "closed", "old-mem", "type: project\n\nbody", ""},
		{"", "FEEDBACK-2026-10-25-1430-old-fb", "feedback", "closed", "old-fb", "old feedback", dir},
		{dir, "BUG-001", "bug", "open", "a bug", "symptom", ""},
	} {
		if _, err := db.Exec(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated) VALUES(?, ?, ?, ?, '2026-10-25', ?, ?, ?, '2026-10-25T14:30:00Z')`,
			row.scope, row.id, row.kind, row.status, row.title, row.body, row.proj); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Prove the condition: before opening through Get, user_version is 1.
	ro, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var uv int
	if err := ro.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil || uv != 1 {
		t.Fatalf("pre-migration user_version = %d, err = %v; want 1", uv, err)
	}
	ro.Close()

	o := opts(dir, home)
	recs, missing, err := Get(o, []string{"MEMORY-old-mem", "BUG-001"})
	if err != nil || len(missing) != 0 || len(recs) != 2 {
		t.Fatalf("Get after migration: %v %v %v", recs, missing, err)
	}
	if recs[0].Provenance != "unknown" {
		t.Errorf("pre-migration memory Provenance = %q, want \"unknown\"", recs[0].Provenance)
	}
	if recs[1].Provenance != "" {
		t.Errorf("non-memory/feedback Provenance = %q, want empty", recs[1].Provenance)
	}

	g := o
	g.Global = true
	fbrecs, missing, err := Get(g, []string{"FEEDBACK-2026-10-25-1430-old-fb"})
	if err != nil || len(missing) != 0 || len(fbrecs) != 1 {
		t.Fatalf("Get feedback after migration: %v %v %v", fbrecs, missing, err)
	}
	if fbrecs[0].Provenance != "unknown" {
		t.Errorf("pre-migration feedback Provenance = %q, want \"unknown\"", fbrecs[0].Provenance)
	}
}

// MemoryAdd sets operator by default, accepts derived, refuses any other
// provenance value, and preserves or updates provenance when replacing a
// same-named entry.
func TestProvenanceMemoryAdd(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)

	idOp, err := MemoryAdd(o, MemoryEntry{Type: "user", Name: "op-pref", Description: "concise tables"}, strings.NewReader("body"))
	if err != nil {
		t.Fatalf("MemoryAdd default: %v", err)
	}
	idDer, err := MemoryAdd(o, MemoryEntry{Type: "project", Name: "der-note", Description: "from a file", Provenance: "derived"}, strings.NewReader("body"))
	if err != nil {
		t.Fatalf("MemoryAdd derived: %v", err)
	}
	for _, bad := range []string{"unknown", "auto", "human", "OPERATOR"} {
		if _, err := MemoryAdd(o, MemoryEntry{Type: "project", Name: "bad", Description: "x", Provenance: bad}, strings.NewReader("body")); !IsRefusal(err) {
			t.Errorf("MemoryAdd with provenance %q: %v, want refusal", bad, err)
		}
	}

	recs, _, err := Get(o, []string{idOp, idDer})
	if err != nil || len(recs) != 2 {
		t.Fatalf("Get: %v %v", recs, err)
	}
	if recs[0].Provenance != "operator" {
		t.Errorf("default MemoryAdd Provenance = %q, want \"operator\"", recs[0].Provenance)
	}
	if recs[1].Provenance != "derived" {
		t.Errorf("derived MemoryAdd Provenance = %q, want \"derived\"", recs[1].Provenance)
	}
	if got := FormatGet(recs[0]); !strings.Contains(got, "(provenance operator)") {
		t.Errorf("FormatGet(operator memory) = %q, want (provenance operator)", got)
	}
	if got := FormatGet(recs[1]); !strings.Contains(got, "(provenance derived)") {
		t.Errorf("FormatGet(derived memory) = %q, want (provenance derived)", got)
	}
}

// MemoryDraft handles auto's add, retire and merge drafts: it marks add and
// merge as derived, refuses retire or merge of a missing ID or another
// project's ID, refuses overwriting an operator memory on add, and never
// writes .agents/trail.jsonl.
func TestProvenanceMemoryDraftActions(t *testing.T) {
	dirA, dirB, home := repo(t), repo(t), t.TempDir()
	oA := opts(dirA, home)
	oB := opts(dirB, home)

	idOpA, err := MemoryAdd(oA, MemoryEntry{Type: "user", Name: "op-rule", Description: "keep it"}, strings.NewReader("operator body"))
	if err != nil {
		t.Fatal(err)
	}
	idDerA, err := MemoryDraft(oA, "add", "auto-note", "first auto note")
	if err != nil {
		t.Fatalf("MemoryDraft add: %v", err)
	}
	idB, err := MemoryAdd(oB, MemoryEntry{Type: "project", Name: "other-proj", Description: "in B"}, strings.NewReader("body in B"))
	if err != nil {
		t.Fatal(err)
	}

	// Overwriting an operator memory via auto draft add is refused.
	if _, err := MemoryDraft(oA, "add", "op-rule", "overwritten"); !IsRefusal(err) {
		t.Errorf("MemoryDraft add over operator memory: %v, want refusal", err)
	}
	// Retiring or merging another project's ID or a missing ID is refused.
	for _, act := range []string{"retire", "merge"} {
		if _, err := MemoryDraft(oA, act, idB, "hijacked"); !IsRefusal(err) {
			t.Errorf("MemoryDraft %s of another project's ID %s: %v, want refusal", act, idB, err)
		}
		if _, err := MemoryDraft(oA, act, "MEMORY-missing", "body"); !IsRefusal(err) {
			t.Errorf("MemoryDraft %s of missing ID: %v, want refusal", act, err)
		}
	}

	// Merging an existing memory in the same project updates its body, archives
	// the prior body for reversibility, and sets provenance=derived.
	if _, err := MemoryDraft(oA, "merge", idOpA, "merged body"); err != nil {
		t.Fatalf("MemoryDraft merge: %v", err)
	}
	recs, _, err := Get(oA, []string{idOpA})
	if err != nil || len(recs) != 1 {
		t.Fatalf("Get merged: %v %v", recs, err)
	}
	if !strings.Contains(recs[0].Body, "merged body") || !strings.Contains(recs[0].Body, "Prior (operator):") || !strings.Contains(recs[0].Body, "operator body") || recs[0].Provenance != "derived" || recs[0].Status != "closed" {
		t.Errorf("after merge: %+v", recs[0])
	}

	// Retiring an existing memory in the same project sets status=retired.
	if _, err := MemoryDraft(oA, "retire", idDerA, "stale"); err != nil {
		t.Fatalf("MemoryDraft retire: %v", err)
	}
	recs, _, err = Get(oA, []string{idDerA})
	if err != nil || len(recs) != 1 {
		t.Fatalf("Get retired: %v %v", recs, err)
	}
	if recs[0].Status != "retired" {
		t.Errorf("after retire: status = %q, want \"retired\"", recs[0].Status)
	}
	if _, err := os.Stat(trailFile(dirA)); !os.IsNotExist(err) {
		t.Errorf("MemoryDraft created trail.jsonl: %v", err)
	}
}

// Add for feedback defaults provenance to operator and accepts derived;
// Add for memory via `fyr record add memory` defaults provenance to unknown.
func TestProvenanceFeedbackAndRecordAdd(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	g := o
	g.Global = true

	fbOp, err := Add(g, AddInput{Kind: "feedback", Title: "op fb", Project: dir, Body: strings.NewReader("body")})
	if err != nil {
		t.Fatal(err)
	}
	fbDer, err := Add(g, AddInput{Kind: "feedback", Title: "der fb", Project: dir, Provenance: "derived", Body: strings.NewReader("body")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(g, AddInput{Kind: "feedback", Title: "bad fb", Project: dir, Provenance: "bogus", Body: strings.NewReader("body")}); !IsRefusal(err) {
		t.Errorf("Add feedback with bogus provenance: %v, want refusal", err)
	}
	fbrecs, _, err := Get(g, []string{fbOp, fbDer})
	if err != nil || len(fbrecs) != 2 {
		t.Fatalf("Get feedback: %v %v", fbrecs, err)
	}
	if fbrecs[0].Provenance != "operator" || fbrecs[1].Provenance != "derived" {
		t.Errorf("feedback provenances = %q, %q; want operator, derived", fbrecs[0].Provenance, fbrecs[1].Provenance)
	}
	if got := FormatGet(fbrecs[1]); !strings.Contains(got, "(provenance derived)") {
		t.Errorf("FormatGet(derived feedback) = %q, want (provenance derived)", got)
	}

	memID, err := Add(o, AddInput{Kind: "memory", Title: "raw record add", Body: strings.NewReader("type: project\n\nbody")})
	if err != nil {
		t.Fatal(err)
	}
	mrecs, _, err := Get(o, []string{memID})
	if err != nil || len(mrecs) != 1 {
		t.Fatalf("Get raw memory: %v %v", mrecs, err)
	}
	if mrecs[0].Provenance != "unknown" {
		t.Errorf("record add memory Provenance = %q, want \"unknown\"", mrecs[0].Provenance)
	}
}
