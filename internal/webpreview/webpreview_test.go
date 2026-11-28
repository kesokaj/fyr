package webpreview

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const wantDefaultCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:"

func TestWebPreviewPackageDeps(t *testing.T) {
	t.Parallel()
	assertSourceImportsStdlibOnly(t)

	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if pkg == "github.com/kesokaj/fyr/internal/webpreview" {
			continue
		}
		first, _, _ := strings.Cut(pkg, "/")
		if pkg == "modernc.org/sqlite" || strings.Contains(first, ".") {
			t.Errorf("internal/webpreview transitively depends on non-stdlib package %q", pkg)
		}
	}
}

func assertSourceImportsStdlibOnly(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			first, _, _ := strings.Cut(p, "/")
			if p == "modernc.org/sqlite" || strings.Contains(first, ".") {
				t.Errorf("%s imports non-stdlib package %q", name, p)
			}
		}
	}
}

func TestGenerateToken(t *testing.T) {
	t.Parallel()
	t1, err := generateToken(rand.Reader)
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	t2, err := generateToken(rand.Reader)
	if err != nil {
		t.Fatalf("generateToken second call: %v", err)
	}
	if len(t1) != 32 || t1 != strings.ToLower(t1) {
		t.Fatalf("token = %q (len %d), want 32 lowercase hex chars", t1, len(t1))
	}
	if raw, err := hex.DecodeString(t1); err != nil || len(raw) != 16 {
		t.Fatalf("hex decode %q: len=%d err=%v, want 16 bytes", t1, len(raw), err)
	}
	if t1 == t2 {
		t.Fatalf("consecutive random tokens collided: %q", t1)
	}
	if _, err := generateToken(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Fatal("generateToken on short reader succeeded; want error")
	}
}

func TestNewHandlerGoldenAndHEAD(t *testing.T) {
	t.Parallel()
	const tok = "0123456789abcdef0123456789abcdef"
	rawBody := []byte("<!doctype html><h1>Report</h1>")
	assets := map[string]Asset{
		"/": {ContentType: "text/html; charset=utf-8", Body: rawBody},
	}
	h := NewHandler(tok, assets, "")
	rawBody[0] = 'X'
	delete(assets, "/")

	for _, reqPath := range []string{"/" + tok + "/", "/" + tok} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html><h1>Report</h1>" {
			t.Fatalf("GET %s: status=%d body=%q", reqPath, rec.Code, rec.Body.String())
		}
		assertSecurityHeaders(t, rec.Header(), wantDefaultCSP)
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
		}
	}

	headRec := httptest.NewRecorder()
	h.ServeHTTP(headRec, httptest.NewRequest(http.MethodHead, "/"+tok+"/", nil))
	if headRec.Code != http.StatusOK || headRec.Body.Len() != 0 {
		t.Fatalf("HEAD status=%d bodyLen=%d, want 200 and 0", headRec.Code, headRec.Body.Len())
	}
	if cl := headRec.Header().Get("Content-Length"); cl != "30" {
		t.Errorf("HEAD Content-Length = %q, want 30", cl)
	}
	assertSecurityHeaders(t, headRec.Header(), wantDefaultCSP)
}

func TestNewHandlerCSPAndContentTypeOverrides(t *testing.T) {
	t.Parallel()
	const tok = "0123456789abcdef0123456789abcdef"
	const customCSP = "default-src 'none'; img-src 'self'"
	h := NewHandler(tok, map[string]Asset{
		"report.json":  {ContentType: "application/json", Body: []byte(`{"ok":true}`), CSP: customCSP},
		"/sub/raw.bin": {Body: []byte{0xde, 0xad, 0xbe, 0xef}},
	}, "")

	jsonRec := httptest.NewRecorder()
	h.ServeHTTP(jsonRec, httptest.NewRequest(http.MethodGet, "/"+tok+"/report.json", nil))
	if jsonRec.Code != http.StatusOK {
		t.Fatalf("GET report.json: status = %d, want 200", jsonRec.Code)
	}
	assertSecurityHeaders(t, jsonRec.Header(), customCSP)

	binRec := httptest.NewRecorder()
	h.ServeHTTP(binRec, httptest.NewRequest(http.MethodGet, "/"+tok+"/sub/raw.bin", nil))
	if binRec.Code != http.StatusOK || binRec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("GET raw.bin: status=%d ct=%q", binRec.Code, binRec.Header().Get("Content-Type"))
	}

	hCustomDefault := NewHandler(tok, map[string]Asset{"/": {ContentType: "text/plain", Body: []byte("hi")}}, "default-src 'none'")
	defRec := httptest.NewRecorder()
	hCustomDefault.ServeHTTP(defRec, httptest.NewRequest(http.MethodGet, "/"+tok+"/", nil))
	assertSecurityHeaders(t, defRec.Header(), "default-src 'none'")
}

func TestNewHandlerMethodNotAllowed(t *testing.T) {
	t.Parallel()
	const tok = "abcdef0123456789abcdef0123456789"
	h := NewHandler(tok, map[string]Asset{"/": {ContentType: "text/html", Body: []byte("ok")}}, "")

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		for _, target := range []string{"/" + tok + "/", "/", "/wrong/"} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader("payload")))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d, want 405", method, target, rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s %s: Allow = %q, want %q", method, target, allow, "GET, HEAD")
			}
			assertSecurityHeaders(t, rec.Header(), wantDefaultCSP)
		}
	}
}

func TestNewHandlerRejectsInvalidTokensAndDiskFiles(t *testing.T) {
	const tok = "fedcba9876543210fedcba9876543210"
	const diskCanary = "DISK_CANARY_DO_NOT_SERVE"

	tmp := t.TempDir()
	for _, name := range []string{".env", "search.db", "canary.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(diskCanary), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(tmp)

	h := NewHandler(tok, map[string]Asset{
		"/":            {ContentType: "text/html", Body: []byte("index")},
		"/report.html": {ContentType: "text/html", Body: []byte("report")},
	}, "")

	cases := []string{
		"/", "/.env", "/search.db", "/canary.txt", "/etc/passwd", "/report.html",
		"/00000000000000000000000000000000/", "/FEDCBA9876543210FEDCBA9876543210/",
		"/" + tok[:31] + "/", "/" + tok + "0/", "/" + tok + "/missing.html",
		"/" + tok + "/.env", "/" + tok + "/canary.txt", "/" + tok + "/report.html/", "/" + tok + "//report.html",
	}
	assertAllNotFound(t, h, cases, diskCanary)
}

func TestNewHandlerRejectsPathTraversal(t *testing.T) {
	t.Parallel()
	const tok = "fedcba9876543210fedcba9876543210"
	h := NewHandler(tok, map[string]Asset{
		"/":            {ContentType: "text/html", Body: []byte("index")},
		"/report.html": {ContentType: "text/html", Body: []byte("report")},
	}, "")

	traversal := []string{
		"/" + tok + "/../.env",
		"/" + tok + "/../canary.txt",
		"/" + tok + "/../../etc/passwd",
		"/" + tok + "/a/../report.html",
		"/" + tok + "/./report.html",
		"/" + tok + "/%2e%2e/.env",
		"/" + tok + "/%2e%2e/report.html",
		"/" + tok + "/%2E%2E/report.html",
		"/" + tok + "/..%2freport.html",
		"/" + tok + "/..%5creport.html",
		"/" + tok + "/report.html%00",
	}
	assertAllNotFound(t, h, traversal, "")

	for _, badTok := range []string{"", ".", "..", "a/b", `a\b`} {
		badH := NewHandler(badTok, map[string]Asset{"/": {Body: []byte("x")}}, "")
		assertAllNotFound(t, badH, []string{"/", "//", "/a/b/", "/./", "/../"}, "")
	}
}

func assertAllNotFound(t *testing.T, h http.Handler, targets []string, forbiddenBody string) {
	t.Helper()
	for _, target := range targets {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %q: status = %d, want 404", target, rec.Code)
		}
		if forbiddenBody != "" && strings.Contains(rec.Body.String(), forbiddenBody) {
			t.Fatalf("GET %q leaked disk file content", target)
		}
		assertSecurityHeaders(t, rec.Header(), wantDefaultCSP)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	ch  chan struct{}
}

func newSyncBuffer() *syncBuffer {
	return &syncBuffer{ch: make(chan struct{}, 1)}
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	select {
	case b.ch <- struct{}{}:
	default:
	}
	return n, err
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForLine(t *testing.T, b *syncBuffer) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if s := b.String(); strings.Contains(s, "\n") {
			line, _, _ := strings.Cut(s, "\n")
			return strings.TrimSpace(line)
		}
		select {
		case <-b.ch:
		case <-deadline:
			t.Fatalf("timed out waiting for URL line; got %q", b.String())
		}
	}
}

func TestServeLoopbackAndCleanShutdown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	outBuf, errBuf := newSyncBuffer(), newSyncBuffer()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, map[string]Asset{
			"/":            {ContentType: "text/html; charset=utf-8", Body: []byte("<title>ok</title>")},
			"/report.html": {ContentType: "text/html; charset=utf-8", Body: []byte("report")},
		}, Options{Out: outBuf, Err: errBuf})
	}()

	previewURL := waitForLine(t, outBuf)
	if errOut := errBuf.String(); errOut != "" {
		t.Errorf("loopback Serve wrote unexpected warning to Err: %q", errOut)
	}
	u, tok := parseLoopbackURL(t, previewURL)
	assertLiveGET(t, previewURL, "<title>ok</title>")
	assertRawTraversal404(t, u.Host, tok)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error after cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not shut down within 5s of context cancel")
	}
}

func parseLoopbackURL(t *testing.T, previewURL string) (*url.URL, string) {
	t.Helper()
	u, err := url.Parse(previewURL)
	if err != nil {
		t.Fatalf("parse preview URL %q: %v", previewURL, err)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("URL host = %q, want 127.0.0.1:<port>", u.Host)
	}
	tok := strings.Trim(u.Path, "/")
	if len(tok) != 32 {
		t.Fatalf("token %q len = %d, want 32", tok, len(tok))
	}
	return u, tok
}

func assertLiveGET(t *testing.T, targetURL, wantBody string) {
	t.Helper()
	resp, err := http.Get(targetURL)
	if err != nil {
		t.Fatalf("GET %s: %v", targetURL, err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != wantBody {
		t.Fatalf("GET status=%d body=%q, want 200 %q", resp.StatusCode, body, wantBody)
	}
	assertSecurityHeaders(t, resp.Header, wantDefaultCSP)
}

func assertRawTraversal404(t *testing.T, hostPort, tok string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", hostPort, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", hostPort, err)
	}
	defer func() {
		_ = conn.Close() // best-effort test socket cleanup
	}()
	rawReq := "GET /" + tok + "/a/../report.html HTTP/1.1\r\nHost: " + hostPort + "\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, rawReq); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	rawResp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	_ = rawResp.Body.Close() // best-effort close after status read
	if rawResp.StatusCode != http.StatusNotFound {
		t.Errorf("raw .. traversal status = %d, want 404", rawResp.StatusCode)
	}
}

func TestServeNonLoopbackWarning(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	outBuf, errBuf := newSyncBuffer(), newSyncBuffer()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, map[string]Asset{
			"/": {ContentType: "text/plain; charset=utf-8", Body: []byte("lan")},
		}, Options{Listen: "0.0.0.0:0", Out: outBuf, Err: errBuf})
	}()

	firstURL := waitForLine(t, outBuf)
	warn := waitForLine(t, errBuf)
	if !strings.Contains(warn, "warning") || !strings.Contains(warn, "non-loopback") ||
		!strings.Contains(warn, "0.0.0.0:") || !strings.Contains(warn, firstURL) {
		t.Errorf("unexpected non-loopback warning: %q (firstURL=%q)", warn, firstURL)
	}
	assertLiveGET(t, firstURL, "lan")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("non-loopback Serve shutdown error: %v", err)
	}
}

func TestServeOptionsAndErrors(t *testing.T) {
	t.Parallel()
	urls, nonLoop := previewURLs("192.0.2.10:8080", &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 8080}, "tok")
	if !nonLoop || len(urls) != 1 || urls[0] != "http://192.0.2.10:8080/tok/" {
		t.Errorf("specific IP previewURLs = (%v, %v)", urls, nonLoop)
	}
	v6URLs, v6NonLoop := previewURLs("[::]:8080", &net.TCPAddr{IP: net.IPv6zero, Port: 8080}, "tok")
	if !v6NonLoop || len(v6URLs) == 0 || v6URLs[0] != "http://[::1]:8080/tok/" {
		t.Errorf("IPv6 unspecified previewURLs = (%v, %v)", v6URLs, v6NonLoop)
	}
	var onlyOut, onlyErr bytes.Buffer
	if o, e := resolveWriters(Options{Out: &onlyOut}); o != &onlyOut || e != &onlyOut {
		t.Error("resolveWriters(Out only) did not route Err to Out")
	}
	if o, e := resolveWriters(Options{Err: &onlyErr}); o != io.Discard || e != &onlyErr {
		t.Error("resolveWriters(Err only) did not discard Out")
	}
	var nilCtx context.Context
	if err := Serve(nilCtx, nil, Options{}); err == nil {
		t.Error("Serve(nil, ...) = nil, want error")
	}
	if err := Serve(t.Context(), nil, Options{Listen: "127.0.0.1:99999", Out: io.Discard, Err: io.Discard}); err == nil {
		t.Error("Serve on invalid port = nil, want error")
	}
}

func TestConcurrentRequestsAndPrecedence(t *testing.T) {
	t.Parallel()
	const tok = "11223344556677889900aabbccddeeff"
	h := NewHandler(tok, map[string]Asset{
		"":           {ContentType: "text/plain", Body: []byte("bare-root")},
		"/":          {ContentType: "text/plain", Body: []byte("slash-root")},
		"page.html":  {ContentType: "text/html", Body: []byte("bare")},
		"/page.html": {ContentType: "text/html", Body: []byte("slash")},
		"/../bad":    {ContentType: "text/plain", Body: []byte("bad")},
	}, "")

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assertPrecedenceRound(t, h, tok)
		}()
	}
	wg.Wait()
}

func assertPrecedenceRound(t *testing.T, h http.Handler, tok string) {
	t.Helper()
	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/" + tok + "/", http.StatusOK, "slash-root"},
		{"/" + tok + "/page.html", http.StatusOK, "slash"},
		{"/" + tok + "/bad", http.StatusNotFound, ""},
		{"/wrong/page.html", http.StatusNotFound, ""},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.code {
			t.Errorf("GET %s: code=%d, want %d", tc.path, rec.Code, tc.code)
		}
		if tc.code == http.StatusOK && rec.Body.String() != tc.body {
			t.Errorf("GET %s: body=%q, want %q", tc.path, rec.Body.String(), tc.body)
		}
	}
}

func assertSecurityHeaders(t *testing.T, hdr http.Header, wantCSP string) {
	t.Helper()
	checks := map[string]string{
		"Content-Security-Policy":      wantCSP,
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"Cache-Control":                "no-store",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for k, want := range checks {
		if got := hdr.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	if cookies := hdr.Values("Set-Cookie"); len(cookies) != 0 {
		t.Errorf("unexpected Set-Cookie headers: %v", cookies)
	}
}
