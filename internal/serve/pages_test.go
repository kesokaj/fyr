package serve

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

var errBoom = errors.New("boom")

// readPages is every read page, with the parameters that reach its reads.
func (f *fix) readPages() []string {
	return []string{"/", "/context", "/records", "/records?kind=plan&open=1", "/records?scope=global",
		"/records/" + f.ids["plan"], "/records/" + f.ids["global"] + "?scope=global", "/search", "/search?q=quokka",
		"/search?q=quokka&kind=plan", "/memory", "/metrics", "/metrics/report", "/evals", "/dream", "/check", "/static/style.css"}
}

func TestPages(t *testing.T) {
	f := newFix(t)
	r := f.run(t)
	want := map[string][]string{
		"/":                         {"installed  nothing — run `fyr install`\nsecond\tcolumn ?[2J\n", "fyr vtest", "dream: ", f.repo},
		"/context":                  {"# the context"},
		"/records":                  {f.ids["plan"], "Plan quokka", f.ids["eval"], `href="` + r.Base() + `/records/` + f.ids["plan"] + `"`},
		"/records?scope=global":     {f.ids["global"], `href="` + r.Base() + `/records/` + f.ids["global"] + `?scope=global"`},
		"/records/" + f.ids["plan"]: {f.ids["plan"] + "  plan", "- [ ] one"},
		"/memory":                   {"quokka-fact"},
		"/metrics":                  {`href="` + r.Base() + `/metrics/report"`},
		"/evals":                    {f.ids["eval"]},
		"/dream":                    {"2026-10-29 dream ran"},
		"/check":                    {"check: 0 findings"},
	}
	for _, p := range f.readPages() {
		w := get(t, r, "GET", p)
		if w.Code != 200 {
			t.Errorf("%s: %d\n%s", p, w.Code, w.Body.String())
			continue
		}
		body := w.Body.String()
		if strings.Contains(strings.ToLower(body), "<script") {
			t.Errorf("%s: holds <script", p)
		}
		// Every link and form stays under the prefix, where the cookie goes.
		for _, m := range regexp.MustCompile(`(?:href|action)="([^"]*)"`).FindAllStringSubmatch(body, -1) {
			if !strings.HasPrefix(m[1], r.Base()+"/") {
				t.Errorf("%s: %q leaves the prefix", p, m[1])
			}
		}
		for _, s := range want[p] {
			if !strings.Contains(body, s) {
				t.Errorf("%s: no %q in\n%s", p, s, body)
			}
		}
	}
	// Multi-line verb text keeps its lines: printable runs per line.
	if pre := preOf(get(t, r, "GET", "/search?q=quokka").Body.String()); strings.Count(pre, "\n") < 2 {
		t.Errorf("/search's file hits lost their lines: %q", pre)
	}
	// The action forms: both on /, the ingest on /metrics, each a POST with
	// the run's csrf; the /search form is a GET and carries none (plan D46).
	csrf := `<input type="hidden" name="csrf" value="` + r.CSRF() + `">`
	for p, actions := range map[string][]string{"/": {"/actions/index", "/actions/metrics-ingest"}, "/metrics": {"/actions/metrics-ingest"}} {
		body := get(t, r, "GET", p).Body.String()
		if n := strings.Count(body, `<form method="post"`); n != len(actions) || strings.Count(body, csrf) != len(actions) {
			t.Errorf("%s: %d post forms, want %d, each with the csrf\n%s", p, n, len(actions), body)
		}
		for _, a := range actions {
			if !strings.Contains(body, `action="`+r.Base()+a+`"`) {
				t.Errorf("%s: no form for %s", p, a)
			}
		}
	}
	for _, p := range f.readPages() {
		if p != "/" && p != "/metrics" && strings.Contains(get(t, r, "GET", p).Body.String(), r.CSRF()) {
			t.Errorf("%s carries the csrf", p)
		}
	}
	if body := get(t, r, "GET", "/evals").Body.String(); strings.Contains(body, f.ids["test"]) {
		t.Errorf("/evals lists a testing record fyr eval did not write")
	}
	rep := get(t, r, "GET", "/metrics/report")
	if ct := rep.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" || strings.Contains(rep.Body.String(), "<nav>") {
		t.Errorf("/metrics/report: %q, or it carries the layout", ct)
	}
	for _, bad := range []string{"<script", "http://", "https://", "@import", "url("} {
		if strings.Contains(rep.Body.String(), bad) {
			t.Errorf("/metrics/report holds %q", bad)
		}
	}
}

func TestBodyEscaped(t *testing.T) {
	f := newFix(t)
	body := get(t, f.run(t), "GET", "/records/"+f.ids["xss"]).Body.String()
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") || strings.Contains(body, "<script>") {
		t.Errorf("the body is not escaped:\n%s", body)
	}
}

func TestSearchAndRecordParams(t *testing.T) {
	f := newFix(t)
	r := f.run(t)
	body := get(t, r, "GET", "/search?q=quokka").Body.String()
	rec, file := strings.Index(body, `href="`+r.Base()+`/records/`+f.ids["plan"]+`"`), strings.Index(body, "README.md")
	if !strings.Contains(body, `records: `) || rec < 0 || file < 0 || rec > file {
		t.Errorf("/search: record hits linked, then file hits: rec %d file %d\n%s", rec, file, body)
	}
	if strings.Contains(body, `README.md"`) {
		t.Errorf("/search links a file hit")
	}
	for p, line := range map[string]string{
		"/search?q=" + strings.Repeat("a", 201):  "q is over 200 bytes",
		"/search?q=quokka&kind=nope":             "--kind must be one of",
		"/records?kind=nope":                     "unknown kind nope; one of adr bug",
		"/records?scope=x":                       "scope takes one value, global",
		"/records?scope=global&scope=global":     "scope takes one value, global",
		"/records/" + f.ids["plan"] + "?scope=x": "scope takes one value, global",
	} {
		if w := get(t, r, "GET", p); w.Code != 400 || !strings.Contains(w.Body.String(), line) {
			t.Errorf("%s: %d, want 400 with %q\n%s", p, w.Code, line, w.Body.String())
		}
	}
	if w := get(t, r, "GET", "/records/NOPE-x"); w.Code != 404 || !strings.Contains(w.Body.String(), "no record NOPE-x") || !strings.Contains(w.Body.String(), "<nav>") {
		t.Errorf("/records/NOPE-x: %d\n%s", w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/records/nope"); w.Code != 404 || w.Body.String() != "not found\n" {
		t.Errorf("/records/nope: %d %q", w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/search?q=+++"); w.Code != 200 || !strings.Contains(w.Body.String(), `<form method="get" action="`+r.Base()+`/search">`) {
		t.Errorf("/search with blank q: %d", w.Code)
	}
}

func TestStoreErrorIsolated(t *testing.T) {
	f := newFix(t)
	conf := filepath.Join(f.home, harness.StateDir)
	for _, db := range []string{"brain.db", "metrics.db"} {
		for _, sfx := range []string{"", "-wal", "-shm"} {
			os.Remove(filepath.Join(conf, db+sfx))
		}
		write(t, filepath.Join(conf, db), strings.Repeat("not a database ", 20))
	}
	r := f.run(t)
	for p, line := range map[string]string{"/records": "brain store: ", "/memory": "brain store: ", "/metrics/report": "metrics store: "} {
		if w := get(t, r, "GET", p); w.Code != 500 || !strings.Contains(w.Body.String(), line) {
			t.Errorf("%s: %d, want 500 with %q\n%s", p, w.Code, line, w.Body.String())
		}
	}
	for _, p := range []string{"/", "/dream", "/metrics", "/context"} {
		if w := get(t, r, "GET", p); w.Code != 200 {
			t.Errorf("%s: %d, want 200 beside a broken store", p, w.Code)
		}
	}
}

func TestMetricsAbsentCreatesNothing(t *testing.T) {
	f := newFix(t)
	path := metrics.Path(f.home)
	for _, sfx := range []string{"", "-wal", "-shm"} {
		os.Remove(path + sfx)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("precondition: the store is still there: %v", err)
	}
	r := f.run(t)
	if w := get(t, r, "GET", "/metrics"); w.Code != 200 || !strings.Contains(w.Body.String(), "no metrics store at "+path+"; run fyr metrics ingest") {
		t.Errorf("/metrics: %d\n%s", w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/metrics/report"); w.Code != 404 || !strings.Contains(w.Body.String(), "no metrics store at") {
		t.Errorf("/metrics/report: %d\n%s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a page created the metrics store: %v", err)
	}
}

func TestNoProject(t *testing.T) {
	f := newFix(t)
	o := f.options(nil)
	o.Dir = t.TempDir()
	r, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	line := "no project: " + o.Dir + " is not a git checkout"
	if r.projectLine() != line {
		t.Fatalf("project line %q", r.projectLine())
	}
	for _, p := range []string{"/records", "/records/" + f.ids["plan"], "/search?q=quokka", "/memory", "/evals", "/check"} {
		w := get(t, r, "GET", p)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<p>"+line+"</p>") {
			t.Errorf("%s: %d, want 200 with the line\n%s", p, w.Code, w.Body.String())
		}
	}
	for _, p := range []string{"/", "/context", "/metrics", "/metrics/report", "/dream", "/records?scope=global", "/records/" + f.ids["global"] + "?scope=global"} {
		if w := get(t, r, "GET", p); w.Code != 200 || strings.Contains(w.Body.String(), "<p>"+line+"</p>") {
			t.Errorf("%s: %d, want it served", p, w.Code)
		}
	}
	// The index needs the project and runs nothing; the ingest is the home's
	// and runs.
	var index, ingest atomic.Int32
	o.Index, o.Ingest = counted(&index, "", nil), counted(&ingest, "", nil)
	if r, err = NewRun(o); err != nil {
		t.Fatal(err)
	}
	csrf := "csrf=" + r.CSRF()
	if w := serve(r, actionReq(r, "/actions/index", csrf)); w.Code != 200 || !strings.Contains(w.Body.String(), "<p>"+line+"</p>") || index.Load() != 0 {
		t.Errorf("POST /actions/index: %d, %d calls, want 200 with the line and none\n%s", w.Code, index.Load(), w.Body.String())
	}
	if w := serve(r, actionReq(r, "/actions/metrics-ingest", csrf)); w.Code != 200 || ingest.Load() != 1 {
		t.Errorf("POST /actions/metrics-ingest: %d, %d calls, want it run", w.Code, ingest.Load())
	}
}

// preOf is the last <pre> of a page.
func preOf(body string) string {
	i := strings.LastIndex(body, "<pre>")
	j := strings.LastIndex(body, "</pre>")
	if i < 0 || j < i {
		return ""
	}
	return body[i+len("<pre>") : j]
}

// One /check at a time: a second request while one runs is 409 and runs
// nothing; once it ends, the next one runs (plan D49).
func TestCheckOneAtATime(t *testing.T) {
	f := newFix(t)
	o := f.options(nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	o.Check = func(w io.Writer) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		_, err := io.WriteString(w, "check: 0 findings\n")
		return err
	}
	r, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan int, 1)
	go func() { first <- get(t, r, "GET", "/check").Code }()
	<-entered
	if w := get(t, r, "GET", "/check"); w.Code != 409 || !strings.Contains(w.Body.String(), "fyr check is already running; reload when it finishes") {
		t.Errorf("a second /check: %d\n%s", w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/dream"); w.Code != 200 {
		t.Errorf("another page while /check runs: %d", w.Code)
	}
	close(release)
	if code := <-first; code != 200 {
		t.Errorf("the first /check: %d", code)
	}
	if w := get(t, r, "GET", "/check"); w.Code != 200 || calls.Load() != 2 {
		t.Errorf("/check after the first ended: %d, %d calls", w.Code, calls.Load())
	}
}
