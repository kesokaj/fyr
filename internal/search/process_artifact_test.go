package search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func extractProcessTelemetryJSON(t *testing.T, md string) (string, ProcessTelemetry) {
	t.Helper()
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.HasSuffix(lines[i], "json") && strings.HasPrefix(lines[i], "```") {
			fence := strings.TrimSuffix(lines[i], "json")
			for j := i + 1; j < len(lines); j++ {
				if lines[j] == fence {
					raw := strings.Join(lines[i+1:j], "\n")
					var doc ProcessTelemetry
					if err := json.Unmarshal([]byte(raw), &doc); err != nil {
						t.Fatalf("unmarshal ProcessTelemetry: %v\nraw:\n%s", err, raw)
					}
					return raw, doc
				}
			}
		}
	}
	t.Fatalf("missing fenced json block in process telemetry:\n%s", md)
	return "", ProcessTelemetry{}
}

func TestRenderProcessArtifactModelCatalog(t *testing.T) {
	// 1. Missing models.json renders role-family policy with zero hardcoded model IDs.
	emptyHome := t.TempDir()
	gotPolicy := RenderProcessArtifactForHome(emptyHome, "conv-1", "2. Vet", nil, nil)
	rawPolicy, docPolicy := extractProcessTelemetryJSON(t, gotPolicy)
	if docPolicy.Stage != "VET" || !strings.Contains(docPolicy.ModelRouting.Policy, "Builder -> Gemini") {
		t.Fatalf("unexpected policy telemetry %+v:\n%s", docPolicy, rawPolicy)
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
	_, docCached := extractProcessTelemetryJSON(t, gotCached)
	if docCached.Stage != "BUILD" ||
		docCached.ModelRouting.BuilderPro != "gemini-4-carbon-high" ||
		docCached.ModelRouting.BuilderFlash != "gemini-3.8-flash-high" ||
		docCached.ModelRouting.JudgePro != "claude-opus-5-5-high" ||
		docCached.ModelRouting.JudgeFlash != "claude-sonnet-5-5-high" ||
		docCached.ModelRouting.UpdateTime != "2026-11-11T10:00:00Z" ||
		docCached.ModelRouting.FallbackReason != "previous model claude-opus-5-4-high is gone" {
		t.Errorf("unexpected cached modelRouting telemetry: %+v", docCached.ModelRouting)
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
	if got := resolveProcessArtifactPath(HookInput{TranscriptPath: "brain/conv-1/logs/transcript.jsonl"}); got != "" {
		t.Errorf("relative TranscriptPath resolved artifact path %q, want empty", got)
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
		if got := resolveProcessArtifactPath(HookInput{TranscriptPath: filepath.Join(symConv, "logs", "transcript.jsonl")}); got != "" {
			t.Errorf("symlinked TranscriptPath resolved artifact path %q, want empty", got)
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

	// 3. Control characters are scrubbed, JSON fields parse cleanly, and fenced content with ``` upgrades outer fences.
	out := RenderProcessArtifact("conv\x00|```evil", "4.`Build`", []VetHistoryRow{{
		TurnNum:         1,
		Intent:          "feat|x",
		Gate:            "ALLOW`y",
		Score:           90,
		SuggestedSkills: []string{"sk|a"},
		PivotFiles:      []string{"a`b.go"},
		ImprovedPrompt:  "hello\n```\ninjected\n```\nbye",
	}}, nil)
	if !strings.Contains(out, "````json\n") {
		t.Errorf("conversationId containing ``` did not upgrade outer json fence:\n%s", out)
	}
	_, docHard := extractProcessTelemetryJSON(t, out)
	if docHard.ConversationID != "conv?|```evil" || docHard.Stage != "4_BUILD" || len(docHard.Turns) != 1 || docHard.Turns[0].Intent != "FEAT_X" || docHard.Turns[0].Gate != "ALLOW_Y" {
		t.Errorf("unexpected hardened telemetry payload: %+v", docHard)
	}
	if !strings.Contains(out, "````text\nhello\n```\ninjected\n```\nbye\n````") {
		t.Errorf("fenced prompt containing ``` did not upgrade outer fence:\n%s", out)
	}
}
