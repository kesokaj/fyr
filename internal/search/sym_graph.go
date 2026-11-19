package search

import "strings"

// RankedSymbol is one AST symbol declaration scored by Personalized PageRank.
type RankedSymbol struct {
	Idx       int
	Project   string
	Path      string
	OID       string
	Name      string
	Recv      string
	Kind      string
	Pkg       string
	StartLine int
	EndLine   int
	Sig       string
	Exported  bool
	BodyHash  string
	Score     float64
	InRefs    int
}

// QualName returns Recv.Name for methods and Name for package-level symbols.
func (s RankedSymbol) QualName() string {
	if s.Recv != "" {
		return s.Recv + "." + s.Name
	}
	return s.Name
}

type graphEdge struct {
	fromIdx      int
	toIdx        int
	kind         string
	confidence   string
	qualifiedPkg bool
	weight       float64
	line         int
	fromPath     string
}

type symGraph struct {
	syms      []RankedSymbol
	out       [][]graphEdge
	in        [][]graphEdge
	byName    map[string][]int
	byQual    map[string][]int
	byPath    map[string][]int
	byOIDFrom map[string][]int
	defFile   map[string]int
}

func (sc *scope) loadSymGraph() (*symGraph, error) {
	syms, err := sc.querySymbolNodes()
	if err != nil {
		return nil, err
	}
	g := newSymGraph(syms)
	if err := sc.populateGraphEdges(g); err != nil {
		return nil, err
	}
	return g, nil
}

func (sc *scope) querySymbolNodes() ([]RankedSymbol, error) {
	q := `SELECT f.project, f.path, sy.oid, sy.name, sy.recv, sy.kind, sy.pkg, sy.start_line, sy.end_line, sy.sig, sy.exported, sy.body_hash
		FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project IN ` + sc.in() + `
		ORDER BY f.path, sy.start_line, sy.end_line, sy.recv, sy.name`
	rows, err := sc.st.db.Query(q, sc.keys()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var syms []RankedSymbol
	for rows.Next() {
		var s RankedSymbol
		var exp int
		if err := rows.Scan(&s.Project, &s.Path, &s.OID, &s.Name, &s.Recv, &s.Kind, &s.Pkg, &s.StartLine, &s.EndLine, &s.Sig, &exp, &s.BodyHash); err != nil {
			return nil, err
		}
		s.Exported = exp != 0
		s.Idx = len(syms)
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

func newSymGraph(syms []RankedSymbol) *symGraph {
	g := &symGraph{
		syms: syms, out: make([][]graphEdge, len(syms)), in: make([][]graphEdge, len(syms)),
		byName: map[string][]int{}, byQual: map[string][]int{}, byPath: map[string][]int{},
		byOIDFrom: map[string][]int{}, defFile: map[string]int{},
	}
	seenFile := map[string]bool{}
	for i, s := range syms {
		qn := s.QualName()
		g.byName[s.Name] = append(g.byName[s.Name], i)
		g.byQual[qn] = append(g.byQual[qn], i)
		g.byPath[s.Path] = append(g.byPath[s.Path], i)
		g.byOIDFrom[s.OID+"\x00"+qn] = append(g.byOIDFrom[s.OID+"\x00"+qn], i)
		if s.Recv != "" {
			g.byOIDFrom[s.OID+"\x00"+s.Name] = append(g.byOIDFrom[s.OID+"\x00"+s.Name], i)
		}
		if fk := s.Name + "\x00" + s.Path; !seenFile[fk] {
			seenFile[fk] = true
			g.defFile[s.Name]++
		}
	}
	return g
}

type rawSymRef struct {
	path, oid, fromSym, toPkg, toName, kind, confidence string
	weight                                              float64
	line                                                int
}

func (sc *scope) populateGraphEdges(g *symGraph) error {
	q := `SELECT f.path, r.oid, r.from_sym, r.to_pkg, r.to_name, r.kind, r.confidence, r.weight, r.line
		FROM sym_refs r JOIN files f ON f.oid = r.oid WHERE f.project IN ` + sc.in() + `
		ORDER BY f.path, r.line, r.from_sym, r.to_name`
	rows, err := sc.st.db.Query(q, sc.keys()...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r rawSymRef
		if err := rows.Scan(&r.path, &r.oid, &r.fromSym, &r.toPkg, &r.toName, &r.kind, &r.confidence, &r.weight, &r.line); err != nil {
			return err
		}
		g.addRawRef(r)
	}
	return rows.Err()
}

func (g *symGraph) addRawRef(r rawSymRef) {
	fromIdxs := g.byOIDFrom[r.oid+"\x00"+r.fromSym]
	fromPkg := ""
	if len(fromIdxs) > 0 {
		fromPkg = g.syms[fromIdxs[0]].Pkg
	} else if pathIdxs := g.byPath[r.path]; len(pathIdxs) > 0 {
		fromPkg = g.syms[pathIdxs[0]].Pkg
	}
	for _, toIdx := range g.resolveTargets(fromPkg, r.toPkg, r.toName) {
		g.syms[toIdx].InRefs++
		qualified := r.toPkg != ""
		if len(fromIdxs) == 0 {
			g.in[toIdx] = append(g.in[toIdx], graphEdge{fromIdx: -1, toIdx: toIdx, kind: r.kind, confidence: r.confidence, qualifiedPkg: qualified, weight: r.weight, line: r.line, fromPath: r.path})
			continue
		}
		for _, fromIdx := range fromIdxs {
			if fromIdx != toIdx {
				e := graphEdge{fromIdx: fromIdx, toIdx: toIdx, kind: r.kind, confidence: r.confidence, qualifiedPkg: qualified, weight: r.weight, line: r.line, fromPath: r.path}
				g.out[fromIdx] = append(g.out[fromIdx], e)
				g.in[toIdx] = append(g.in[toIdx], e)
			}
		}
	}
}

func (g *symGraph) resolveTargets(fromPkg, toPkg, toName string) []int {
	if strings.Contains(toName, ".") {
		if idxs := g.byQual[toName]; len(idxs) > 0 {
			return g.filterByPkg(idxs, toPkg)
		}
	}
	if toPkg != "" {
		if idxs := g.byQual[toPkg+"."+toName]; len(idxs) > 0 {
			return idxs
		}
		if idxs := g.filterByPkg(g.byName[toName], toPkg); len(idxs) > 0 {
			return g.preferIfaceOrTopLevel(idxs)
		}
	}
	cands := g.byName[toName]
	if len(cands) == 0 {
		return nil
	}
	if fromPkg != "" {
		if same := g.filterByPkg(cands, fromPkg); len(same) > 0 {
			return g.preferIfaceOrTopLevel(same)
		}
	}
	if len(cands) > maxImplicitImplementers {
		return nil
	}
	return g.preferIfaceOrTopLevel(cands)
}

func (g *symGraph) preferIfaceOrTopLevel(idxs []int) []int {
	var pref []int
	for _, idx := range idxs {
		if g.syms[idx].Recv == "" || g.syms[idx].Kind == "interface_method" {
			pref = append(pref, idx)
		}
	}
	if len(pref) > 0 {
		return pref
	}
	return idxs
}

func (g *symGraph) filterByPkg(idxs []int, pkg string) []int {
	if pkg == "" {
		return idxs
	}
	var out []int
	for _, idx := range idxs {
		if g.syms[idx].Pkg == pkg {
			out = append(out, idx)
		}
	}
	return out
}
