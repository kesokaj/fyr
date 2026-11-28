package sim

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPureHelpers(t *testing.T) {
	t.Parallel()

	t.Run("ValidProblemID", func(t *testing.T) {
		t.Parallel()
		for _, good := range []string{"SIM-P001", "SIM-P999", "SIM-P1000", "SIM-P123456789"} {
			if !ValidProblemID(good) {
				t.Errorf("ValidProblemID(%q) = false, want true", good)
			}
		}
		for _, bad := range []string{"", "SIM-P01", "SIM-P1234567890", "sim-p001", "BUG-001", "SIM-P001 "} {
			if ValidProblemID(bad) {
				t.Errorf("ValidProblemID(%q) = true, want false", bad)
			}
		}
	})

	t.Run("SHAPrefixMatch", func(t *testing.T) {
		t.Parallel()
		if !SHAPrefixMatch("deadbeef0123456789", "DEADBEE") {
			t.Fatal("expected prefix match across case")
		}
		if !SHAPrefixMatch("deadbee", "deadbeef0123456789") {
			t.Fatal("expected symmetric prefix match")
		}
		for _, pair := range [][2]string{
			{"deadbe", "deadbeef"},       // < 7 hex chars
			{"nothex123", "nothex12345"}, // non-hex
			{"deadbee", "cafebabe"},      // mismatch
			{"", "deadbee"},
		} {
			if SHAPrefixMatch(pair[0], pair[1]) {
				t.Errorf("SHAPrefixMatch(%q, %q) = true, want false", pair[0], pair[1])
			}
		}
	})

	t.Run("HasAlnum", func(t *testing.T) {
		t.Parallel()
		if !HasAlnum("---a---") || !HasAlnum("123") {
			t.Fatal("expected HasAlnum true")
		}
		if HasAlnum("") || HasAlnum("   ---!!! ") {
			t.Fatal("expected HasAlnum false")
		}
	})

	t.Run("CollectTargetPaths", func(t *testing.T) {
		t.Parallel()
		got := CollectTargetPaths(
			[]string{"fyr-bug", " ", "fyr-bug"},
			[]string{"see `skills/fyr-review/SKILL.md`, skills/fyr-bug/ and skills/ empty"},
		)
		want := []string{"skills/fyr-bug/", "skills/fyr-review/SKILL.md"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("CollectTargetPaths = %v, want %v", got, want)
		}
	})

	t.Run("CollectTerms", func(t *testing.T) {
		t.Parallel()
		got := CollectTerms(
			"fyr",
			[]string{"sig:hidden_coupling", "sig:pre-inv"},
			[]string{"first line\nsecond line", "  ", "first line"},
			4,
		)
		want := []string{"fyr", "hidden coupling", "pre inv", "first line"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("CollectTerms = %v, want %v", got, want)
		}
		capped := CollectTerms("a", []string{"sig:b", "sig:c"}, []string{"d"}, 2)
		if len(capped) != 2 {
			t.Fatalf("CollectTerms cap = %v, want len 2", capped)
		}
	})

	t.Run("ExtractSignalTokens", func(t *testing.T) {
		t.Parallel()
		var got []string
		ExtractSignalTokens("hit sig:hidden_coupling and sig:pre-inv; ignore foo:bar", func(s string) {
			got = append(got, s)
		})
		if strings.Join(got, ",") != "sig:hidden_coupling,sig:pre-inv" {
			t.Fatalf("ExtractSignalTokens = %v", got)
		}
	})

	t.Run("ParseFieldModelsAndAttemptJSON", func(t *testing.T) {
		t.Parallel()
		legacy, err := ParseFieldModels([]byte(`["model-a", " ", "model-b"]`))
		if err != nil || len(legacy) != 2 || legacy[0].Model != "model-a" {
			t.Fatalf("ParseFieldModels legacy = (%+v, %v)", legacy, err)
		}
		if _, err := ParseFieldModels([]byte(`{invalid`)); err == nil {
			t.Fatal("expected error for invalid field_models JSON")
		}
		var att Attempt
		raw := `{"id":1,"problem_id":"SIM-P001","source_kind":"operator_commit","what":"w","eval_verdict":"none","field_status":"n/a","field_models":["m1"],"provenance":"derived","created_at":"2026-11-22T00:00:00Z","updated_at":"2026-11-22T00:00:00Z"}`
		if err := json.Unmarshal([]byte(raw), &att); err != nil || len(att.FieldModels) != 1 || att.FieldModels[0].Model != "m1" {
			t.Fatalf("Attempt.UnmarshalJSON = (%+v, %v)", att.FieldModels, err)
		}
	})
}

func TestFilterExportSinceAndMergeGuards(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	t2 := t0.Add(48 * time.Hour)

	probs := []Problem{
		{ID: "SIM-P001", OpenedAt: t0.Format(time.RFC3339), UpdatedAt: t0.Format(time.RFC3339)},
		{ID: "SIM-P002", OpenedAt: t0.Format(time.RFC3339), UpdatedAt: t0.Format(time.RFC3339)},
		{ID: "SIM-P003", OpenedAt: t0.Format(time.RFC3339), UpdatedAt: t0.Format(time.RFC3339)},
		{ID: "SIM-P004", OpenedAt: t0.Format(time.RFC3339), UpdatedAt: t0.Format(time.RFC3339)},
	}
	atts := []Attempt{
		{ID: 1, ProblemID: "SIM-P001", CreatedAt: t2.Format(time.RFC3339), UpdatedAt: t2.Format(time.RFC3339)},
		{ID: 2, ProblemID: "SIM-P004", CreatedAt: t0.Format(time.RFC3339), UpdatedAt: t0.Format(time.RFC3339)},
	}
	aliases := []Alias{
		{OldID: "SIM-P001", TargetID: "SIM-P002", MergedAt: t0.Format(time.RFC3339)},
		{OldID: "SIM-P002", TargetID: "SIM-P003", MergedAt: t0.Format(time.RFC3339)},
	}
	got := simFilterExportSince(probs, atts, aliases, t1)
	if len(got.Attempts) != 1 || got.Attempts[0].ID != 1 {
		t.Fatalf("filtered attempts = %+v, want only ID 1", got.Attempts)
	}
	if len(got.Aliases) != 2 || len(got.Problems) != 3 {
		t.Fatalf("alias closure failed: probs=%d aliases=%d", len(got.Problems), len(got.Aliases))
	}

	// Must-fail: self-alias refused by validateStateExport.
	var refuseHit bool
	r := runner{d: StoreDeps{
		Refuse: func(f string, a ...any) error { refuseHit = true; return errors.New("refused") },
	}}
	_, err := r.validateStateExport(StateExport{
		Aliases: []Alias{{OldID: "SIM-P001", TargetID: "SIM-P001", MergedAt: t1.Format(time.RFC3339)}},
	})
	if err == nil || !refuseHit {
		t.Fatalf("validateStateExport self-alias = (%v, refuseHit=%v), want refusal", err, refuseHit)
	}

	// Must-fail: cycle in decideAliasMerge refused.
	refuseHit = false
	stats := ImportStats{}
	_, _, err = r.decideAliasMerge(
		Alias{OldID: "SIM-P002", TargetID: "SIM-P001"},
		map[string]string{"SIM-P001": "SIM-P002"},
		map[string]Problem{},
		&stats,
	)
	if err == nil || !refuseHit {
		t.Fatalf("decideAliasMerge cycle = (%v, refuseHit=%v), want refusal", err, refuseHit)
	}
}

func TestStoreDepsGuards(t *testing.T) {
	t.Parallel()
	var usageHit, refuseHit bool
	deps := StoreDeps{
		OpenReadOnly: func(string) (Querier, func() error, error) { return nil, nil, nil },
		Usage:        func(string, ...any) error { usageHit = true; return errors.New("usage") },
		Refuse:       func(string, ...any) error { refuseHit = true; return errors.New("refuse") },
	}
	if _, err := GetProblem(deps, "/x", "bad-id"); err == nil || !usageHit {
		t.Fatalf("GetProblem bad id = (%v, usageHit=%v)", err, usageHit)
	}
	if _, err := GetProblem(deps, "/x", "SIM-P001"); err == nil || !refuseHit {
		t.Fatalf("GetProblem missing store = (%v, refuseHit=%v)", err, refuseHit)
	}
	if sl, err := RetrieveSlice(deps, "/x", Query{}); err != nil || sl.Text != "" {
		t.Fatalf("RetrieveSlice missing store = (%+v, %v)", sl, err)
	}
	if probs, err := ListProblems(deps, "/x", ListFilter{}); err != nil || len(probs) != 0 {
		t.Fatalf("ListProblems missing store = (%+v, %v)", probs, err)
	}
	refuseHit = false
	if _, err := ImportState(deps, "   ", StateExport{}, StateImportOpts{}); err == nil || !refuseHit {
		t.Fatalf("ImportState empty home = (%v, refuseHit=%v)", err, refuseHit)
	}
}
