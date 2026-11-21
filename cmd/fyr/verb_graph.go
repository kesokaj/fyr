package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/graph"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/webpreview"
)

// runGraph serves `fyr graph` (SDD M25, ADR-528).
func runGraph(c *ctx) error {
	if err := c.validateGraphFlags(); err != nil {
		return err
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	snap, err := search.LoadGraphSnapshot(
		search.Options{Dir: wd, Home: c.home, IncludeDocs: c.graphIncludeDocs},
		search.GraphOptions{IncludeTests: c.graphIncludeTests, IncludeDocs: c.graphIncludeDocs, IncludeVendored: c.graphIncludeVendored},
	)
	if search.IsUsage(err) {
		fmt.Fprintln(c.out, "fyr", err)
		return exitCode(2)
	}
	if err != nil {
		return err
	}
	filePaths := make([]string, len(snap.Files))
	for i, f := range snap.Files {
		filePaths[i] = f.Path
	}
	links, err := brain.IndexRecordLinks(c.home, snap.RepoRoot, filePaths)
	if err != nil {
		return err
	}
	payload := graph.Build(adaptGraphInput(snap, links))
	if c.graphWeb {
		return c.serveGraphWeb(payload)
	}
	return c.writeOrPrintGraph(payload, wd, snap.RepoRoot)
}

func (c *ctx) validateGraphFlags() error {
	given := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	modes := 0
	for _, set := range []bool{c.graphDot, c.graphMD, c.graphJSON, c.graphWeb} {
		if set {
			modes++
		}
	}
	if modes > 1 {
		return usageErr(c.out, "graph", "fyr graph: choose at most one of --dot, --md, --json or --web")
	}
	if c.graphWeb && (given["out"] || c.graphOut != "" || c.force) {
		return usageErr(c.out, "graph", "fyr graph: --web cannot be combined with --out or --force")
	}
	if (given["listen"] || c.graphListen != "") && !c.graphWeb {
		return usageErr(c.out, "graph", "fyr graph: --listen requires --web")
	}
	if given["out"] && (strings.TrimSpace(c.graphOut) == "" || strings.HasPrefix(c.graphOut, "-")) {
		return usageErr(c.out, "graph", "fyr graph: --out needs a file: --out <file>")
	}
	if given["listen"] && (strings.TrimSpace(c.graphListen) == "" || strings.HasPrefix(c.graphListen, "-")) {
		return usageErr(c.out, "graph", "fyr graph: --listen needs an address: --listen <addr>")
	}
	return nil
}

func (c *ctx) serveGraphWeb(p *graph.Payload) error {
	var htmlBuf, jsonBuf, dotBuf, mdBuf bytes.Buffer
	if err := graph.RenderHTML(&htmlBuf, p); err != nil {
		return err
	}
	if err := graph.RenderJSON(&jsonBuf, p); err != nil {
		return err
	}
	if err := graph.RenderDOT(&dotBuf, p); err != nil {
		return err
	}
	if err := graph.RenderMarkdown(&mdBuf, p); err != nil {
		return err
	}
	htmlBytes := htmlBuf.Bytes()
	assets := map[string]webpreview.Asset{
		"/":           {ContentType: "text/html; charset=utf-8", Body: htmlBytes, CSP: graph.DefaultHTMLCSP},
		"/graph.html": {ContentType: "text/html; charset=utf-8", Body: htmlBytes, CSP: graph.DefaultHTMLCSP},
		"/graph.json": {ContentType: "application/json; charset=utf-8", Body: jsonBuf.Bytes(), CSP: "default-src 'none'"},
		"/graph.dot":  {ContentType: "text/vnd.graphviz; charset=utf-8", Body: dotBuf.Bytes(), CSP: "default-src 'none'"},
		"/graph.md":   {ContentType: "text/markdown; charset=utf-8", Body: mdBuf.Bytes(), CSP: "default-src 'none'"},
	}
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveWebPreview(sigCtx, assets, webpreview.Options{
		Listen:     c.graphListen,
		DefaultCSP: graph.DefaultHTMLCSP,
		Out:        c.out,
		Err:        stderr,
	})
}

func (c *ctx) writeOrPrintGraph(p *graph.Payload, wd, repoRoot string) error {
	var render func(io.Writer, *graph.Payload) error
	switch {
	case c.graphDot:
		render = graph.RenderDOT
	case c.graphMD:
		render = graph.RenderMarkdown
	case c.graphJSON:
		render = graph.RenderJSON
	}
	if render != nil {
		if c.graphOut == "" {
			return render(c.out, p)
		}
		var buf bytes.Buffer
		if err := render(&buf, p); err != nil {
			return err
		}
		outPath := c.resolveGraphOutPath(wd)
		if err := graph.WriteFile(outPath, buf.Bytes(), c.force); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "graph: written to "+outPath)
		return nil
	}
	var htmlBuf bytes.Buffer
	if err := graph.RenderHTML(&htmlBuf, p); err != nil {
		return err
	}
	outPath := c.resolveGraphOutPath(wd)
	if outPath == "" {
		outPath = filepath.Join(repoRoot, ".agents", "scratch", "graph.html")
	}
	if err := graph.WriteFile(outPath, htmlBuf.Bytes(), c.force); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "graph: written to "+outPath)
	return nil
}

func (c *ctx) resolveGraphOutPath(wd string) string {
	if c.graphOut == "" {
		return ""
	}
	if !filepath.IsAbs(c.graphOut) && c.dirFlag != "" {
		return filepath.Join(wd, c.graphOut)
	}
	return c.graphOut
}

func adaptGraphInput(snap *search.GraphSnapshot, links map[string]brain.RecordLinks) graph.Input {
	pkgs := make([]graph.PackageInput, len(snap.Packages))
	for i, p := range snap.Packages {
		pkgs[i] = graph.PackageInput{
			Path: p.Path, Layer: p.Layer, FileCount: p.FileCount,
			SymbolCount: p.SymbolCount, LineCount: p.LineCount,
			PageRank: p.PageRank, Community: p.Community,
			InDegree: p.InDegree, OutDegree: p.OutDegree,
			ClampHits: p.ClampHits, RepeatReads: p.RepeatReads,
			ChurnScore: p.ChurnScore, GodNode: p.GodNode,
		}
	}
	files := make([]graph.FileInput, len(snap.Files))
	for i, f := range snap.Files {
		files[i] = graph.FileInput{
			Path: f.Path, Pkg: f.Pkg, PkgIdx: f.PkgIdx, Layer: f.Layer,
			IsTest: f.IsTest, IsDoc: f.IsDoc, SymbolCount: f.SymbolCount,
			LineCount: f.LineCount, PageRank: f.PageRank, Community: f.Community,
			InDegree: f.InDegree, OutDegree: f.OutDegree,
			ClampHits: f.ClampHits, RepeatReads: f.RepeatReads,
			ChurnScore: f.ChurnScore, GodNode: f.GodNode,
		}
	}
	syms := make([]graph.SymbolInput, len(snap.Symbols))
	for i, s := range snap.Symbols {
		syms[i] = graph.SymbolInput{
			Name: s.Name, QualName: s.QualName, Recv: s.Recv, Kind: s.Kind,
			Pkg: s.Pkg, PkgIdx: s.PkgIdx, File: s.File, FileIdx: s.FileIdx,
			StartLine: s.StartLine, EndLine: s.EndLine, Sig: s.Sig,
			Exported: s.Exported, IsTest: s.IsTest, PageRank: s.PageRank,
			Community: s.Community, InRefs: s.InRefs, OutRefs: s.OutRefs, GodNode: s.GodNode,
		}
	}
	return graph.Input{
		RepoName:       snap.RepoName,
		Packages:       pkgs,
		Files:          files,
		Symbols:        syms,
		PkgEdges:       adaptGraphEdges(snap.PkgEdges),
		FileEdges:      adaptGraphEdges(snap.FileEdges),
		SymEdges:       adaptGraphEdges(snap.SymEdges),
		PkgCycles:      snap.PkgCycles,
		HiddenCoupling: adaptHiddenCoupling(snap.HiddenCoupling),
		FileLinks:      adaptBrainLinks(links),
	}
}

func adaptGraphEdges(edges []search.GraphEdge) []graph.EdgeInput {
	out := make([]graph.EdgeInput, len(edges))
	for i, e := range edges {
		out[i] = graph.EdgeInput(e)
	}
	return out
}

func adaptHiddenCoupling(in []search.GraphHiddenCoupling) []graph.HiddenCouplingInput {
	out := make([]graph.HiddenCouplingInput, len(in))
	for i, h := range in {
		out[i] = graph.HiddenCouplingInput{
			FileAIdx: h.FileAIdx,
			FileBIdx: h.FileBIdx,
			Count:    h.Count,
			Kind:     h.Kind,
		}
	}
	return out
}

func adaptBrainLinks(links map[string]brain.RecordLinks) map[string]graph.RecordLinks {
	out := make(map[string]graph.RecordLinks, len(links))
	for path, l := range links {
		out[path] = graph.RecordLinks{
			Bugs:     l.Bugs,
			ADRs:     l.ADRs,
			Memories: l.Memories,
			IDs:      append([]string(nil), l.IDs...),
		}
	}
	return out
}
