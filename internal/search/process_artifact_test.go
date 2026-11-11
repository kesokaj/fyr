package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestRenderProcessArtifactModelCatalog(t *testing.T) {
	// 1. Missing models.json renders role-family policy with zero hardcoded model IDs.
	emptyHome := t.TempDir()
	gotPolicy := RenderProcessArtifactForHome(emptyHome, "conv-1", "2. Vet", nil, nil)
	if !strings.Contains(gotPolicy, "Cross-Family Model Routing:") || !strings.Contains(gotPolicy, "Builder → Gemini") {
		t.Fatalf("missing policy routing line:\n%s", gotPolicy)
	}
	for _, forbidden := range []string{"gemini-4-" + "argon", "claude-sonnet-" + "4-6"} {
		if strings.Contains(gotPolicy, forbidden) {
			t.Errorf("policy output leaked hardcoded model ID %q:\n%s", forbidden, gotPolicy)
		}
	}

	// 2. Cached models.json renders active picks and fallback note without spawning agy.
	home := t.TempDir()
	stateDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	modelsJSON := `{
		"updated_at": "2026-11-11T10:00:00Z",
		"picks": {
			"builder:pro": {"model": "gemini-4-carbon-high"},
			"builder:flash": {"model": "gemini-3.8-flash-high"},
			"judge:pro": {"model": "claude-opus-5-5-high", "fallback_reason": "previous model claude-opus-5-4-high is gone"},
			"judge:flash": {"model": "claude-sonnet-5-5-high"}
		}
	}`
	if err := os.WriteFile(filepath.Join(stateDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	gotCached := RenderProcessArtifactForHome(home, "conv-2", "4. Build", nil, nil)
	for _, want := range []string{
		"`gemini-4-carbon-high`",
		"`gemini-3.8-flash-high`",
		"`claude-opus-5-5-high`",
		"`claude-sonnet-5-5-high`",
		"previous model claude-opus-5-4-high is gone",
	} {
		if !strings.Contains(gotCached, want) {
			t.Errorf("cached process artifact missing %q:\n%s", want, gotCached)
		}
	}
}
