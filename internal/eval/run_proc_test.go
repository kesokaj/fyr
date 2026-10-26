package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// alive reports whether pid is running and not a zombie, waiting up to a
// few seconds for it to go.
func alive(pid string) bool {
	for i := 0; i < 100; i++ {
		b, err := os.ReadFile("/proc/" + pid + "/status")
		if err != nil || strings.Contains(string(b), "State:\tZ") {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// TestRunTimeout: at the timeout only agy is killed, the check still runs,
// and the agy exit is printed but is not a verdict condition.
func TestRunTimeout(t *testing.T) {
	r := newRig(t)
	old := agyTimeout
	agyTimeout = time.Second
	t.Cleanup(func() { agyTimeout = old })
	r.on("hang", "")
	x := one(t, r.run(named("slow", "p", "true")))
	wantVerdict(t, x, "PASS")
	if x.AgyExit != -1 || !strings.Contains(r.out.String(), "agy timed out after 1s") {
		t.Errorf("agy exit %d\n%s", x.AgyExit, r.out.String())
	}
}

// TestRunInterrupted: on ctx's end the row FAILs interrupted, the rows after
// it never run, and the token link and settings are still removed.
func TestRunInterrupted(t *testing.T) {
	r := newRig(t)
	r.on("hang", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(r.rec, "started")); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	res, err := Run(ctx, r.opts(), []Row{named("a", "p", "true"), named("b", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	x := one(t, res)
	wantVerdict(t, x, "FAIL", ReasonInterrupted)
	if time.Since(start) > 30*time.Second {
		t.Errorf("the interrupt took %s", time.Since(start))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(x.Dir), "b")); !os.IsNotExist(err) {
		t.Error("the row after the interrupt ran")
	}
}

// TestRunRefusals: step 0 refuses before anything is created.
func TestRunRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		change func(r *rig, o *Options)
		rows   []Row
		want   string
	}{
		"not a checkout":       {func(r *rig, o *Options) { o.Root = r.real }, nil, "is not a git checkout"},
		"a home in a checkout": {func(r *rig, o *Options) { o.Home = filepath.Join(r.root, ".agents", "scratch", "home") }, nil, "(BUG-106)"},
		"no login":             {func(r *rig, o *Options) { os.Remove(filepath.Join(r.real, tokenRel)) }, nil, "no Antigravity login at"},
		"a login that is a link": {func(r *rig, o *Options) {
			p := filepath.Join(r.real, tokenRel)
			os.Rename(p, p+".real")
			os.Symlink(p+".real", p)
		}, nil, "no Antigravity login at"},
		"no agy on PATH": {func(r *rig, o *Options) {
			only := filepath.Join(r.tmp, "only-git")
			os.MkdirAll(only, 0o755)
			g, _ := exec.LookPath("git")
			os.Symlink(g, filepath.Join(only, "git"))
			r.t.Setenv("PATH", only)
		}, nil, "agy not found on PATH"},
		"a row named twice": {nil, []Row{named("a", "p", "true"), named("a", "p", "true")}, "row a is named twice"},
		"no rows":           {nil, []Row{}, "no row to run"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			o := r.opts()
			if c.change != nil {
				c.change(r, &o)
			}
			rows := c.rows
			if rows == nil {
				rows = []Row{named("a", "p", "true")}
			}
			res, err := Run(context.Background(), o, rows)
			if err == nil || !strings.Contains(err.Error(), c.want) || res != nil {
				t.Errorf("want a refusal holding %q, got %v, %+v", c.want, err, res)
			}
			if _, err := os.Stat(SandboxRoot(o.Home)); !os.IsNotExist(err) {
				t.Error("a refusal created the sandbox root")
			}
			if r.read("agy.n") != "" || r.read("self.calls") != "" {
				t.Error("a refusal ran the CLI or the install")
			}
		})
	}
}

// TestRunRealFyrBaseline is the A5 case with the real binary: a fixture
// whose trail quotes a private-key header is loaded into the sandbox's
// brain.db by `record list` (step 4), and the baseline of step 8b absorbs it
// (D17), so the row passes; the same fixture with a token the run writes
// fails. Self is a build of this checkout, and the install source is this
// checkout, as `fyr eval` resolves it here (D16).
func TestRunRealFyrBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and installs fyr")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// The go tool keeps its caches under the real HOME, which the rig moves.
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		out, err := exec.Command("go", "env", k).Output()
		if err != nil {
			t.Skipf("go env %s: %v", k, err)
		}
		t.Setenv(k, strings.TrimSpace(string(out)))
	}
	t.Setenv("GOPROXY", "off")
	r := newRig(t)
	self := filepath.Join(r.tmp, "fyr")
	if out, err := exec.Command("go", "build", "-o", self, filepath.Join(root, "cmd", "fyr")).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	fixture := filepath.Join(r.tmp, "fixture")
	gitInit(t, fixture)
	add := exec.Command(self, "record", "add", "guide", "--title", "Key shapes", "--home", filepath.Join(r.tmp, "import-home"))
	add.Dir = fixture
	add.Stdin = strings.NewReader("The redactor's test quotes a header: -----BEGIN OPENSSH PRIVATE KEY-----\n")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("record add: %v\n%s", err, out)
	}
	for _, args := range [][]string{{"add", ".agents/trail.jsonl"}, {"commit", "-qm", "trail"}} {
		cmd := exec.Command("git", append([]string{"-C", fixture, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	o := r.opts()
	o.Self, o.Fixture, o.Repo = self, fixture, root
	res, err := Run(context.Background(), o, []Row{named("a5", "p", `"$SB/bin/fyr" record list | grep -q "Key shapes"`)})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	x := one(t, res)
	wantVerdict(t, x, "PASS")
	if x.Verdict != "PASS" {
		b, _ := os.ReadFile(filepath.Join(x.Dir, "install.log"))
		t.Fatalf("output:\n%s\ninstall.log:\n%s", r.out.String(), b)
	}
	if b, _ := os.ReadFile(filepath.Join(x.Dir, "install.log")); !strings.Contains(string(b), root) {
		t.Errorf("the install did not name the checkout as its source:\n%s", b)
	}
	// The positive control: the store does hold the header the baseline absorbed.
	if f := scanCreds(filepath.Join(x.Dir, "home"))[filepath.Join(harness.StateDir, "brain.db")]; f.kinds["private-key"] == 0 {
		t.Fatalf("the sandbox's brain.db holds no key header, so the case proves nothing: %+v", f)
	}
	r.on("leak", "gh"+"o_"+strings.Repeat("C", 36))
	res, err = Run(context.Background(), o, []Row{named("a5-leak", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	wantVerdict(t, one(t, res), "FAIL", ReasonCredentials)
}
