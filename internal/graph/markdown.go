package graph

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderMarkdown writes a deterministic Markdown architecture summary of p.
func RenderMarkdown(w io.Writer, p *Payload) error {
	if p == nil {
		p = Build(Input{})
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Architecture Graph — %s\n\n", escapeMDInline(p.Meta.Repo))
	writeMDSummary(&b, p)
	writeMDGodPackages(&b, p)
	writeMDGodSymbols(&b, p)
	writeMDCycles(&b, p)
	writeMDHiddenCoupling(&b, p)
	writeMDChurn(&b, p)
	writeMDBrainHotspots(&b, p)
	_, err := w.Write(b.Bytes())
	return err
}

func writeMDSummary(b *bytes.Buffer, p *Payload) {
	m := p.Meta
	prodPkgEdges := countProdEdges(p.PkgEdges)
	prodFileEdges := countProdEdges(p.FileEdges)
	prodSymEdges := countProdEdges(p.SymEdges)
	b.WriteString("## Summary\n\n")
	b.WriteString("| Metric | Production | Total |\n|---|---:|---:|\n")
	fmt.Fprintf(b, "| Packages | %d | %d |\n", m.ProdPackages, m.TotalPackages)
	fmt.Fprintf(b, "| Files | %d | %d |\n", m.ProdFiles, m.TotalFiles)
	fmt.Fprintf(b, "| Symbols | %d | %d |\n", m.ProdSymbols, m.TotalSymbols)
	fmt.Fprintf(b, "| Package Edges | %d | %d |\n", prodPkgEdges, m.PkgEdges)
	fmt.Fprintf(b, "| File Edges | %d | %d |\n", prodFileEdges, m.FileEdges)
	fmt.Fprintf(b, "| Symbol Edges | %d | %d |\n", prodSymEdges, m.SymEdges)
	fmt.Fprintf(b, "| Package Cycles | %d | %d |\n", len(m.PkgCycles), len(m.PkgCycles))
	editPairs, readPairs := resolvedHiddenCouplingCounts(p)
	fmt.Fprintf(b, "| Hidden Co-Activity Pairs | %d | %d |\n", len(p.HiddenCoupling), len(p.HiddenCoupling))
	fmt.Fprintf(b, "| Hidden Co-Edit Pairs | %d | %d |\n", editPairs, editPairs)
	fmt.Fprintf(b, "| Hidden Co-Read Pairs | %d | %d |\n\n", readPairs, readPairs)
}

func countProdEdges(edges [][5]int) int {
	n := 0
	for _, e := range edges {
		if e[2] > 0 {
			n++
		}
	}
	return n
}

func writeMDGodPackages(b *bytes.Buffer, p *Payload) {
	b.WriteString("## Top 10 God Packages\n\n")
	var pkgs []PackageNode
	for _, pkg := range p.Packages {
		if !pkg.IsTest {
			pkgs = append(pkgs, pkg)
		}
	}
	if len(pkgs) == 0 {
		pkgs = append(pkgs, p.Packages...)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].PageRank != pkgs[j].PageRank {
			return pkgs[i].PageRank > pkgs[j].PageRank
		}
		if pkgs[i].InDegree != pkgs[j].InDegree {
			return pkgs[i].InDegree > pkgs[j].InDegree
		}
		return pkgs[i].ID < pkgs[j].ID
	})
	if len(pkgs) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	if len(pkgs) > 10 {
		pkgs = pkgs[:10]
	}
	b.WriteString("| # | Package | Layer | Community | Symbols | In | Out | PageRank |\n|---:|---|---|---:|---:|---:|---:|---:|\n")
	for i, pkg := range pkgs {
		fmt.Fprintf(b, "| %d | `%s` | %s | C%d | %d | %d | %d | %.5f |\n",
			i+1, escapeMDCode(pkg.ID), escapeMDInline(pkg.Layer), pkg.Community,
			pkg.SymbolCount, pkg.InDegree, pkg.OutDegree, pkg.PageRank)
	}
	b.WriteString("\n")
}

func writeMDGodSymbols(b *bytes.Buffer, p *Payload) {
	b.WriteString("## Top 10 God Symbols\n\n")
	var syms []SymbolNode
	for _, s := range p.Symbols {
		if !s.IsTest {
			syms = append(syms, s)
		}
	}
	if len(syms) == 0 {
		syms = append(syms, p.Symbols...)
	}
	sort.Slice(syms, func(i, j int) bool {
		if syms[i].PageRank != syms[j].PageRank {
			return syms[i].PageRank > syms[j].PageRank
		}
		if syms[i].InDegree != syms[j].InDegree {
			return syms[i].InDegree > syms[j].InDegree
		}
		return syms[i].QualName < syms[j].QualName
	})
	if len(syms) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	if len(syms) > 10 {
		syms = syms[:10]
	}
	b.WriteString("| # | Symbol | Kind | File | In | Out | PageRank |\n|---:|---|---|---|---|---:|---:|\n")
	for i, s := range syms {
		label := s.Disp
		if s.Pkg != "" {
			label = s.Pkg + "." + s.Disp
		}
		fmt.Fprintf(b, "| %d | `%s` | %s | `%s:%d` | %d | %d | %.5f |\n",
			i+1, escapeMDCode(label), escapeMDInline(s.Kind), escapeMDCode(s.File), s.StartLine,
			s.InDegree, s.OutDegree, s.PageRank)
	}
	b.WriteString("\n")
}

func writeMDCycles(b *bytes.Buffer, p *Payload) {
	b.WriteString("## Package Cycles\n\n")
	if len(p.Meta.PkgCycles) == 0 {
		b.WriteString("0 cycles detected among production packages.\n\n")
		return
	}
	for i, comp := range p.Meta.PkgCycles {
		names := make([]string, 0, len(comp))
		for _, idx := range comp {
			if idx >= 0 && idx < len(p.Packages) {
				names = append(names, "`"+escapeMDCode(p.Packages[idx].ID)+"`")
			}
		}
		fmt.Fprintf(b, "%d. (%d packages): %s\n", i+1, len(comp), strings.Join(names, " <-> "))
	}
	b.WriteString("\n")
}

func writeMDHiddenCoupling(b *bytes.Buffer, p *Payload) {
	b.WriteString("## Hidden Co-Activity Coupling\n\n")
	if len(p.HiddenCoupling) == 0 {
		b.WriteString("None detected.\n\n")
		return
	}
	b.WriteString("| File A | File B | Co-Activity | Kind |\n|---|---|---:|---|\n")
	limit := min(15, len(p.HiddenCoupling))
	for _, h := range p.HiddenCoupling[:limit] {
		if h.U < 0 || h.V < 0 || h.U >= len(p.Files) || h.V >= len(p.Files) {
			continue
		}
		fmt.Fprintf(b, "| `%s` | `%s` | %d | %s |\n",
			escapeMDCode(p.Files[h.U].ID), escapeMDCode(p.Files[h.V].ID), h.Count, escapeMDInline(h.Kind))
	}
	b.WriteString("\n")
}

func writeMDChurn(b *bytes.Buffer, p *Payload) {
	b.WriteString("## High-Churn Files\n\n")
	var hot []FileNode
	for _, f := range p.Files {
		if f.ChurnScore > 0 {
			hot = append(hot, f)
		}
	}
	sort.Slice(hot, func(i, j int) bool {
		if hot[i].ChurnScore != hot[j].ChurnScore {
			return hot[i].ChurnScore > hot[j].ChurnScore
		}
		return hot[i].ID < hot[j].ID
	})
	if len(hot) == 0 {
		b.WriteString("None recorded.\n\n")
		return
	}
	b.WriteString("| File | Lines | Clamp Hits | Repeat Reads | Churn Score |\n|---|---:|---:|---:|---:|\n")
	for _, f := range hot[:min(15, len(hot))] {
		fmt.Fprintf(b, "| `%s` | %d | %d | %d | %d |\n",
			escapeMDCode(f.ID), f.LineCount, f.ClampHits, f.RepeatReads, f.ChurnScore)
	}
	b.WriteString("\n")
}

func writeMDBrainHotspots(b *bytes.Buffer, p *Payload) {
	b.WriteString("## Brain-Linked Hotspots\n\n")
	var linked []FileNode
	for _, f := range p.Files {
		if f.BrainLinks.Bugs+f.BrainLinks.ADRs+f.BrainLinks.Memories+len(f.BrainLinks.IDs) > 0 {
			linked = append(linked, f)
		}
	}
	sort.Slice(linked, func(i, j int) bool {
		ti := linked[i].BrainLinks.Bugs + linked[i].BrainLinks.ADRs + linked[i].BrainLinks.Memories
		tj := linked[j].BrainLinks.Bugs + linked[j].BrainLinks.ADRs + linked[j].BrainLinks.Memories
		if ti != tj {
			return ti > tj
		}
		return linked[i].ID < linked[j].ID
	})
	if len(linked) == 0 {
		b.WriteString("None linked.\n")
		return
	}
	b.WriteString("| File | Bugs | ADRs | Memories | Record IDs |\n|---|---:|---:|---:|---|\n")
	for _, f := range linked[:min(20, len(linked))] {
		ids := make([]string, len(f.BrainLinks.IDs))
		for i, id := range f.BrainLinks.IDs {
			ids[i] = "`" + escapeMDCode(id) + "`"
		}
		fmt.Fprintf(b, "| `%s` | %d | %d | %d | %s |\n",
			escapeMDCode(f.ID), f.BrainLinks.Bugs, f.BrainLinks.ADRs, f.BrainLinks.Memories, strings.Join(ids, ", "))
	}
}

func escapeMDInline(s string) string {
	r := strings.NewReplacer("|", `\|`, "\n", " ", "\r", " ", "\t", " ")
	return strings.TrimSpace(r.Replace(s))
}

func escapeMDCode(s string) string {
	r := strings.NewReplacer("`", "'", "|", `\|`, "\n", " ", "\r", " ", "\t", " ")
	return strings.TrimSpace(r.Replace(s))
}
