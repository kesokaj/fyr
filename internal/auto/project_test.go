package auto

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
)

func targetsOf(roots ...string) []dream.Target {
	var out []dream.Target
	for _, r := range roots {
		out = append(out, dream.Target{Root: r})
	}
	return out
}

func TestGateCleanPasses(t *testing.T) {
	t.Parallel()

	if got := gateClean(idleProject(t)); got != "" {
		t.Error(got)
	}
}

func TestGateCleanStops(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		set  func(*testing.T, string)
		want string
	}{
		{"dirty", func(t *testing.T, root string) { writeTree(t, root, "main.go", "package changed\n") }, "dirty"},
		{"untracked", func(t *testing.T, root string) { writeTree(t, root, "new.go", "x") }, "dirty"},
		{"detached", func(t *testing.T, root string) { git(t, root, "checkout", "-q", "--detach") }, "not a branch"},
		{"no upstream", func(t *testing.T, root string) { git(t, root, "branch", "-q", "--unset-upstream") }, "no upstream"},
		{"ahead", func(t *testing.T, root string) { git(t, root, "commit", "-q", "--allow-empty", "-m", "x") }, "ahead"},
		{"merge", func(t *testing.T, root string) { writeTree(t, root, ".git/MERGE_HEAD", "x") }, "MERGE_HEAD"},
		{"cherry-pick", func(t *testing.T, root string) { writeTree(t, root, ".git/CHERRY_PICK_HEAD", "x") }, "CHERRY_PICK_HEAD"},
		{"rebase", func(t *testing.T, root string) { writeTree(t, root, ".git/rebase-merge/head", "x") }, "rebase-merge"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := idleProject(t)
			c.set(t, root)
			if got := gateClean(root); !strings.Contains(got, c.want) {
				t.Errorf("%q, want %q", got, c.want)
			}
		})
	}
}

func TestGateProjectSkipped(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfig(t, home, validConfig)
	root := idleProject(t)
	var out strings.Builder
	o := runOpts(home, nil)
	o.Out = &out
	o.Env = &fakeEnv{targets: []dream.Target{{Root: "/lost", Skip: "gone"}, {Root: root}}, usage: usageText("90%", "90%")}
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	log := logOf(t, home)
	if !strings.Contains(log, "/lost: skipped: gone") || !strings.Contains(log, root+": health: ") {
		t.Errorf("%s", log)
	}
}

func TestGateRereadBeforeEachProject(t *testing.T) {
	t.Parallel()

	for name, set := range map[string]func(home string, e *fakeEnv) func(){
		"off": func(home string, _ *fakeEnv) func() {
			return func() {
				writeConfig(t, home, `{"enabled": false, "conversation_idle": "1h"}`)
			}
		},
		"on battery": func(_ string, e *fakeEnv) func() {
			return func() { e.supplies = []Supply{{Type: "Battery"}} }
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, validConfig)
			first, second := idleProject(t), idleProject(t)
			e := &fakeEnv{targets: targetsOf(first, second), usage: usageText("90%", "90%")}
			e.onUsage = set(home, e)
			var out strings.Builder
			o := runOpts(home, nil)
			o.Out, o.Env = &out, e
			if err := Run(o, false); err != nil {
				t.Fatal(err)
			}
			log := logOf(t, home)
			if !strings.Contains(log, first+": ") || strings.Contains(log, second+": ") {
				t.Errorf("%s", log)
			}
		})
	}
}

func TestGateCorruptConfigMidRun(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfig(t, home, validConfig)
	e := &fakeEnv{targets: targetsOf(idleProject(t), idleProject(t)), usage: usageText("90%", "90%")}
	e.onUsage = func() { writeConfig(t, home, "garbage") }
	var out strings.Builder
	o := runOpts(home, nil)
	o.Out, o.Env = &out, e
	if exit(Run(o, false)) != 1 {
		t.Errorf("%s", out.String())
	}
}

func TestGateTargetsUnreadable(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfig(t, home, validConfig)
	var out strings.Builder
	o := runOpts(home, nil)
	o.Out, o.Env = &out, &fakeEnv{targetsErr: errFake}
	if exit(Run(o, false)) != 1 || !strings.Contains(logOf(t, home), "targets: fake failure") {
		t.Errorf("%s", out.String())
	}
}

func TestGoneProjectsLifecycleAndPruning(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfig(t, home, validConfig)
	returned := idleProject(t)
	now := runAt
	var out strings.Builder
	env := &fakeEnv{
		targets: []dream.Target{{Root: "/lost", Skip: "gone"}, {Root: returned, Skip: "gone"}},
		usage:   usageText("90%", "90%"),
	}
	o := runOpts(home, nil)
	o.Out, o.Env = &out, env
	o.Now = func() time.Time { return now }

	// Day 0: GoneSince recorded on skipped: gone.
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	st, _, err := loadState(home)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Projects["/lost"].GoneSince.Equal(now) || st.Projects["/lost"].Verdict != "skipped: gone" {
		t.Fatalf("day 0 /lost = %+v", st.Projects["/lost"])
	}
	if !st.Projects[returned].GoneSince.Equal(now) {
		t.Fatalf("day 0 returned = %+v", st.Projects[returned])
	}

	// Day 3: returned path comes back (skip == "") -> GoneSince cleared; /lost stays with original GoneSince.
	now = runAt.Add(3 * 24 * time.Hour)
	env.targets = []dream.Target{{Root: "/lost", Skip: "gone"}, {Root: returned}}
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	st, _, _ = loadState(home)
	if !st.Projects[returned].GoneSince.IsZero() || !strings.HasPrefix(st.Projects[returned].Verdict, "health:") {
		t.Errorf("returned project did not clear GoneSince: %+v", st.Projects[returned])
	}
	if !st.Projects["/lost"].GoneSince.Equal(runAt) {
		t.Errorf("day 3 /lost GoneSince mutated: %+v", st.Projects["/lost"])
	}

	// Day 6 (< 7d): /lost still retained and logged as skipped: gone.
	now = runAt.Add(6 * 24 * time.Hour)
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	st, _, _ = loadState(home)
	if _, ok := st.Projects["/lost"]; !ok {
		t.Errorf("day 6 /lost prematurely pruned")
	}
	if !strings.Contains(logOf(t, home), "/lost: skipped: gone") {
		t.Errorf("missing fresh skipped: gone log")
	}

	// Day 7 + 1h (> 7d): /lost pruned while active returned project remains.
	now = runAt.Add(7*24*time.Hour + time.Hour)
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	st, _, _ = loadState(home)
	if _, ok := st.Projects["/lost"]; ok {
		t.Errorf("day 7+ /lost was not pruned: %+v", st.Projects["/lost"])
	}
	if _, ok := st.Projects[returned]; !ok {
		t.Errorf("active project pruned unexpectedly")
	}

	// Cap at maxGoneKeep (20): 25 recent gone entries keep only the 20 newest.
	projects := map[string]Project{returned: {Verdict: "health: no findings", LastRun: now}}
	for i := 0; i < 25; i++ {
		projects[fmt.Sprintf("/gone-%02d", i)] = Project{
			Verdict:   "skipped: gone",
			LastRun:   now,
			GoneSince: now.Add(-time.Duration(i+1) * time.Hour),
		}
	}
	pruneGoneProjects(projects, now)
	goneLeft := 0
	for k, p := range projects {
		if p.Verdict == "skipped: gone" {
			goneLeft++
		}
		for i := 20; i < 25; i++ {
			if k == fmt.Sprintf("/gone-%02d", i) {
				t.Errorf("stale gone cap did not drop older entry %s", k)
			}
		}
	}
	if goneLeft != maxGoneKeep {
		t.Errorf("goneLeft = %d, want %d", goneLeft, maxGoneKeep)
	}
	if _, ok := projects[returned]; !ok {
		t.Errorf("active project dropped during gone cap pruning")
	}
}
