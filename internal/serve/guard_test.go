package serve

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestTokenSetsCookieAndRedirects(t *testing.T) {
	r := newFix(t).run(t)
	b := r.Base()
	if !regexp.MustCompile(`^/s/[A-Za-z0-9_-]{43}$`).MatchString(b) {
		t.Fatalf("prefix %q", b)
	}
	for _, c := range []struct{ method, target, want string }{
		{"GET", b + "/?token=" + r.Token(), b + "/"},
		{"HEAD", b + "/?token=" + r.Token(), b + "/"},
		{"GET", b + "/records?kind=plan&token=" + r.Token() + "&open=1", b + "/records?kind=plan&open=1"},
		{"GET", "//evil.example/x?token=" + r.Token(), "/evil.example/x"},
		{"GET", `/\evil.example?token=` + r.Token(), "/%5Cevil.example"},
		{"GET", "/%5C%5Cevil.example?token=" + r.Token(), "/%5C%5Cevil.example"},
	} {
		w := serve(r, raw(c.method, c.target))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s %s: %d, want 303", c.method, c.target, w.Code)
		}
		if got := w.Header().Get("Location"); got != c.want {
			t.Errorf("%s %s: Location %q, want %q", c.method, c.target, got, c.want)
		}
		ck := w.Result().Cookies()
		if len(ck) != 1 || ck[0].Name != "fyr_serve_3999" || ck[0].Value != r.Token() || !ck[0].HttpOnly ||
			ck[0].SameSite != http.SameSiteStrictMode || ck[0].Path != b || ck[0].Secure || !ck[0].Expires.IsZero() {
			t.Errorf("%s %s: cookie %+v", c.method, c.target, ck)
		}
		// The cookie alone then serves the page.
		req := bare(r, "GET", "/")
		req.AddCookie(ck[0])
		if w := serve(r, req); w.Code != http.StatusOK {
			t.Errorf("with the cookie: %d, want 200", w.Code)
		}
	}
}

func TestRefusals(t *testing.T) {
	r := newFix(t).run(t)
	wrong := wrongToken(r.Token())
	if wrong == r.Token() {
		t.Fatal("precondition: the wrong token is the real one")
	}
	other, err := NewRun(newFix(t).options(nil))
	if err != nil {
		t.Fatal(err)
	}
	withCookie := func(req *http.Request, v string) *http.Request {
		req.AddCookie(&http.Cookie{Name: "fyr_serve_3999", Value: v})
		return req
	}
	host := func(h string) *http.Request {
		req := withCookie(bare(r, "GET", "/"), r.Token())
		req.Host = h
		return req
	}
	fetch := func(site string) *http.Request {
		req := withCookie(bare(r, "GET", "/"), r.Token())
		req.Header.Set("Sec-Fetch-Site", site)
		return req
	}
	for name, c := range map[string]struct {
		req  *http.Request
		code int
		body string
	}{
		"no token":               {bare(r, "GET", "/"), 403, lineForbidden},
		"no token, unknown path": {bare(r, "GET", "/nope"), 403, lineForbidden},
		// The cookie is checked before the prefix: without it, a path outside
		// /s/<P> is refused like one inside, so 403 or 404 tells nothing of P.
		"no token, outside the prefix": {raw("GET", "/"), 403, lineForbidden},
		"no token, another prefix":     {raw("GET", "/s/other/records"), 403, lineForbidden},
		"wrong token":                  {bare(r, "GET", "/?token="+wrong), 403, lineForbidden},
		"empty token":                  {bare(r, "GET", "/?token="), 403, lineForbidden},
		"repeated token":               {bare(r, "GET", "/?token="+r.Token()+"&token="+r.Token()), 403, lineForbidden},
		"token beside a cookie":        {withCookie(bare(r, "GET", "/?token=x"), r.Token()), 403, lineForbidden},
		"stale cookie":                 {withCookie(bare(r, "GET", "/"), other.Token()), 403, lineForbidden},
		"planted second cookie":        {withCookie(withCookie(bare(r, "GET", "/"), "planted"), r.Token()), 200, ""},
		"other cookie name":            {bare(r, "GET", "/"), 403, lineForbidden},
		"host localhost":               {host("localhost:3999"), 403, lineForbidden},
		"host evil":                    {host("evil.example:3999"), 403, lineForbidden},
		"host other port":              {host("127.0.0.1:4000"), 403, lineForbidden},
		"host no port":                 {host("127.0.0.1"), 403, lineForbidden},
		"fetch cross-site":             {fetch("cross-site"), 403, lineFetchSite},
		"fetch same-site":              {fetch("same-site"), 403, lineFetchSite},
		"fetch same-origin":            {fetch("same-origin"), 200, ""},
		"fetch none":                   {fetch("none"), 200, ""},
		"fetch absent":                 {withCookie(bare(r, "GET", "/"), r.Token()), 200, ""},
		"fetch cross-site token": {func() *http.Request {
			q := bare(r, "GET", "/?token="+r.Token())
			q.Header.Set("Sec-Fetch-Site", "cross-site")
			return q
		}(), 403, lineFetchSite},
	} {
		w := serve(r, c.req)
		if w.Code != c.code {
			t.Errorf("%s: %d, want %d", name, w.Code, c.code)
			continue
		}
		if c.code == 403 {
			if got := w.Body.String(); got != c.body+"\n" {
				t.Errorf("%s: body %q, want %q", name, got, c.body)
			}
			if strings.Contains(w.Body.String(), r.Token()) || w.Header().Get("Set-Cookie") != "" {
				t.Errorf("%s: a refusal carries the token or a cookie", name)
			}
		}
	}
}

// A token that starts with the character wrongToken would write is the case
// BUG-196 hit one run in 64: pinned for both characters it writes, and the
// old "x".
func TestWrongTokenIsWrong(t *testing.T) {
	r := newFix(t).run(t)
	for _, first := range []string{"x", "a", "b"} {
		r.token = first + r.Token()[1:]
		wrong := wrongToken(r.Token())
		if wrong == r.Token() {
			t.Errorf("token %s…: the wrong token is the real one", first)
			continue
		}
		if w := serve(r, bare(r, "GET", "/?token="+wrong)); w.Code != 403 {
			t.Errorf("token %s…: %d, want 403", first, w.Code)
		}
	}
}

// wrongToken is tok with its first character changed, so it differs from tok
// whatever tok starts with (BUG-196).
func wrongToken(tok string) string {
	if tok[0] == 'a' {
		return "b" + tok[1:]
	}
	return "a" + tok[1:]
}

func TestRoutesAndMethods(t *testing.T) {
	r := newFix(t).run(t)
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		for _, p := range []string{"/nope", "/static/", "/static/x.css", "/records/", "/records/bad id", "/records/lower-1", "/records/A-1/x",
			"/actions", "/actions/", "/actions/nope", "/actions/index/", "/actions/check"} {
			if w := get(t, r, m, (&url.URL{Path: p}).String()); w.Code != 404 || w.Body.String() != lineNotFound+"\n" {
				t.Errorf("%s %s: %d %q, want 404 not found", m, p, w.Code, w.Body.String())
			}
		}
	}
	paths := []string{"/records/PLAN-x"}
	for p := range pages {
		paths = append(paths, p)
	}
	for _, p := range paths {
		for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			w := get(t, r, m, p)
			if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" || w.Body.String() != lineMethod+"\n" {
				t.Errorf("%s %s: %d Allow %q, want 405 GET, HEAD", m, p, w.Code, w.Header().Get("Allow"))
			}
		}
	}
	for p := range actions {
		for _, m := range []string{"GET", "HEAD", "PUT", "DELETE", "PATCH"} {
			w := get(t, r, m, p)
			if w.Code != 405 || w.Header().Get("Allow") != "POST" {
				t.Errorf("%s %s: %d Allow %q, want 405 POST", m, p, w.Code, w.Header().Get("Allow"))
			}
		}
	}
	if len(actions) != 2 {
		t.Errorf("%d actions, want the two of plan D39", len(actions))
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFix(t)
	r := f.run(t)
	failing := f.options(nil)
	failing.Status = printer("partial\n", errBoom)
	failing.Err = &strings.Builder{}
	rf, err := NewRun(failing)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]*http.Response{
		"200":      get(t, r, "GET", "/").Result(),
		"HEAD":     get(t, r, "HEAD", "/").Result(),
		"303":      serve(r, bare(r, "GET", "/?token="+r.Token())).Result(),
		"400":      get(t, r, "GET", "/records?kind=nope").Result(),
		"403":      serve(r, bare(r, "GET", "/")).Result(),
		"404":      get(t, r, "GET", "/nope").Result(),
		"404 p":    get(t, r, "GET", "/records/NOPE-x").Result(),
		"405":      get(t, r, "POST", "/").Result(),
		"500":      get(t, rf, "GET", "/").Result(),
		"409":      busyCheck(t, f).Result(),
		"POST":     serve(r, actionReq(r, "/actions/index", "csrf="+r.CSRF())).Result(),
		"POST 403": serve(r, actionReq(r, "/actions/index", "")).Result(),
		"css":      get(t, r, "GET", "/static/style.css").Result(),
	}
	want := map[string]int{"200": 200, "HEAD": 200, "303": 303, "400": 400, "403": 403, "404": 404, "404 p": 404, "405": 405, "500": 500, "409": 409, "css": 200, "POST": 200, "POST 403": 403}
	for name, res := range cases {
		if res.StatusCode != want[name] {
			t.Errorf("%s: status %d, want %d", name, res.StatusCode, want[name])
		}
		for _, h := range [][2]string{
			{"Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"},
			{"X-Content-Type-Options", "nosniff"},
			{"Referrer-Policy", "no-referrer"},
			{"Cache-Control", "no-store"},
			{"Cross-Origin-Resource-Policy", "same-origin"},
		} { // the spec's five, written out: never read from the code under test
			if got := res.Header.Get(h[0]); got != h[1] {
				t.Errorf("%s: %s %q, want %q", name, h[0], got, h[1])
			}
		}
	}
	if ct := cases["css"].Header.Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Errorf("css Content-Type %q", ct)
	}
	if !strings.Contains(failing.Err.(*strings.Builder).String(), "fyr serve: GET /: boom") {
		t.Errorf("the 500 was not logged: %q", failing.Err.(*strings.Builder).String())
	}
}

// The cookie is scoped to the run's prefix: a browser sends it to every port
// of 127.0.0.1, but only for paths under /s/<P>, which a server on another
// port never learns (plan D48).
func TestCookieScopedToPrefix(t *testing.T) {
	r := newFix(t).run(t)
	w := serve(r, bare(r, "GET", "/?token="+r.Token()))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	at := func(p string) *url.URL { u, _ := url.Parse("http://127.0.0.1:3999" + p); return u }
	jar.SetCookies(at(r.Base()+"/"), w.Result().Cookies())
	if len(jar.Cookies(at(r.Base()+"/records"))) != 1 || len(jar.Cookies(at(r.Base()))) != 1 {
		t.Fatal("precondition: the jar does not send the cookie under the prefix")
	}
	for _, p := range []string{"/", "/records", "/s/", "/s/other/", r.Base() + "x/"} {
		if got := jar.Cookies(at(p)); len(got) != 0 {
			t.Errorf("the cookie goes to %s: %v", p, got)
		}
	}
	// Outside the prefix, even with the cookie, there is no page.
	for _, p := range []string{"/", "/records", "/s/other/records", r.Base(), r.Base() + "x/records"} {
		req := raw("GET", p)
		req.AddCookie(&http.Cookie{Name: r.cookie, Value: r.Token()})
		if w := serve(r, req); w.Code != 404 || w.Body.String() != lineNotFound+"\n" {
			t.Errorf("%s with the cookie: %d %q, want the plain 404", p, w.Code, w.Body.String())
		}
	}
}

// busyCheck is the 409 a second /check gets while the first one blocks.
func busyCheck(t *testing.T, f *fix) *httptest.ResponseRecorder {
	t.Helper()
	o := f.options(nil)
	entered, release := make(chan struct{}), make(chan struct{})
	o.Check = func(io.Writer) error { close(entered); <-release; return nil }
	r, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { get(t, r, "GET", "/check"); close(done) }()
	<-entered
	w := get(t, r, "GET", "/check")
	close(release)
	<-done
	return w
}
