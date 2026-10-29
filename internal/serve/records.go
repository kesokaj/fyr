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

// scopeOf reads ?scope=: absent is the project's, global the global
// records', anything else a 400.
func scopeOf(q url.Values) (global, ok bool) {
	vs, has := q["scope"]
	if !has {
		return false, true
	}
	return true, len(vs) == 1 && vs[0] == "global"
}

// records is `fyr record list`: the project's, or with scope=global the
// global records, filtered by kind and open.
func (r *Run) records(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	global, ok := scopeOf(q)
	if !ok {
		r.badRequest(w, req, "records", "scope takes one value, global")
		return
	}
	kind := q.Get("kind")
	if q.Has("kind") && !slices.Contains(brain.Kinds(), kind) {
		r.badRequest(w, req, "records", fmt.Sprintf("unknown kind %s; one of %s", kind, strings.Join(brain.Kinds(), " ")))
		return
	}
	if !global && r.noProject(w, req, "records") {
		return
	}
	v := view{Title: "records"}
	if global {
		v.Title = "global records"
	}
	recs, err := brain.List(brain.Options{Home: r.o.Home, Dir: r.o.Dir, Global: global}, brain.ListFilter{Kind: kind, Open: q.Get("open") == "1"})
	if err != nil {
		r.fail(w, req, v, storeErr("brain store", err))
		return
	}
	rows := make([]recordRow, 0, len(recs))
	for _, rec := range recs {
		rows = append(rows, r.rowOf(rec, global))
	}
	v.Blocks = []block{listBlock(rows, "no records")}
	r.page(w, req, http.StatusOK, v)
}

// record is `fyr record get ID`: its header line and its body, as text.
func (r *Run) record(w http.ResponseWriter, req *http.Request, id string) {
	global, ok := scopeOf(req.URL.Query())
	if !ok {
		r.badRequest(w, req, id, "scope takes one value, global")
		return
	}
	if !global && r.noProject(w, req, id) {
		return
	}
	v := view{Title: id}
	recs, missing, err := brain.Get(brain.Options{Home: r.o.Home, Dir: r.o.Dir, Global: global}, []string{id})
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
}

// search is `fyr search WORDS [--kind K]`: the record hits, each ID linked,
// then the file hits as search prints them.
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
	if r.noProject(w, req, "search") {
		return
	}
	v.Blocks = append(v.Blocks, r.recordHits(words, form.Kind))
	var buf bytes.Buffer
	err := search.Search(o, &buf)
	v.Blocks = append(v.Blocks, block{Pre: printableText(buf.String())})
	if err != nil {
		r.fail(w, req, v, err)
		return
	}
	r.page(w, req, http.StatusOK, v)
}

// recordHits is cmd/fyr's recordHits for a words search: nothing with no
// hits, one line when the store could not be searched.
func (r *Run) recordHits(words []string, kind string) block {
	hits, err := brain.Search(brain.Options{Home: r.o.Home, Dir: r.o.Dir}, words, kind, 10)
	if err != nil {
		return block{Line: search.Printable("records: not searched: " + err.Error())}
	}
	if len(hits) == 0 {
		return block{}
	}
	b := block{Line: search.Printable(fmt.Sprintf("records: %d hits for %q", len(hits), strings.Join(words, " ")))}
	for _, h := range hits {
		label, rest, _ := strings.Cut(brain.FormatHit(h), "  ")
		b.Hits = append(b.Hits, hitRow{Href: r.recordHref(h.ID, h.Scope == ""), Label: label, Rest: "  " + rest, Snippet: h.Snippet})
	}
	return b
}

// recordHref links a record by its ID field, never by a pattern over text.
func (r *Run) recordHref(id string, global bool) string {
	u := url.URL{Path: r.base + "/records/" + id}
	if global {
		u.RawQuery = "scope=global"
	}
	return u.String()
}

func (r *Run) rowOf(rec brain.Record, global bool) recordRow {
	st := rec.Status
	if st == "" {
		st = "-"
	}
	p := search.Printable
	return recordRow{Href: r.recordHref(rec.ID, global), ID: p(rec.ID), Kind: p(rec.Kind), Status: p(st), Date: p(rec.Date), Title: p(rec.Title)}
}

func listBlock(rows []recordRow, empty string) block {
	if len(rows) == 0 {
		return block{Line: empty}
	}
	return block{Records: rows}
}
