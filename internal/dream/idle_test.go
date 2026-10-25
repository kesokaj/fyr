package dream

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// recordsProject is a checkout whose trail is records: one bug stored at
// recordAt (so the export is committed), committed at commitAt, with one
// memory record. Its home holds no Antigravity conversation.
func recordsProject(t *testing.T, recordAt, commitAt time.Time) (home, root string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	run(t, root, "git", "init", "-q")
	o := brain.Options{Home: home, Dir: root, Now: func() time.Time { return recordAt }}
	if _, err := brain.Add(o, brain.AddInput{Kind: "bug", Title: "the deploy broke on a Friday", Body: strings.NewReader("Prevention: deploy on Thursdays.")}); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.MemoryAdd(o, brain.MemoryEntry{Type: "project", Name: "deploy-day", Description: "we deploy on Thursdays now"}, strings.NewReader("Thursdays.")); err != nil {
		t.Fatal(err)
	}
	date := commitAt.Format(time.RFC3339)
	for _, args := range [][]string{{"add", ".agents/trail.jsonl"}, {"commit", "-qm", "trail"}} {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@e.invalid"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return home, root
}

// dryRunAll is `fyr dream --all --dry-run` with the real idle check, the
// projects found by their records only, and root's watermark at since.
func dryRunAll(t *testing.T, home, root string, since time.Time) string {
	t.Helper()
	st := state{Projects: map[string]time.Time{root: since}}
	if err := saveState(home, st); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o := Options{Home: home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now },
		Projects: func(string) ([]search.Project, error) { return nil, nil }}
	if err := Run(o); err != nil {
		t.Fatalf("dream: %v\n%s", err, out.String())
	}
	return out.String()
}

// D22: a project in use within the last day is not dreamt over; an idle one
// with something new since its last dream is; an idle one with nothing new
// is not. A migrated project is found by its records, though the file index
// no longer registers it.
func TestIdleRule(t *testing.T) {
	home, root := recordsProject(t, now.Add(-30*time.Hour), now.Add(-48*time.Hour))
	out := dryRunAll(t, home, root, now.Add(-72*time.Hour))
	if !strings.Contains(out, root+": would dream (idle 24h0m0s, changed since") {
		t.Errorf("an idle project with a new record is dreamt over:\n%s", out)
	}
	out = dryRunAll(t, home, root, now.Add(-29*time.Hour))
	if !strings.Contains(out, root+": idle, unchanged since") {
		t.Errorf("an idle project with nothing new is skipped:\n%s", out)
	}

	home, root = recordsProject(t, now.Add(-2*time.Hour), now.Add(-48*time.Hour))
	out = dryRunAll(t, home, root, now.Add(-72*time.Hour))
	if !strings.Contains(out, root+": skip (in use within 24h0m0s: a record updated at") {
		t.Errorf("a record updated within a day keeps the project in use:\n%s", out)
	}

	home, root = recordsProject(t, now.Add(-30*time.Hour), now.Add(-3*time.Hour))
	out = dryRunAll(t, home, root, now.Add(-72*time.Hour))
	if !strings.Contains(out, root+": skip (in use within 24h0m0s: a commit at") {
		t.Errorf("a commit within a day keeps the project in use:\n%s", out)
	}
}

// An Antigravity session within the day keeps a project in use too.
func TestIdleRuleSeesASession(t *testing.T) {
	f := newFixture(t) // its conversations were modified an hour before now
	if why := activity(f.home, project(f), now); !strings.HasPrefix(why, "an Antigravity session at ") {
		t.Errorf("activity = %q", why)
	}
	if why := activity(f.home, project(f), now.Add(48*time.Hour)); why != "" {
		t.Errorf("two days later the project is idle, got %q", why)
	}
}

// A migrated project's memory, trail and feedback come from its records.
func TestCollectReadsRecords(t *testing.T) {
	home, root := recordsProject(t, now.Add(-30*time.Hour), now.Add(-48*time.Hour))
	g := brain.Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return now.Add(-26 * time.Hour) }}
	if _, err := brain.Add(g, brain.AddInput{Kind: "feedback", Title: "use Thursdays", Project: root, Body: strings.NewReader(`{"corrected":"deploy on Thursdays"}`)}); err != nil {
		t.Fatal(err)
	}
	c, err := collect(home, search.Project{Root: root, Name: filepath.Base(root)}, now.Add(-72*time.Hour), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	got := labels(c)
	for _, want := range []string{"memory:MEMORY-deploy-day", "trail:BUG-001", "feedback:FEEDBACK-"} {
		if !strings.Contains(got, want) {
			t.Errorf("inputs lack %q:\n%s", want, got)
		}
	}
	if !c.Changed || !strings.Contains(c.Prompt, "deploy on Thursdays") {
		t.Errorf("changed %v, prompt holds the records' text: %v", c.Changed, strings.Contains(c.Prompt, "Thursdays"))
	}
}
