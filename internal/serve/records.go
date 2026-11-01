package serve

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// maxQuery is the longest /search q, in bytes.
const maxQuery = 200

type scopeMode int

const (
	scopeDefault scopeMode = iota
	scopeGlobal
	scopeProject
)

// resolveScope validates ?scope= inside and outside a project checkout (D9).
func (r *Run) resolveScope(q url.Values) (scopeMode, string, string) {
	vs, has := q["scope"]
	if !has {
		return scopeDefault, "", ""
	}
	if r.root != "" {
		if len(vs) == 1 && vs[0] == "global" {
			return scopeGlobal, "", ""
		}
		return 0, "", "scope takes one value, global"
	}
	if len(vs) != 1 || vs[0] == "" {
		return 0, "", "scope must be global or a known project"
	}
	if vs[0] == "global" {
		return scopeGlobal, "", ""
	}
	if scopes, err := brain.Scopes(r.o.Home); err == nil && slices.Contains(scopes, vs[0]) {
		return scopeProject, vs[0], ""
	}
	return 0, "", "scope must be global or a known project"
}

// records is `fyr record list`: the project's inside a checkout, every
// scope's outside one, or global/project-scoped when ?scope= is set.
func (r *Run) records(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	mode, projScope, bad := r.resolveScope(q)
	if bad != "" {
		r.badRequest(w, req, "records", bad)
		return
	}
	kind := q.Get("kind")
	if q.Has("kind") && !slices.Contains(brain.Kinds(), kind) {
		r.badRequest(w, req, "records", fmt.Sprintf("unknown kind %s; one of %s", kind, strings.Join(brain.Kinds(), " ")))
		return
	}
	v := view{Title: "records"}
	switch mode {
	case scopeGlobal:
		v.Title = "global records"
	case scopeProject:
		v.Title = "records — " + search.Printable(projScope)
	}
	recs, err := r.listRecords(mode, projScope, brain.ListFilter{Kind: kind, Open: q.Get("open") == "1"})
	if err != nil {
		r.fail(w, req, v, storeErr("brain store", err))
		return
	}
	rows := make([]recordRow, 0, len(recs))
	for _, rec := range recs {
		rows = append(rows, r.rowOf(rec))
	}
	b := listBlock(rows, "no records")
	b.ShowScope = r.root == "" && mode == scopeDefault
	v.Blocks = []block{b}
	r.page(w, req, http.StatusOK, v)
}

func (r *Run) listRecords(mode scopeMode, projScope string, f brain.ListFilter) ([]brain.Record, error) {
	if r.root != "" && mode != scopeGlobal {
		return brain.List(brain.Options{Home: r.o.Home, Dir: r.o.Dir}, f)
	}
	recs, err := r.listAll(f)
	if err != nil || mode == scopeDefault {
		return recs, err
	}
	var out []brain.Record
	for _, rec := range recs {
		if rec.Scope == projScope {
			out = append(out, rec)
		}
	}
	return out, nil
}

// record is `fyr record get ID`: its header line and its body, as text.
func (r *Run) record(w http.ResponseWriter, req *http.Request, id string) {
	mode, projScope, bad := r.resolveScope(req.URL.Query())
	if bad != "" {
		r.badRequest(w, req, id, bad)
		return
	}
	v := view{Title: id}
	if r.root != "" && mode != scopeGlobal {
		recs, missing, err := brain.Get(brain.Options{Home: r.o.Home, Dir: r.o.Dir}, []string{id})
		switch {
		case len(missing) > 0:
			v.Blocks = []block{{Line: "no record " + id}}
			r.page(w, req, http.StatusNotFound, v)
		case err != nil:
			r.fail(w, req, v, storeErr("brain store", err))
		default:
			v.Blocks = []block{{Pre: brain.FormatGet(recs[0])}}
			r.page(w, req, http.StatusOK, v)
		}
		return
	}
	r.recordOutside(w, req, v, id, mode, projScope)
}

func (r *Run) recordOutside(w http.ResponseWriter, req *http.Request, v view, id string, mode scopeMode, projScope string) {
	all, err := r.listAll(brain.ListFilter{})
	if err != nil {
		r.fail(w, req, v, storeErr("brain store", err))
		return
	}
	var matches []brain.Record
	for _, rec := range all {
		if rec.ID == id && (mode == scopeDefault || rec.Scope == projScope) {
			matches = append(matches, rec)
		}
	}
	switch {
	case len(matches) == 0:
		v.Blocks = []block{{Line: "no record " + id}}
		r.page(w, req, http.StatusNotFound, v)
	case len(matches) > 1:
		r.badRequest(w, req, id, "ambiguous record "+id+"; pass ?scope=<project>")
	default:
		v.Blocks = []block{{Pre: brain.FormatGet(matches[0])}}
		r.page(w, req, http.StatusOK, v)
	}
}

// search is `fyr search WORDS [--kind K]`: the record hits, each ID linked,
// then inside a checkout the file hits as search prints them.
func (r *Run) search(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	form := &searchForm{Q: q.Get("q"), Kind: q.Get("kind")}
	v := view{Title: "search", Blocks: []block{{Search: form}}}
	if len(form.Q) > maxQuery {
		r.badRequest(w, req, "search", fmt.Sprintf("q is over %d bytes", maxQuery))
		return
	}
	words := strings.Fields(form.Q)
	if len(words) == 0 {
		r.page(w, req, http.StatusOK, v)
		return
	}
	o := search.Options{Dir: r.o.Dir, Home: r.o.Home, Words: words, Kind: form.Kind}
	if err := search.Validate(o); err != nil {
		r.badRequest(w, req, "search", err.Error())
		return
	}
	v.Blocks = append(v.Blocks, r.recordHits(words, form.Kind))
	if r.root != "" {
		var buf bytes.Buffer
		err := search.Search(o, &buf)
		v.Blocks = append(v.Blocks, block{Pre: printableText(buf.String())})
		if err != nil {
			r.fail(w, req, v, err)
			return
		}
	}
	r.page(w, req, http.StatusOK, v)
}

// recordHits is cmd/fyr's recordHits for a words search: nothing with no
// hits inside a checkout (or "0 hits" outside one), one line when the store
// could not be searched.
func (r *Run) recordHits(words []string, kind string) block {
	hits, err := brain.Search(brain.Options{Home: r.o.Home, Dir: r.o.Dir, Global: r.root == ""}, words, kind, 10)
	if err != nil {
		return block{Line: search.Printable("records: not searched: " + err.Error())}
	}
	if len(hits) == 0 {
		if r.root == "" {
			return block{Line: "0 hits"}
		}
		return block{}
	}
	b := block{Line: search.Printable(fmt.Sprintf("records: %d hits for %q", len(hits), strings.Join(words, " ")))}
	for _, h := range hits {
		label, rest, _ := strings.Cut(brain.FormatHit(h), "  ")
		b.Hits = append(b.Hits, hitRow{Href: r.recordHref(h.ID, h.Scope), Label: label, Rest: "  " + rest, Snippet: h.Snippet})
	}
	return b
}

// recordHref links a record by its ID and Scope fields, never by a pattern
// over text (D9).
func (r *Run) recordHref(id, scope string) string {
	u := url.URL{Path: r.base + "/records/" + id}
	if scope == "" {
		u.RawQuery = "scope=global"
	} else if r.root == "" {
		u.RawQuery = url.Values{"scope": {scope}}.Encode()
	}
	return u.String()
}

func (r *Run) rowOf(rec brain.Record) recordRow {
	st := rec.Status
	if st == "" {
		st = "-"
	}
	p := search.Printable
	scope := "global"
	if rec.Scope != "" {
		scope = p(rec.Scope)
	}
	return recordRow{Href: r.recordHref(rec.ID, rec.Scope), ID: p(rec.ID), Kind: p(rec.Kind), Status: p(st), Date: p(rec.Date), Scope: scope, Title: p(rec.Title)}
}

func listBlock(rows []recordRow, empty string) block {
	if len(rows) == 0 {
		return block{Line: empty}
	}
	return block{Records: rows}
}
