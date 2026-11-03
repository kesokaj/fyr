package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// outcome is one way a row's session could end, written as bash run in the
// set-up clone with F set to the trial's fyr wrapper.
type outcome struct {
	name   string
	pass   bool
	script string
}

// prelude is every outcome's helpers: rec stores a project record.
const prelude = `set -e
rec() { "$F" record add "$@" >/dev/null; }
`

// checkWrapper is SB/bin/fyr as the trial installs it: the home pinned
// after the verb (sandbox.wrapperScript's shape).
const checkWrapper = "#!/usr/bin/env bash\nexec %s \"$1\" --home %s \"${@:2}\"\n"

// TestSuiteChecks shows every row's check passing on a good outcome and
// failing on each bad one, after the row's own setup: the evidence item 4
// of PLAN-2026-11-02-1806-fyr-speed asks for, kept as a test so a changed
// check or fixture is shown again.
func TestSuiteChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("builds fyr and runs every row's setup")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rows, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("reading %s: found %v, %v", SuitePath, found, err)
	}
	fixture, err := os.ReadFile(filepath.Join(root, ".agents", "eval", "fixture.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fake, err := os.ReadFile(filepath.Join(root, ".agents", "eval", "fake-work.sh"))
	if err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(outsideTemp(t), "fyr")
	if out, err := exec.Command("go", "build", "-o", self, filepath.Join(root, "cmd", "fyr")).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cases := suiteOutcomes()
	for _, r := range rows {
		outs := cases[r.ID]
		if len(outs) < 2 || !outs[0].pass {
			t.Errorf("row %s: want a good outcome first and at least one bad one", r.ID)
			continue
		}
		for _, o := range outs {
			t.Run(r.ID+"/"+o.name, func(t *testing.T) {
				t.Parallel()
				got := runOutcome(t, self, string(fixture), string(fake), r, o)
				if got != o.pass {
					t.Errorf("check passed %v, want %v", got, o.pass)
				}
			})
		}
	}
	for id := range cases {
		if _, err := SelectRows(rows, []string{id}); err != nil {
			t.Errorf("outcomes for %s, which the suite has no row for", id)
		}
	}
}

// runOutcome sets up a clone as `fyr eval` would (a repository holding the
// fixture and its stand-in verb, the row's setup with FYR and the fixture
// identity), runs the outcome, then the row's check, and reports whether the
// check passed.
func runOutcome(t *testing.T, self, fixture, fake string, r Row, o outcome) bool {
	tmp := outsideTemp(t)
	clone, sb := filepath.Join(tmp, "app"), filepath.Join(tmp, "sb")
	gitInit(t, clone)
	put(t, filepath.Join(clone, ".agents", "eval", "fixture.sh"), fixture, 0o644)
	put(t, filepath.Join(clone, ".agents", "eval", "fake-work.sh"), fake, 0o644)
	put(t, filepath.Join(sb, "bin", "fyr"), fmt.Sprintf(checkWrapper, shq(self), shq(sb)), 0o755)
	check := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sb, "SB=" + sb,
		"OUT=" + filepath.Join(tmp, "out.txt"), "OUT2=" + filepath.Join(tmp, "out2.txt"),
		"FIXDIR=" + clone, "CLONE=" + clone, "GIT_CONFIG_NOSYSTEM=1"}
	setup := append(append([]string{}, check...), "FYR="+self, "F="+filepath.Join(sb, "bin", "fyr"),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@localhost",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@localhost")
	// The clone's source is on a branch of its own, with a tag: the setup
	// must leave neither, only the demo history (fixture.sh replace_history).
	steps := []string{"git add -A && git commit -qm 'the checkout' && git tag v9.9.9 && git checkout -qb harness",
		r.Setup, noHarnessHistory, prelude + o.script}
	for _, s := range steps {
		cmd := exec.Command("bash", "-c", s)
		cmd.Dir, cmd.Env = clone, setup
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%q: %v\n%s", s, err, out)
		}
	}
	cmd := exec.Command("bash", "-c", r.Check)
	cmd.Dir, cmd.Env = clone, check
	out, err := cmd.CombinedOutput()
	if err != nil && o.pass {
		t.Logf("check output:\n%s", out)
	}
	return err == nil
}

// noHarnessHistory fails when the checkout's branch or tag survived a setup.
const noHarnessHistory = `! git rev-parse -q --verify refs/heads/harness >/dev/null && ! git rev-parse -q --verify refs/tags/v9.9.9 >/dev/null && test "$(git symbolic-ref --short HEAD)" = main`

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
