package search

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
)

const (
	multInPrompt   = 10.0
	multLongIdent  = 10.0
	multUnexported = 0.1
	multManyFiles  = 0.1
	multActiveFile = 50.0
	pprIterations  = 20
	pprDamping     = 0.85
)

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
	fromIdx    int
	toIdx      int
	kind       string
	confidence string
	weight     float64
	line       int
	fromPath   string
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
	var refs []rawSymRef
	for rows.Next() {
		var r rawSymRef
		if err := rows.Scan(&r.path, &r.oid, &r.fromSym, &r.toPkg, &r.toName, &r.kind, &r.confidence, &r.weight, &r.line); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range refs {
		g.addRawRef(r)
	}
	return nil
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
		if len(fromIdxs) == 0 {
			g.in[toIdx] = append(g.in[toIdx], graphEdge{fromIdx: -1, toIdx: toIdx, kind: r.kind, confidence: r.confidence, weight: r.weight, line: r.line, fromPath: r.path})
			continue
		}
		for _, fromIdx := range fromIdxs {
			if fromIdx != toIdx {
				e := graphEdge{fromIdx: fromIdx, toIdx: toIdx, kind: r.kind, confidence: r.confidence, weight: r.weight, line: r.line, fromPath: r.path}
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

func (sc *scope) sessionActiveFiles(sessionID string, extra map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range extra {
		if v {
			out[strings.TrimPrefix(filepath.ToSlash(k), "./")] = true
		}
	}
	if sessionID == "" || sc.st == nil {
		return out
	}
	rows, err := sc.st.db.Query(`SELECT DISTINCT resolved_path FROM read_ledger WHERE conversation_id = ? AND start_line >= -1`, sessionID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out[filepath.ToSlash(p)] = true
			for _, root := range sc.projects {
				if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
					out[filepath.ToSlash(rel)] = true
				}
			}
		}
	}
	return out
}

// rankSymbols computes 20-iteration Personalized PageRank over the scope's AST symbol graph.
func (sc *scope) rankSymbols(query string, activeFiles map[string]bool) ([]RankedSymbol, error) {
	g, err := sc.loadSymGraph()
	if err != nil {
		return nil, err
	}
	return g.rank(query, activeFiles), nil
}

func (g *symGraph) rank(query string, activeFiles map[string]bool) []RankedSymbol {
	if len(g.syms) == 0 {
		return nil
	}
	terms := parseQueryTerms(query)
	mul, matched := g.computeMultipliers(terms, activeFiles)
	pers := g.buildPersonalization(mul, matched, activeFiles)
	scores := g.runPowerMethod(mul, pers, len(terms) > 0 || len(activeFiles) > 0)
	for i := range g.syms {
		g.syms[i].Score = scores[i]
	}
	out := make([]RankedSymbol, len(g.syms))
	copy(out, g.syms)
	sort.Slice(out, func(i, j int) bool {
		if math.Abs(out[i].Score-out[j].Score) > 1e-12 {
			return out[i].Score > out[j].Score
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].StartLine < out[j].StartLine
	})
	return out
}

func (g *symGraph) computeMultipliers(terms []string, activeFiles map[string]bool) ([]float64, []bool) {
	mul, matched := make([]float64, len(g.syms)), make([]bool, len(g.syms))
	for i, s := range g.syms {
		m := 1.0
		if matched[i] = matchesPromptTerms(s, terms); matched[i] {
			m *= multInPrompt
		}
		if len(s.Name) >= 8 {
			m *= multLongIdent
		}
		if !s.Exported {
			m *= multUnexported
		}
		if g.defFile[s.Name] > 5 {
			m *= multManyFiles
		}
		if activeFiles[s.Path] {
			m *= multActiveFile
		}
		if !matched[i] && strings.HasSuffix(s.Path, "_test.go") {
			m *= multUnexported
		}
		mul[i] = m
	}
	return mul, matched
}

func (g *symGraph) buildPersonalization(mul []float64, matched []bool, activeFiles map[string]bool) []float64 {
	pers, hasSeed, sum := make([]float64, len(g.syms)), false, 0.0
	for i, s := range g.syms {
		if matched[i] || activeFiles[s.Path] {
			hasSeed = true
			break
		}
	}
	for i, s := range g.syms {
		w := mul[i] / math.Sqrt(float64(max(1, s.InRefs)))
		if hasSeed {
			if matched[i] || activeFiles[s.Path] {
				w = mul[i]
			} else {
				w *= 1e-3
			}
		}
		pers[i] = w
		sum += w
	}
	if sum > 0 {
		for i := range pers {
			pers[i] /= sum
		}
	}
	return pers
}

func (g *symGraph) runPowerMethod(mul, pers []float64, personalized bool) []float64 {
	n := len(g.syms)
	scores, next, nodeW := make([]float64, n), make([]float64, n), make([]float64, n)
	for i, s := range g.syms {
		nodeW[i] = mul[i] / math.Sqrt(float64(max(1, s.InRefs)))
	}
	copy(scores, pers)
	revFactor := 0.0
	if personalized {
		revFactor = 0.35
	}
	for iter := 0; iter < pprIterations; iter++ {
		for i := range next {
			next[i] = (1.0 - pprDamping) * pers[i]
		}
		for u := 0; u < n; u++ {
			g.stepNodePower(u, mul[u], nodeW, scores[u], revFactor, next)
		}
		copy(scores, next)
	}
	return scores
}

func (g *symGraph) stepNodePower(u int, selfW float64, nodeW []float64, scoreU, revFactor float64, next []float64) {
	denom := selfW
	for _, e := range g.out[u] {
		denom += e.weight * nodeW[e.toIdx]
	}
	for _, e := range g.in[u] {
		if e.fromIdx >= 0 && revFactor > 0 {
			denom += revFactor * e.weight * nodeW[e.fromIdx]
		}
	}
	if denom <= 0 {
		return
	}
	mass := pprDamping * scoreU / denom
	next[u] += mass * selfW
	for _, e := range g.out[u] {
		next[e.toIdx] += mass * e.weight * nodeW[e.toIdx]
	}
	for _, e := range g.in[u] {
		if e.fromIdx >= 0 && revFactor > 0 {
			next[e.fromIdx] += mass * revFactor * e.weight * nodeW[e.fromIdx]
		}
	}
}
