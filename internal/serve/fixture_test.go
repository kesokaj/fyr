package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// TestMain drops every GIT_* variable before the package's tests run, as
// the other packages that run git in a fixture do.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	// Nor the operator's git config: a global hooksPath or signing key
	// never reaches a fixture commit (the v5.0.0 gate's security review).
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// A HOME of its own: a page that wrote under the operator's real home
	// would reach the real store (the v5.0.0 gate's security review).
	home, err := os.MkdirTemp("", "fyr-serve-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

const port = 3999

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fix is a scratch home and a project checkout, seeded: a plan, an eval
// testing record, a record whose body holds a script tag, a global record,
// a memory, a metrics store at the current schema and a dream log.
type fix struct {
	home, repo string
	ids        map[string]string
}

func newFix(t *testing.T) *fix {
	t.Helper()
	f := &fix{home: t.TempDir(), repo: t.TempDir(), ids: map[string]string{}}
	git(t, f.repo, "init", "-q")
	write(t, filepath.Join(f.repo, ".gitignore"), ".agents/scratch/*\n")
	write(t, filepath.Join(f.repo, "README.md"), "# demo\n\nThe quokka lives here.\n")
	git(t, f.repo, "add", ".")
	git(t, f.repo, "commit", "-qm", "init")
	o := brain.Options{Home: f.home, Dir: f.repo}
	f.ids["plan"] = f.add(t, o, "plan", "Plan quokka", "## Phases\n- [ ] one\n")
	f.ids["eval"] = f.add(t, o, "testing", "Eval — 2 of 2 PASS at abc123", "rows\n")
	f.ids["test"] = f.add(t, o, "testing", "Verifier log", "PASS\n")
	f.ids["xss"] = f.add(t, o, "bug", "Escaping", "<script>alert(1)</script>\n")
	f.ids["global"] = f.add(t, brain.Options{Home: f.home, Dir: f.repo, Global: true}, "feedback", "Quokka feedback", "kept\n", "/p")
	if _, err := brain.MemoryAdd(o, brain.MemoryEntry{Type: "project", Name: "quokka-fact", Description: "quokkas smile"}, strings.NewReader("They smile.\n")); err != nil {
		t.Fatal(err)
	}
	s, err := metrics.Open(metrics.Path(f.home))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	write(t, filepath.Join(f.home, harness.StateDir, "dream.log"), "2026-10-29 dream ran\n")
	return f
}

func (f *fix) add(t *testing.T, o brain.Options, kind, title, body string, project ...string) string {
	t.Helper()
	in := brain.AddInput{Kind: kind, Title: title, Body: strings.NewReader(body)}
	if len(project) > 0 {
		in.Project = project[0]
	}
	id, err := brain.Add(o, in)
	if err != nil {
		t.Fatalf("add %s: %v", kind, err)
	}
	return id
}

// printer is a fake page printer.
func printer(text string, err error) func(io.Writer) error {
	return func(w io.Writer) error {
		_, _ = io.WriteString(w, text)
		return err
	}
}

func (f *fix) options(errOut io.Writer) Options {
	return Options{Home: f.home, Dir: f.repo, Port: port, Out: io.Discard, Err: errOut, Version: "vtest",
		Status: printer("installed  nothing — run `fyr install`\nsecond\tcolumn \x1b[2J\n", nil),
		Render: printer("# the context\n", nil),
		Check:  printer("check: 0 findings\n", nil),
		Index:  printer("files    1\n", nil),
		Ingest: printer("metrics: 0 sessions, 0 steps, 0 events added; 0 sources failed\n", nil)}
}

func (f *fix) run(t *testing.T) *Run {
	t.Helper()
	r, err := NewRun(f.options(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// get is an authenticated request under the run's prefix: the right Host
// and the run's cookie.
func get(t *testing.T, r *Run, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, r.base+target, nil)
	req.Host = r.host
	req.AddCookie(&http.Cookie{Name: r.cookie, Value: r.Token()})
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, req)
	return w
}

// bare is a request under the run's prefix with the right Host and nothing
// else.
func bare(r *Run, method, target string) *http.Request {
	return raw(method, r.base+target)
}

// raw is a request for target as given, with the right Host.
func raw(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:3999"
	return req
}

func serve(r *Run, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, req)
	return w
}
