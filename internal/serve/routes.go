package serve

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

// pages is every read page by its exact path (row 8); /records/{id} is
// matched by idPath. Every page takes GET and HEAD and nothing else.
var pages = map[string]func(*Run, http.ResponseWriter, *http.Request){
	"/":                 (*Run).home,
	"/context":          (*Run).context,
	"/records":          (*Run).records,
	"/search":           (*Run).search,
	"/memory":           (*Run).memory,
	"/metrics":          (*Run).metrics,
	"/metrics/report":   (*Run).metricsReport,
	"/evals":            (*Run).evals,
	"/dream":            (*Run).dream,
	"/check":            (*Run).check,
	"/static/style.css": (*Run).style,
}

// idPath is the record ID grammar of /records/{id}; another {id} is the
// plain 404, and a well-formed ID the store's per-kind grammar refuses comes
// back from brain.Get as missing.
var idPath = regexp.MustCompile(`^/records/([A-Z]+-[A-Za-z0-9._-]{1,200})$`)

// rel is the request's path under the run's prefix, "" when it lies outside.
// A path that only begins with the prefix (/s/<P>x/…) yields a rel without a
// leading slash, which names no page: the route's lookup refuses it.
func (r *Run) rel(req *http.Request) string {
	rel, _ := strings.CutPrefix(req.URL.Path, r.base)
	if rel == req.URL.Path {
		return ""
	}
	return rel
}

// route is step 4: a path outside the prefix or unknown under it is 404
// whatever the method, a known one with a method it does not take 405.
func (r *Run) route(w http.ResponseWriter, req *http.Request) {
	rel := r.rel(req)
	h, ok := pages[rel]
	if !ok && strings.HasPrefix(rel, "/records/") {
		if m := idPath.FindStringSubmatch(rel); m != nil {
			id := m[1]
			h, ok = func(r *Run, w http.ResponseWriter, req *http.Request) { r.record(w, req, id) }, true
		}
	}
	if !ok {
		plain(w, http.StatusNotFound, lineNotFound)
		return
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		plain(w, http.StatusMethodNotAllowed, lineMethod)
		return
	}
	h(r, w, req)
}

// page renders v through the layout with status; a template error after
// the header is written can only be logged.
func (r *Run) page(w http.ResponseWriter, req *http.Request, status int, v view) {
	v.Project = r.projectLine()
	v.Base = r.base
	v.Nav = navFor(r.base, r.rel(req))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := layout.Execute(w, v); err != nil {
		r.logf(req, err)
	}
}

// fail is a page's 500: its output so far, then the error, logged once.
func (r *Run) fail(w http.ResponseWriter, req *http.Request, v view, err error) {
	r.logf(req, err)
	v.Blocks = append(v.Blocks, block{Line: search.Printable(err.Error())})
	r.page(w, req, http.StatusInternalServerError, v)
}

// badRequest is a 400 with the parameter's message in the layout.
func (r *Run) badRequest(w http.ResponseWriter, req *http.Request, title, msg string) {
	r.page(w, req, http.StatusBadRequest, view{Title: title, Blocks: []block{{Line: search.Printable(msg)}}})
}

// noProject is a project page outside a checkout: 200 with the line.
func (r *Run) noProject(w http.ResponseWriter, req *http.Request, title string) bool {
	if r.root != "" {
		return false
	}
	r.page(w, req, http.StatusOK, view{Title: title, Blocks: []block{{Line: r.noProj}}})
	return true
}

// logf is the one line a handler error gets: the path under the prefix,
// without its query, so never a token and never P.
func (r *Run) logf(req *http.Request, err error) {
	fmt.Fprintln(r.errOut, search.Printable(fmt.Sprintf("fyr serve: %s %s: %v", req.Method, r.rel(req), err)))
}
