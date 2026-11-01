package auto

import (
	"os"
	"path/filepath"
	"strconv"
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

func idleGate(e Env, root string) string {
	return gateIdle(e, "/nowhere", root, 6*time.Hour, runAt)
}

func TestGateIdlePasses(t *testing.T) {
	if got := idleGate(&fakeEnv{}, idleProject(t)); got != "" {
		t.Error(got)
	}
}

func TestGateIdleEachClock(t *testing.T) {
	recent := runAt.Add(-time.Hour)
	ahead := runAt.Add(48 * time.Hour)
	touch := func(name string, at time.Time) func(*testing.T, string) {
		return func(t *testing.T, root string) {
			if err := os.Chtimes(filepath.Join(root, name), at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	reflog := func(entry string) func(*testing.T, string) {
		return func(t *testing.T, root string) {
			p := filepath.Join(root, ".git", "logs", "HEAD")
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString(entry); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name string
		set  func(*testing.T, string)
		env  *fakeEnv
		want string
	}{
		{name: "trail", set: touch(".agents/trail.jsonl", recent), want: "the trail"},
		{name: "trail ahead of the clock", set: touch(".agents/trail.jsonl", ahead), want: "the trail"},
		{name: "tracked file", set: touch("main.go", recent), want: "main.go"},
		{name: "tracked file ahead of the clock", set: touch("main.go", ahead), want: "main.go"},
		{name: "reflog", set: reflog(entryAt(recent)), want: "a git operation"},
		{name: "reflog ahead of the clock", set: reflog(entryAt(ahead)), want: "a git operation"},
		{name: "reflog that cannot be dated", set: reflog("garbage\n"), want: "unreadable"},
		{name: "conversation", env: &fakeEnv{conv: recent}, want: "an Antigravity session"},
		{name: "conversation unreadable", env: &fakeEnv{convErr: errFake}, want: "unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := idleProject(t)
			if c.set != nil {
				c.set(t, root)
			}
			e := c.env
			if e == nil {
				e = &fakeEnv{}
			}
			if got := idleGate(e, root); !strings.Contains(got, c.want) {
				t.Errorf("%q, want %q", got, c.want)
			}
		})
	}
}

func entryAt(at time.Time) string {
	return strings.Repeat("0", 40) + " " + strings.Repeat("1", 40) + " f <f@localhost> " + strconv.FormatInt(at.Unix(), 10) + " +0000\tcommit: x\n"
}

func TestGateIdleNoReflog(t *testing.T) {
	root := idleProject(t)
	if err := os.Remove(filepath.Join(root, ".git", "logs", "HEAD")); err != nil {
		t.Fatal(err)
	}
	if got := idleGate(&fakeEnv{}, root); got != "" {
		t.Error(got)
	}
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
	o.Env = &fakeEnv{targets: []dream.Target{{Root: "/lost", Skip: "not registered (fyr index --register)"}, {Root: root}}}
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	log := logOf(t, home)
	if !strings.Contains(log, "/lost: G6 registered: not registered") || !strings.Contains(log, root+": health: ") {
		t.Errorf("%s", log)
	}
}

func TestGateRereadBeforeEachProject(t *testing.T) {
	cases := map[string]func(home string, e *fakeEnv) (hook func()){
		"off": func(home string, e *fakeEnv) func() {
			return func() {
				writeConfig(t, home, `{"enabled": false, "level": "project", "project_idle": "6h", "user_idle": "15m"}`)
			}
		},
		"operator back": func(home string, e *fakeEnv) func() {
			return func() { e.ptys = []time.Time{runAt} }
		},
		"session opened": func(home string, e *fakeEnv) func() {
			return func() { e.agy = func() (bool, error) { return true, nil } }
		},
		"on battery": func(home string, e *fakeEnv) func() {
			return func() { e.supplies = []Supply{{Type: "Battery"}} }
		},
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, validConfig)
			first, second := idleProject(t), idleProject(t)
			e := &fakeEnv{targets: targetsOf(first, second), usage: usageText("90%", "90%")}
			e.onConv = set(home, e)
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
	e := &fakeEnv{targets: targetsOf(idleProject(t), idleProject(t))}
	e.onConv = func() { writeConfig(t, home, "garbage") }
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
	if exit(Run(o, false)) != 1 || !strings.Contains(logOf(t, home), "G6 registered: fake failure") {
		t.Errorf("%s", out.String())
	}
}
