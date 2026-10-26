package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestCollectInputs(t *testing.T) {
	f := newFixture(t)
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Changed {
		t.Fatal("changed = false with a fresh bug row, feedback and transcript")
	}
	text := c.Prompt
	for _, want := range []string{
		"We deploy on Fridays.",           // memory, sent whole
		"fresh bug row",                   // trail after the watermark
		"fresh feedback for this project", // feedback for this root
		"Go expert",                       // installed skill
		"please remember the deploy day",  // transcript, at the root
		"noted from a subdirectory",       // transcript, under the root
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt lacks %q\ninputs:\n%s", want, labels(c))
		}
	}
	for _, not := range []string{"an old change entry", "old feedback", "another project", "an old line before", "TOOL OUTPUT"} {
		if strings.Contains(text, not) {
			t.Errorf("prompt carries %q", not)
		}
	}
}

func TestCollectUnchanged(t *testing.T) {
	f := newFixture(t)
	later := now.Add(time.Hour)
	c, err := collect(f.home, project(f), later, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if c.Changed {
		t.Errorf("changed = true with nothing after the watermark:\n%s", labels(c))
	}
}

// Input 4 is global: a skill updated after the watermark never makes a
// project changed on its own (D-row in the plan's Interfaces).
func TestCollectSkillAloneIsNoChange(t *testing.T) {
	f := newFixture(t)
	later := now.Add(time.Hour)
	write(t, filepath.Join(f.home, harness.AgyRoot, "skills", "golang", "SKILL.md"), "---\nname: golang\ndescription: \"Go expert, updated\"\n---\n", now.Add(2*time.Hour))
	c, err := collect(f.home, project(f), later, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if c.Changed {
		t.Error("a skill update alone made the project changed")
	}
}

func TestCollectRedactsAndHidesHome(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, ".agents", "memory", "leak.md"), "token = FAKEVALUE\nstored under "+f.home+"/x\n", fresh)
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "FAKEVALUE") {
		t.Error("a secret reached the prompt")
	}
	if strings.Contains(c.Prompt, f.home) || !strings.Contains(c.Prompt, "~/x") {
		t.Error("the home path was not rewritten to ~")
	}
}

func TestCollectCapDropsTranscriptThenTrail(t *testing.T) {
	f := newFixture(t)
	full, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	// Room for everything but the transcript.
	small := len(full.Prompt) - 40
	c, err := collect(f.home, project(f), since, small)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "please remember the deploy day") || !strings.Contains(c.Prompt, "fresh bug row") {
		t.Errorf("the transcript should go first and the trail stay:\n%s", labels(c))
	}
	if len(c.Dropped) == 0 || !strings.Contains(strings.Join(c.Dropped, " "), "transcript") {
		t.Errorf("dropped = %v, want the transcript named", c.Dropped)
	}
	// Room for less than memory alone: the project fails.
	if _, err := collect(f.home, project(f), since, 10); err == nil || !strings.Contains(err.Error(), "memory over cap") {
		t.Errorf("err = %v, want memory over cap", err)
	}
}

// TestCollectDoesNotFollowLinkedTrail: a committed symlink under the docs
// root is named and not read (security review 2026-09-30).
func TestCollectDoesNotFollowLinkedTrail(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "private.md"), "private notes elsewhere\n", fresh)
	if err := os.Symlink(elsewhere, filepath.Join(f.root, ".agents", "docs", "reviews")); err != nil {
		t.Fatal(err)
	}
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "private notes elsewhere") {
		t.Error("a linked record directory was read")
	}
	if !strings.Contains(strings.Join(c.Dropped, " "), "reviews: a symlink") {
		t.Errorf("dropped = %v", c.Dropped)
	}
}

func TestCommand(t *testing.T) {
	agy := strings.Join(command(), " ")
	if !strings.HasPrefix(agy, "agy --input-format stream-json --output-format stream-json --mode plan -p") {
		t.Errorf("command: %s", agy)
	}
}

// TestCollectDoesNotFollowALinkedAgentsDir: .agents itself as a symlink.
func TestCollectDoesNotFollowALinkedAgentsDir(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "docs", "changes", "x.md"), "outside the repository\n", fresh)
	os.RemoveAll(filepath.Join(f.root, ".agents"))
	if err := os.Symlink(elsewhere, filepath.Join(f.root, ".agents")); err != nil {
		t.Fatal(err)
	}
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "outside the repository") {
		t.Error("a linked .agents was followed")
	}
}

// A secret hidden behind a JSON escape in feedback.jsonl is redacted on the
// decoded line (v2.0.0 gate, security code slice).
func TestFeedbackRedactsEscapedSecret(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.home, harness.StateDir, "feedback.jsonl")
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// json.Marshal writes & as \u0026, so the raw line reads "\u0026sig=…" and the
	// query-string needle matches only the decoded line (the gate's measurement).
	fmt.Fprintf(fh, `{"ts":%q,"project":%q,"original":"see https://h.example/p?x=1\u0026sig=0123456789abcdef0123456789abcdef"}`+"\n", fresh.Format(time.RFC3339), f.root)
	fh.Close()
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "0123456789abcdef0123456789abcdef") {
		t.Fatalf("the escaped key reached the prompt:\n%s", labels(c))
	}
	if !strings.Contains(c.Prompt, "fresh feedback for this project") {
		t.Error("the clean feedback line was lost")
	}
}

// A skill whose description is a block scalar (`description: >`) reached the
// dreamer as the literal `>` while dream read the key line itself; it reads
// through the installer's frontmatter parser now (project review 2026-10-09,
// row 16).
func TestSkillsReadsBlockScalarDescription(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, harness.AgyRoot, "skills", "folded")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: folded\ndescription: >\n  one line\n  and two\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := skills(home)
	if len(got) != 1 || got[0].name != "folded" {
		t.Fatalf("skills = %+v, want the one folded skill", got)
	}
	if d := got[0].description; strings.Contains(d, ">") || !strings.Contains(d, "one line") || !strings.Contains(d, "and two") {
		t.Errorf("description = %q, want the folded text", d)
	}
}
