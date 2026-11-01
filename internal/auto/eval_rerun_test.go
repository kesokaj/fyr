package auto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

const testHarnessSuite = "# GUIDE-eval\n\n" +
	"| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n" +
	"|---|---|---|---|---|---|---|---|---|---|---|\n" +
	"| `alpha` |  | use skills/alpha/SKILL.md |  | `true` |  | named | 1 | capability | alpha |  |\n" +
	"| `agent-reviewer` |  | dispatch reviewer |  | `true` | reviewer | named | 1 | adversarial | agent-reviewer |  |\n" +
	"| `smoke` |  | say hi |  | `true` |  | named | 1 | regression | smoke |  |\n" +
	"| `untouched` |  | use skills/beta/SKILL.md |  | `true` |  | named | 1 | capability | untouched |  |\n"

func initHarnessEvalFixture(t *testing.T, home string) (root, c1 string) {
	t.Helper()
	root = idleProject(t)
	writeTree(t, root, "skills/alpha/SKILL.md", "---\nname: alpha\ndescription: alpha\n---\n# alpha v1\n")
	writeTree(t, root, "skills/beta/SKILL.md", "---\nname: beta\ndescription: beta\n---\n# beta v1\n")
	writeTree(t, root, "agents/reviewer/agent.md", "---\nname: reviewer\ndescription: reviewer\n---\n# reviewer v1\n")
	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: func() time.Time { return runAt.Add(-9 * time.Hour) }}, brain.AddInput{
		ID:     "GUIDE-eval",
		Kind:   "guide",
		Status: "current",
		Title:  "GUIDE-eval",
		Body:   strings.NewReader(testHarnessSuite),
	}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "harness suite")
	git(t, root, "push", "-q")
	backdate(t, root)
	c1 = git(t, root, "rev-parse", "--short", "HEAD")
	return root, c1
}

func seedEvalTrials(t *testing.T, home string, trials ...metrics.Trial) {
	t.Helper()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tr := range trials {
		if err := s.RecordTrial(tr); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEvalRerunTriggers(t *testing.T) {
	t.Run("skill-or-agent-changed", func(t *testing.T) {
		home := t.TempDir()
		root, c1 := initHarnessEvalFixture(t, home)
		seedEvalTrials(t, home,
			metrics.Trial{Run: "s/alpha/1", Label: "eval=alpha", Harness: c1, CLI: "agy 1.4.12", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
			metrics.Trial{Run: "s/agent-reviewer/1", Label: "eval=agent-reviewer", Harness: c1, CLI: "agy 1.4.12", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
			metrics.Trial{Run: "s/smoke/1", Label: "eval=smoke", Harness: c1, CLI: "agy 1.4.12", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
			metrics.Trial{Run: "s/untouched/1", Label: "eval=untouched", Harness: c1, CLI: "agy 1.4.12", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
		)
		writeTree(t, root, "skills/alpha/SKILL.md", "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
		writeTree(t, root, "agents/reviewer/agent.md", "---\nname: reviewer\ndescription: reviewer\n---\n# reviewer v2\n")
		git(t, root, "add", ".")
		git(t, root, "commit", "-q", "-m", "update alpha and reviewer")
		git(t, root, "push", "-q")
		backdate(t, root)

		opts := Options{Home: home, RealHome: t.TempDir(), Repo: root, Now: func() time.Time { return runAt }, AgyVersion: func() string { return "agy 1.4.12" }}
		rProj := &run{o: opts, cfg: Config{Level: LevelProject}}
		p := Project{LastEvalRerun: time.Now().Add(500 * time.Millisecond)}
		if got := rProj.pickProjectTask(root, &p, nil); got != nil && got.Class == ClassEvalRerun {
			t.Fatalf("level project triggered eval-rerun: %+v", got)
		}
		rHarn := &run{o: opts, cfg: Config{Level: LevelHarness}}
		task := rHarn.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassEvalRerun || len(task.Reruns) != 2 {
			t.Fatalf("got task %+v, want eval-rerun with 2 rows", task)
		}
		if task.Reruns[0] != (EvalRerunRow{ID: "alpha", Runs: 3, Reason: "skill-or-agent-changed"}) ||
			task.Reruns[1] != (EvalRerunRow{ID: "agent-reviewer", Runs: 3, Reason: "skill-or-agent-changed"}) {
			t.Errorf("reruns = %+v", task.Reruns)
		}
	})

	t.Run("unstable-row-and-cli-changed", func(t *testing.T) {
		home := t.TempDir()
		root, c1 := initHarnessEvalFixture(t, home)
		seedEvalTrials(t, home,
			metrics.Trial{Run: "s/untouched/1", Label: "eval=untouched", Harness: c1, CLI: "agy 1.4.11", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
			metrics.Trial{Run: "s/untouched/2", Label: "eval=untouched", Harness: c1, CLI: "agy 1.4.11", Status: "fail", Cause: "check", StartedAt: runAt.Add(-9 * time.Hour)},
			metrics.Trial{Run: "s/smoke/1", Label: "eval=smoke", Harness: c1, CLI: "agy 1.4.11", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
		)
		opts := Options{Home: home, RealHome: t.TempDir(), Repo: root, Now: func() time.Time { return runAt }, AgyVersion: func() string { return "agy 1.4.12" }}
		rHarn := &run{o: opts, cfg: Config{Level: LevelHarness}}
		var p Project
		task := rHarn.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassEvalRerun || len(task.Reruns) != 2 {
			t.Fatalf("got task %+v, want eval-rerun with 2 rows", task)
		}
		if task.Reruns[0] != (EvalRerunRow{ID: "smoke", Runs: 1, Reason: "cli-changed"}) ||
			task.Reruns[1] != (EvalRerunRow{ID: "untouched", Runs: 5, Reason: "unstable"}) {
			t.Errorf("reruns = %+v", task.Reruns)
		}
	})
}

func TestEvalRerunExecAndBetweenRowsG9(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	root, c1 := initHarnessEvalFixture(t, home)
	seedEvalTrials(t, home,
		metrics.Trial{Run: "s/alpha/1", Label: "eval=alpha", Harness: c1, CLI: "agy 1.4.11", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
		metrics.Trial{Run: "s/smoke/1", Label: "eval=smoke", Harness: c1, CLI: "agy 1.4.11", Status: "pass", StartedAt: runAt.Add(-10 * time.Hour)},
	)
	writeTree(t, root, "skills/alpha/SKILL.md", "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "update alpha")
	git(t, root, "push", "-q")
	backdate(t, root)

	if err := save(home, Config{Enabled: true, Level: LevelHarness, ProjectIdle: "6h", UserIdle: "15m"}); err != nil {
		t.Fatal(err)
	}
	usageCalls := 0
	lowUsage := usageText("80%", "40%")
	fe := &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	var evalCalls []EvalRequest
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.RealHome = realHome
	o.Repo = root
	o.AgyVersion = func() string { return "agy 1.4.12" }
	o.Env = &countingUsageEnv{fakeEnv: fe, calls: &usageCalls, secondText: lowUsage}
	head := git(t, root, "rev-parse", "--short", "HEAD")
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		evalCalls = append(evalCalls, req)
		for i := 0; i < req.Runs; i++ {
			seedEvalTrials(t, home, metrics.Trial{
				Run:       "rerun/" + req.Row + "/" + string(rune('1'+i)),
				Label:     "eval=" + req.Row,
				Harness:   head,
				CLI:       "agy 1.4.12",
				Status:    "pass",
				StartedAt: runAt.Add(time.Duration(i) * time.Millisecond),
			})
		}
		return EvalOutcome{Row: req.Row, Passed: req.Runs, Graded: req.Runs, RecordID: "TEST-fake"}, nil
	}

	if err := Run(o, false); err != nil {
		t.Fatalf("first Run (G9 stops before 2nd row): %v\n%s", err, out.String())
	}
	if len(evalCalls) != 1 || evalCalls[0].Row != "alpha" || evalCalls[0].Runs != 3 {
		t.Fatalf("evalCalls = %+v, want only alpha (k=3) before G9 stopped", evalCalls)
	}
	if usageCalls < 2 {
		t.Errorf("usageCalls = %d, want >= 2 (re-read between rows)", usageCalls)
	}
	assertEvalRerunSecondRun(t, o, fe, home, root, &out, &evalCalls)
}

func assertEvalRerunSecondRun(t *testing.T, o Options, fe *fakeEnv, home, root string, out *bytes.Buffer, evalCalls *[]EvalRequest) {
	t.Helper()
	if st := git(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("dirty before second Run:\n%s\nfirst out:\n%s", st, out.String())
	}
	*evalCalls = nil
	out.Reset()
	o.Env = fe
	if err := Run(o, false); err != nil {
		t.Fatalf("second Run: %v\n%s", err, out.String())
	}
	if len(*evalCalls) != 1 || (*evalCalls)[0].Row != "smoke" || (*evalCalls)[0].Runs != 1 {
		t.Fatalf("second Run evalCalls = %+v, want only smoke (k=1)\nout:\n%s", *evalCalls, out.String())
	}
	recs, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "testing", Project: root})
	if err != nil || len(recs) < 2 {
		t.Fatalf("testing records = %+v, %v; want >= 2", recs, err)
	}
	if status := git(t, root, "status", "--porcelain"); status != "" {
		t.Errorf("root dirty after eval-rerun:\n%s", status)
	}
}

type countingUsageEnv struct {
	*fakeEnv
	calls      *int
	secondText string
}

func (c *countingUsageEnv) Usage(home, dir string) (string, error) {
	*c.calls++
	if *c.calls >= 2 && c.secondText != "" {
		return c.secondText, nil
	}
	return c.fakeEnv.Usage(home, dir)
}

func TestEvalRerunSubprocessRunner(t *testing.T) {
	home := t.TempDir()
	root, _ := initHarnessEvalFixture(t, home)
	fakeSelf := filepath.Join(t.TempDir(), "fake-fyr")
	script := "#!/usr/bin/env bash\necho 'eval: 3 of 3 PASS, 0 errors; record TEST-2026-11-01-1200-eval'\n"
	if err := os.WriteFile(fakeSelf, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := runFyrEval(context.Background(), fakeSelf, EvalRequest{
		Dir:  root,
		Home: home,
		Runs: 3,
		Row:  "alpha",
	})
	if err != nil || out.Passed != 3 || out.Graded != 3 || out.Errors != 0 || out.RecordID != "TEST-2026-11-01-1200-eval" {
		t.Fatalf("runFyrEval = %+v, %v", out, err)
	}
	if entries, _ := os.ReadDir(dir(home)); len(entries) != 0 {
		t.Errorf("leftover clone in %s: %v", dir(home), entries)
	}
}
