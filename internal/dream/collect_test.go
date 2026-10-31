package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestCollectInputs(t *testing.T) {
	f := newFixture(t)
	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
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
	c, err := collect(f.home, project(f), later, time.Now(), maxPrompt)
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
	c, err := collect(f.home, project(f), later, time.Now(), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if c.Changed {
		t.Error("a skill update alone made the project changed")
	}
}

func TestCollectRedactsAndHidesHome(t *testing.T) {
	f := newFixture(t)
	f.addTrail(t, brain.Options{Home: f.home, Dir: f.root, Now: func() time.Time { return fresh }}, "review", "a leak", "token = FAKEVALUE\nstored under "+f.home+"/x\n")
	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
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

// Over the cap the newest news goes first, whatever its kind: the oldest
// conversation stays, the newer one and the trail row of the same time are
// dropped and named, and Through stops just before them (ADR-069).
func TestCollectCapDropsNewestNewsFirst(t *testing.T) {
	f := newFixture(t)
	full, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	small := len(full.Prompt) - 40
	c, err := collect(f.home, project(f), since, time.Now(), small)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Prompt, "please remember the deploy day") || strings.Contains(c.Prompt, "noted from a subdirectory") {
		t.Errorf("the newer conversation should go first and the older stay:\n%s", labels(c))
	}
	if !strings.Contains(strings.Join(c.Dropped, " "), "transcript antigravity under (dropped to fit") {
		t.Errorf("dropped = %v, want the newer conversation named", c.Dropped)
	}
	if !c.Through.Equal(fresh.Add(-time.Nanosecond)) {
		t.Errorf("Through = %v, want just before %v", c.Through, fresh)
	}
	// Room for less than memory alone: the project fails.
	if _, err := collect(f.home, project(f), since, time.Now(), 10); err == nil || !strings.Contains(err.Error(), "memory over cap") {
		t.Errorf("err = %v, want memory over cap", err)
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
	write(t, filepath.Join(elsewhere, "trail.jsonl"), "outside the repository\n", fresh)
	os.RemoveAll(filepath.Join(f.root, ".agents"))
	if err := os.Symlink(elsewhere, filepath.Join(f.root, ".agents")); err != nil {
		t.Fatal(err)
	}
	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v, want the brain store's refusal", err)
	}
	if strings.Contains(c.Prompt, "outside the repository") {
		t.Error("a linked .agents was followed")
	}
}

// A secret hidden behind a JSON escape in a feedback record is redacted on
// the decoded line (v2.0.0 gate, security code slice).
func TestFeedbackRedactsEscapedSecret(t *testing.T) {
	f := newFixture(t)
	// json.Marshal writes & as \u0026, so the raw line reads "\u0026sig=…" and the
	// query-string needle matches only the decoded line (the gate's measurement).
	f.addFeedbackBody(t, fresh, f.root, fmt.Sprintf(`{"ts":%q,"project":%q,"original":"see https://h.example/p?x=1\u0026sig=0123456789abcdef0123456789abcdef"}`, fresh.Format(time.RFC3339), f.root))
	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
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

// A secret-shaped label (here a record's ID) is redacted with the
// texts before the prompt leaves fyr (SD31 of the 2026-10-27 audit).
func TestCollectRedactsLabels(t *testing.T) {
	f := newFixture(t)
	token := "ghp_" + strings.Repeat("A1b2C3d4E5", 4)[:36]
	saved := forDream
	t.Cleanup(func() { forDream = saved })
	forDream = func(string, string, time.Time, time.Time) (brain.DreamInput, error) {
		return brain.DreamInput{Trail: []brain.Record{{ID: "CHANGE-" + token, Title: "a change", Body: "entry", Updated: fresh.Format(time.RFC3339)}}}, nil
	}
	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, token) {
		t.Error("the prompt carries a secret-shaped label")
	}
	for _, in := range c.Inputs {
		if strings.Contains(in.Label, token) {
			t.Errorf("label %q is not redacted", in.Label)
		}
	}
}

// The brain read takes the run's clock, not the wall's (SD32 of the
// 2026-10-27 audit).
func TestCollectUsesTheRunsClock(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".agents", "trail.jsonl"), "", old)
	var got time.Time
	saved := forDream
	t.Cleanup(func() { forDream = saved })
	forDream = func(home, root string, since, now time.Time) (brain.DreamInput, error) {
		got = now
		return brain.DreamInput{}, nil
	}
	// The metrics summary takes the same clock (the 2026-10-27 re-audit,
	// SD59a: it still read the wall's).
	var gotMetrics time.Time
	savedM := metricsSummary
	t.Cleanup(func() { metricsSummary = savedM })
	metricsSummary = func(home, root string, since, now time.Time) (string, error) {
		gotMetrics = now
		return "", nil
	}
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := collect(t.TempDir(), search.Project{Root: root, Name: "p"}, since, clock, maxPrompt); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(clock) {
		t.Errorf("forDream got now %v, want the run's %v", got, clock)
	}
	if !gotMetrics.Equal(clock) {
		t.Errorf("metricsSummary got now %v, want the run's %v", gotMetrics, clock)
	}
}
