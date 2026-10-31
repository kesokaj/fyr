package eval

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
)

type rig struct {
	t                                    *testing.T
	tmp, root, home, real, ctl, rec, bin string
	self, repo                           string
	out                                  bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	tmp := outsideTemp(t)
	r := &rig{t: t, tmp: tmp, root: filepath.Join(tmp, "project"), home: filepath.Join(tmp, "fyr-home"),
		real: filepath.Join(tmp, "real"), ctl: filepath.Join(tmp, "ctl"), rec: filepath.Join(tmp, "rec"),
		bin: filepath.Join(tmp, "bin"), repo: filepath.Join(tmp, "harness")}
	for _, d := range []string{r.ctl, r.rec, r.bin, r.repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, r.root)
	put(t, filepath.Join(r.root, "README.md"), "demo\n", 0o644)
	put(t, filepath.Join(r.root, ".agents", "trail.jsonl"), "", 0o644)
	r.git("add", "-A")
	r.git("commit", "-qm", "fixture")
	put(t, filepath.Join(r.real, tokenRel), "token", 0o600)
	put(t, filepath.Join(r.real, harness.StateDir, "brain.db"), "store", 0o600)
	sub := strings.NewReplacer("@CTL@", sandbox.ShellQuote(r.ctl), "@REC@", sandbox.ShellQuote(r.rec))
	put(t, filepath.Join(r.bin, "agy"), sub.Replace(fakeAgy), 0o755)
	r.self = filepath.Join(tmp, "self")
	put(t, r.self, sub.Replace(fakeSelf), 0o755)
	t.Setenv("PATH", r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", r.real)
	t.Setenv("FYR_REPO", "/must/not/reach/the/install")
	t.Setenv("GIT_EVAL_MARK", "must not reach git")
	t.Cleanup(func() {
		b, _ := os.ReadFile(filepath.Join(r.rec, "pids"))
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return r
}

func (r *rig) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *rig) on(mode, value string) { put(r.t, filepath.Join(r.ctl, mode), value, 0o644) }

func (r *rig) opts() Options {
	return Options{Root: r.root, Home: r.home, RealHome: r.real, Self: r.self, Repo: r.repo, Out: &r.out, Now: func() time.Time { return stampTime }}
}

func (r *rig) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.rec, name))
	return string(b)
}

// run runs rows and asserts what every case owes: no error, and the token
// link and settings.json gone from every sandbox it made.
func (r *rig) run(rows ...Row) []Result {
	r.t.Helper()
	res, err := Run(context.Background(), r.opts(), rows)
	if err != nil {
		r.t.Fatalf("Run: %v", err)
	}
	r.clean(res)
	return res
}

func (r *rig) clean(res []Result) {
	r.t.Helper()
	for _, x := range res {
		for _, p := range []string{tokenRel, ".gemini/antigravity-cli/settings.json"} {
			if _, err := os.Lstat(filepath.Join(x.Dir, "home", p)); !os.IsNotExist(err) {
				r.t.Errorf("row %s: %s is still in the sandbox", x.Row, p)
			}
		}
	}
}

func one(t *testing.T, res []Result) Result {
	t.Helper()
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(res), res)
	}
	return res[0]
}

func wantVerdict(t *testing.T, x Result, verdict string, reasons ...string) {
	t.Helper()
	if x.Status != verdict || strings.Join(x.Reasons, "; ") != strings.Join(reasons, "; ") {
		t.Errorf("row %s: %s %v, want %s %v", x.Row, x.Status, x.Reasons, verdict, reasons)
	}
}

func named(id, prompt, check string) Row {
	return Row{ID: id, Prompt: prompt, Check: check, Grants: "named"}
}

func envMap(s string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}
