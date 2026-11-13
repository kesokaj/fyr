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

func TestProcessArtifactSecurityHardening(t *testing.T) {
	// 1. Anchored brain directory refuses nested arbitrary /brain/<conv>/sub paths and relative paths.
	if isUnderBrainDir("relative/brain/conv-1") {
		t.Errorf("relative brain dir accepted")
	}
	if isUnderBrainDir("/tmp/brain/conv-1/nested") {
		t.Errorf("nested subdir under brain accepted")
	}
	tmpRoot := t.TempDir()
	realDir := filepath.Join(tmpRoot, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	symBrain := filepath.Join(tmpRoot, "brain")
	if err := os.Symlink(realDir, symBrain); err == nil {
		symConv := filepath.Join(symBrain, "conv-1")
		if isUnderBrainDir(symConv) {
			t.Errorf("symlinked brain dir accepted")
		}
		if err := writeProcessArtifactAtomic(filepath.Join(symConv, "fyr-process.md"), []byte("x")); err == nil {
			t.Errorf("writeProcessArtifactAtomic succeeded through symlinked brain dir")
		}
	}

	// 2. loadLatestWorkStatus refuses symlinked status.json.
	fakeHome := t.TempDir()
	workWave := filepath.Join(fakeHome, harness.StateDir, "work", "2026-11-12-100000-1")
	if err := os.MkdirAll(workWave, 0o700); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(fakeHome, "secret.json")
	if err := os.WriteFile(secretFile, []byte(`{"stamp":"s","workers":[{"name":"w"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretFile, filepath.Join(workWave, "status.json")); err == nil {
		if _, ok := loadLatestWorkStatus(fakeHome); ok {
			t.Errorf("loadLatestWorkStatus followed symlinked status.json")
		}
	}

	// 3. Rendered cells escape '|' and backticks, and fenced prompts with ``` use longer fences.
	out := RenderProcessArtifact("conv|evil", "4.`Build`", []VetHistoryRow{{
		TurnNum:         1,
		Intent:          "feat|x",
		Gate:            "ALLOW`y",
		Score:           90,
		SuggestedSkills: []string{"sk|a"},
		PivotFiles:      []string{"a`b.go"},
		ImprovedPrompt:  "hello\n```\ninjected\n```\nbye",
	}}, nil)
	if strings.Contains(out, "conv|evil") || strings.Contains(out, "feat|x") || strings.Contains(out, "`a`b.go`") {
		t.Errorf("unescaped markdown cell characters in:\n%s", out)
	}
	if !strings.Contains(out, "````text\nhello\n```\ninjected\n```\nbye\n````") {
		t.Errorf("fenced prompt containing ``` did not upgrade outer fence:\n%s", out)
	}
}
