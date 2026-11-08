package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writer writes a file into a fresh directory and returns its path.
func writer(t *testing.T) (dir string, write func(name, body string) string) {
	dir = t.TempDir()
	return dir, func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
}

// TestParseKickoff is the two entry forms a Files: line takes and what they
// cover (D16, D17); a missing file, a link and the longest name.
func TestParseKickoff(t *testing.T) {
	dir, write := writer(t)
	ok := write("fix-a.md", "Project: x\nFiles: src/a.go, internal/b/, ADR-077, PLAN-2026-10-29-workers\r\nDone.\n")
	k, err := ParseKickoff(ok)
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "fix-a" || k.Branch() != "work/fix-a" || strings.Join(k.Files, "|") != "src/a.go|internal/b/|ADR-077|PLAN-2026-10-29-workers" || !strings.HasPrefix(k.Text, "Project: x\n") {
		t.Errorf("parsed %+v", k)
	}
	for _, c := range []struct{ p, want string }{
		{"src/a.go", "y"}, {"src/a.go.bak", "n"}, {"internal/b/c.go", "y"}, {"internal/bc.go", "n"}, {"internal/b", "n"},
	} {
		if got := k.covers(c.p); got != (c.want == "y") {
			t.Errorf("covers(%s) %v", c.p, got)
		}
	}
	if !k.names("ADR-077") || k.names("ADR-07") {
		t.Error("names: the record entries")
	}
	if _, err := ParseKickoff(filepath.Join(dir, "missing.md")); err == nil {
		t.Error("a missing file was read")
	}
	if err := os.Symlink(ok, filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKickoff(filepath.Join(dir, "link.md")); err == nil {
		t.Error("a link was read as the Kickoff")
	}
	if _, err := ParseKickoff(write(strings.Repeat("a", 63)+".md", "Files: a\n")); err != nil {
		t.Errorf("a 63-character name: %v", err)
	}
}

// TestParseKickoffModel: one Model: line at column 0 is the worker's model,
// trimmed, and stays in the text agy gets; none is ""; a line not at column
// 0 is not one (D113 item 3).
func TestParseKickoffModel(t *testing.T) {
	_, write := writer(t)
	for _, c := range []struct{ body, want string }{
		{"Files: a\nModel:  gemini-3.8-flash-low \r\nDone.\n", "gemini-3.8-flash-low"},
		{"Files: a\nModel: " + strings.Repeat("a", 64) + "\n", strings.Repeat("a", 64)},
		{"Files: a\nDone.\n", ""},
		{"Files: a\n  Model: indented, so not the line\n", ""},
	} {
		k, err := ParseKickoff(write("m.md", c.body))
		if err != nil || k.Model != c.want || k.Text != c.body {
			t.Errorf("%q: model %q, %v; want %q", c.body, k.Model, err, c.want)
		}
	}
}

// TestParseKickoffRefusals is refusal 3 and refusal 4's name rule (D40).
func TestParseKickoffRefusals(t *testing.T) {
	_, write := writer(t)
	for _, c := range []struct{ name, body, want string }{
		{"none.md", "Project: x\n", "no Files: line"},
		{"two.md", "Files: a\nFiles: b\n", "2 Files: lines"},
		{"empty.md", "Files: \n", "an empty Files: entry"},
		{"gap.md", "Files: a, , b\n", "an empty Files: entry"},
		{"abs.md", "Files: /etc/passwd\n", "is absolute"},
		{"up.md", "Files: src/../../x\n", "contains .."},
		{"upend.md", "Files: ../\n", "contains .."},
		{"glob.md", "Files: src/*.go\n", "is a glob"},
		{"dot.md", "Files: ./src/a.go\n", "not a clean"},
		{"double.md", "Files: src//a.go\n", "not a clean"},
		{"trail.md", "Files: .agents/trail.jsonl\n", "by ID"},
		{"ctl.md", "Files: a\x1bb\n", "control character"},
		{"Upper.md", "Files: a\n", "the name"},
		{"a.b.md", "Files: a\n", "the name"},
		{"-x.md", "Files: a\n", "the name"},
		{strings.Repeat("a", 64) + ".md", "Files: a\n", "the name"},
		{"big.md", "Files: a\n" + strings.Repeat("x", maxKickoff), "over 64 KiB"},
		{"two-models.md", "Files: a\nModel: m-a\nModel: m-b\n", "2 Model: lines; give one"},
		{"empty-model.md", "Files: a\nModel: \n", `the Model: line "" is not a model ID`},
		{"flag-model.md", "Files: a\nModel: -x\n", `the Model: line "-x" is not a model ID`},
		{"space-model.md", "Files: a\nModel: m a\n", `the Model: line "m a" is not a model ID`},
		{"ctl-model.md", "Files: a\nModel: m\x1ba\n", "is not a model ID"},
		{"long-model.md", "Files: a\nModel: " + strings.Repeat("a", 65) + "\n", "is not a model ID"},
	} {
		if _, err := ParseKickoff(write(c.name, c.body)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestFormatKickoffCapsuleAndPreseed(t *testing.T) {
	root := t.TempDir()
	k := Kickoff{
		Name:  "phase-4-worker",
		Text:  "Project: x\nFiles: ADR-511, internal/search/hook.go, internal/work/\nDone.\n",
		Files: []string{"ADR-511", "internal/search/hook.go", "internal/work/"},
	}
	if got := k.SliceQuery(); got != "internal/search/hook.go internal/work" {
		t.Fatalf("SliceQuery() = %q", got)
	}
	// Without .agents/scratch/search.db, FormatKickoffCapsule leaves k.Text unchanged.
	unchanged := FormatKickoffCapsule(k, root, func(dir, query string, budget int) (string, error) {
		t.Fatal("sliceFn should not be called when search.db is absent")
		return "", nil
	}, nil)
	if unchanged != k.Text {
		t.Fatalf("expected unchanged kickoff text when search.db is absent, got:\n%s", unchanged)
	}

	// Create .agents/scratch/search.db so FormatKickoffCapsule activates.
	scratchDir := filepath.Join(root, ".agents", "scratch")
	if err := os.MkdirAll(scratchDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchDir, "search.db"), []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}

	sampleCapsule := strings.Join([]string{
		"[Hop 0] internal/search/hook.go:10-45 (func RunHook)",
		"func RunHook(o Options) error {",
		"\treturn nil",
		"}",
		"[Hop 1] internal/work/kickoff.go:48-77 (func ParseKickoff)",
		"func ParseKickoff(path string) (Kickoff, error) {",
		"\treturn parseKickoff(path, false)",
		"}",
		"[Hop 1] internal/work/work.go:110-159 (func Run) snippet lines 120-125",
		"120: ps, err := preflight(ctx, &o, files)",
		"[Hop 2] internal/auto/kickoff.go:146-185: func BuildKickoff(now time.Time, t Task) (Kickoff, error)",
	}, "\n")

	var seenQuery string
	var seenBudget int
	var seenPivots []CapsulePivot
	formatted := FormatKickoffCapsule(k, root, func(dir, query string, budget int) (string, error) {
		seenQuery = query
		seenBudget = budget
		return sampleCapsule, nil
	}, func(dir string, pivots []CapsulePivot) error {
		seenPivots = pivots
		return nil
	})
	if seenQuery != "internal/search/hook.go internal/work" || seenBudget != KickoffCapsuleBudget {
		t.Fatalf("seenQuery=%q seenBudget=%d", seenQuery, seenBudget)
	}
	if !strings.Contains(formatted, "## Structural Context Capsule\n") || !strings.Contains(formatted, "[Hop 0] internal/search/hook.go:10-45") {
		t.Fatalf("formatted kickoff missing capsule:\n%s", formatted)
	}
	if len(seenPivots) != 2 || seenPivots[0].Path != "internal/search/hook.go" || seenPivots[0].StartLine != 10 || seenPivots[0].EndLine != 45 || seenPivots[1].Path != "internal/work/kickoff.go" {
		t.Fatalf("unexpected pre-seeded pivots: %+v", seenPivots)
	}

	// Verify <= 1,500 token cap (6,000 chars) on oversized capsule output and
	// that a mid-symbol truncated trailing Hop block is not pre-seeded.
	var truncatedPivots []CapsulePivot
	oversized := FormatKickoffCapsule(k, root, func(dir, query string, budget int) (string, error) {
		return "[Hop 0] internal/search/hook.go:10-45 (func RunHook)\nfunc RunHook() {}\n" +
			"[Hop 1] internal/work/kickoff.go:48-150 (func ParseKickoff)\n" +
			strings.Repeat("a", KickoffCapsuleBudget*4+2000), nil
	}, func(dir string, pivots []CapsulePivot) error {
		truncatedPivots = pivots
		return nil
	})
	idx := strings.Index(oversized, "## Structural Context Capsule\n")
	if idx < 0 {
		t.Fatal("missing capsule header on oversized output")
	}
	capsulePart := oversized[idx+len("## Structural Context Capsule\n"):]
	if len(capsulePart)/4 > KickoffCapsuleBudget {
		t.Fatalf("capsule tokens %d exceed budget %d", len(capsulePart)/4, KickoffCapsuleBudget)
	}
	if len(truncatedPivots) != 1 || truncatedPivots[0].Path != "internal/search/hook.go" {
		t.Fatalf("expected truncated trailing pivot to be dropped, got %+v", truncatedPivots)
	}

	// Verify ParseKickoff attaches capsule automatically when DefaultSliceFunc is registered.
	prevSlice, prevPreseed := DefaultSliceFunc, DefaultPreseedFunc
	t.Cleanup(func() {
		DefaultSliceFunc = prevSlice
		DefaultPreseedFunc = prevPreseed
	})
	DefaultSliceFunc = func(dir, query string, budget int) (string, error) {
		return sampleCapsule, nil
	}
	var parsedPivots []CapsulePivot
	DefaultPreseedFunc = func(dir string, pivots []CapsulePivot) error {
		parsedPivots = pivots
		return nil
	}
	kFile := filepath.Join(root, "worker-a.md")
	if err := os.WriteFile(kFile, []byte("Project: "+root+".\nFiles: internal/search/hook.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pk, err := ParseKickoff(kFile)
	if err != nil {
		t.Fatalf("ParseKickoff: %v", err)
	}
	if !strings.Contains(pk.Text, "## Structural Context Capsule\n") || len(parsedPivots) != 2 {
		t.Fatalf("ParseKickoff did not attach capsule or pre-seed pivots: pivots=%+v text=%s", parsedPivots, pk.Text)
	}
}
