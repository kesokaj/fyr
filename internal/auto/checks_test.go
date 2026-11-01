package auto

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
)

func rules(fs []Finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Check+"/"+f.Rule+" "+f.Where)
	}
	return strings.Join(out, "\n")
}

func TestChecksTrail(t *testing.T) {
	recs := []brain.Record{
		{Kind: "bug", ID: "BUG-1", Status: "fixed", Body: "## Prevention rule\nx"},
		{Kind: "bug", ID: "BUG-2", Status: "open", Body: "no rule, see BUG-1 and BUG-1"},
		{Kind: "bug", ID: "BUG-3", Status: "open", Body: "Prevention. BUG-1. BUG-10."},
		{Kind: "bug", ID: "BUG-4", Status: "graduated", Body: "prevention. BUG-1"},
		{Kind: "bug", ID: "BUG-10", Status: "fixed", Body: "prevention. BUG-10 BUG-10 BUG-10"},
		{Kind: "plan", ID: "PLAN-1", Body: "BUG-1"},
	}
	got := rules(trailFindings(recs))
	want := "trail/bug-repeats BUG-1\ntrail/bug-no-prevention BUG-2"
	if got != want {
		t.Errorf("%s", got)
	}
}

func TestChecksTrailTwoCitesAreNoRepeat(t *testing.T) {
	recs := []brain.Record{
		{Kind: "bug", ID: "BUG-1", Status: "fixed", Body: "prevention"},
		{Kind: "bug", ID: "BUG-2", Status: "open", Body: "prevention BUG-1"},
		{Kind: "bug", ID: "BUG-3", Status: "open", Body: "prevention BUG-1"},
	}
	if got := trailFindings(recs); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestChecksResume(t *testing.T) {
	root := idleProject(t) // HEAD is eight hours before runAt
	old := runAt.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	head := runAt.Add(-8 * time.Hour)
	cases := []struct {
		name string
		recs []brain.Record
		todo string
		want string
	}{
		{name: "nothing"},
		{name: "a handoff behind", recs: []brain.Record{{Kind: "handoff", ID: "H1", Updated: head.Add(-2 * time.Hour).Format(time.RFC3339)}}, want: "resume/handoff-behind H1"},
		{name: "a handoff the commit carried", recs: []brain.Record{{Kind: "handoff", ID: "H1", Updated: head.Add(-time.Minute).Format(time.RFC3339)}}},
		{name: "the newest handoff counts", recs: []brain.Record{{Kind: "handoff", ID: "H1", Updated: old}, {Kind: "handoff", ID: "H2", Updated: head.Format(time.RFC3339)}}},
		{name: "a stale plan", recs: []brain.Record{{Kind: "plan", ID: "P1", Status: "open", Updated: old, Body: "- [ ] x"}}, want: "resume/plan-stale P1"},
		{name: "a stale plan, all ticked", recs: []brain.Record{{Kind: "plan", ID: "P1", Status: "open", Updated: old, Body: "- [x] x"}}},
		{name: "a closed plan", recs: []brain.Record{{Kind: "plan", ID: "P1", Status: "closed", Updated: old, Body: "- [ ] x"}}},
		{name: "a fresh plan", recs: []brain.Record{{Kind: "plan", ID: "P1", Status: "open", Updated: runAt.Format(time.RFC3339), Body: "- [ ] x"}}},
		{name: "Now with no plan", todo: "# TODO\n\n## Now\n\nwork\n\n## Next\n", want: "resume/todo-now TODO.md"},
		{name: "Now with a plan", recs: []brain.Record{{Kind: "plan", ID: "P1", Status: "open", Updated: runAt.Format(time.RFC3339)}}, todo: "## Now\n\nwork\n"},
		{name: "Now says none", todo: "<!-- a note -->\n## Now\n\nNone.\n\n## Next\n\nmore\n"},
		{name: "Now empty", todo: "## Now\n\n## Backlog\n\nmore\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Remove(filepath.Join(root, "TODO.md"))
			if c.todo != "" {
				writeTree(t, root, "TODO.md", c.todo)
			}
			got, err := resumeFindings(root, c.recs, runAt)
			if err != nil || rules(got) != c.want {
				t.Errorf("%q, %v", rules(got), err)
			}
		})
	}
}

func TestChecksKeptBranches(t *testing.T) {
	root := idleProject(t)
	git(t, root, "branch", "work/auto-doc-1")
	git(t, root, "branch", "work/other")
	got, err := keptBranches(root, runAt.Add(15*24*time.Hour))
	if err != nil || rules(got) != "resume/branch-kept work/auto-doc-1" {
		t.Errorf("%q, %v", rules(got), err)
	}
	if got, _ := keptBranches(root, runAt); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

// reportRun is one real run over root whose health finds a bug with no rule.
func reportRun(t *testing.T, home string, e *fakeEnv) string {
	t.Helper()
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = e
	if err := Run(o, false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String()
}

func reports(t *testing.T, home string) []brain.Record {
	t.Helper()
	o := brain.Options{Home: home, Global: true}
	recs, err := brain.List(o, brain.ListFilter{Kind: "review"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range recs {
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	full, _, err := brain.Get(o, ids)
	if err != nil {
		t.Fatal(err)
	}
	return full
}

func trailLine(t *testing.T, kind, id, status, body string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"id": id, "kind": kind, "status": status, "date": "2026-10-01", "title": id, "body": body, "updated": "2026-10-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func TestReportWrittenOnlyWhenFound(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"enabled": true, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`)
	root := idleProject(t)
	e := &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	out := reportRun(t, home, e)
	if !strings.Contains(out, "health: no findings") || !strings.Contains(out, "machine: health: no findings") {
		t.Errorf("%s", out)
	}
	if got := reports(t, home); len(got) != 0 {
		t.Errorf("%v", got)
	}
	if st, _, _ := loadState(home); !strings.Contains(st.Budget, "P 90% of 5h") || st.Projects[root].LastClass != "health" {
		t.Errorf("%+v", st)
	}
	if e.usageCalls != 1 {
		t.Errorf("%d /usage calls", e.usageCalls)
	}
}

func TestReportNamesTheFindingOnce(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"enabled": true, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`)
	root := idleProject(t)
	writeTree(t, root, ".agents/trail.jsonl", trailLine(t, "bug", "BUG-001", "open", "no rule"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "a bug")
	git(t, root, "push", "-q")
	backdate(t, root)
	e := &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}

	out := reportRun(t, home, e)
	got := reports(t, home)
	if len(got) != 1 || !strings.Contains(got[0].Body, "trail/bug-no-prevention  BUG-001") || got[0].Project == "" {
		t.Fatalf("%+v\n%s", got, out)
	}
	if gotRoot, _ := filepath.EvalSymlinks(root); !strings.HasSuffix(got[0].Project, filepath.Base(gotRoot)) {
		t.Errorf("%q", got[0].Project)
	}
	if !strings.Contains(out, "report "+got[0].ID) {
		t.Errorf("%s", out)
	}

	out = reportRun(t, home, e)
	if len(reports(t, home)) != 1 || !strings.Contains(out, "unchanged since "+got[0].ID) {
		t.Errorf("a second report for the same findings: %s", out)
	}

	// A changed set is a new report.
	writeTree(t, root, ".agents/trail.jsonl", trailLine(t, "bug", "BUG-001", "open", "no rule")+trailLine(t, "bug", "BUG-002", "open", "no rule"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "another")
	git(t, root, "push", "-q")
	backdate(t, root)
	reportRun(t, home, e)
	if got := reports(t, home); len(got) != 2 {
		t.Errorf("%d reports", len(got))
	}
}

// A report is a global record: the project's tree, trail and HEAD stay as they
// were, and the report is found in the machine's store.
func TestReportStaysOutOfTheProject(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"enabled": true, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`)
	root := idleProject(t)
	writeTree(t, root, ".agents/trail.jsonl", trailLine(t, "bug", "BUG-001", "open", "no rule"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "a bug")
	git(t, root, "push", "-q")
	backdate(t, root)
	head := git(t, root, "rev-parse", "HEAD")
	before := snapshot(t, root)
	reportRun(t, home, &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")})
	if len(reports(t, home)) != 1 {
		t.Fatal("no report")
	}
	// Before any git of the test's own: this compares .git too.
	after := snapshot(t, root)
	if len(after) != len(before) {
		t.Errorf("%d paths to %d", len(before), len(after))
	}
	for p, v := range before {
		if after[p] != v {
			t.Errorf("%s changed", p)
		}
	}
	if git(t, root, "rev-parse", "HEAD") != head {
		t.Error("HEAD moved")
	}
}

func TestReportLevelReadsTheBudget(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	e := &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("10%", "90%")}
	out := reportRun(t, home, e)
	if !strings.Contains(out, "G9 budget: P 90% of 5h, 10% of week remaining; floors 50/30; no model task is built yet") {
		t.Errorf("%s", out)
	}
	if e.usageCalls != 1 {
		t.Errorf("%d /usage calls", e.usageCalls)
	}
}

func TestReportNoUsageBeforeAProjectPasses(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	root := idleProject(t)
	writeTree(t, root, "main.go", "package changed\n")
	backdate(t, root)
	e := &fakeEnv{targets: targetsOf(root)}
	if out := reportRun(t, home, e); !strings.Contains(out, "G8 clean: the tree is dirty") || e.usageCalls != 0 {
		t.Errorf("%d calls: %s", e.usageCalls, out)
	}
}

func TestReportUnreadableQuotaIsAFinding(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"enabled": true, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`)
	e := &fakeEnv{targets: targetsOf(idleProject(t)), usageErr: errFake}
	reportRun(t, home, e)
	got := reports(t, home)
	if len(got) != 1 || !strings.Contains(got[0].Body, "quota/unreadable") || got[0].Project != "" {
		t.Errorf("%+v", got)
	}
}

func TestReportLongListSaysWhatIsLeftOut(t *testing.T) {
	fs := make([]Finding, reportLimit+3)
	body := reportBody("p", "observe", fs)
	if strings.Count(body, "\n- ") != reportLimit || !strings.Contains(body, "3 more findings are not listed") {
		t.Errorf("%d lines", strings.Count(body, "\n- "))
	}
}

func TestChecksFailedCheckExitsOne(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"enabled": true, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`)
	root := idleProject(t)
	writeTree(t, root, ".agents/trail.jsonl", "not json\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "broken trail")
	git(t, root, "push", "-q")
	backdate(t, root)
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	if exit(Run(o, false)) != 1 {
		t.Errorf("%s", out.String())
	}
	if got := reports(t, home); len(got) != 1 || !strings.Contains(got[0].Body, "/failed") {
		t.Errorf("%+v", got)
	}
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		v := info.Mode().String() + info.ModTime().String()
		if d.Type().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			v += string(data)
		}
		out[p] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAutoDryRunWritesNothingPastTheGates(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	root := idleProject(t)
	writeTree(t, root, ".agents/trail.jsonl", trailLine(t, "bug", "BUG-001", "open", "no rule"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "a bug")
	git(t, root, "push", "-q")
	backdate(t, root)
	e := &fakeEnv{targets: targetsOf(root, "/skipped"), usage: usageText("90%", "90%")}
	e.targets[1] = dream.Target{Root: "/skipped", Skip: "gone"}
	before, tree := snapshot(t, home), snapshot(t, root)
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = e
	if err := Run(o, true); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	after, treeAfter := snapshot(t, home), snapshot(t, root)
	if len(before) != len(after) || len(tree) != len(treeAfter) {
		t.Errorf("files: %d to %d, %d to %d", len(before), len(after), len(tree), len(treeAfter))
	}
	for p, v := range before {
		if after[p] != v {
			t.Errorf("%s changed", p)
		}
	}
	for _, want := range []string{"G2 lock: not taken in a dry run", root + ": would run health", "/skipped: G6 registered: gone", "budget: not read in a dry run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in %s", want, out.String())
		}
	}
	if e.usageCalls != 0 {
		t.Errorf("%d /usage calls", e.usageCalls)
	}
}

func TestAutoDryRunStopsWhereTheRunWould(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = &fakeEnv{agy: func() (bool, error) { return true, nil }}
	if err := Run(o, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "G5 agy session: an interactive agy is running") {
		t.Errorf("%s", out.String())
	}
}
