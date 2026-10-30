package serve

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

// action is one maintenance verb a POST runs (row 9). Only these two exist
// (plan D39): no flag of either verb is reachable, and no other verb is.
type action struct {
	title   string
	project bool // outside a checkout it runs nothing and says why
	fn      func(Options) func(io.Writer) error
}

var actions = map[string]action{
	"/actions/index":          {"index", true, func(o Options) func(io.Writer) error { return o.Index }},
	"/actions/metrics-ingest": {"metrics ingest", false, func(o Options) func(io.Writer) error { return o.Ingest }},
}

const (
	maxBody   = 4 << 10 // a POST body: the csrf field and nothing else is needed
	outputCap = 1 << 20 // what a page shows of an action's output
)

// act runs a after step 5 (CSRF), one action at a time: a second POST while
// one runs is 409 and runs nothing. The page is the output and ok or
// failed, never a redirect.
func (r *Run) act(w http.ResponseWriter, req *http.Request, a action) {
	if !r.csrfOK(w, req) {
		plain(w, http.StatusForbidden, lineForbidden)
		return
	}
	if a.project && r.noProject(w, req, a.title) {
		return
	}
	if !r.acting.TryLock() {
		r.page(w, req, http.StatusConflict, view{Title: a.title, Blocks: []block{{Line: "an action is running"}}})
		return
	}
	defer r.acting.Unlock()
	var out capped
	err := a.fn(r.o)(&out)
	v := view{Title: a.title, Blocks: []block{{Pre: out.text()}}}
	if err != nil {
		r.logf(req, err)
		v.Blocks = append(v.Blocks, block{Line: search.Printable("failed: " + err.Error())})
		r.page(w, req, http.StatusInternalServerError, v)
		return
	}
	v.Blocks = append(v.Blocks, block{Line: "ok"})
	r.page(w, req, http.StatusOK, v)
}

// csrfOK is step 5: the cookie authenticated the request (the guard checked
// it; a token query is refused here), the body is a form of at most 4 KiB
// whose one csrf field is C, and an Origin, when sent, is this server's.
func (r *Run) csrfOK(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Query().Has("token") {
		return false
	}
	if mt, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || mt != "application/x-www-form-urlencoded" {
		return false
	}
	if vs := req.Header.Values("Origin"); len(vs) > 0 && (len(vs) != 1 || vs[0] != "http://"+r.host) {
		return false
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxBody)
	if err := req.ParseForm(); err != nil {
		return false
	}
	// The body only, never the query; exactly one field, as with the token
	// (plan D53).
	vs := req.PostForm["csrf"]
	return len(vs) == 1 && equal(vs[0], r.csrf)
}

// capped keeps the first outputCap bytes of an action's output and drops
// the rest without failing the action.
type capped struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := outputCap - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

// text is the output made printable, then the truncation line when cut.
func (c *capped) text() string {
	s := printableText(c.buf.String())
	if c.truncated {
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += "[output truncated]\n"
	}
	return s
}

// actionForm is the button for one action: a POST carrying the run's csrf.
func (r *Run) actionForm(path, label string) block {
	return block{Action: &actionForm{Href: r.base + path, Label: label, CSRF: r.csrf}}
}
