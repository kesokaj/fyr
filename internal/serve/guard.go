package serve

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// The refusal lines (SPEC-fyr-serve § Every request): one line, no token, no
// path.
const (
	lineForbidden = "forbidden"
	lineFetchSite = "forbidden: open the printed URL by pasting it into the address bar"
	lineNotFound  = "not found"
	lineMethod    = "method not allowed"
)

// securityHeaders go on every response, refusals and the 303 included. The
// one inline style is the metrics report's <style> element; no page carries a
// script.
var securityHeaders = [][2]string{
	{"Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"},
	{"X-Content-Type-Options", "nosniff"},
	{"Referrer-Policy", "no-referrer"},
	{"Cache-Control", "no-store"},
	{"Cross-Origin-Resource-Policy", "same-origin"},
}

// serveHTTP checks every request in the spec's order (Host, fetch metadata,
// token or cookie) before it is routed: a request that fails one is refused
// and reaches no page.
func (r *Run) serveHTTP(w http.ResponseWriter, req *http.Request) {
	for _, h := range securityHeaders {
		w.Header().Set(h[0], h[1])
	}
	if req.Host != r.host { // DNS rebinding; localhost is not accepted
		plain(w, http.StatusForbidden, lineForbidden)
		return
	}
	if s := req.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" && s != "none" {
		plain(w, http.StatusForbidden, lineFetchSite)
		return
	}
	q := req.URL.Query()
	if (req.Method == http.MethodGet || req.Method == http.MethodHead) && q.Has("token") {
		r.exchangeToken(w, req, q)
		return
	}
	if !r.cookieOK(req) {
		plain(w, http.StatusForbidden, lineForbidden)
		return
	}
	r.route(w, req)
}

// exchangeToken answers ?token=T: exactly one value equal to T sets the
// cookie and redirects to the same path without the token; anything else is
// refused, never a fallback to the cookie.
func (r *Run) exchangeToken(w http.ResponseWriter, req *http.Request, q url.Values) {
	if vs := q["token"]; len(vs) != 1 || !equal(vs[0], r.token) {
		plain(w, http.StatusForbidden, lineForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: r.cookie, Value: r.token, Path: r.base, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	q.Del("token")
	// Built as a url.URL, never by concatenation; a leading run of slashes
	// collapsed to one, so the Location is never protocol-relative.
	loc := url.URL{Path: "/" + strings.TrimLeft(req.URL.Path, "/"), RawQuery: q.Encode()}
	w.Header().Set("Location", loc.String())
	w.WriteHeader(http.StatusSeeOther)
}

// cookieOK compares every cookie of the run's name: a same-site page can
// plant a second one beside the right one.
func (r *Run) cookieOK(req *http.Request) bool {
	ok := false
	for _, c := range req.Cookies() {
		if c.Name == r.cookie && equal(c.Value, r.token) {
			ok = true
		}
	}
	return ok
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// plain is a refusal or a plain-text error: one line.
func plain(w http.ResponseWriter, status int, line string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(line + "\n"))
}
