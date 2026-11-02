package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// TestRunTimeout: at the timeout only agy is killed, the check still runs
// and grades the trial, and the agy exit is printed but is not a verdict
// condition; a check that fails after a timeout is a FAIL with cause
// timeout, never an ERROR (the eval-metrics plan D6).
func TestRunTimeout(t *testing.T) {
	old := agyTimeout
	agyTimeout = time.Second
	t.Cleanup(func() { agyTimeout = old })
	for _, c := range []struct{ check, status, cause string }{{"true", "PASS", ""}, {"false", "FAIL", "timeout"}} {
		r := newRig(t)
		r.on("hang", "")
		x := one(t, r.run(named("slow", "p", c.check)))
		if x.Status != c.status || x.Cause != c.cause {
			t.Errorf("check %s: %s (%s), want %s (%s)", c.check, x.Status, x.Cause, c.status, c.cause)
		}
		if x.AgyExit != -1 || !strings.Contains(r.out.String(), "agy timed out after 1s") {
			t.Errorf("agy exit %d\n%s", x.AgyExit, r.out.String())
		}
		if x.SessionSeconds < 1 {
			t.Errorf("session %ds, want at least the 1s timeout", x.SessionSeconds)
		}
	}
}

// TestRunInterrupted: on ctx's end the trial is an ERROR (interrupted), the
// row's remaining trials and the rows after it never run, and the token
// link and settings are still removed.
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
	o := r.opts()
	o.Runs = 3
	res, err := Run(ctx, o, []Row{named("a", "p", "true"), named("b", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	x := one(t, res)
	wantVerdict(t, x, "ERROR", ReasonInterrupted)
	if x.Cause != "interrupted" {
		t.Errorf("cause %q, want interrupted", x.Cause)
	}
	if time.Since(start) > 30*time.Second {
		t.Errorf("the interrupt took %s", time.Since(start))
	}
	// x.Dir is <stamp>/a/1: row b's sandbox would be <stamp>/b.
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(x.Dir)), "b")); !os.IsNotExist(err) {
		t.Error("the row after the interrupt ran")
	}
	if x.Trial != 1 {
		t.Errorf("the interrupted trial is %d, want 1", x.Trial)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(x.Dir), "2")); !os.IsNotExist(err) {
		t.Error("the row's next trial ran after the interrupt")
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
	exp := exec.Command(self, "record", "export", "--home", filepath.Join(r.tmp, "import-home"))
	exp.Dir = fixture
	if out, err := exp.CombinedOutput(); err != nil {
		t.Fatalf("record export: %v\n%s", err, out)
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
	if x.Status != "PASS" {
		b, _ := os.ReadFile(filepath.Join(x.Dir, "install.log"))
		t.Fatalf("output:\n%s\ninstall.log:\n%s", r.out.String(), b)
	}
	if b, _ := os.ReadFile(filepath.Join(x.Dir, "install.log")); !strings.Contains(string(b), root) {
		t.Errorf("the install did not name the checkout as its source:\n%s", b)
	}
	// The positive control: the store does hold the header the baseline absorbed.
	if b, _ := os.ReadFile(filepath.Join(x.Dir, "home", harness.StateDir, "brain.db")); !keyHeader.Match(b) {
		t.Fatal("the sandbox's brain.db holds no key header, so the case proves nothing")
	}
	r.on("leak", "gh"+"o_"+strings.Repeat("C", 36))
	res, err = Run(context.Background(), o, []Row{named("a5-leak", "p", "true")})
	if err != nil {
		t.Fatal(err)
	}
	r.clean(res)
	wantVerdict(t, one(t, res), "FAIL", ReasonCredentials)
}

// keyHeader is the credential scan's private-key shape (internal/sandbox
// creds.go), which this package no longer reaches.
var keyHeader = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
