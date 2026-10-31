package dream

import (
	"bytes"
	"encoding/json"
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
	markRegistered(t, home, root)
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
	home, root := recordsProject(t, old, old)
	agyConversations(t, home, map[string]agyConv{"at-root": {root, fresh, []agyStep{{typ: 14, text: "a line"}}}})
	p := projectAt(root)
	if why := activity(home, p, now); !strings.HasPrefix(why, "an Antigravity session at ") {
		t.Errorf("activity = %q", why)
	}
	if why := activity(home, p, now.Add(48*time.Hour)); why != "" {
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
	c, err := collect(home, search.Project{Root: root, Name: filepath.Base(root)}, now.Add(-72*time.Hour), time.Now(), maxPrompt)
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

// Security review of the dream over records, finding 1: a feedback record's
// body is a JSON line and is redacted as one.
func TestFeedbackRecordsAreRedactedAsJSON(t *testing.T) {
	home, root := recordsProject(t, now.Add(-30*time.Hour), now.Add(-48*time.Hour))
	g := brain.Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return now.Add(-26 * time.Hour) }}
	// json.Marshal writes & as \u0026: the raw needles miss the signature,
	// the decoded line's catch it (the security probe's shape).
	raw, _ := json.Marshal(map[string]string{"ts": "2026-09-29T10:00:00Z", "project": root, "original": "see https://h.example/api?x=1&sig=AbCdEfGhIjKlMnOpQrSt123"})
	line := string(raw)
	if _, err := brain.Add(g, brain.AddInput{Kind: "feedback", Title: "a link", Project: root, Body: strings.NewReader(line)}); err != nil {
		t.Fatal(err)
	}
	c, err := collect(home, search.Project{Root: root, Name: filepath.Base(root)}, now.Add(-72*time.Hour), time.Now(), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(labels(c), "feedback:FEEDBACK-") || strings.Contains(c.Prompt, "AbCdEfGhIjKlMnOpQrSt123") {
		t.Errorf("the signature behind a JSON escape reached the prompt")
	}
}

// Finding 4: a record dated after now never keeps a project in use.
func TestFutureRecordIsNotUse(t *testing.T) {
	home, root := recordsProject(t, now.Add(48*time.Hour), now.Add(-48*time.Hour))
	if why := activity(home, search.Project{Root: root}, now); why != "" {
		t.Errorf("a future-dated record made the project in use: %q", why)
	}
}

// Finding 5: a dry run creates, migrates and loads no brain store.
func TestDryRunLeavesTheBrainStore(t *testing.T) {
	src, root := recordsProject(t, now.Add(-30*time.Hour), now.Add(-48*time.Hour))
	_ = src
	home := t.TempDir() // no brain.db here; the project's trail.jsonl is committed
	st := state{Projects: map[string]time.Time{root: now.Add(-72 * time.Hour)}}
	if err := saveState(home, st); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o := Options{Home: home, Project: root, DryRun: true, Out: &out, Now: func() time.Time { return now }}
	if err := Run(o); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), root+": would dream") {
		t.Errorf("the dry run read the project's records from a copy:\n%s", out.String())
	}
	if _, err := os.Lstat(brain.Path(home)); !os.IsNotExist(err) {
		t.Errorf("the dry run created the brain store: %v", err)
	}
	// With a store: its files are byte-identical after the dry run.
	o.Home = src
	saveState(src, st)
	before, _ := os.ReadFile(brain.Path(src))
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(brain.Path(src))
	if !bytes.Equal(before, after) {
		t.Error("the dry run wrote the live brain store")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(brain.Path(src)), "dream-brain-*")); len(left) != 0 {
		t.Errorf("the copy was left behind: %v", left)
	}
}

// Finding 6: one checkout reached two ways is one target.
func TestTargetsDedupeOneCheckout(t *testing.T) {
	home, root := recordsProject(t, now.Add(-30*time.Hour), now.Add(-48*time.Hour))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	o := Options{Home: home, All: true, Projects: func(string) ([]search.Project, error) { return []search.Project{{Root: link}}, nil },
		Scopes: func(string) ([]string, error) { return []string{root}, nil }}
	got, skips, err := o.targets()
	if err != nil || len(got) != 1 || len(skips) != 0 {
		t.Errorf("one checkout, two paths: %+v, %v", got, err)
	}
}

// Finding 7: a project's proposals are stored together or not at all.
func TestProposalsStoredTogether(t *testing.T) {
	home := t.TempDir()
	ps := []brain.Proposal{{Title: "one", Body: "a"}, {Title: "two", Body: "b"}}
	ids, err := brain.ProposeAll(home, "/p/x", ps, now)
	if err != nil || len(ids) != 2 {
		t.Fatalf("%v %v", ids, err)
	}
	// A body the store refuses (over 1 MiB) fails the whole batch.
	big := strings.Repeat("x", 1<<20+1)
	if _, err := brain.ProposeAll(home, "/p/x", []brain.Proposal{{Title: "three", Body: "c"}, {Title: "four", Body: big}}, now); err == nil {
		t.Fatal("an oversize body was stored")
	}
	recs, _ := brain.List(brain.Options{Home: home, Dir: t.TempDir(), Global: true}, brain.ListFilter{Kind: "proposal"})
	if len(recs) != 2 {
		t.Errorf("a failed batch stored part of itself: %d records", len(recs))
	}
}
