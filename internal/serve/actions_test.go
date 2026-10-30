package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

// counted is a fake action that counts its calls and writes text.
func counted(n *atomic.Int32, text string, err error) func(io.Writer) error {
	return func(w io.Writer) error {
		n.Add(1)
		_, _ = io.WriteString(w, text)
		return err
	}
}

// actionReq is an action's POST under the prefix with every part a browser's
// form sends: the Host, the cookie, the form media type, the Origin, and a
// body holding the run's csrf. Each refusal case takes one part away.
func actionReq(r *Run, target, body string) *http.Request {
	req := httptest.NewRequest("POST", r.base+target, strings.NewReader(body))
	req.Host = r.host
	req.AddCookie(&http.Cookie{Name: r.cookie, Value: r.Token()})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1:3999")
	return req
}

// actionRun is a run whose two actions are counting fakes.
func actionRun(t *testing.T, f *fix) (*Run, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var index, ingest atomic.Int32
	o := f.options(nil)
	o.Index = counted(&index, "files    1\n", nil)
	o.Ingest = counted(&ingest, "metrics: 0 sessions, 0 steps, 0 events added; 0 sources failed\n", nil)
	r, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	return r, &index, &ingest
}

func TestActionCSRF(t *testing.T) {
	r, index, ingest := actionRun(t, newFix(t))
	csrf := "csrf=" + r.CSRF()
	if wrongToken(r.CSRF()) == r.CSRF() {
		t.Fatal("precondition: the wrong csrf equals the right one")
	}
	for _, path := range []string{"/actions/index", "/actions/metrics-ingest"} {
		for name, req := range refusedPosts(r, path, csrf) {
			want := lineForbidden
			if strings.HasPrefix(name, "Sec-Fetch-Site") {
				want = lineFetchSite
			}
			if w := serve(r, req); w.Code != 403 || w.Body.String() != want+"\n" {
				t.Errorf("%s, %s: %d %q, want 403 %s", path, name, w.Code, w.Body.String(), want)
			}
		}
	}
	if index.Load() != 0 || ingest.Load() != 0 {
		t.Fatalf("a refused POST ran: index %d, ingest %d", index.Load(), ingest.Load())
	}

	// With every part, with a charset parameter, and with no Origin (a client
	// that holds both tokens), it runs.
	charset := actionReq(r, "/actions/index", csrf)
	charset.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	noOrigin := actionReq(r, "/actions/index", csrf)
	noOrigin.Header.Del("Origin")
	for i, req := range []*http.Request{actionReq(r, "/actions/index", csrf), charset, noOrigin} {
		w := serve(r, req)
		if w.Code != 200 || index.Load() != int32(i+1) || !strings.Contains(w.Body.String(), "<p>ok</p>") || !strings.Contains(w.Body.String(), "files    1") {
			t.Errorf("POST %d: %d, %d calls\n%s", i, w.Code, index.Load(), w.Body.String())
		}
	}
	if w := serve(r, actionReq(r, "/actions/metrics-ingest", csrf)); w.Code != 200 || ingest.Load() != 1 || !strings.Contains(w.Body.String(), "0 sources failed") {
		t.Errorf("POST /actions/metrics-ingest: %d, %d calls\n%s", w.Code, ingest.Load(), w.Body.String())
	}
	if index.Load() != 3 {
		t.Errorf("the ingest ran the index: %d calls", index.Load())
	}
}

// refusedPosts is every way an action POST to path misses step 5 (or the
// guard's step 2 or 3), by name, each otherwise the full request.
func refusedPosts(r *Run, path, csrf string) map[string]*http.Request {
	refused := map[string]*http.Request{
		"a token query":     actionReq(r, path+"?token="+r.Token(), csrf),
		"no csrf":           actionReq(r, path, ""),
		"a wrong csrf":      actionReq(r, path, "csrf="+wrongToken(r.CSRF())),
		"two csrf fields":   actionReq(r, path, csrf+"&"+csrf),
		"csrf in the query": actionReq(r, path+"?"+csrf, ""),
		"a 5 KiB body":      actionReq(r, path, csrf+"&pad="+strings.Repeat("x", 5<<10)),
	}
	noCookie := httptest.NewRequest("POST", r.base+path, strings.NewReader(csrf))
	noCookie.Host = r.host
	noCookie.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refused["no cookie"] = noCookie
	set := func(name, header, value string) {
		req := actionReq(r, path, csrf)
		req.Header.Set(header, value)
		refused[name] = req
	}
	set("Origin null", "Origin", "null")
	set("a foreign Origin", "Origin", "http://127.0.0.1:4000")
	set("another site", "Origin", "http://evil.example")
	set("text/plain", "Content-Type", "text/plain")
	set("multipart", "Content-Type", "multipart/form-data; boundary=x")
	set("no media type", "Content-Type", "")
	set("Sec-Fetch-Site cross-site", "Sec-Fetch-Site", "cross-site")
	set("Sec-Fetch-Site same-site", "Sec-Fetch-Site", "same-site")
	twoOrigins := actionReq(r, path, csrf)
	twoOrigins.Header.Add("Origin", "http://evil.example")
	refused["a second Origin"] = twoOrigins
	return refused
}

// One action at a time, across both: a POST while one runs is 409 and runs
// nothing; once it ends, the next one runs.
func TestActionBusy(t *testing.T) {
	f := newFix(t)
	o := f.options(nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var index, ingest atomic.Int32
	o.Index = func(w io.Writer) error {
		if index.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	}
	o.Ingest = counted(&ingest, "", nil)
	r, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "csrf=" + r.CSRF()
	first := make(chan int, 1)
	go func() { first <- serve(r, actionReq(r, "/actions/index", csrf)).Code }()
	select {
	case <-entered:
	case code := <-first:
		t.Fatalf("precondition: the first action returned %d without blocking", code)
	}
	for _, p := range []string{"/actions/index", "/actions/metrics-ingest"} {
		if w := serve(r, actionReq(r, p, csrf)); w.Code != 409 || !strings.Contains(w.Body.String(), "<p>an action is running</p>") {
			t.Errorf("%s while one runs: %d\n%s", p, w.Code, w.Body.String())
		}
	}
	// Step 5 comes before the lock: a refused POST learns nothing about a
	// running action (plan D52).
	if w := serve(r, actionReq(r, "/actions/metrics-ingest", "")); w.Code != 403 {
		t.Errorf("a POST without csrf while one runs: %d, want 403", w.Code)
	}
	if w := get(t, r, "GET", "/dream"); w.Code != 200 {
		t.Errorf("a page while an action runs: %d", w.Code)
	}
	close(release)
	if code := <-first; code != 200 {
		t.Errorf("the first action: %d", code)
	}
	if index.Load() != 1 || ingest.Load() != 0 {
		t.Fatalf("a refused POST ran: index %d, ingest %d", index.Load(), ingest.Load())
	}
	if w := serve(r, actionReq(r, "/actions/metrics-ingest", csrf)); w.Code != 200 || ingest.Load() != 1 {
		t.Errorf("an action after the first ended: %d, %d calls", w.Code, ingest.Load())
	}

	// The no-project line comes before the lock too: outside a checkout, the
	// index says why while an ingest runs.
	o.Dir = t.TempDir()
	entered, release = make(chan struct{}), make(chan struct{})
	o.Ingest = func(io.Writer) error { close(entered); <-release; return nil }
	if r, err = NewRun(o); err != nil {
		t.Fatal(err)
	}
	csrf = "csrf=" + r.CSRF()
	done := make(chan struct{})
	go func() { serve(r, actionReq(r, "/actions/metrics-ingest", csrf)); close(done) }()
	select {
	case <-entered:
	case <-done:
		t.Fatal("precondition: the ingest returned without blocking")
	}
	if w := serve(r, actionReq(r, "/actions/index", csrf)); w.Code != 200 || !strings.Contains(w.Body.String(), "is not a git checkout") {
		t.Errorf("POST /actions/index outside a checkout while an ingest runs: %d\n%s", w.Code, w.Body.String())
	}
	close(release)
	<-done
}

// An action's output is cut at 1 MiB with a line saying so; the action
// itself still succeeds.
func TestActionOutputCapped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		n     int
		trunc bool
	}{{"exactly 1 MiB", 1 << 20, false}, {"one byte over", 1<<20 + 1, true}, {"twice over", 2 << 20, true}} {
		f := newFix(t)
		o := f.options(nil)
		o.Index = func(w io.Writer) error {
			half := strings.Repeat("x", tc.n/2)
			for _, s := range []string{half, half, strings.Repeat("x", tc.n%2)} {
				if _, err := io.WriteString(w, s); err != nil {
					return err
				}
			}
			return nil
		}
		r, err := NewRun(o)
		if err != nil {
			t.Fatal(err)
		}
		w := serve(r, actionReq(r, "/actions/index", "csrf="+r.CSRF()))
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "<p>ok</p>") {
			t.Errorf("%s: %d", tc.name, w.Code)
		}
		pre := preOf(body)
		if got := strings.Count(pre, "x"); got != min(tc.n, 1<<20) {
			t.Errorf("%s: %d bytes shown, want %d", tc.name, got, min(tc.n, 1<<20))
		}
		if got := strings.HasSuffix(pre, "\n[output truncated]\n"); got != tc.trunc {
			t.Errorf("%s: truncated line %v, want %v (tail %q)", tc.name, got, tc.trunc, pre[max(0, len(pre)-40):])
		}
	}
}

// An action's output is text: escaped by the template, terminal bytes made
// printable, and a character cut at 1 MiB shown as U+FFFD, never a broken
// byte.
func TestActionOutputEscaped(t *testing.T) {
	f := newFix(t)
	o := f.options(nil)
	o.Index = printer("<script>alert(1)</script> & \x1b[2J\n", nil)
	o.Ingest = printer(strings.Repeat("x", 1<<20-1)+"é", nil)
	r, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	body := serve(r, actionReq(r, "/actions/index", "csrf="+r.CSRF())).Body.String()
	if strings.Contains(strings.ToLower(body), "<script") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt; &amp; ?[2J") {
		t.Errorf("the output is not escaped text:\n%s", preOf(body))
	}
	pre := preOf(serve(r, actionReq(r, "/actions/metrics-ingest", "csrf="+r.CSRF())).Body.String())
	if !utf8.ValidString(pre) || !strings.HasSuffix(pre, "x\uFFFD\n[output truncated]\n") {
		t.Errorf("a character cut at the cap: valid %v, tail %q", utf8.ValidString(pre), pre[max(0, len(pre)-40):])
	}
}

// A failing action is 500 with its output so far and `failed: <error>`,
// logged once without the query.
func TestActionFails(t *testing.T) {
	f := newFix(t)
	o := f.options(nil)
	var errOut strings.Builder
	o.Err = &errOut
	o.Ingest = printer("metrics: 0 sessions, 0 steps, 0 events added; 2 sources failed\n", errBoom)
	r, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	w := serve(r, actionReq(r, "/actions/metrics-ingest", "csrf="+r.CSRF()))
	body := w.Body.String()
	if w.Code != 500 || !strings.Contains(body, "2 sources failed") || !strings.Contains(body, "<p>failed: boom</p>") || strings.Contains(body, "<p>ok</p>") {
		t.Errorf("a failing action: %d\n%s", w.Code, body)
	}
	if got := errOut.String(); got != "fyr serve: POST /actions/metrics-ingest: boom\n" {
		t.Errorf("logged %q", got)
	}
}
