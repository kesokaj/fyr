package brain

import (
	"math"
	"os"
	"path/filepath"
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
			want: 0.55,
		},
		{
			name: "derived_evidence_3",
			top:  dir,
			rec: Record{
				ID:            "MEMORY-der-3",
				Kind:          "memory",
				Provenance:    "derived",
				MemoryKind:    "semantic",
				EvidenceCount: 3,
				VerifiedAt:    tsFixed,
				Updated:       tsFixed,
			},
			now:  fixed,
			want: 0.70,
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
			want: 0.70,
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
			want: 0.85,
		},
		{
			name: "stale_operator_subtracts_0_45",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-stale",
				Kind:       "memory",
				Provenance: "operator",
				MemoryKind: "semantic",
				Body:       "type: semantic\n\nSee stale.go",
				VerifiedAt: tsFixed,
				Updated:    tsFixed,
			},
			now:  fixed,
			want: 0.55,
		},
		{
			name: "contradictions_capped_at_2",
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
			want:   0.50,
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
			name: "age_decay_floor_0_40",
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
			want: 0.40,
		},
		{
			name: "clamp_floor_zero",
			top:  dir,
			rec: Record{
				ID:         "MEMORY-clamp",
				Kind:       "memory",
				Provenance: "derived",
				MemoryKind: "semantic",
				Body:       "type: semantic\n\nSee stale.go",
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
