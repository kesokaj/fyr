package search

import (
	"database/sql"
	"strings"
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

type indexedFileMeta struct {
	project   string
	path      string
	oid       string
	pkg       string
	lineCount int
}

type resolvedImportEdge struct {
	fromPath string
	fromPkg  string
	toPath   string
	toPkg    string
	line     int
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
	syms        []RankedSymbol
	files       []indexedFileMeta
	out         [][]graphEdge
	in          [][]graphEdge
	byName      map[string][]int
	byQual      map[string][]int
	byPath      map[string][]int
	byOIDFrom   map[string][]int
	defFile     map[string]int
	pkgSet      map[string]bool
	fileByStem  map[string]string
	goMods      map[string]string
	pkgImports  map[string]map[string]bool
	importSeen  map[string]bool
	importEdges []resolvedImportEdge
}

func (sc *scope) loadSymGraph() (*symGraph, error) {
	return sc.loadSymGraphOpts(false)
}

func (sc *scope) loadSymGraphOpts(includeDocs bool) (*symGraph, error) {
	return buildSymGraphFromDB(sc.st.db, sc.projects, loadGoModules(sc), includeDocs)
}

func buildSymGraphFromDB(db *sql.DB, projects []string, goMods map[string]string, includeDocs bool) (*symGraph, error) {
	if len(projects) == 0 {
		return newSymGraphWithMeta(nil, nil, goMods), nil
	}
	files, err := queryIndexedFilesDB(db, projects, includeDocs)
	if err != nil {
		return nil, err
	}
	syms, err := querySymbolNodesDB(db, projects, includeDocs)
	if err != nil {
		return nil, err
	}
	g := newSymGraphWithMeta(syms, files, goMods)
	rawRefs, err := queryRawSymRefsDB(db, projects, includeDocs)
	if err != nil {
		return nil, err
	}
	for _, r := range rawRefs {
		if r.kind == "import" {
			g.addImportRef(r)
		}
	}
	for _, r := range rawRefs {
		if r.kind != "import" {
			g.addRawRef(r)
		}
	}
	return g, nil
}

func projectsInClause(projects []string) (string, []any) {
	args := make([]any, len(projects))
	for i, p := range projects {
		args[i] = p
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(projects)), ",") + ")", args
}

func queryIndexedFilesDB(db *sql.DB, projects []string, includeDocs bool) ([]indexedFileMeta, error) {
	inSQL, args := projectsInClause(projects)
	hasLC, _ := tableHasColumn(db, "blobs", "line_count")
	lcExpr := "0"
	if hasLC {
		lcExpr = "COALESCE(b.line_count, 0)"
	}
	q := `SELECT f.project, f.path, f.oid, ` + lcExpr + `
		FROM files f LEFT JOIN blobs b ON b.oid = f.oid
		WHERE f.project IN ` + inSQL + ` ORDER BY f.path`
	return scanIndexedFilesDB(db, q, args, includeDocs)
}

func queryIndexedFilePathsDB(db *sql.DB, projects []string, includeDocs bool) ([]indexedFileMeta, error) {
	inSQL, args := projectsInClause(projects)
	q := `SELECT f.project, f.path, f.oid, 0 FROM files f WHERE f.project IN ` + inSQL + ` ORDER BY f.path`
	return scanIndexedFilesDB(db, q, args, includeDocs)
}

func scanIndexedFilesDB(db *sql.DB, q string, args []any, includeDocs bool) ([]indexedFileMeta, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []indexedFileMeta
	for rows.Next() {
		var m indexedFileMeta
		if err := rows.Scan(&m.project, &m.path, &m.oid, &m.lineCount); err != nil {
			return nil, err
		}
		if isExcludedASTPath(m.path) || (!isCodePath(m.path) && !(includeDocs && isDocPath(m.path))) {
			continue
		}
		m.pkg = canonicalPkgDir(m.path)
		out = append(out, m)
	}
	return out, rows.Err()
}

func querySymbolNodesDB(db *sql.DB, projects []string, includeDocs bool) ([]RankedSymbol, error) {
	inSQL, args := projectsInClause(projects)
	q := `SELECT f.project, f.path, sy.oid, sy.name, sy.recv, sy.kind, sy.pkg, sy.start_line, sy.end_line, sy.sig, sy.exported, sy.body_hash
		FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project IN ` + inSQL + `
		ORDER BY f.path, sy.start_line, sy.end_line, sy.recv, sy.name`
	rows, err := db.Query(q, args...)
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
		if isExcludedASTPath(s.Path) || (!includeDocs && (isDocPath(s.Path) || s.Kind == "heading")) {
			continue
		}
		if !isDocPath(s.Path) && s.Kind != "heading" {
			s.Pkg = canonicalPkgDir(s.Path)
		}
		s.Exported = exp != 0
		s.Idx = len(syms)
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

func newSymGraph(syms []RankedSymbol) *symGraph {
	return newSymGraphWithMeta(syms, nil, nil)
}

func newSymGraphWithMeta(syms []RankedSymbol, files []indexedFileMeta, goMods map[string]string) *symGraph {
	g := &symGraph{
		syms: syms, files: files,
		out: make([][]graphEdge, len(syms)), in: make([][]graphEdge, len(syms)),
		byName: map[string][]int{}, byQual: map[string][]int{}, byPath: map[string][]int{},
		byOIDFrom: map[string][]int{}, defFile: map[string]int{},
		pkgSet: map[string]bool{}, fileByStem: map[string]string{},
		goMods: goMods, pkgImports: map[string]map[string]bool{}, importSeen: map[string]bool{},
	}
	for _, f := range files {
		if f.pkg != "" {
			g.pkgSet[f.pkg] = true
		}
		g.fileByStem[f.path] = f.path
		if isTSSource(f.path) {
			stem := trimTSExtension(f.path)
			if _, exists := g.fileByStem[stem]; !exists {
				g.fileByStem[stem] = f.path
			}
		}
	}
	indexGraphSymbols(g)
	return g
}

func indexGraphSymbols(g *symGraph) {
	seenFile := map[string]bool{}
	for i, s := range g.syms {
		qn := s.QualName()
		g.byName[s.Name] = append(g.byName[s.Name], i)
		g.byQual[qn] = append(g.byQual[qn], i)
		g.byPath[s.Path] = append(g.byPath[s.Path], i)
		g.byOIDFrom[s.OID+"\x00"+qn] = append(g.byOIDFrom[s.OID+"\x00"+qn], i)
		if s.Recv != "" {
			g.byOIDFrom[s.OID+"\x00"+s.Name] = append(g.byOIDFrom[s.OID+"\x00"+s.Name], i)
		}
		if s.Pkg != "" {
			g.pkgSet[s.Pkg] = true
		}
		if _, exists := g.fileByStem[s.Path]; !exists {
			g.fileByStem[s.Path] = s.Path
			if isTSSource(s.Path) {
				g.fileByStem[trimTSExtension(s.Path)] = s.Path
			}
		}
		if fk := s.Name + "\x00" + s.Path; !seenFile[fk] {
			seenFile[fk] = true
			g.defFile[s.Name]++
		}
	}
}

func matchesPromptSymbolName(s RankedSymbol, terms []string) bool {
	nameLow := strings.ToLower(s.Name)
	if len(terms) == 0 || (len(nameLow) < hookMinIdentLen && strings.HasSuffix(strings.ToLower(s.Path), ".sh")) {
		return false
	}
	qualLow := strings.ToLower(s.QualName())
	for _, t := range terms {
		tl := strings.ToLower(t)
		if tl == "" || strings.Contains(tl, "/") || ftsFallbackStopwords[tl] {
			continue
		}
		if nameLow == tl || qualLow == tl || (len(tl) >= 4 && (strings.Contains(nameLow, tl) || strings.Contains(qualLow, tl))) {
			return true
		}
	}
	return false
}
