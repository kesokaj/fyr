package dream

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// fakeBranchAgy is an agy on PATH for a whole run: it answers the oracle
// from $HOME/oracle/<name>.json, or once from <name>.once, which it then
// removes (the settings changed between two projects), and for a call it
// appends the HOME, the number of private homes under conf at that moment
// and the stdin under rec, writes that call's environment to rec/env.<n>,
// and prints out.
func fakeBranchAgy(t *testing.T, conf, out string) (rec string) {
	t.Helper()
	bin, rec := t.TempDir(), t.TempDir()
	write(t, filepath.Join(rec, "out"), out, fresh)
	script := "#!/bin/sh\n" +
		"if [ \"$1 $3 $4\" = \"-p --output-format json\" ]; then\n" +
		"  echo \"$2\" >> \"" + rec + "/oracle\"\n" +
		"  o=\"$HOME/oracle/${2#/}\"\n" +
		"  if [ -f \"$o.once\" ]; then cat \"$o.once\"; rm \"$o.once\"; exit 0; fi\n" +
		"  exec cat \"$o.json\"\n" +
		"fi\n" +
		"printf '%s\\n' \"$HOME\" >> \"" + rec + "/homes\"\n" +
		"n=$(wc -l < \"" + rec + "/homes\" | tr -d ' ')\n" +
		"env > \"" + rec + "/env.$n\"\n" +
		"ls -d \"" + conf + "\"/dream-agy-home-* 2>/dev/null | wc -l | tr -d ' ' >> \"" + rec + "/privates\"\n" +
		"cat >> \"" + rec + "/stdin\"\n" +
		"cat \"" + rec + "/out\"\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return rec
}

// branchHome gives home a login and the oracle's answers: variant's saved
// reports (testdata/oracle), and once, for the first project that asks,
// first's /config instead when it is not "".
func branchHome(t *testing.T, home, variant, first string) {
	t.Helper()
	write(t, filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token"), "token", fresh)
	for _, c := range []string{"permissions", "config"} {
		write(t, filepath.Join(home, "oracle", c+".json"), read(t, filepath.Join("testdata", "oracle", variant+"."+c+".json")), fresh)
	}
	write(t, filepath.Join(home, "oracle", "hooks.json"), read(t, filepath.Join("testdata", "oracle", "hooks.json")), fresh)
	if first != "" {
		write(t, filepath.Join(home, "oracle", "config.once"), read(t, filepath.Join("testdata", "oracle", first+".config.json")), fresh)
	}
}

// branchRun is n fixture projects registered in the first one's home.
func branchRun(t *testing.T, n int) (Options, []fixture, *bytes.Buffer) {
	t.Helper()
	fs := []fixture{newFixture(t)}
	for len(fs) < n {
		g := newFixture(t)
		g.home = fs[0].home
		markRegistered(t, g.home, g.root)
		fs = append(fs, g)
	}
	var ps []search.Project
	for _, f := range fs {
		scaffold(t, f)
		ps = append(ps, project(f))
	}
	var out bytes.Buffer
	o := runOpts(fs[0], &out)
	o.Projects = func(string) ([]search.Project, error) { return ps, nil }
	return o, fs, &out
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	return strings.Split(strings.TrimSpace(read(t, path)), "\n")
}

// TestRunBranchPerProject (plan D12): the oracle is asked for every project.
// The first finds no tool granted without review and runs in --home itself,
// with no private home made yet; the settings then grant one, so the second
// and third run private, in one private home made for the second, and
// removed after the run. A credential in fyr's environment reaches neither.
func TestRunBranchPerProject(t *testing.T) {
	o, fs, _ := branchRun(t, 3)
	conf := filepath.Join(o.Home, harness.StateDir)
	agyLogin(t)
	branchHome(t, o.Home, "always-proceed", "none")
	rec := fakeBranchAgy(t, conf, agyResult(`{"proposals":[],"skills":[]}`, false))
	t.Setenv("GEMINI_API_KEY", "canary-gemini")
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	homes := lines(t, filepath.Join(rec, "homes"))
	if len(homes) != 3 || homes[0] != o.Home || !strings.Contains(homes[1], "dream-agy-home-") || homes[2] != homes[1] {
		t.Errorf("call HOMEs = %q, want --home, then one private home twice", homes)
	}
	if got := strings.Join(lines(t, filepath.Join(rec, "privates")), " "); got != "0 1 1" {
		t.Errorf("private homes at each call = %s, want 0 1 1 (made on the first private project, reused)", got)
	}
	if got := len(lines(t, filepath.Join(rec, "oracle"))); got != 7 {
		t.Errorf("%d oracle reports, want three for the visible project and two for each private one", got)
	}
	for i := 1; i <= 3; i++ {
		if env := read(t, filepath.Join(rec, "env."+strconv.Itoa(i))); strings.Contains(env, "canary-") {
			t.Errorf("call %d: a credential reached agy", i)
		}
	}
	if !strings.Contains(read(t, filepath.Join(rec, "env.1")), "HOME="+o.Home+"\n") {
		t.Error("the visible call's HOME is not --home")
	}
	log := read(t, filepath.Join(conf, "dream.log"))
	for _, want := range []string{
		fs[0].root + " agy home visible\n",
		fs[1].root + " agy home private (tool permission always-proceed approves tools without review)\n",
		fs[2].root + " agy home private (tool permission always-proceed approves tools without review)\n",
		fs[2].root + " ok 0 proposals\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("dream.log lacks %q:\n%s", want, log)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(conf, "dream-agy-home-*")); len(left) > 0 {
		t.Errorf("the private home was left behind: %v", left)
	}
}

// TestRunBranchPrivateFailsOneProject: the first project's oracle shows a
// grant and there is no login under $HOME to build the private home from,
// which fails that project; the second runs visible, and the run exits 1.
func TestRunBranchPrivateFailsOneProject(t *testing.T) {
	o, fs, _ := branchRun(t, 2)
	conf := filepath.Join(o.Home, harness.StateDir)
	t.Setenv("HOME", t.TempDir())
	branchHome(t, o.Home, "none", "always-proceed")
	rec := fakeBranchAgy(t, conf, agyResult(`{"proposals":[],"skills":[]}`, false))
	var ee *ExitError
	if err := Run(o); !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if homes := lines(t, filepath.Join(rec, "homes")); len(homes) != 1 || homes[0] != o.Home {
		t.Errorf("call HOMEs = %q, want the second project's alone, in --home", homes)
	}
	log := read(t, filepath.Join(conf, "dream.log"))
	for _, want := range []string{fs[0].root + " fail no Antigravity login", fs[1].root + " agy home visible\n", fs[1].root + " ok 0 proposals\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("dream.log lacks %q:\n%s", want, log)
		}
	}
}

// TestDryRunBranchLine: a dry run asks the oracle per project and says the
// branch, or that it could not ask before dream-work exists; it makes
// neither dream-work nor a private home.
func TestDryRunBranchLine(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	agyLogin(t)
	o := Options{Home: f.home, Project: f.root, DryRun: true, Now: runOpts(f, nil).Now, Scopes: noScopes}
	conf := filepath.Join(f.home, harness.StateDir)
	fakeBranchAgy(t, conf, "")
	dry := func() string {
		var out bytes.Buffer
		o.Out = &out
		if err := Run(o); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	branchHome(t, f.home, "none", "")
	if got := dry(); !strings.Contains(got, "[dry-run] "+f.root+": agy home not asked: no dream-work yet") {
		t.Errorf("before dream-work:\n%s", got)
	}
	if _, err := os.Stat(harness.DreamWork(f.home)); !os.IsNotExist(err) {
		t.Error("the dry run made dream-work")
	}
	if err := os.MkdirAll(harness.DreamWork(f.home), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := dry(); !strings.Contains(got, "[dry-run] "+f.root+": agy home visible (HOME "+f.home+")\n") || strings.Contains(got, "a private copy") {
		t.Errorf("visible:\n%s", got)
	}
	branchHome(t, f.home, "always-proceed", "")
	if got := dry(); !strings.Contains(got, "[dry-run] "+f.root+": agy home private (tool permission always-proceed approves tools without review): a copy of the login, with no grants\n") {
		t.Errorf("private:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(conf, "dream-agy-home-*")); len(left) > 0 {
		t.Errorf("a dry run made a private home: %v", left)
	}
}

// TestLogReason: the oracle's why may quote a rule from the operator's
// settings: a credential in it is redacted, a line break folded, a control
// character (a terminal escape) dropped, and a path kept as written.
func TestLogReason(t *testing.T) {
	why := "an allow rule in the settings (command(curl -H \"Authorization: Bearer abc.def\"))\n\x1b[31mred\x1b[0m /home/x/.config"
	got := logReason(why)
	if strings.Contains(got, "abc.def") || strings.ContainsAny(got, "\n\x1b") || !strings.Contains(got, "/home/x/.config") {
		t.Errorf("logReason = %q", got)
	}
	if got := logReason("dream-work is inside a git checkout (/a b)"); got != "dream-work is inside a git checkout (/a b)" {
		t.Errorf("a plain reason changed: %q", got)
	}
}
