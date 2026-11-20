package graph

import (
	_ "embed"
	"encoding/json"
	"html"
	"io"
	"strings"
)

// DefaultHTMLCSP is the strict offline Content-Security-Policy embedded into
// self-contained graph HTML files and served by `fyr graph --web`.
const DefaultHTMLCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:"

var (
	//go:embed assets/graph.html
	graphHTMLTemplate string

	//go:embed assets/graph.css
	graphCSSAsset string

	//go:embed assets/graph_state.js
	graphJSState string

	//go:embed assets/graph_layout.js
	graphJSLayout string

	//go:embed assets/graph_render.js
	graphJSRender string

	//go:embed assets/graph_ui.js
	graphJSUI string

	//go:embed assets/graph.js
	graphJSBoot string
)

func bundledGraphJS() string {
	parts := []string{
		"(() => {\n\"use strict\";",
		graphJSState,
		graphJSLayout,
		graphJSRender,
		graphJSUI,
		graphJSBoot,
		"})();",
	}
	return strings.Join(parts, "\n")
}

// RenderJSON marshals p as compact UTF-8 JSON followed by a trailing newline.
// Go's encoding/json escapes '<', '>', and '&' as \u003c, \u003e, and \u0026.
func RenderJSON(w io.Writer, p *Payload) error {
	norm := ensureNonNilPayload(p)
	data, err := json.Marshal(norm)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// RenderHTML renders a self-contained offline interactive HTML constellation
// with inline CSS, script-safe JSON, inline JS, and strict offline CSP.
func RenderHTML(w io.Writer, p *Payload) error {
	norm := ensureNonNilPayload(p)
	rawJSON, err := json.Marshal(norm)
	if err != nil {
		return err
	}
	safeJSON := sanitizeEmbeddedJSON(string(rawJSON))
	repoTitle := html.EscapeString(norm.Meta.Repo)
	replacer := strings.NewReplacer(
		"__GRAPH_CSP__", DefaultHTMLCSP,
		"__GRAPH_TITLE__", repoTitle,
		"__GRAPH_CSS__", graphCSSAsset,
		"__GRAPH_JSON__", safeJSON,
		"__GRAPH_JS__", bundledGraphJS(),
	)
	_, err = io.WriteString(w, replacer.Replace(graphHTMLTemplate))
	return err
}

func sanitizeEmbeddedJSON(s string) string {
	// encoding/json.Marshal already escapes '<', '>', and '&' to \u003c, \u003e, \u0026;
	// defensively neutralize any residual HTML comment or closing script sequence.
	s = strings.ReplaceAll(s, "-->", `--\u003e`)
	s = strings.ReplaceAll(s, "</", `\u003c/`)
	s = strings.ReplaceAll(s, "<!--", `\u003c!--`)
	return s
}

func ensureNonNilPayload(p *Payload) *Payload {
	if p == nil {
		return Build(Input{})
	}
	cp := *p
	if cp.Meta.Palette == nil {
		cp.Meta.Palette = append([]string(nil), DefaultPalette...)
	}
	if cp.Meta.LayerColors == nil {
		cp.Meta.LayerColors = seedLayerColors()
	}
	if cp.Meta.PkgCycles == nil {
		cp.Meta.PkgCycles = [][]int{}
	}
	if cp.Packages == nil {
		cp.Packages = []PackageNode{}
	}
	if cp.Files == nil {
		cp.Files = []FileNode{}
	}
	if cp.Symbols == nil {
		cp.Symbols = []SymbolNode{}
	}
	if cp.PkgEdges == nil {
		cp.PkgEdges = [][5]int{}
	}
	if cp.FileEdges == nil {
		cp.FileEdges = [][5]int{}
	}
	if cp.SymEdges == nil {
		cp.SymEdges = [][5]int{}
	}
	if cp.HiddenCoupling == nil {
		cp.HiddenCoupling = []HiddenCouplingTuple{}
	}
	if cp.BrainLinks == nil {
		cp.BrainLinks = map[string]RecordLinks{}
	}
	return &cp
}
