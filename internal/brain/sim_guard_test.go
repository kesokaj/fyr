package brain

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSIMAlias(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	r1, err := RecordSIMAttempt(home, SIMRecordInput{RootCause: "cause alpha", What: "a1", Provenance: "derived", Now: fixed})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := RecordSIMAttempt(home, SIMRecordInput{RootCause: "cause beta", What: "a2", Provenance: "derived", Now: fixed.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := RecordSIMAttempt(home, SIMRecordInput{RootCause: "cause gamma", What: "a3", Provenance: "derived", Now: fixed.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}

	// Self-merge refused as cycle; missing problem refused as not found.
	if _, err := MergeSIMProblem(home, r1.ProblemID, r1.ProblemID, "derived", fixed); !IsRefusal(err) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("self merge = %v, want cycle refusal", err)
	}
	if _, err := MergeSIMProblem(home, "SIM-P999", r1.ProblemID, "derived", fixed); !IsRefusal(err) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing old merge = %v, want not found refusal", err)
	}

	// Multi-hop chain: P001 -> P002 -> P003.
	if _, err := MergeSIMProblem(home, r1.ProblemID, r2.ProblemID, "derived", fixed.Add(3*time.Minute)); err != nil {
		t.Fatalf("merge P1->P2: %v", err)
	}
	if _, err := MergeSIMProblem(home, r2.ProblemID, r3.ProblemID, "derived", fixed.Add(4*time.Minute)); err != nil {
		t.Fatalf("merge P2->P3: %v", err)
	}
	if _, err := MergeSIMProblem(home, r3.ProblemID, r1.ProblemID, "derived", fixed.Add(5*time.Minute)); !IsRefusal(err) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("3-hop cycle P3->P1 = %v, want cycle refusal", err)
	}

	// GetSIMProblem via old alias resolves transitively and aggregates all attempts.
	det, err := GetSIMProblem(home, r1.ProblemID)
	if err != nil {
		t.Fatalf("GetSIMProblem via chain: %v", err)
	}
	if det.Problem.ID != r3.ProblemID || len(det.Aliases) != 2 || len(det.Attempts) != 3 {
		t.Fatalf("GetSIMProblem chain = %+v, want target=%s 2 aliases 3 attempts", det, r3.ProblemID)
	}

	// RecordSIMAttempt targeting old alias resolves to canonical ID.
	r4, err := RecordSIMAttempt(home, SIMRecordInput{
		ProblemID:  r1.ProblemID,
		What:       "attempt via old alias",
		Provenance: "derived",
		Now:        fixed.Add(6 * time.Minute),
	})
	if err != nil || r4.ProblemID != r3.ProblemID || !r4.ResolvedAlias {
		t.Fatalf("RecordSIMAttempt via alias = (%+v, %v), want %s ResolvedAlias=true", r4, err, r3.ProblemID)
	}

	// Merged rows remain append-only with status='merged'.
	mergedList, err := ListSIMProblems(home, SIMListFilter{Status: "merged"})
	if err != nil || len(mergedList) != 2 {
		t.Fatalf("ListSIMProblems merged = (%+v, %v), want 2", mergedList, err)
	}

	// Depth-16 cycle guard rejects a 17-hop chain.
	rw, err := sql.Open("sqlite", "file:"+Path(home))
	if err != nil {
		t.Fatal(err)
	}
	for i := 100; i < 117; i++ {
		if _, err := rw.Exec(`INSERT INTO sim_aliases(old_id, target_id, merged_at) VALUES(?, ?, ?)`,
			fmt.Sprintf("SIM-P%03d", i), fmt.Sprintf("SIM-P%03d", i+1), fixed.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	rw.Close()
	if _, err := GetSIMProblem(home, "SIM-P100"); !IsRefusal(err) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("17-hop chain = %v, want depth-16 cycle refusal", err)
	}
}

func TestSIMOperatorGuard(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	opRes, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:      "operator-owned root cause",
		MechanismClass: "structural",
		SignalID:       "sig:global:op_sig",
		Files:          []string{"skills/fyr-auto/SKILL.md"},
		What:           "operator initial fix",
		EvalVerdict:    "operator_verified",
		FieldStatus:    "pending_data",
		CommitSHA:      "aaaaaaa1111111",
		Provenance:     "operator",
		Now:            fixed,
	})
	if err != nil {
		t.Fatal(err)
	}
	derRes, err := RecordSIMAttempt(home, SIMRecordInput{
		RootCause:  "derived problem target",
		What:       "derived fix",
		Provenance: "derived",
		Now:        fixed.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Derived status mutation, root_cause overwrite, and merge are refused.
	if _, err := RecordSIMAttempt(home, SIMRecordInput{
		ProblemID:     opRes.ProblemID,
		ProblemStatus: "obsolete",
		What:          "trying to retire operator problem",
		Provenance:    "derived",
		Now:           fixed.Add(2 * time.Minute),
	}); !IsRefusal(err) || !strings.Contains(err.Error(), "operator-provenance") {
		t.Fatalf("derived status mutation = %v, want operator-provenance refusal", err)
	}
	if _, err := RecordSIMAttempt(home, SIMRecordInput{
		ProblemID:  opRes.ProblemID,
		RootCause:  "hijacked root cause",
		What:       "trying to overwrite root cause",
		Provenance: "derived",
		Now:        fixed.Add(2 * time.Minute),
	}); !IsRefusal(err) || !strings.Contains(err.Error(), "operator-provenance") {
		t.Fatalf("derived root_cause overwrite = %v, want operator-provenance refusal", err)
	}
	if _, err := MergeSIMProblem(home, opRes.ProblemID, derRes.ProblemID, "derived", fixed.Add(2*time.Minute)); !IsRefusal(err) || !strings.Contains(err.Error(), "operator-provenance") {
		t.Fatalf("derived merge of operator problem = %v, want operator-provenance refusal", err)
	}

	// 2. Derived attempt append referencing operator problem IS allowed without mutating sim_problems.
	before, err := GetSIMProblem(home, opRes.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	appRes, err := RecordSIMAttempt(home, SIMRecordInput{
		ProblemID:   opRes.ProblemID,
		SignalID:    "sig:global:different",
		Files:       []string{"skills/other/SKILL.md"},
		What:        "derived follow-up attempt",
		EvalVerdict: "not_kept",
		FieldStatus: "n/a",
		Provenance:  "derived",
		Now:         fixed.Add(3 * time.Minute),
	})
	if err != nil || appRes.ProblemID != opRes.ProblemID || appRes.CreatedProblem {
		t.Fatalf("derived attempt append on operator problem = (%+v, %v)", appRes, err)
	}
	after, err := GetSIMProblem(home, opRes.ProblemID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Problem != before.Problem || len(after.Attempts) != 2 {
		t.Fatalf("operator problem changed or missing attempt:\nbefore=%+v\nafter =%+v (attempts=%d)", before.Problem, after.Problem, len(after.Attempts))
	}

	// 3. Stage B field update leaves operator problem status untouched.
	ok, err := UpdateSIMAttemptFieldOutcome(home, SIMFieldUpdate{
		CommitSHA:   "aaaaaaa",
		FieldStatus: "recovered",
		Now:         fixed.Add(4 * time.Minute),
	})
	if err != nil || !ok {
		t.Fatalf("UpdateSIMAttemptFieldOutcome on operator attempt = (%v, %v)", ok, err)
	}
	afterField, _ := GetSIMProblem(home, opRes.ProblemID)
	if afterField.Problem.Status != "active" {
		t.Fatalf("operator problem status flipped to %q on recovered, want active", afterField.Problem.Status)
	}

	// 4. Explicit operator write transitions status; subsequent derived not_kept does NOT reopen it.
	if _, err := RecordSIMAttempt(home, SIMRecordInput{
		ProblemID:     opRes.ProblemID,
		ProblemStatus: "resolved",
		What:          "operator marks resolved",
		Provenance:    "operator",
		Now:           fixed.Add(5 * time.Minute),
	}); err != nil {
		t.Fatalf("operator status transition: %v", err)
	}
	if _, err := RecordSIMAttempt(home, SIMRecordInput{
		ProblemID:   opRes.ProblemID,
		What:        "derived failed attempt against resolved operator problem",
		EvalVerdict: "not_kept",
		Provenance:  "derived",
		Now:         fixed.Add(6 * time.Minute),
	}); err != nil {
		t.Fatalf("derived append on resolved operator problem: %v", err)
	}
	afterOp, _ := GetSIMProblem(home, opRes.ProblemID)
	if afterOp.Problem.Status != "resolved" {
		t.Fatalf("resolved operator problem status = %q after derived not_kept, want resolved", afterOp.Problem.Status)
	}
	if _, err := MergeSIMProblem(home, opRes.ProblemID, derRes.ProblemID, "operator", fixed.Add(7*time.Minute)); err != nil {
		t.Fatalf("operator merge of operator problem: %v", err)
	}
}
