package relay

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/models"
)

func TestCheckBudgetFloorTable(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	freshTS := now.Add(-2 * time.Minute).Format(time.RFC3339)
	staleTS := now.Add(-20 * time.Minute).Format(time.RFC3339)
	tests := []struct {
		name         string
		saveCatalog  bool
		quotaTS      string
		pools        []models.Pool
		requireQuota bool
		wantOK       bool
		wantNoteSub  string
	}{
		{"missing models.json optional", false, "", nil, false, true, "absent"},
		{"missing models.json required", false, "", nil, true, false, "missing"},
		{"empty pools optional", true, freshTS, nil, false, true, "absent"},
		{"empty pools required", true, freshTS, nil, true, false, "missing"},
		{"healthy fresh gemini pool", true, freshTS, []models.Pool{{Name: "Gemini 3 Pro", Five: 45, Week: 30}}, true, true, "5h=45%"},
		{"exact floor boundary passes", true, freshTS, []models.Pool{{Name: "Gemini models", Five: 20, Week: 15}}, true, true, "5h=20%"},
		{"below 5h floor breaches", true, freshTS, []models.Pool{{Name: "Gemini 3 Pro", Five: 19, Week: 80}}, false, false, "below floor"},
		{"below weekly floor breaches", true, freshTS, []models.Pool{{Name: "Gemini 3 Pro", Five: 80, Week: 14}}, false, false, "below floor"},
		{"disabled 5h window skipped when week healthy", true, freshTS, []models.Pool{{Name: "Gemini 3 Pro", Five: models.DisabledWindow, Week: 50}}, true, true, "5h=disabled"},
		{"disabled week window skipped when 5h healthy", true, freshTS, []models.Pool{{Name: "Gemini 3 Pro", Five: 50, Week: models.DisabledWindow}}, true, true, "week=disabled"},
		{"disabled 5h still breaches when week low", true, freshTS, []models.Pool{{Name: "Gemini 3 Pro", Five: models.DisabledWindow, Week: 10}}, false, false, "below floor"},
		{"stale cache optional warns and passes", true, staleTS, []models.Pool{{Name: "Gemini 3 Pro", Five: 60, Week: 50}}, false, true, "stale quota cache"},
		{"stale cache required halts", true, staleTS, []models.Pool{{Name: "Gemini 3 Pro", Five: 60, Week: 50}}, true, false, "stale quota cache"},
		{"claude low does not trip gemini floor", true, freshTS, []models.Pool{{Name: "Claude models", Five: 5, Week: 5}, {Name: "Gemini 3 Pro", Five: 60, Week: 50}}, true, true, "Gemini 3 Pro"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.saveCatalog {
				err := models.SaveCatalog(home, models.Catalog{
					UpdatedAt:      now.Format(time.RFC3339),
					QuotaUpdatedAt: tc.quotaTS,
					Pools:          tc.pools,
				})
				if err != nil {
					t.Fatalf("SaveCatalog: %v", err)
				}
			}
			got, err := CheckBudgetFloorAt(home, tc.requireQuota, now)
			if err != nil {
				t.Fatalf("CheckBudgetFloorAt: %v", err)
			}
			if got.OK != tc.wantOK {
				t.Errorf("CheckBudgetFloorAt OK=%v (%s), want %v", got.OK, got.Note, tc.wantOK)
			}
			if tc.wantNoteSub != "" && !strings.Contains(got.Note, tc.wantNoteSub) {
				t.Errorf("Note=%q missing %q", got.Note, tc.wantNoteSub)
			}
		})
	}
}

func TestLoadPriorErrorHashesNewestFirstOrdering(t *testing.T) {
	t.Run("PASS after HALT resets prior hashes", func(t *testing.T) {
		repo, home := initRelayRepo(t)
		t1 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
		t2 := t1.Add(5 * time.Minute)
		addHandoff := func(at time.Time, id, body string) {
			_, err := brain.Add(brain.Options{Home: home, Dir: repo, Now: func() time.Time { return at }}, brain.AddInput{
				Kind: "handoff", ID: id, Title: id, Body: strings.NewReader(body),
			})
			if err != nil {
				t.Fatalf("brain.Add %s: %v", id, err)
			}
		}
		// Give the older HALT a lexicographically greater ID than the newer PASS to prove timestamp sorting wins.
		addHandoff(t1, "HANDOFF-zzz-older-halt", "# H\n\nPlan: PLAN-ORD\nRelay-Outcome: HALTED\nRelay-Error-Hash: oldhash\n")
		addHandoff(t2, "HANDOFF-aaa-newer-pass", "# P\n\nPlan: PLAN-ORD\nRelay-Outcome: PASS\n")
		if got := LoadPriorErrorHashes(home, repo, "PLAN-ORD"); len(got) != 0 {
			t.Fatalf("PASS-after-HALT: got %v, want empty", got)
		}
	})

	t.Run("HALT after PASS collects only post-PASS hashes", func(t *testing.T) {
		repo, home := initRelayRepo(t)
		t1 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
		t2 := t1.Add(5 * time.Minute)
		t3 := t1.Add(10 * time.Minute)
		addHandoff := func(at time.Time, id, body string) {
			_, err := brain.Add(brain.Options{Home: home, Dir: repo, Now: func() time.Time { return at }}, brain.AddInput{
				Kind: "handoff", ID: id, Title: id, Body: strings.NewReader(body),
			})
			if err != nil {
				t.Fatalf("brain.Add %s: %v", id, err)
			}
		}
		addHandoff(t1, "HANDOFF-zzz-prepass-halt", "# H1\n\nPlan: PLAN-ORD2\nRelay-Outcome: HALTED\nRelay-Error-Hash: prepass\n")
		addHandoff(t2, "HANDOFF-mmm-mid-pass", "# P\n\nPlan: PLAN-ORD2\nRelay-Outcome: PASS\n")
		addHandoff(t3, "HANDOFF-aaa-postpass-halt", "# H2\n\nPlan: PLAN-ORD2\nRelay-Outcome: HALTED\nRelay-Error-Hash: postpass\n")
		got := LoadPriorErrorHashes(home, repo, "PLAN-ORD2")
		if !slices.Equal(got, []string{"postpass"}) {
			t.Fatalf("HALT-after-PASS: got %v, want [postpass]", got)
		}
	})
}

func TestErrorTrackerNormalizationAndRepeat(t *testing.T) {
	tr := NewErrorTracker()
	h1, rep1 := tr.Record("FAIL foo_test.go:12: mismatch at 2026-11-22T01:02:03Z commit a1b2c3d4 (125ms)")
	if rep1 || h1 == "" {
		t.Fatalf("first record: hash=%q rep=%v", h1, rep1)
	}
	h2, rep2 := tr.Record("FAIL foo_test.go:99: mismatch at 2026-11-22T09:10:11Z commit deadbeef (420ms)")
	if !rep2 || h2 != h1 {
		t.Fatalf("second normalized identical error: hash=%q (want %q) rep=%v (want true)", h2, h1, rep2)
	}
	h3, rep3 := tr.Record("go-vet: unreachable code in bar.go:10")
	if rep3 || h3 == h1 {
		t.Fatalf("distinct error collided: hash=%q rep=%v", h3, rep3)
	}
}
