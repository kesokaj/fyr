package brain

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComputeMemoryValidity(t *testing.T) {
	dir := repo(t)
	staleRel := "stale.go"
	stalePath := filepath.Join(dir, staleRel)
	past := fixed.Add(-2 * time.Hour)
	future := fixed.Add(2 * time.Hour)
	if err := os.WriteFile(stalePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stalePath, future, future); err != nil {
		t.Fatal(err)
	}
	freshRel := "fresh.go"
	freshPath := filepath.Join(dir, freshRel)
	if err := os.WriteFile(freshPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(freshPath, past, past); err != nil {
		t.Fatal(err)
	}

	tsFixed := fixed.UTC().Format(time.RFC3339)

	cases := []struct {
		name   string
		top    string
		rec    Record
		contra int
		now    time.Time
		want   float64
	}{
		{
			name: "operator_fresh",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-op",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "semantic",
				Body:       "type: semantic\n\nSee fresh.go",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed,
			want: 1.00,
		},
		{
			name: "derived_evidence_cap",
			top:  dir,
			rec: Record{
				ID:            "MEMORY-der-0",
				Kind:          "memory",
				Provenance:    "derived",
				MemoryKind:    "semantic",
				EvidenceCount: 0,
				VerifiedAt:    tsFixed,
				Updated:       tsFixed,
			},
			now:  fixed,
			want: 0.80,
		},
		{
			name: "derived_evidence_2",
			top:  dir,
			rec: Record{
				ID:            "MEMORY-der-2",
				Kind:          "memory",
				Provenance:    "derived",
				MemoryKind:    "semantic",
				EvidenceCount: 2,
				VerifiedAt:    tsFixed,
				Updated:       tsFixed,
			},
			now:  fixed,
			want: 0.90,
		},
		{
			name: "derived_evidence_10_capped",
			top:  dir,
			rec: Record{
				ID:            "MEMORY-der-10",
				Kind:          "memory",
				Provenance:    "derived",
				MemoryKind:    "semantic",
				EvidenceCount: 10,
				VerifiedAt:    tsFixed,
				Updated:       tsFixed,
			},
			now:  fixed,
			want: 0.90,
		},
		{
			name: "unknown_evidence_3",
			top:  dir,
			rec: Record{
				ID:            "MEMORY-unk-3",
				Kind:          "memory",
				Provenance:    "unknown",
				MemoryKind:    "semantic",
				EvidenceCount: 3,
				VerifiedAt:    tsFixed,
				Updated:       tsFixed,
			},
			now:  fixed,
			want: 0.90,
		},
		{
			name: "unchanged_mtime_touch_zero_penalty",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-touch",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "semantic",
				Body:       "type: semantic\n\nSee stale.go",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed,
			want: 1.00,
		},
		{
			name: "stale_operator_subtracts_0_45",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-stale",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "semantic",
				Body:       "type: semantic\n\nSee stale.go sha256:0000000",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed,
			want: 0.55,
		},
		{
			name: "contradictions_capped_at_3",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-contra",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "semantic",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			contra: 5,
			now:    fixed,
			want:   0.25,
		},
		{
			name: "age_decay_semantic_180d",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-age-sem",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "semantic",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed.Add(180 * 24 * time.Hour),
			want: 0.50,
		},
		{
			name: "age_decay_procedural_90d",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-age-proc",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "procedural",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed.Add(90 * 24 * time.Hour),
			want: 0.50,
		},
		{
			name: "age_decay_episodic_30d",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-age-ep",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "episodic",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed.Add(30 * 24 * time.Hour),
			want: 0.50,
		},
		{
			name: "age_decay_floor_0_35",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-age-floor",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "episodic",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed.Add(365 * 24 * time.Hour),
			want: 0.35,
		},
		{
			name: "clamp_floor_zero",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-clamp",
				Kind:       "memory",
				Provenance: "derived",
				MemoryKind: "semantic",
				Body:       "type: semantic\n\nSee stale.go sha256:0000000",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			contra: 2,
			now:    fixed,
			want:   0.00,
		},
		{
			name: "verified_at_beats_older_updated",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-verified",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "episodic",
				Updated:    fixed.Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339),
				VerifiedAt: tsFixed,
			},
			now:  fixed,
			want: 1.00,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeMemoryValidity(tc.top, tc.rec, tc.contra, tc.now)
			if math.Abs(got-tc.want) > 0.001 {
				t.Errorf("ComputeMemoryValidity = %.2f, want %.2f", got, tc.want)
			}
		})
	}
}

func TestCountMemoryContradictions(t *testing.T) {
	t0 := fixed
	tOlder := fixed.Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	tEqual := fixed.UTC().Format(time.RFC3339)
	tNewer := fixed.Add(24 * time.Hour).UTC().Format(time.RFC3339)

	mem := Record{
		ID:         "MEMORY-use-real-postgres",
		Kind:       "memory",
		Title:      "Integration tests must hit real postgres database",
		VerifiedAt: t0.UTC().Format(time.RFC3339),
		Updated:    tOlder,
	}

	feedbacks := []Record{
		{ID: "FEEDBACK-2026-10-24-0001-old", Kind: "feedback", Title: "use-real-postgres is wrong", Updated: tOlder},
		{ID: "FEEDBACK-2026-10-25-1430-eq", Kind: "feedback", Title: "use-real-postgres equal time", Updated: tEqual},
		{ID: "FEEDBACK-2026-10-26-0001-slug", Kind: "feedback", Title: "do not require use-real-postgres", Body: "changed mind", Updated: tNewer},
		{ID: "FEEDBACK-2026-10-26-0002-tokens", Kind: "feedback", Title: "mock database in unit tests", Body: "integration tests use sqlite instead of postgres", Updated: tNewer},
		{ID: "FEEDBACK-2026-10-26-0003-one-token", Kind: "feedback", Title: "postgres tuning", Body: "only one token matches", Updated: tNewer},
		{ID: "FEEDBACK-2026-10-26-0004-stopwords", Kind: "feedback", Title: "must with this from that", Body: "only stopwords match", Updated: tNewer},
	}

	got := CountMemoryContradictions(mem, feedbacks)
	if got != 2 {
		t.Errorf("CountMemoryContradictions = %d, want 2 (slug + >=2 non-stopword tokens)", got)
	}
}

func writeTestDoc(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestV910BrainDocsGuard(t *testing.T) {
	// Ordinary docs/ and empty .agents/docs/ must open cleanly without migration refusal.
	for _, rel := range []string{"docs/ARCHITECTURE.md", "docs/plans/roadmap.md", ".agents/docs/README.md"} {
		dir, home := repo(t), t.TempDir()
		writeTestDoc(t, dir, rel, "# Docs\n")
		if _, err := List(opts(dir, home), ListFilter{}); err != nil {
			t.Errorf("%s caused false migration refusal: %v", rel, err)
		}
	}
	// Legacy file-era trail records under docs/ or .agents/docs/ must refuse with fyr record import.
	for _, rel := range []string{
		"docs/ADR-001.md",
		"docs/plans/PLAN-2026-01-01-x.md",
		".agents/docs/bugs/BUG-001.md",
		".agents/docs/changes/CHANGE-2026-01-01.md",
		"docs/handoffs/HANDOFF-2026-01-01.md",
	} {
		dir, home := repo(t), t.TempDir()
		writeTestDoc(t, dir, rel, "# Legacy\n")
		if _, err := List(opts(dir, home), ListFilter{}); !IsRefusal(err) || !strings.Contains(err.Error(), "fyr record import") {
			t.Errorf("%s: got %v, want refusal naming fyr record import", rel, err)
		}
		writeTestDoc(t, dir, ".agents/trail.jsonl", "")
		if _, err := List(opts(dir, home), ListFilter{}); err != nil {
			t.Errorf("%s with trail.jsonl: %v", rel, err)
		}
	}
}

func TestV910BrainProvenanceAndStale(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	writeTestDoc(t, dir, "core.go", "package core\n")

	idOp, err := MemoryAdd(o, MemoryEntry{
		Type: "procedural", Name: "op-guard", Description: "Core invariant in core.go",
	}, strings.NewReader("sacred operator invariant for core.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Symmetric guard: derived MemoryAdd MUST NOT overwrite existing operator card.
	if _, err := MemoryAdd(o, MemoryEntry{
		Type: "semantic", Name: "op-guard", Description: "hijack", Provenance: "derived",
	}, strings.NewReader("derived hijack")); !IsRefusal(err) {
		t.Errorf("derived MemoryAdd over operator card: err=%v, want refusal", err)
	}
	// Symmetric guard: draft merge MUST keep operator primary body & kind intact.
	for i := 1; i <= 3; i++ {
		if _, err := MemoryDraftWithOptions(o, "merge", idOp, fmt.Sprintf("derived note %d", i), MemoryDraftOptions{Type: "episodic"}); err != nil {
			t.Fatalf("merge %d: %v", i, err)
		}
	}
	recs, _, err := Get(o, []string{idOp})
	if err != nil || len(recs) != 1 {
		t.Fatalf("Get %s: %v", idOp, err)
	}
	mainBody, priors := splitMemoryMainAndPriors(recs[0].Body)
	if !strings.Contains(mainBody, "sacred operator invariant for core.go") || strings.Contains(mainBody, "derived note") {
		t.Errorf("operator primary body altered by merge: %q", mainBody)
	}
	if recs[0].Provenance != "operator" || recs[0].MemoryKind != "procedural" || len(priors) != 2 {
		t.Errorf("operator metadata/priors broken: prov=%q kind=%q priors=%d", recs[0].Provenance, recs[0].MemoryKind, len(priors))
	}
	assertMemoryShowStaleFlags(t, o, dir)
}

func assertMemoryShowStaleFlags(t *testing.T, o Options, dir string) {
	t.Helper()
	assertStaleState := func(wantStale bool, label string) {
		show, err := MemoryShow(o)
		if err != nil {
			t.Fatalf("%s MemoryShow: %v", label, err)
		}
		if has := strings.Contains(show, "[STALE:"); has != wantStale {
			t.Errorf("%s MemoryShow [STALE:] = %v, want %v:\n%s", label, has, wantStale, show)
		}
		raw, err := MemoryShowJSONWithOptions(o, MemoryShowOptions{})
		if err != nil {
			t.Fatalf("%s MemoryShowJSON: %v", label, err)
		}
		var rows []struct {
			Name  string `json:"name"`
			Stale *bool  `json:"stale"`
		}
		if err := json.Unmarshal([]byte(raw), &rows); err != nil || len(rows) != 1 || rows[0].Stale == nil {
			t.Fatalf("%s JSON missing boolean stale field: err=%v raw=%s", label, err, raw)
		}
		if *rows[0].Stale != wantStale {
			t.Errorf("%s JSON stale = %v, want %v", label, *rows[0].Stale, wantStale)
		}
	}
	// Touch mtime without changing content: NOT stale.
	future := fixed.Add(2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "core.go"), future, future); err != nil {
		t.Fatal(err)
	}
	assertStaleState(false, "unchanged_touch")
	// Change content: STALE in both text and JSON.
	writeTestDoc(t, dir, "core.go", "package core\n// edited\n")
	assertStaleState(true, "content_changed")
}

func TestV910BrainDocsGuardProvenanceAndValidity(t *testing.T) {
	t.Run("docs_guard", TestV910BrainDocsGuard)
	t.Run("provenance_and_stale", TestV910BrainProvenanceAndStale)
	t.Run("validity_calibration", func(t *testing.T) {
		if MinActiveValidity != 0.40 {
			t.Fatalf("MinActiveValidity = %.2f, want 0.40", MinActiveValidity)
		}
		dir := repo(t)
		ts := fixed.UTC().Format(time.RFC3339)
		der := Record{ID: "MEMORY-der", Kind: "memory", Provenance: "derived", MemoryKind: "semantic", VerifiedAt: ts, Updated: ts}
		op := Record{ID: "MEMORY-op", Kind: "memory", Provenance: "operator", MemoryKind: "semantic", VerifiedAt: ts, Updated: ts}
		unk := Record{ID: "MEMORY-unk", Kind: "memory", Provenance: "unknown", MemoryKind: "semantic", EvidenceCount: 2, VerifiedAt: ts, Updated: ts}

		checks := []struct {
			name   string
			rec    Record
			contra int
			now    time.Time
			want   float64
		}{
			{"derived_ev0_fresh", der, 0, fixed, 0.80},
			{"derived_ev0_1x_halflife_active", der, 0, fixed.Add(180 * 24 * time.Hour), 0.40},
			{"derived_ev0_1_1x_halflife_hidden", der, 0, fixed.Add(198 * 24 * time.Hour), 0.37},
			{"unknown_ev2_cap", unk, 0, fixed, 0.90},
			{"operator_1000d_floor_0_35", op, 0, fixed.Add(1000 * 24 * time.Hour), 0.35},
			{"operator_3_contras_0_25", op, 3, fixed, 0.25},
		}
		for _, tc := range checks {
			got := ComputeMemoryValidity(dir, tc.rec, tc.contra, tc.now)
			if math.Abs(got-tc.want) > 0.001 {
				t.Errorf("%s: got %.2f, want %.2f", tc.name, got, tc.want)
			}
		}
	})
}
