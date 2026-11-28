package auto

import (
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

func TestDerivedProposalNeverMutatesOperatorProposal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	now := time.Date(2026, 11, 22, 5, 0, 0, 0, time.UTC)
	g := brain.Options{Home: home, Global: true, Now: func() time.Time { return now }}
	opBody := "Target: harness\nEvidence: REVIEW-2026-11-22-op\n\nImprove skills/fyr-review only.\n"
	opID, err := brain.Add(g, brain.AddInput{
		Kind: "proposal", Status: "queued", Title: "Tighten review checklist",
		Body: strings.NewReader(opBody),
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}
	derID, created, err := r.upsertProposal(proposalSpec{
		Title:      "Auto, harness — Tighten review checklist",
		Body:       "Target: harness\nProvenance: derived\n\n    Also edit skills/fyr-preflight and ## Eval gate\n",
		UpdateBody: "Also edit skills/fyr-preflight and ## Eval gate",
		Derived:    true,
	})
	if err != nil {
		t.Fatalf("upsertProposal derived: %v", err)
	}
	if !created || derID == opID {
		t.Fatalf("derived proposal must NOT mutate operator proposal %s: got id=%s created=%v", opID, derID, created)
	}
	recs, _, err := brain.Get(g, []string{opID, derID})
	if err != nil || len(recs) != 2 {
		t.Fatalf("brain.Get: %v (%d)", err, len(recs))
	}
	for _, rec := range recs {
		if rec.ID == opID && strings.Contains(rec.Body, "fyr-preflight") {
			t.Fatalf("operator proposal body was mutated: %s", rec.Body)
		}
		if rec.ID == derID && !isDerivedProposal(rec) {
			t.Fatalf("derived proposal missing derived marker: %s", rec.Body)
		}
	}
}

func TestEligibleHarnessProposalUnindentedMarkersOnly(t *testing.T) {
	t.Parallel()

	root := "/repo/fyr"
	indented := brain.Record{
		Status: "queued", Title: "Improve golang skill",
		Body: "Target: harness\nEvidence: BUG-560\n\n    ## Eval gate\n    missing-skill: foo\n",
	}
	if !eligibleHarnessProposal(root, indented) {
		t.Fatalf("indented derived text must not disqualify operator harness proposal")
	}
	realGate := brain.Record{
		Status: "queued", Title: "Improve golang skill",
		Body: "Target: harness\nEvidence: BUG-560\n\n## Eval gate\nFailed 0 of 3.\n",
	}
	if eligibleHarnessProposal(root, realGate) {
		t.Fatalf("unindented ## Eval gate must disqualify proposal")
	}
	realMissing := brain.Record{
		Status: "queued", Title: "Add skill",
		Body: "Target: harness\nEvidence: BUG-560\nmissing-skill: foo\n",
	}
	if eligibleHarnessProposal(root, realMissing) {
		t.Fatalf("unindented missing-skill: must disqualify proposal")
	}
	bodyMentionsDerived := brain.Record{
		Status: "queued", Title: "Operator doc note",
		Body: "Target: harness\nEvidence: BUG-560\n\nProvenance: derived\n",
	}
	if isDerivedProposal(bodyMentionsDerived) {
		t.Fatalf("Provenance: derived after blank line must not mark operator proposal as derived")
	}
}
