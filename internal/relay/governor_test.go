package relay

import (
	"testing"

	"github.com/kesokaj/fyr/internal/models"
)

func TestCheckBudgetFloorTable(t *testing.T) {
	tests := []struct {
		name         string
		saveCatalog  bool
		pools        []models.Pool
		requireQuota bool
		wantOK       bool
	}{
		{"missing models.json optional", false, nil, false, true},
		{"missing models.json required", false, nil, true, false},
		{"empty pools optional", true, nil, false, true},
		{"empty pools required", true, nil, true, false},
		{"healthy gemini pool", true, []models.Pool{{Name: "Gemini 3 Pro", Five: 45, Week: 30}}, true, true},
		{"exact floor boundary passes", true, []models.Pool{{Name: "Gemini models", Five: 20, Week: 15}}, true, true},
		{"below 5h floor breaches", true, []models.Pool{{Name: "Gemini 3 Pro", Five: 19, Week: 80}}, false, false},
		{"below weekly floor breaches", true, []models.Pool{{Name: "Gemini 3 Pro", Five: 80, Week: 14}}, false, false},
		{"disabled window breaches", true, []models.Pool{{Name: "Gemini 3 Pro", Five: models.DisabledWindow, Week: 50}}, false, false},
		{"claude low does not trip gemini floor", true, []models.Pool{{Name: "Claude models", Five: 5, Week: 5}, {Name: "Gemini 3 Pro", Five: 60, Week: 50}}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.saveCatalog {
				err := models.SaveCatalog(home, models.Catalog{
					UpdatedAt: "2026-11-22T00:00:00Z",
					Pools:     tc.pools,
				})
				if err != nil {
					t.Fatalf("SaveCatalog: %v", err)
				}
			}
			got, err := CheckBudgetFloor(home, tc.requireQuota)
			if err != nil {
				t.Fatalf("CheckBudgetFloor: %v", err)
			}
			if got.OK != tc.wantOK {
				t.Errorf("CheckBudgetFloor OK=%v (%s), want %v", got.OK, got.Note, tc.wantOK)
			}
		})
	}
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
