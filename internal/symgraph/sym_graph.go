package symgraph

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

// FileMeta holds indexed file metadata used to build a symbol graph.
type FileMeta struct {
	Project   string
	Path      string
	OID       string
	Pkg       string
	LineCount int
}

// ResolvedImportEdge is one resolved file/package import edge.
type ResolvedImportEdge struct {
	FromPath string
	FromPkg  string
	ToPath   string
	ToPkg    string
	Line     int
}

// Edge is one directed symbol reference edge in a Graph.
type Edge struct {
	FromIdx      int
	ToIdx        int
	Kind         string
	Confidence   string
	QualifiedPkg bool
	Weight       float64
	Line         int
	FromPath     string
}

// Graph is the in-memory AST symbol and import dependency graph.
type Graph struct {
	Syms        []RankedSymbol
	Files       []FileMeta
	Out         [][]Edge
	In          [][]Edge
	ByPath      map[string][]int
	ImportEdges []ResolvedImportEdge
	ByName      map[string][]int
	byQual      map[string][]int
	byOIDFrom   map[string][]int
	defFile     map[string]int
	pkgSet      map[string]bool
	fileByStem  map[string]string
	goMods      map[string]string
	pkgImports  map[string]map[string]bool
	importSeen  map[string]bool
}

// LoadSymGraphOpts builds a symbol graph from sc's database and workspace projects.
func (sc *Scope) LoadSymGraphOpts(includeDocs bool) (*Graph, error) {
	return BuildSymGraphFromDB(sc.DB, sc.Projects, LoadGoModules(sc), includeDocs)
}

// BuildSymGraphFromDB loads a symbol graph directly from db.
func BuildSymGraphFromDB(db *sql.DB, projects []string, goMods map[string]string, includeDocs bool) (*Graph, error) {
	return BuildSymGraphFromDBOpts(db, projects, goMods, includeDocs, false)
}

// BuildSymGraphFromDBOpts loads a symbol graph with optional docs and vendored skills.
func BuildSymGraphFromDBOpts(db *sql.DB, projects []string, goMods map[string]string, includeDocs, includeVendored bool) (*Graph, error) {
	if len(projects) == 0 {
		return NewGraphWithMeta(nil, nil, goMods), nil
	}
	files, err := QueryIndexedFilesDB(db, projects, includeDocs)
	if err != nil {
		return nil, err
	}
	syms, err := QuerySymbolNodesDB(db, projects, includeDocs)
	if err != nil {
		return nil, err
	}
	rawRefs, err := queryRawSymRefsDB(db, projects, includeDocs)
	if err != nil {
		return nil, err
	}
	if includeVendored {
		var vendRefs []RawSymRef
		files, syms, vendRefs, err = appendVendoredGraphData(db, projects, includeDocs, files, syms)
		if err != nil {
			return nil, err
		}
		rawRefs = append(rawRefs, vendRefs...)
	}
	g := NewGraphWithMeta(syms, files, goMods)
	populateSymGraphRefs(g, rawRefs)
	return g, nil
}

func populateSymGraphRefs(g *Graph, rawRefs []RawSymRef) {
	for _, r := range rawRefs {
		if r.Kind == "import" {
			g.AddImportRef(r)
		}
	}
	for _, r := range rawRefs {
		if r.Kind != "import" {
			g.AddRawRef(r)
		}
	}
}

func projectsInClause(projects []string) (string, []any) {
	args := make([]any, len(projects))
	for i, p := range projects {
		args[i] = p
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(projects)), ",") + ")", args
}

func queryIndexedFilesSQL(db *sql.DB, projects []string) (string, []any) {
	inSQL, args := projectsInClause(projects)
	hasLC, _ := TableHasColumn(db, "blobs", "line_count")
	lcExpr := "0"
	if hasLC {
		lcExpr = "COALESCE(b.line_count, 0)"
	}
	q := `SELECT f.project, f.Path, f.oid, ` + lcExpr + `
		FROM files f LEFT JOIN blobs b ON b.oid = f.oid
		WHERE f.project IN ` + inSQL + ` ORDER BY f.Path`
	return q, args
}

// QueryIndexedFilesDB returns indexed file metadata for projects.
func QueryIndexedFilesDB(db *sql.DB, projects []string, includeDocs bool) ([]FileMeta, error) {
	q, args := queryIndexedFilesSQL(db, projects)
	return scanIndexedFilesDB(db, q, args, includeDocs, false)
}

func scanIndexedFilesDB(db *sql.DB, q string, args []any, includeDocs, vendoredOnly bool) ([]FileMeta, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileMeta
	for rows.Next() {
		var m FileMeta
		if err := rows.Scan(&m.Project, &m.Path, &m.OID, &m.LineCount); err != nil {
			return nil, err
		}
		if !keepScannedFile(m.Path, includeDocs, vendoredOnly) {
			continue
		}
		m.Pkg = CanonicalPkgDir(m.Path)
		out = append(out, m)
	}
	return out, rows.Err()
}

func keepScannedFile(p string, includeDocs, vendoredOnly bool) bool {
	if vendoredOnly {
		if !IsVendoredSkillPath(p) || isExcludedASTPathOpt(p, true) {
			return false
		}
	} else if IsExcludedASTPath(p) {
		return false
	}
	return IsCodePath(p) || (includeDocs && IsDocPath(p))
}

// QuerySymbolNodesDB returns indexed symbols for projects.
func QuerySymbolNodesDB(db *sql.DB, projects []string, includeDocs bool) ([]RankedSymbol, error) {
	inSQL, args := projectsInClause(projects)
	q := `SELECT f.project, f.Path, sy.oid, sy.name, sy.recv, sy.kind, sy.pkg, sy.start_line, sy.end_line, sy.sig, sy.exported, sy.body_hash
		FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project IN ` + inSQL + `
		ORDER BY f.Path, sy.start_line, sy.end_line, sy.recv, sy.name`
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
		if IsExcludedASTPath(s.Path) || (!includeDocs && (IsDocPath(s.Path) || s.Kind == "heading")) {
			continue
		}
		if !IsDocPath(s.Path) && s.Kind != "heading" {
			s.Pkg = CanonicalPkgDir(s.Path)
		}
		s.Exported = exp != 0
		s.Idx = len(syms)
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

// NewGraphWithMeta constructs an indexed Graph from symbols, files, and Go modules.
func NewGraphWithMeta(syms []RankedSymbol, files []FileMeta, goMods map[string]string) *Graph {
	g := &Graph{
		Syms: syms, Files: files,
		Out: make([][]Edge, len(syms)), In: make([][]Edge, len(syms)),
		ByName: map[string][]int{}, byQual: map[string][]int{}, ByPath: map[string][]int{},
		byOIDFrom: map[string][]int{}, defFile: map[string]int{},
		pkgSet: map[string]bool{}, fileByStem: map[string]string{},
		goMods: goMods, pkgImports: map[string]map[string]bool{}, importSeen: map[string]bool{},
	}
	for _, f := range files {
		if f.Pkg != "" {
			g.pkgSet[f.Pkg] = true
		}
		g.fileByStem[f.Path] = f.Path
		if isTSSource(f.Path) {
			stem := trimTSExtension(f.Path)
			if _, exists := g.fileByStem[stem]; !exists {
				g.fileByStem[stem] = f.Path
			}
		}
	}
	indexGraphSymbols(g)
	return g
}

func indexGraphSymbols(g *Graph) {
	seenFile := map[string]bool{}
	for i, s := range g.Syms {
		qn := s.QualName()
		g.ByName[s.Name] = append(g.ByName[s.Name], i)
		g.byQual[qn] = append(g.byQual[qn], i)
		g.ByPath[s.Path] = append(g.ByPath[s.Path], i)
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
	if len(terms) == 0 || (len(nameLow) < HookMinIdentLen && strings.HasSuffix(strings.ToLower(s.Path), ".sh")) {
		return false
	}
	qualLow := strings.ToLower(s.QualName())
	for _, t := range terms {
		tl := strings.ToLower(t)
		if tl == "" || strings.Contains(tl, "/") || FTSFallbackStopwords[tl] {
			continue
		}
		if nameLow == tl || qualLow == tl || (len(tl) >= 4 && (strings.Contains(nameLow, tl) || strings.Contains(qualLow, tl))) {
			return true
		}
	}
	return false
}
