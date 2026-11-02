package auto

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestRubricDenied(t *testing.T) {
	for _, e := range []string{
		// Roots, as a file, a directory, and a path under the directory.
		"PRD.md", "REQUIREMENTS.md", "context/", "context/AGENTS.md", "rules", "rules/x.md",
		".agents/rules/", ".agents/rules/y.md", "internal/eval/", "internal/eval/run.go",
		"internal/harness/size_test.go", "internal/harness/inventory.go",
		".github/", ".github/workflows/ci.yml", ".gitlab-ci.yml", "Jenkinsfile", "web/Jenkinsfile",
		"web/.github/workflows/x.yml", ".gitlab/ci.yml", ".circleci/config.yml", "web/cloudbuild.yaml",
		"cloudbuild.yml", "cloudbuild.json", "azure-pipelines.yml", "azure-pipelines.yaml",
		"web/.agents/rules/x.md",
		// The tracked eval suite and its fixture (ADR-500).
		".agents/eval/", ".agents/eval/GUIDE-eval.md", ".agents/eval/fixture.sh",
		// What holds a run in.
		"internal/work/fetch.go", "internal/sandbox/", "internal/redact/redact.go", "cmd/fyr/main.go",
		"docker/work.Dockerfile", "embed.go",
		// A CLI's other instruction files, and what redirects a build.
		"GEMINI.md", "web/GEMINI.md", "CLAUDE.md", ".cursorrules", "go.work", "go.work.sum", ".npmrc",
		"npm-shrinkwrap.json", "requirements.txt", "pyproject.toml", "Cargo.toml", "vendor/modules.txt",
		".gitignore", "web/.gitattributes",
		"skills/VENDORED.md", "skills/google-catalog/catalog/", "skills/google-catalog/catalog/a/SKILL.md",
		// auto's own contract and code (D36).
		"internal/auto/", "internal/auto/rubric.go", "skills/auto/", "skills/auto/SKILL.md",
		// .agents/skills links to skills/ in this repository.
		".agents/skills", ".agents/skills/VENDORED.md", ".agents/skills/google-catalog/catalog/x", ".agents/skills/auto/SKILL.md",
		// Names at any depth.
		"AGENTS.md", "web/AGENTS.md", "x/y/PRD.md", "a_test.go", "internal/x/a_test.go",
		"go.mod", "tools/go.sum", "web/package.json", "web/package-lock.json", "web/pnpm-lock.yaml",
		"yarn.lock", "web/bun.lockb", "Cargo.lock", "x/testdata/", "x/testdata/a.txt", "testdata",
		// A file system that folds case reaches the same file.
		"agents.md", "Web/Agents.MD", "PRD.MD", "Context/x.md", "A_TEST.GO",
		// A spelling that is not clean is refused, never resolved.
		"./PRD.md", "docs/../PRD.md", "docs/../internal/eval/x.go", "./context/x.md", "../x", "/etc/passwd", "", ".", "a//AGENTS.md", `a\AGENTS.md`,
		// A directory entry that holds a denied root would carry it in.
		"internal/", ".agents/", "skills/", "skills/google-catalog/", ".github",
		// Records.
		"GUIDE-eval", "GUIDE-testing", "SPEC-fyr-auto", "SPEC-fyr-eval", "SPEC-fyr-work", "ADR-090", "ADR-089a",
		// Lines git quotes, a control character, and past ASCII, which a
		// file system may fold onto a denied name.
		"\"a\\303.md\"", "a\nAGENTS.md", "AGENT\u017f.md", "\u017fkills/auto/SKILL.md", "A_\u017fest.go", "AGENT\u200cS.md",
	} {
		if !Denied(e) {
			t.Errorf("Denied(%q) = false, want true", e)
		}
	}
	for _, e := range []string{
		"README.md", "SDD.md", "TODO.md", "SPEC-greet", "GUIDE-onboarding",
		"docs/guide.md", "internal/greet/greet.go", "internal/worker.go", "cmd/server/main.go",
		"skills/retro/", "skills/retro/SKILL.md", ".agents/skills/new-one/", "agents/verifier/agent.md",
		"latest_test.go.txt", "contexts/x.md", "rules.md", "go.modx", "lock.go", "my-package.json",
		"web/context/x.md", "web/rules/x.md", "internal/evaluate/x.go", "adr-notes.md",
	} {
		if Denied(e) {
			t.Errorf("Denied(%q) = true, want false", e)
		}
	}
}

func TestRubricAllowedFiles(t *testing.T) {
	in := []string{"README.md", "AGENTS.md", "SPEC-greet", "internal/x_test.go", "ADR-001", "skills/retro/SKILL.md", ".agents/workflows/x.md"}
	kept, denied := AllowedFiles(in, false)
	if want := []string{"README.md", "SPEC-greet"}; !slices.Equal(kept, want) {
		t.Errorf("kept %v, want %v", kept, want)
	}
	if want := []string{"AGENTS.md", "internal/x_test.go", "ADR-001", "skills/retro/SKILL.md", ".agents/workflows/x.md"}; !slices.Equal(denied, want) {
		t.Errorf("denied %v, want %v", denied, want)
	}
	// An instruction class gets the instruction files, and nothing denied.
	kept, _ = AllowedFiles(in, true)
	if want := []string{"README.md", "SPEC-greet", "skills/retro/SKILL.md", ".agents/workflows/x.md"}; !slices.Equal(kept, want) {
		t.Errorf("an instruction class kept %v, want %v", kept, want)
	}
}

func TestRubricInstruction(t *testing.T) {
	for _, e := range []string{"skills/", "skills/retro/SKILL.md", "agents/verifier/agent.md", "commands/x.md",
		".agents/skills/new/", ".agents/agents/x.md", ".agents/commands/x.md", ".agents/workflows/x.md", ".agents/", "SKILLS/x", "../x"} {
		if !Instruction(e) {
			t.Errorf("Instruction(%q) = false, want true", e)
		}
	}
	for _, e := range []string{"README.md", "docs/skills/x.md", "SPEC-greet", "GUIDE-onboarding", "skillset/x.md"} {
		if Instruction(e) {
			t.Errorf("Instruction(%q) = true, want false", e)
		}
	}
}

// section is the text of the skill between heading and the next heading of
// any level, or "" when the heading is absent.
func section(text, heading string) string {
	_, after, ok := strings.Cut(text, "\n"+heading+"\n")
	if !ok {
		return ""
	}
	if i := strings.Index(after, "\n#"); i >= 0 {
		return after[:i]
	}
	return after
}

var (
	backticked = regexp.MustCompile("`([^`]+)`")
	bullet     = regexp.MustCompile(`(?m)^- (.+)$`)
)

// TestRubricMatchesSkill pins rubric.go and the skill's words to each other,
// both ways: a token or sentence on one side only fails.
func TestRubricMatchesSkill(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "auto", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)

	for heading, tokens := range map[string][]string{"### Always denied": DeniedTokens(), "### Instruction files": InstructionTokens()} {
		sec := section(text, heading)
		if sec == "" {
			t.Fatalf("the skill has no %s section", heading)
		}
		var words []string
		for _, b := range bullet.FindAllString(sec, -1) {
			for _, m := range backticked.FindAllStringSubmatch(b, -1) {
				// fyr check reads a bare path as this repository's;
				// <project>/ tells it this one is any project's.
				words = append(words, strings.TrimPrefix(m[1], "<project>/"))
			}
		}
		slices.Sort(words)
		if !slices.Equal(slices.Compact(words), tokens) {
			t.Errorf("%s:\nthe skill %q\nrubric.go %q", heading, words, tokens)
		}
	}

	for heading, want := range map[string][]string{"### Always an ask": Asks(), "### Network": Network()} {
		sec := section(text, heading)
		if sec == "" {
			t.Fatalf("the skill has no %s section", heading)
		}
		var got []string
		for _, m := range bullet.FindAllStringSubmatch(sec, -1) {
			got = append(got, m[1])
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s:\nthe skill %q\nrubric.go %q", heading, got, want)
		}
	}
}
