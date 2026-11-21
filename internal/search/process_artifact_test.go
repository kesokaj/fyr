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
	for _, want := range []string{
		"# fyr Turn Telemetry",
		"- **Conversation:** `conv-1`",
		"- **Stage:** `VET`",
		"## Model Routing",
		"- **Policy:** ",
		"Builder -> Gemini",
	} {
		if !strings.Contains(gotPolicy, want) {
			t.Fatalf("policy telemetry missing %q:\n%s", want, gotPolicy)
		}
	}
	for _, forbidden := range []string{"gemini-4-" + "argon", "claude-sonnet-" + "4-6", "```json"} {
		if strings.Contains(gotPolicy, forbidden) {
			t.Errorf("policy output leaked forbidden token %q:\n%s", forbidden, gotPolicy)
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
		"- **Conversation:** `conv-2`",
		"- **Stage:** `BUILD`",
		"| Builder (Pro) | `gemini-4-carbon-high` |",
		"| Builder (Flash) | `gemini-3.8-flash-high` |",
		"| Judge (Pro) | `claude-opus-5-5-high` |",
		"| Judge (Flash) | `claude-sonnet-5-5-high` |",
		"- **Updated:** `2026-11-11T10:00:00Z`",
		"- **Fallback:** previous model claude-opus-5-4-high is gone",
	} {
		if !strings.Contains(gotCached, want) {
			t.Errorf("cached modelRouting missing %q in:\n%s", want, gotCached)
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

	// 3. Control characters are scrubbed, Markdown cells/spans stay escaped, and fenced content with ``` upgrades outer fences.
	out := RenderProcessArtifactForHome("", "conv\x00|```evil", "4.`Build`", []VetHistoryRow{{
		TurnNum:         1,
		Intent:          "feat|x",
		Gate:            "ALLOW`y",
		Score:           90,
		SuggestedSkills: []string{"sk|a", `sk\|b`},
		PivotFiles:      []string{"a`b.go"},
		ImprovedPrompt:  "hello\n```\ninjected\n```\nbye",
	}}, nil)
	if strings.Contains(out, "```json") {
		t.Errorf("process telemetry should not emit json fence:\n%s", out)
	}
	for _, want := range []string{
		"- **Conversation:** ````conv?|```evil````",
		"- **Stage:** `4_BUILD`",
		"| 1 | FEAT_X | ALLOW_Y | 90/100 |",
		"`sk\\|a`, `sk\\\\\\|b`",
		"``a`b.go``",
		"````text\nhello\n```\ninjected\n```\nbye\n````",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hardened telemetry missing %q:\n%s", want, out)
		}
	}

	// 4. appendProcessImprovedPrompts scrubs C0, C1, BiDi Cf, and Zl/Zp control runes while keeping newlines.
	bidiPrompt := "line1\x1b[31mred\nline2\u009bCSI\nline3\u202eRLO\u2028zl\u2029zp\u200bzw"
	outBidi := RenderProcessArtifactForHome("", "conv-bidi", "4. Build", []VetHistoryRow{{
		TurnNum:        2,
		Intent:         "task",
		Gate:           "ENRICH_AND_PROCEED",
		Score:          85,
		ImprovedPrompt: bidiPrompt,
	}}, nil)
	for _, bad := range []string{"\x1b", "\u009b", "\u202e", "\u2028", "\u2029", "\u200b"} {
		if strings.Contains(outBidi, bad) {
			t.Errorf("ImprovedPrompt leaked control/BiDi rune %U in:\n%s", []rune(bad)[0], outBidi)
		}
	}
	if !strings.Contains(outBidi, "line1?[31mred\nline2?CSI\nline3?RLO?zl?zp?zw") {
		t.Errorf("ImprovedPrompt did not preserve multi-line scrubbed lines:\n%s", outBidi)
	}
}

func TestHomeAnchoredBrainDirAndRejectsOutsideHome(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "brain", "conv-1")
	if !isUnderBrainDir(outside) {
		t.Fatalf("unanchored home==\"\" should preserve isUnderBrainDir(%q)", outside)
	}
	if isUnderBrainDirForHome(home, outside) {
		t.Fatalf("isUnderBrainDirForHome accepted outside brain dir %q for home %q", outside, home)
	}
	anchored := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-1")
	if !isUnderBrainDirForHome(home, anchored) {
		t.Fatalf("isUnderBrainDirForHome rejected valid anchored dir %q", anchored)
	}
	if dbPath, _ := resolveHookConvDBPathForHome(home, HookInput{ConversationID: "conv-1", ArtifactDirectoryPath: outside}); dbPath != "" {
		t.Fatalf("resolveHookConvDBPathForHome accepted outside path: %q", dbPath)
	}
}
