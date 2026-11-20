package graph

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// RenderDOT renders p as a deterministic Graphviz DOT digraph grouped by layer,
// escaping backslashes, double quotes, newlines, carriage returns, and tabs.
func RenderDOT(w io.Writer, p *Payload) error {
	if p == nil {
		p = Build(Input{})
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "digraph \"%s\" {\n", escapeDOT(p.Meta.Repo))
	b.WriteString("  graph [rankdir=LR, bgcolor=\"#0b0f17\", fontname=\"Helvetica\", pad=\"0.4\", nodesep=\"0.35\", ranksep=\"0.85\", splines=true, overlap=false];\n")
	b.WriteString("  node [shape=circle, style=filled, fontname=\"Helvetica\", fontsize=10, fontcolor=\"#e2e8f0\", color=\"#1e293b\", penwidth=1.4];\n")
	b.WriteString("  edge [fontname=\"Helvetica\", fontsize=8, arrowsize=0.65, color=\"#64748b88\"];\n\n")

	writeDOTClusters(&b, p)
	writeDOTEdges(&b, p)
	b.WriteString("}\n")
	_, err := w.Write(b.Bytes())
	return err
}

func writeDOTClusters(b *bytes.Buffer, p *Payload) {
	byLayer := map[string][]int{}
	maxPR := 1e-6
	for i, pkg := range p.Packages {
		byLayer[pkg.Layer] = append(byLayer[pkg.Layer], i)
		if pkg.PageRank > maxPR {
			maxPR = pkg.PageRank
		}
	}
	layers := make([]string, 0, len(byLayer))
	for l := range byLayer {
		layers = append(layers, l)
	}
	sort.Strings(layers)
	palette := p.Meta.Palette
	if len(palette) == 0 {
		palette = DefaultPalette
	}
	for _, layer := range layers {
		idxs := byLayer[layer]
		sort.Slice(idxs, func(a, c int) bool {
			pa, pc := p.Packages[idxs[a]].PageRank, p.Packages[idxs[c]].PageRank
			if pa != pc {
				return pa > pc
			}
			return p.Packages[idxs[a]].ID < p.Packages[idxs[c]].ID
		})
		lcolor := safeHexColor(p.Meta.LayerColors[layer], "#94a3b8")
		fmt.Fprintf(b, "  subgraph \"cluster_%s\" {\n", escapeDOT(layer))
		fmt.Fprintf(b, "    label=\"%s\";\n", escapeDOT(layer))
		fmt.Fprintf(b, "    fontcolor=\"%s\";\n    color=\"%s55\";\n    style=\"rounded,dashed\";\n", lcolor, lcolor)
		for _, i := range idxs {
			writeDOTPackageNode(b, i, p.Packages[i], layer, maxPR, palette)
		}
		b.WriteString("  }\n\n")
	}
}

func writeDOTPackageNode(b *bytes.Buffer, idx int, pkg PackageNode, layer string, maxPR float64, palette []string) {
	fill := safeHexColor(palette[max(0, pkg.Community)%len(palette)], "#7aa2f7")
	norm := math.Sqrt(math.Max(0, pkg.PageRank) / math.Max(1e-9, maxPR))
	width := 0.45 + 0.95*norm
	lbl := pkg.ID
	if strings.HasPrefix(lbl, layer+"/") {
		lbl = strings.TrimPrefix(lbl, layer+"/")
	}
	tip := fmt.Sprintf("%s (%d syms, PR=%.4f)", pkg.ID, pkg.SymbolCount, pkg.PageRank)
	if len(pkg.BrainLinks.IDs) > 0 {
		tip += " [" + strings.Join(pkg.BrainLinks.IDs, ", ") + "]"
	}
	fmt.Fprintf(b, "    p%d [label=\"%s\", fillcolor=\"%s\", width=%.2f, fixedsize=false, tooltip=\"%s\"];\n",
		idx, escapeDOT(lbl), fill, width, escapeDOT(tip))
}

func writeDOTEdges(b *bytes.Buffer, p *Payload) {
	cyclePairs := collectCyclePairs(p.Meta.PkgCycles, p.PkgEdges)
	for _, e := range p.PkgEdges {
		u, v, pw, tw := e[0], e[1], e[2], e[3]
		if u < 0 || v < 0 || u >= len(p.Packages) || v >= len(p.Packages) {
			continue
		}
		weight := pw + tw
		if weight <= 0 {
			continue
		}
		penW := 0.7 + math.Min(3.2, math.Log1p(float64(weight))*0.65)
		if cyclePairs[[2]int{u, v}] {
			fmt.Fprintf(b, "  p%d -> p%d [penwidth=%.2f, color=\"#f7768e\", label=\"%d\", fontcolor=\"#f7768e\"];\n",
				u, v, penW, weight)
			continue
		}
		tip := fmt.Sprintf("%s -> %s (%d refs)", p.Packages[u].ID, p.Packages[v].ID, weight)
		fmt.Fprintf(b, "  p%d -> p%d [penwidth=%.2f, tooltip=\"%s\"];\n", u, v, penW, escapeDOT(tip))
	}
}

func collectCyclePairs(cycles [][]int, edges [][5]int) map[[2]int]bool {
	pairs := map[[2]int]bool{}
	for _, comp := range cycles {
		set := make(map[int]bool, len(comp))
		for _, n := range comp {
			set[n] = true
		}
		for _, e := range edges {
			if e[2] > 0 && set[e[0]] && set[e[1]] {
				pairs[[2]int{e[0], e[1]}] = true
			}
		}
	}
	return pairs
}

func escapeDOT(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				b.WriteByte(' ')
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

func safeHexColor(c, fallback string) string {
	if len(c) == 7 && c[0] == '#' {
		ok := true
		for i := 1; i < 7; i++ {
			ch := c[i]
			ok = ok && ((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F'))
		}
		if ok {
			return c
		}
	}
	return fallback
}
