package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// workAgy is the worker's CLI the verb tests drive: by default one commit in
// the Kickoff's scope and one handoff record, the strict export line written
// as fyr record writes it. internal/work's tests cover every other shape.
const workAgy = `#!/bin/bash
CTL=@CTL@
if [ -e "$CTL/hang" ]; then touch "$CTL/started"; exec sleep 300; fi
[ -e "$CTL/nocommit" ] && { echo "agy says ok"; exit 0; }
echo work >> a.txt; git add a.txt; git commit -qm "work on a.txt"
mkdir -p .agents
printf '%s\n' '{"date":"2026-10-29","id":"HANDOFF-2026-10-29-1800-verb","kind":"handoff","status":"","title":"Handoff","updated":"2026-10-29T18:00:00Z","body":"# Handoff\n\n## Next Steps\n\n1. next\n"}' >> .agents/trail.jsonl
git add .agents/trail.jsonl; git commit -qm records
echo "agy says ok"
`

// workSelfScript stands in for the fyr each sandbox installs: install makes
// the binary the sandbox's wrapper execs.
const workSelfScript = `#!/bin/bash
case "$1" in
install) mkdir -p "$3/.local/bin"; printf '#!/bin/bash\necho "fyr $*"\n' > "$3/.local/bin/fyr"; chmod +x "$3/.local/bin/fyr" ;;
esac
`

// workRig is a lead checkout with an identity and a gitignored scratch, a
// real home with a login, a fyr home, the fake agy on PATH and the fake fyr
// as the sandbox's, all outside every checkout (D11).
type workRig struct {
	t                                   *testing.T
	project, home, real, ctl, kick, src string
}

func newWorkRig(t *testing.T) *workRig {
	t.Helper()
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	checkout, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if r, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = r
	}
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): work refuses that by design", strings.TrimSpace(string(out)))
	}
	r := &workRig{t: t, project: filepath.Join(tmp, "project"), home: filepath.Join(tmp, "fyr-home"), real: filepath.Join(tmp, "real"),
		ctl: filepath.Join(tmp, "ctl"), kick: filepath.Join(tmp, "kick"), src: checkout}
	bin := filepath.Join(tmp, "bin")
	for _, d := range []string{r.project, r.home, r.ctl, r.kick, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(r.real, sandbox.TokenRel), "token")
	writeTestFile(t, filepath.Join(bin, "agy"), strings.ReplaceAll(workAgy, "@CTL@", sandbox.ShellQuote(r.ctl)))
	self := filepath.Join(tmp, "self")
	writeTestFile(t, self, workSelfScript)
	for _, f := range []string{filepath.Join(bin, "agy"), self} {
		if err := os.Chmod(f, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := workSelf
	workSelf = func() (string, error) { return self, nil }
	t.Cleanup(func() { workSelf = old })
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", r.real)
	t.Setenv("FYR_REPO", "")

	r.git("init", "-q")
	r.git("config", "user.name", "Lead Person")
	r.git("config", "user.email", "lead@localhost")
	writeTestFile(t, filepath.Join(r.project, ".gitignore"), ".agents/scratch/\n")
	writeTestFile(t, filepath.Join(r.project, "a.txt"), "a\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	t.Chdir(r.project)
	return r
}

func (r *workRig) git(args ...string) string {
	r.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", r.project}, args...)...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// kickoff writes the Kickoff file name.md, its scope a.txt.
func (r *workRig) kickoff(name string) string {
	p := filepath.Join(r.kick, name+".md")
	writeTestFile(r.t, p, "Project: this clone. Do the phase.\nFiles: a.txt\nStanding approvals: commit.\n")
	return p
}

// work runs `fyr work` with the rig's home and install source and returns
// the output and the exit status.
func (r *workRig) work(args ...string) (string, int) {
	r.t.Helper()
	var out bytes.Buffer
	err := run(append([]string{"work", "--repo", r.src, "--home", r.home}, args...), &out)
	var code exitCode
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &code):
		return out.String(), int(code)
	}
	r.t.Fatalf("work %v: %v\n%s", args, err, out.String())
	return "", -1
}

// sandboxes is every worker directory made under the work root.
func (r *workRig) sandboxes() []string {
	m, _ := filepath.Glob(filepath.Join(sandbox.Root(r.home, "work"), "*", "*"))
	return m
}

// TestWorkVerbPass: one Kickoff, one good worker, exit 0, the branch
// fetched into the checkout and the result contract printed.
func TestWorkVerbPass(t *testing.T) {
	r := newWorkRig(t)
	out, code := r.work(r.kickoff("fix-a"))
	if code != 0 || !strings.HasPrefix(out, "work: stamp ") || !strings.Contains(out, "\nwork: fix-a  PASS  agy exit 0  ") ||
		!strings.Contains(out, "work: fix-a: handoff HANDOFF-2026-10-29-1800-verb") {
		t.Fatalf("exit %d, want 0 with the stamp, the PASS line and the handoff:\n%s", code, out)
	}
	if got := r.git("log", "-1", "--format=%s", "work/fix-a"); got != "records" {
		t.Errorf("work/fix-a in the checkout ends at %q", got)
	}
}

// TestWorkVerbFail: a worker that commits nothing fails, exit 1.
func TestWorkVerbFail(t *testing.T) {
	r := newWorkRig(t)
	writeTestFile(t, filepath.Join(r.ctl, "nocommit"), "")
	out, code := r.work(r.kickoff("fix-a"))
	if code != 1 || !strings.Contains(out, "work: fix-a  FAIL (") || !strings.Contains(out, "no commits") {
		t.Fatalf("exit %d, want 1 with no commits:\n%s", code, out)
	}
}

// TestWorkVerbRefusals: every refusal and usage error exits 2 before any
// sandbox exists, the second file among them (one file until Phase 4, D89).
func TestWorkVerbRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(r *workRig) []string
		want  string
	}{
		"no file":     {func(r *workRig) []string { return nil }, "usage: fyr work"},
		"second file": {func(r *workRig) []string { return []string{r.kickoff("fix-a"), r.kickoff("fix-b")} }, "work: one Kickoff file at a time"},
		"dirty tree": {func(r *workRig) []string {
			writeTestFile(r.t, filepath.Join(r.project, "new.txt"), "x\n")
			return []string{r.kickoff("fix-a")}
		}, "uncommitted or untracked changes"},
		"not a checkout": {func(r *workRig) []string {
			r.t.Chdir(r.kick)
			return []string{r.kickoff("fix-a")}
		}, "is not a git checkout"},
		"short timeout": {func(r *workRig) []string { return []string{"--timeout", "9m", r.kickoff("fix-a")} }, "--timeout 9m0s"},
		"zero timeout":  {func(r *workRig) []string { return []string{"--timeout", "0", r.kickoff("fix-a")} }, "--timeout 0s"},
		"no Files line": {func(r *workRig) []string {
			p := filepath.Join(r.kick, "fix-a.md")
			writeTestFile(r.t, p, "Project: this clone.\n")
			return []string{p}
		}, "Files:"},
		"bad install source": {func(r *workRig) []string {
			return []string{"--repo", r.kick, r.kickoff("fix-a")}
		}, "work: "},
	} {
		t.Run(name, func(t *testing.T) {
			r := newWorkRig(t)
			out, code := r.work(tc.setup(r)...)
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want 2 with %q:\n%s", code, tc.want, out)
			}
			if strings.Contains(out, "work: stamp ") || len(r.sandboxes()) > 0 {
				t.Errorf("a refusal started a run: sandboxes %v\n%s", r.sandboxes(), out)
			}
		})
	}
}

// TestWorkVerbSignals: SIGINT, SIGTERM, SIGHUP and SIGQUIT during the session each
// end the worker killed, exit 1, with the result contract printed and the
// token link gone from every sandbox left.
func TestWorkVerbSignals(t *testing.T) {
	for _, s := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT} {
		t.Run(s.String(), func(t *testing.T) {
			r := newWorkRig(t)
			writeTestFile(t, filepath.Join(r.ctl, "hang"), "")
			go func() {
				for i := 0; i < 600; i++ {
					if _, err := os.Stat(filepath.Join(r.ctl, "started")); err == nil {
						syscall.Kill(os.Getpid(), s)
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
			}()
			out, code := r.work(r.kickoff("fix-a"))
			if code != 1 || !strings.Contains(out, "work: fix-a  FAIL (") || !strings.Contains(out, "killed") {
				t.Fatalf("exit %d, want 1 with the worker killed:\n%s", code, out)
			}
			for _, dir := range r.sandboxes() {
				if _, err := os.Lstat(filepath.Join(dir, "home", sandbox.TokenRel)); !os.IsNotExist(err) {
					t.Errorf("the token link is still in %s: %v", dir, err)
				}
			}
		})
	}
}
