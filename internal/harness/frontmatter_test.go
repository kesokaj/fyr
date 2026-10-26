package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	front := "name: x\ndescription: |\n  line one\n  line two\nquoted: \"a: b\"\nmetadata:\n  version: \"1.2\"\n  author: me\ntools: [Read, Grep]\nskills:\n  - alpha\n  - beta\nplain: first\n  second\nempty:\n"
	fm := Parse(front)
	want := map[string]string{
		"name":        "x",
		"description": "line one line two",
		"quoted":      "a: b",
		"tools":       "[Read, Grep]",
		"skills":      "[alpha, beta]",
		"plain":       "first second",
		"empty":       "",
	}
	for k, v := range want {
		if fm[k] != v {
			t.Errorf("%s = %q, want %q", k, fm[k], v)
		}
	}
	if _, ok := fm["metadata"]; ok {
		t.Errorf("nested map should be skipped, got %q", fm["metadata"])
	}
	if got := splitList("[Read, Grep]"); strings.Join(got, ",") != "Read,Grep" {
		t.Errorf("splitList brackets = %v", got)
	}
	if got := splitList("Read Grep"); strings.Join(got, ",") != "Read,Grep" {
		t.Errorf("splitList spaces = %v", got)
	}
}

func TestLintRejectsUnquotedComment(t *testing.T) {
	// A plain scalar with " #" is a YAML comment start: strict parsers
	// truncate it or, inside a flow sequence, fail the whole file. The
	// Antigravity CLI dropped the `bug` and `feature` skills that way.
	bad := []string{
		"name: x\nargument-hint: [feature description or #issue]\n",
		"name: x\ndescription: fixes #1 and #2\n",
		"name: x\ndescription: #comment only\n",
		"name: x\ndescription: first line\n  wraps with #issue\n",
		"name: x\ndescription: value\t#tab-comment\n",
		"name: x\ndescription: \"quoted\" # trailing\n",
	}
	for _, front := range bad {
		if _, err := parseStrict(front); err == nil {
			t.Errorf("Lint accepted %q", front)
		} else if !strings.Contains(err.Error(), "#") {
			t.Errorf("error should name the marker: %v", err)
		}
	}
	good := []string{
		"name: x\nargument-hint: \"[feature description or #issue]\"\n",
		"name: x\nargument-hint: '[#issue]'\n",
		"name: x\ndescription: |\n  a #b\n  c #d\nmetadata:\n  tag: \"#1\"\n",
		"name: x\ntools: [Read, Grep]\ncolor: \"#ff0000\"\n",
	}
	for _, front := range good {
		if _, err := parseStrict(front); err != nil {
			t.Errorf("Lint rejected %q: %v", front, err)
		}
	}
}

func TestLoadSkillsRejectsUnquotedComment(t *testing.T) {
	repo := newRepo(t)
	p := filepath.Join(repo, "skills", "gamma", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\nname: gamma\ndescription: Gamma\nargument-hint: [topic or #issue]\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadSkills(DirSource(repo))
	if err == nil || !strings.Contains(err.Error(), "gamma") || !strings.Contains(err.Error(), "argument-hint") {
		t.Fatalf("loadSkills should name the skill and the key, got %v", err)
	}
}

func TestSplitRejectsMissingFrontmatter(t *testing.T) {
	if _, _, err := Split("# no frontmatter\n"); err == nil {
		t.Fatal("expected an error")
	}
	if _, _, err := Split("---\nname: x\n"); err == nil {
		t.Fatal("expected an error for an unterminated block")
	}
}

// TestLoadSkillsRefusesDuplicateNames: two directories whose frontmatter
// claims one name install to a single destination, so the walk order alone
// would decide which text every machine reads. That is what an interrupted
// `adopt` used to be able to leave behind (its staging now lives outside
// skills/), and what a hand-copied directory can still produce.
func TestLoadSkillsRefusesDuplicateNames(t *testing.T) {
	repo := newRepo(t)
	dup := filepath.Join(repo, "skills", "alpha-copy")
	if err := os.MkdirAll(dup, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: alpha\ndescription: a second directory claiming the same name\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dup, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadSkills(DirSource(repo))
	if err == nil {
		t.Fatal("two skills named alpha must be refused")
	}
	for _, want := range []string{"alpha", "alpha-copy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %s, got %v", want, err)
		}
	}
}

// TestSplitFenceIsOneLine: a fence is `---` and at most one carriage return.
// A line of `---` and two carriage returns once closed the block, so a key
// written after it (and before a real closing line a lenient reader would
// take) was never checked (security read of a62eb9d..768efce).
func TestSplitFenceIsOneLine(t *testing.T) {
	if _, _, err := Split("---\r\nname: x\r\n---\r\nbody\r\n"); err != nil {
		t.Errorf("a CRLF file is not read: %v", err)
	}
	front, _, err := Split("---\nname: x\n---\r\r\nallowed-tools: Bash\n---\nbody\n")
	if err == nil && !strings.Contains(front, "allowed-tools") {
		t.Errorf("the block closed at a fence with two carriage returns; front = %q", front)
	}
	if _, _, err := Split("---\r\r\nname: x\n---\nbody\n"); err == nil {
		t.Error("an opening fence with two carriage returns was accepted")
	}
	repo := newRepo(t)
	dir := filepath.Join(repo, "skills", "x")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n---\r\r\nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := loadSkills(DirSource(repo)); err == nil {
		t.Error("a skill with a permission key behind a two-carriage-return fence loaded")
	} else if !strings.Contains(err.Error(), "line break") || strings.Contains(err.Error(), "quoted or spaced key") {
		// The refusal names the real problem (a line break that is not a
		// newline), not a key that cannot be read.
		t.Errorf("the refusal blames the wrong thing: %v", err)
	}
	// A `---` with a trailing space is not a fence to Split either; inside
	// the block it is refused as a fence that is not one.
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n--- \nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := loadSkills(DirSource(repo)); err == nil || !strings.Contains(err.Error(), "not a fence") {
		t.Errorf("a skill with a spaced fence inside its block: want the not-a-fence refusal, got %v", err)
	}
}
