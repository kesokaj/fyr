package fyr_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/check"
)

const minTrack1Docs = 11

// steSentenceRatchetViolations filters check.Report.Notes for Track 1 STE
// sentence-length advisory notes emitted by internal/check.
func steSentenceRatchetViolations(notes []string) []string {
	var out []string
	for _, note := range notes {
		if strings.HasPrefix(note, "docs-style-note:") && strings.Contains(note, "STE sentence-length") {
			out = append(out, note)
		}
	}
	return out
}

// TestTrack1DocsSTESentenceRatchet ratchets fyr's own Track 1 human docs to
// zero STE sentence-length advisory notes while verifying internal/check keeps
// STE sentence length advisory (zero blocking Findings) for consumer repos
// (PLAN-2026-11-27-craft-cleanup, ADR-535, ADR-537).
func TestTrack1DocsSTESentenceRatchet(t *testing.T) {
	requireGitCheckoutForDocs(t)
	if n := countTrack1Docs(t); n < minTrack1Docs {
		t.Fatalf("found %d tracked Track 1 docs, want at least %d", n, minTrack1Docs)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	rep, err := check.Run(wd, nil, t.TempDir())
	if err != nil {
		t.Fatalf("check.Run on checkout: %v", err)
	}
	if violations := steSentenceRatchetViolations(rep.Notes); len(violations) > 0 {
		t.Fatalf("Track 1 STE sentence-length ratchet failed (%d):\n%s", len(violations), strings.Join(violations, "\n"))
	}
	t.Run("catches_long_sentences_while_keeping_consumer_advisory", testTrack1STERatchetBites)
}

func testTrack1STERatchetBites(t *testing.T) {
	longDoc := "# Sample Guide\n\n" +
		"This synthetic documentation paragraph intentionally constructs a very long English prose sentence with more than thirty ordinary words so the deterministic sentence length counter clearly falls below the eighty percent pass threshold during automated verification.\n\n" +
		"Adding a second deliberately verbose prose sentence containing well over thirty plain words guarantees that the overall document pass rate remains zero percent without introducing any banned terms or markdown tables or diagrams anywhere.\n\n" +
		"Keeping this third lengthy prose sentence completely free of frontmatter and destructive shell commands ensures that the repository checker emits only an advisory style note while keeping the blocking findings list completely empty.\n"
	dir := initTempGitRepo(t, map[string]string{"README.md": longDoc})
	rep, err := check.Run(dir, nil, t.TempDir())
	if err != nil {
		t.Fatalf("check.Run on temp repo: %v", err)
	}
	violations := steSentenceRatchetViolations(rep.Notes)
	if len(violations) == 0 {
		t.Fatalf("expected >= 1 STE sentence-length violation for README.md, got 0 (notes=%q)", rep.Notes)
	}
	hasReadmeSTE := slices.ContainsFunc(violations, func(v string) bool {
		return strings.Contains(v, "README.md") && strings.Contains(v, "STE sentence-length")
	})
	if !hasReadmeSTE {
		t.Errorf("expected violation mentioning README.md and STE sentence-length, got %q", violations)
	}
	for _, f := range rep.Findings {
		if strings.Contains(f.Text, "STE sentence-length") || strings.Contains(f.Rule, "STE") {
			t.Errorf("internal/check emitted blocking STE finding (must stay advisory per ADR-535): %+v", f)
		}
	}
	if len(rep.Findings) != 0 {
		t.Errorf("expected 0 findings on clean-except-STE README.md, got %+v", rep.Findings)
	}
	unrelated := []string{
		"no .agents/trail.jsonl: records, specs not checked",
		"docs-style-note: README.md:1: prefer allowlist over whitelist",
	}
	if got := steSentenceRatchetViolations(unrelated); len(got) != 0 {
		t.Errorf("unrelated notes flagged as STE sentence-length violations: %q", got)
	}
}

func requireGitCheckoutForDocs(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout")
	}
}

func countTrack1Docs(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "--", "README.md", "SDD.md", "TDD.md", "Workflow.md", "docs", "features")
	cmd.Env = cleanGitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files Track 1 docs: %v", err)
	}
	n := 0
	for _, raw := range bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0}) {
		p := string(raw)
		if p == "" || !strings.HasSuffix(p, ".md") {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			n++
		}
	}
	return n
}

func initTempGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGitInDir(t, dir, "init")
	runGitInDir(t, dir, "config", "user.email", "test@example.com")
	runGitInDir(t, dir, "config", "user.name", "test")
	var paths []string
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, rel)
	}
	slices.Sort(paths)
	runGitInDir(t, dir, append([]string{"add"}, paths...)...)
	runGitInDir(t, dir, "commit", "-m", "init")
	return dir
}

func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func cleanGitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}
