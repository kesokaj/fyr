package auto

import (
	"strings"
	"testing"

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
	if got := gateClean(idleProject(t)); got != "" {
		t.Error(got)
	}
}

func TestGateCleanStops(t *testing.T) {
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
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	var out strings.Builder
	o := runOpts(home, nil)
	o.Out, o.Env = &out, &fakeEnv{targetsErr: errFake}
	if exit(Run(o, false)) != 1 || !strings.Contains(logOf(t, home), "targets: fake failure") {
		t.Errorf("%s", out.String())
	}
}
