package serve

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed assets/layout.html assets/style.css
var assets embed.FS

// layout is the one page template; every value is escaped by html/template,
// and verb text lands in a <pre>, never as HTML.
var layout = template.Must(template.ParseFS(assets, "assets/layout.html"))

var styleCSS = mustRead("assets/style.css")

func mustRead(name string) []byte {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

// view is what the layout renders: a title, the project line, the nav bar,
// then blocks in order.
type view struct {
	Base    string // /s/<P>, before every href
	Title   string
	Project string
	Nav     []link
	Blocks  []block
}

// block is one part of a page; the layout renders each field that is set,
// in this order.
type block struct {
	Heading string
	Line    string
	Link    *link
	Search  *searchForm
	Action  *actionForm
	Records []recordRow
	Hits    []hitRow
	Pre     string
}

type link struct {
	Href    string
	Text    string
	Current bool
}

type recordRow struct {
	Href, ID, Kind, Status, Date, Title string
}

// hitRow is a record hit as `fyr search` prints it, its ID linked.
type hitRow struct {
	Href, Label, Rest, Snippet string
}

type searchForm struct {
	Q, Kind string
}

// actionForm is a POST to an action with the run's csrf (SPEC-fyr-serve
// § Tokens); the /search form is a GET and carries none.
type actionForm struct {
	Href, Label, CSRF string
}

// nav is the bar on every page, in this order.
var nav = []link{
	{Href: "/", Text: "status"},
	{Href: "/context", Text: "context"},
	{Href: "/records", Text: "records"},
	{Href: "/search", Text: "search"},
	{Href: "/memory", Text: "memory"},
	{Href: "/metrics", Text: "metrics"},
	{Href: "/evals", Text: "evals"},
	{Href: "/dream", Text: "dream"},
	{Href: "/check", Text: "check"},
}

func navFor(base, rel string) []link {
	out := make([]link, len(nav))
	copy(out, nav)
	for i := range out {
		out[i].Current = out[i].Href == rel
		out[i].Href = base + out[i].Href
	}
	return out
}

// style serves the one stylesheet from its embedded bytes: no file server,
// so /static/ lists nothing.
func (r *Run) style(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(styleCSS)
}
