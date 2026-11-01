package serve

import (
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
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
	for p, line := range map[string]string{"/records": "brain store: ", "/memory": "brain store: ", "/metrics/report": "metrics store: ", "/evals": "metrics store: "} {
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
	if w := get(t, r, "GET", "/"); w.Code != 200 {
		t.Errorf("/: %d\n%s", w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/metrics"); w.Code != 200 || !strings.Contains(w.Body.String(), "no metrics store at "+path+"; run fyr metrics ingest") {
		t.Errorf("/metrics: %d\n%s", w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/metrics/report"); w.Code != 404 || !strings.Contains(w.Body.String(), "no metrics store at") {
		t.Errorf("/metrics/report: %d\n%s", w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/evals"); w.Code != 200 || !strings.Contains(w.Body.String(), "no metrics store at "+path) || !strings.Contains(w.Body.String(), f.ids["eval"]) {
		t.Errorf("/evals: %d, want the missing store's line and the records\n%s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a page created the metrics store: %v", err)
	}

	// An empty home outside a checkout creates neither brain.db nor metrics.db.
	emptyO := f.options(io.Discard)
	emptyO.Home, emptyO.Dir = t.TempDir(), t.TempDir()
	rEmpty, err := NewRun(emptyO)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/records", "/records?scope=global", "/memory", "/search?q=quokka", "/evals", "/metrics"} {
		if w := get(t, rEmpty, "GET", p); w.Code != 200 {
			t.Errorf("empty home %s: %d\n%s", p, w.Code, w.Body.String())
		}
	}
	if w := get(t, rEmpty, "GET", "/records/NOPE-1?scope=global"); w.Code != 404 {
		t.Errorf("empty home /records/NOPE-1?scope=global: %d\n%s", w.Code, w.Body.String())
	}
	for _, db := range []string{brain.Path(emptyO.Home), metrics.Path(emptyO.Home)} {
		if _, err := os.Lstat(db); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a read page created %s: %v", db, err)
		}
	}
}

func TestNoProject(t *testing.T) {
	f := newFix(t)
	if _, err := trail.Register(filepath.Join(f.home, harness.StateDir), f.repo, time.Now()); err != nil {
		t.Fatal(err)
	}
	repo2 := t.TempDir()
	git(t, repo2, "init", "-q")
	write(t, filepath.Join(repo2, "README.md"), "# two\n")
	git(t, repo2, "add", ".")
	git(t, repo2, "commit", "-qm", "init")
	bug2 := f.add(t, brain.Options{Home: f.home, Dir: repo2}, "bug", "Second bug", "body from repo2\n")
	if bug2 != f.ids["xss"] {
		t.Fatalf("precondition: expected duplicate bug ID %s, got %s", f.ids["xss"], bug2)
	}

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
	if w := get(t, r, "GET", "/check"); w.Code != 200 || !strings.Contains(w.Body.String(), "<p>"+line+"</p>") {
		t.Errorf("/check: %d, want 200 with the line\n%s", w.Code, w.Body.String())
	}
	scopeQ := url.Values{"scope": {f.repo}}.Encode()
	scope2Q := url.Values{"scope": {repo2}}.Encode()
	for _, p := range []string{"/", "/context", "/metrics", "/metrics/report", "/dream", "/records", "/records?scope=global",
		"/records?" + scopeQ, "/records/" + f.ids["plan"], "/records/" + f.ids["plan"] + "?" + scopeQ,
		"/records/" + bug2 + "?" + scope2Q, "/records/" + f.ids["global"] + "?scope=global", "/search?q=quokka", "/memory", "/evals"} {
		w := get(t, r, "GET", p)
		if w.Code != 200 || strings.Contains(w.Body.String(), "<p>"+line+"</p>") {
			t.Errorf("%s: %d, want 200 without no-project line\n%s", p, w.Code, w.Body.String())
		}
	}
	homeBody := get(t, r, "GET", "/").Body.String()
	if strings.Contains(homeBody, "/actions/index") || !strings.Contains(homeBody, "/actions/metrics-ingest") || !strings.Contains(homeBody, "<h2>projects</h2>") {
		t.Errorf("outside-project /: want metrics-ingest only and projects table\n%s", homeBody)
	}
	if memBody := get(t, r, "GET", "/memory").Body.String(); !strings.Contains(memBody, f.repo+"\n- quokka-fact (project) — quokkas smile") {
		t.Errorf("outside-project /memory:\n%s", memBody)
	}
	if searchBody := get(t, r, "GET", "/search?q=quokka").Body.String(); !strings.Contains(searchBody, f.ids["plan"]) || !strings.Contains(searchBody, f.ids["global"]) {
		t.Errorf("outside-project /search:\n%s", searchBody)
	}
	if w := get(t, r, "GET", "/records/"+bug2); w.Code != 400 || !strings.Contains(w.Body.String(), "ambiguous record "+bug2) {
		t.Errorf("ambiguous /records/%s: %d\n%s", bug2, w.Code, w.Body.String())
	}
	if w := get(t, r, "GET", "/records?scope=/nope"); w.Code != 400 || !strings.Contains(w.Body.String(), "scope must be global or a known project") {
		t.Errorf("/records?scope=/nope: %d\n%s", w.Code, w.Body.String())
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

func TestOverviewAndScopedMetrics(t *testing.T) {
	f := newFix(t)
	repo2 := t.TempDir()
	git(t, repo2, "init", "-q")
	write(t, filepath.Join(repo2, "README.md"), "# two\n")
	git(t, repo2, "add", ".")
	git(t, repo2, "commit", "-qm", "init")
	f.add(t, brain.Options{Home: f.home, Dir: repo2}, "plan", "Plan wombat", "## Phases\n- [ ] one\n")

	repo3 := t.TempDir()
	git(t, repo3, "init", "-q")
	write(t, filepath.Join(repo3, ".gitignore"), ".agents/scratch/*\n")
	write(t, filepath.Join(repo3, ".agents", "trail.jsonl"), "")
	write(t, filepath.Join(repo3, "README.md"), "# three\n")
	git(t, repo3, "add", ".")
	git(t, repo3, "commit", "-qm", "init")
	if _, err := trail.Register(filepath.Join(f.home, harness.StateDir), repo3, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := search.Index(search.Options{Home: f.home, Dir: repo3}, io.Discard); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", metrics.Path(f.home))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Hour).UnixMilli()
	for _, row := range []struct {
		id, root string
		tok      int64
	}{
		{"S1", f.repo, 111},
		{"S2", filepath.Join(f.repo, "sub"), 222},
		{"S3", repo2, 9999},
	} {
		if _, err := db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`, row.id, row.root, now, now+1000); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO steps(session_id, ext_id, kind, ts, tok_in, tok_out, context) VALUES (?, ?, 'generation', ?, ?, 0, 5000)`, row.id, row.id+"-e1", now, row.tok); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	// Inside f.repo: / and /metrics and /metrics/report show only f.repo (S1 + S2 = 2 sessions, 333 tokens), never repo2 (9999).
	rIn := f.run(t)
	inHome := get(t, rIn, "GET", "/").Body.String()
	if !strings.Contains(inHome, "<h2>project</h2>") || !strings.Contains(inHome, "<td>333</td>") || strings.Contains(inHome, repo2) {
		t.Errorf("inside-project /:\n%s", inHome)
	}
	inMetrics := get(t, rIn, "GET", "/metrics").Body.String()
	if strings.Contains(inMetrics, "<h2>projects (the last 30 days)</h2>") || !strings.Contains(inMetrics, "333") || strings.Contains(inMetrics, "9999") {
		t.Errorf("inside-project /metrics:\n%s", inMetrics)
	}
	inReport := get(t, rIn, "GET", "/metrics/report").Body.String()
	if !strings.Contains(inReport, ">2<") || strings.Contains(inReport, "10332") {
		t.Errorf("inside-project /metrics/report includes other project's sessions:\n%s", inReport)
	}

	// Outside any project: / and /metrics show both canonical projects (with f.repo/sub rolled into f.repo), and /metrics/report aggregates all 3 sessions (10332 tokens).
	o := f.options(io.Discard)
	o.Dir = t.TempDir()
	rOut, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	outHome := get(t, rOut, "GET", "/").Body.String()
	if !strings.Contains(outHome, "<h2>projects</h2>") || !strings.Contains(outHome, f.repo) || !strings.Contains(outHome, repo2) || !strings.Contains(outHome, repo3) || strings.Contains(outHome, filepath.Join(f.repo, "sub")) {
		t.Errorf("outside-project /:\n%s", outHome)
	}
	outMetrics := get(t, rOut, "GET", "/metrics").Body.String()
	if !strings.Contains(outMetrics, "<h2>projects (the last 30 days)</h2>") || !strings.Contains(outMetrics, repo3) || !strings.Contains(outMetrics, "<td>333</td>") || !strings.Contains(outMetrics, "<td>9999</td>") || !strings.Contains(outMetrics, "10332") {
		t.Errorf("outside-project /metrics:\n%s", outMetrics)
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
