package metrics

import "html/template"

// reportTmpl is the report's one page: every value is escaped by
// html/template, and the only markup it takes as is is the SVG drawn here.
var reportTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>fyr metrics {{.Sum.Window.since}} – {{.Sum.Window.until}}</title>
<style>
:root{color-scheme:light;--surface:#fcfcfb;--card:#ffffff;--ink:#0b0b0b;--ink-2:#52514e;--ink-3:#8a8984;--rule:#e6e5e0;--series-1:#2a78d6;--series-2:#eb6834}
@media (prefers-color-scheme:dark){:root{color-scheme:dark;--surface:#1a1a19;--card:#232321;--ink:#ffffff;--ink-2:#c3c2b7;--ink-3:#8f8e86;--rule:#3a3a37;--series-1:#3987e5;--series-2:#d95926}}
*{box-sizing:border-box}
body{margin:0;padding:24px 16px 48px;background:var(--surface);color:var(--ink);font:14px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:760px;margin:0 auto}
h1{font-size:22px;margin:0 0 4px}
h2{font-size:15px;margin:0 0 2px}
.sub,.note{color:var(--ink-2);margin:0 0 12px}
.legend{display:flex;gap:16px;flex-wrap:wrap;margin:0 0 16px;color:var(--ink-2)}
.sw{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:6px;vertical-align:-1px}
section{background:var(--card);border:1px solid var(--rule);border-radius:8px;padding:16px;margin:0 0 16px}
svg{width:100%;height:auto;display:block;overflow:visible}
.grid{stroke:var(--rule);stroke-width:1}
.tick,.value{fill:var(--ink-3);font-size:11px}
.cat,.direct{fill:var(--ink-2);font-size:12px}
.line{fill:none;stroke-width:2;stroke-linejoin:round}
.line.s1{stroke:var(--series-1)}.line.s2{stroke:var(--series-2)}
.dot.s1,.bar.s1,.sw.s1{fill:var(--series-1);background:var(--series-1)}.dot.s2,.bar.s2,.sw.s2{fill:var(--series-2);background:var(--series-2)}
.dot{stroke:var(--card);stroke-width:2}
.track{fill:var(--rule)}
.whisker{stroke:var(--ink);stroke-width:1.5}
table{border-collapse:collapse;width:100%;font-size:12px;font-variant-numeric:tabular-nums}
th,td{text-align:right;padding:3px 8px;border-bottom:1px solid var(--rule)}
th:first-child,td:first-child{text-align:left}
th{color:var(--ink-2);font-weight:600}
details{margin-top:8px}summary{color:var(--ink-2);cursor:pointer}
.tablewrap{overflow-x:auto}
ul.notes{color:var(--ink-2);padding-left:18px}
</style></head><body><main>
<h1>Agent metrics</h1>
<p class="sub">{{.Sum.Window.since}} to {{.Sum.Window.until}} · generated {{.Generated}} by <code>fyr metrics report</code> from the local metrics store</p>
<div class="legend">{{range .Series}}<span><span class="sw {{.Class}}"></span>{{.Label}}</span>{{end}}</div>
<section><h2>Summary</h2><p class="note">n/a is a metric the CLI does not record.</p>
<div class="tablewrap"><table><tr>{{range .Outcome.Head}}<th>{{.}}</th>{{end}}</tr>{{range .Outcome.Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</table></div></section>
{{range .Charts}}<section><h2>{{.Title}}</h2>{{if .Note}}<p class="note">{{.Note}}</p>{{end}}{{.SVG}}
<details><summary>Table</summary><div class="tablewrap"><table><tr>{{range .Table.Head}}<th>{{.}}</th>{{end}}</tr>{{range .Table.Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</table></div></details></section>
{{end}}
{{with .Evals}}<section><h2>Eval rows</h2><p class="note">fyr metrics evals for this window: one line per row and version; the flags read the row's whole history; n/a and - are a rate or a mean the row cannot have.</p>
<div class="tablewrap"><table><tr>{{range .Table.Head}}<th>{{.}}</th>{{end}}</tr>{{range .Table.Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</table></div>
<ul class="notes">{{range .Lines}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
{{if .Sum.Notes}}<section><h2>Notes</h2><ul class="notes">{{range .Sum.Notes}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
</main></body></html>
`))
