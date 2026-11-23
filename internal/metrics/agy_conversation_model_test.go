package metrics

import (
	"path/filepath"
	"testing"
)

func TestNormalizeSessionModel(t *testing.T) {
	if got := normalizeSessionModel("gemini-4-neon-medium", map[string]bool{"gemini-4-neon-medium": true}); got != "gemini-4-neon-medium" {
		t.Errorf("single model = %q, want gemini-4-neon-medium", got)
	}
	if got := normalizeSessionModel("gemini-4-neon", map[string]bool{"gemini-4-neon": true, "gemini-4-neon-high": true}); got != "gemini-4-neon-high" {
		t.Errorf("stem+effort = %q, want gemini-4-neon-high", got)
	}
	if got := normalizeSessionModel("gemini-4-neon-medium", map[string]bool{"gemini-4-neon-medium": true, "gemini-pro-orchestrator": true}); got != "gemini-pro-orchestrator" {
		t.Errorf("explicit orchestrator = %q, want gemini-pro-orchestrator", got)
	}
	if got := normalizeSessionModel("gemini-4-carbon-medium", map[string]bool{"gemini-4-carbon-medium": true, "gemini-4-hydrogen-medium": true}); got != "orchestrator(gemini-4-carbon-medium+gemini-4-hydrogen-medium)" {
		t.Errorf("codenames must not collapse to hardcoded orchestrator: got %q", got)
	}
	if got := normalizeSessionModel("gemini-4-carbon-medium", map[string]bool{"gemini-4-carbon-medium": true, "claude-opus-5-5-medium": true}); got != "orchestrator(claude-opus-5-5-medium+gemini-4-carbon-medium)" {
		t.Errorf("mixed distinct models = %q, want orchestrator(claude-opus-5-5-medium+gemini-4-carbon-medium)", got)
	}
}

func TestAgyGensPrefersConcreteModelField328WithFallbackTo119(t *testing.T) {
	genWith := func(f119, f328 string) []byte {
		var parts [][]byte
		parts = append(parts, pbB(1, cat(pbB(4, cat(pbV(2, 100), pbV(3, 20))), pbB(19, []byte(f119)))))
		if f328 != "" {
			parts = append(parts, pbB(3, pbB(28, []byte(f328))))
		}
		return cat(parts...)
	}

	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	w := writer{tx: tx, salt: "s"}
	cBoth := agyConv{id: "conv-concrete", root: t.TempDir()}
	w.agySession(cBoth)
	w.agyGensWithOverrides(cBoth, [][]byte{
		genWith("gemini-4-neon", ""),
		genWith("gemini-4-neon", "gemini-4-neon-high"),
	}, []int64{1000, 2000}, nil)

	cFallback := agyConv{id: "conv-fallback", root: t.TempDir()}
	w.agySession(cFallback)
	w.agyGensWithOverrides(cFallback, [][]byte{
		genWith("claude-sonnet-5-5", ""),
	}, []int64{3000}, nil)
	if w.err != nil {
		t.Fatal(w.err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var gotConcrete, gotFallback string
	if err := s.db.QueryRow(`SELECT model FROM sessions WHERE id = 'conv-concrete'`).Scan(&gotConcrete); err != nil || gotConcrete != "gemini-4-neon-high" {
		t.Errorf("conv-concrete model = %q, err=%v; want gemini-4-neon-high", gotConcrete, err)
	}
	if err := s.db.QueryRow(`SELECT model FROM sessions WHERE id = 'conv-fallback'`).Scan(&gotFallback); err != nil || gotFallback != "claude-sonnet-5-5" {
		t.Errorf("conv-fallback model = %q, err=%v; want claude-sonnet-5-5", gotFallback, err)
	}
}
