package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNamedRowSkillProhibitions pins BUG-533: skills exercised under named
// grants in GUIDE-eval must not prompt ungranted shell commands — single-phase
// plans in fyr-feature omit model: without running `agy models`, and fyr-spec
// derives failure modes statically without live server or `curl` probes.
func TestNamedRowSkillProhibitions(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	featureBytes, err := os.ReadFile(filepath.Join(root, "skills", "fyr-feature", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	feature := string(featureBytes)
	_, afterTemplate, ok := strings.Cut(feature, "```markdown\n# Plan —")
	if !ok {
		t.Fatal("skills/fyr-feature/SKILL.md is missing its Plan markdown template block")
	}
	planTemplate, _, ok := strings.Cut(afterTemplate, "\n```")
	if !ok {
		t.Fatal("skills/fyr-feature/SKILL.md has an unterminated Plan markdown template block")
	}
	if strings.Contains(planTemplate, "agy models") {
		t.Error("skills/fyr-feature/SKILL.md Plan template mentions `agy models`, prompting single-phase named-grant runs to invoke agy")
	}
	if !strings.Contains(feature, "do not run `agy models`") {
		t.Error("skills/fyr-feature/SKILL.md does not tell single-phase / workers: no plans not to run `agy models`")
	}

	specBytes, err := os.ReadFile(filepath.Join(root, "skills", "fyr-spec", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	spec := string(specBytes)
	if !strings.Contains(spec, "never build or start live servers") || !strings.Contains(spec, "`curl` probes") {
		t.Error("skills/fyr-spec/SKILL.md Step 2 does not forbid building/starting live servers or `curl` probes while writing a spec")
	}
}
