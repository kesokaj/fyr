package search

import (
	"database/sql"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/redact"
)

const defaultHiddenCouplingMinCount = 3

// StructuralSignals holds strictly numeric, shape-only structural and behavioral health metrics.
type StructuralSignals struct {
	HiddenCouplingPairs int     `json:"hiddenCouplingPairs"`
	HiddenCouplingRate  float64 `json:"hiddenCouplingRate"`
	GodNodeClampHits    int     `json:"godNodeClampHits"`
	GodNodeRepeatReads  int     `json:"godNodeRepeatReads"`
	GodNodeChurnRate    float64 `json:"godNodeChurnRate"`
	ProdPkgCycles       int     `json:"prodPkgCycles"`
	HubConcentration    float64 `json:"hubConcentration"`
	LargeFilesOver350   int     `json:"largeFilesOver350"`
	TotalCodeFiles      int     `json:"totalCodeFiles"`
	TotalProdPkgs       int     `json:"totalProdPkgs"`
}

// HiddenCouplingHint is a project-local redacted co-activity pair lacking any AST edge.
type HiddenCouplingHint struct {
	FileA string `json:"fileA"`
	FileB string `json:"fileB"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type fileChurnStat struct {
	clampHits   int
	repeatReads int
}

// ReadStructuralSignals computes shape-only AST and ledger signals from dbPath without writing to it.
func ReadStructuralSignals(dbPath, repoRoot string) (StructuralSignals, error) {
	db, root, err := openReadOnlySearchDB(dbPath, repoRoot)
	if err != nil {
		return StructuralSignals{}, err
	}
	defer db.Close()
	projects := resolveDBProjects(db, root)
	if len(projects) == 0 {
		return StructuralSignals{}, nil
	}
	g, err := buildSymGraphFromDB(db, projects, loadGoModulesFromDB(db, projects), false)
	if err != nil {
		return StructuralSignals{}, err
	}
	return computeStructuralSignalsFromGraph(db, root, g), nil
}

// ReadHiddenCouplingHints returns project-local redacted co-activity pairs with zero AST edge.
func ReadHiddenCouplingHints(dbPath, repoRoot string, minCount, limit int) ([]HiddenCouplingHint, error) {
	if minCount <= 0 {
		minCount = defaultHiddenCouplingMinCount
	}
	db, root, err := openReadOnlySearchDB(dbPath, repoRoot)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	projects := resolveDBProjects(db, root)
	if len(projects) == 0 {
		return nil, nil
	}
	g, err := buildSymGraphFromDB(db, projects, loadGoModulesFromDB(db, projects), false)
	if err != nil {
		return nil, err
	}
	astLinked := collectUndirectedASTFilePairs(g)
	hints, _ := queryHiddenCouplingPairs(db, root, minCount, astLinked)
	if limit > 0 && len(hints) > limit {
		hints = hints[:limit]
	}
	return hints, nil
}

func openReadOnlySearchDB(dbPath, repoRoot string) (*sql.DB, string, error) {
	root := filepath.Clean(repoRoot)
	if repoRoot == "" {
		root = ""
	}
	if dbPath == "" && root != "" {
		dbPath = projectDB(root)
	}
	if _, err := os.Lstat(dbPath); err != nil {
		return nil, root, err
	}
	ok, err := isRegularSQLiteFiles(dbPath)
	if err != nil {
		return nil, root, err
	}
	if !ok {
		return nil, root, ErrNotRegular
	}
	db, err := sql.Open("sqlite", roDSN(dbPath))
	if err != nil {
		return nil, root, err
	}
	db.SetMaxOpenConns(1)
	return db, root, nil
}

func resolveDBProjects(db *sql.DB, repoRoot string) []string {
	rows, err := db.Query(`SELECT DISTINCT project FROM files`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var all []string
	canonRoot := CanonicalizePath("", repoRoot)
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil && p != "" {
			if repoRoot != "" && (filepath.Clean(p) == filepath.Clean(repoRoot) || CanonicalizePath("", p) == canonRoot) {
				return []string{p}
			}
			all = append(all, p)
		}
	}
	sort.Strings(all)
	return all
}

func computeStructuralSignalsFromGraph(db *sql.DB, repoRoot string, g *symGraph) StructuralSignals {
	var sig StructuralSignals
	prodPkgs := map[string]bool{}
	for _, f := range g.files {
		if !isCodePath(f.path) || isTestPath(f.path) || isDocPath(f.path) {
			continue
		}
		sig.TotalCodeFiles++
		if f.lineCount > 350 {
			sig.LargeFilesOver350++
		}
		if f.pkg != "" {
			prodPkgs[f.pkg] = true
		}
	}
	_, pkgDeps := g.summarizePackages()
	for p := range pkgDeps {
		prodPkgs[p] = true
	}
	pkgs := sortedMapKeys(prodPkgs)
	sig.TotalProdPkgs = len(pkgs)
	sig.ProdPkgCycles = len(TarjanPackageSCCs(pkgs, pkgDeps))

	churnMap, totalReadEvents := queryFileChurnStats(db, repoRoot)
	godFiles, hubConc := computeGodNodeFilesAndHubShare(g)
	sig.HubConcentration = hubConc
	for gf := range godFiles {
		st := churnMap[gf]
		sig.GodNodeClampHits += st.clampHits
		sig.GodNodeRepeatReads += st.repeatReads
	}
	if totalReadEvents > 0 && (sig.GodNodeClampHits+sig.GodNodeRepeatReads) > 0 {
		sig.GodNodeChurnRate = roundSignal4(float64(sig.GodNodeClampHits+sig.GodNodeRepeatReads) / float64(totalReadEvents))
	}
	astLinked := collectUndirectedASTFilePairs(g)
	hidden, totalCoPairs := queryHiddenCouplingPairs(db, repoRoot, defaultHiddenCouplingMinCount, astLinked)
	sig.HiddenCouplingPairs = len(hidden)
	if totalCoPairs > 0 {
		sig.HiddenCouplingRate = roundSignal4(float64(len(hidden)) / float64(totalCoPairs))
	}
	return sig
}

func computeGodNodeFilesAndHubShare(g *symGraph) (map[string]bool, float64) {
	ranked := g.rank("", nil)
	var prod []RankedSymbol
	totalMass := 0.0
	for _, s := range ranked {
		if isTestPath(s.Path) || isDocPath(s.Path) || s.Kind == "heading" {
			continue
		}
		prod = append(prod, s)
		totalMass += s.Score
	}
	godFiles := map[string]bool{}
	for i := 0; i < min(10, len(prod)); i++ {
		godFiles[prod[i].Path] = true
	}
	if len(prod) == 0 || totalMass <= 0 {
		return godFiles, 0
	}
	topK := max(1, min(10, len(prod)/5))
	topMass := 0.0
	for i := 0; i < topK; i++ {
		topMass += prod[i].Score
	}
	return godFiles, roundSignal4(topMass / totalMass)
}

func collectUndirectedASTFilePairs(g *symGraph) map[[2]string]bool {
	pairs := map[[2]string]bool{}
	add := func(a, b string) {
		if a == "" || b == "" || a == b {
			return
		}
		if a > b {
			a, b = b, a
		}
		pairs[[2]string{a, b}] = true
	}
	for i, s := range g.syms {
		for _, e := range g.out[i] {
			add(s.Path, g.syms[e.toIdx].Path)
		}
	}
	for _, ie := range g.importEdges {
		if ie.toPath != "" {
			add(ie.fromPath, ie.toPath)
		} else {
			for _, f := range g.files {
				if f.pkg == ie.toPkg {
					add(ie.fromPath, f.path)
				}
			}
		}
	}
	return pairs
}

func queryFileChurnStats(db *sql.DB, repoRoot string) (map[string]fileChurnStat, int) {
	out := map[string]fileChurnStat{}
	rows, err := db.Query(`SELECT resolved_path, start_line, end_line, hit_count FROM read_ledger WHERE start_line > 0 OR (start_line = ? AND end_line = ?)`,
		clampOnceSentinelLine, clampOnceSentinelLine)
	if err != nil {
		return out, 0
	}
	defer rows.Close()
	canonRoot := CanonicalizePath("", repoRoot)
	totalEvents := 0
	for rows.Next() {
		var rawPath string
		var sLine, eLine, hits int
		if rows.Scan(&rawPath, &sLine, &eLine, &hits) != nil {
			continue
		}
		rel := normalizeRepoRel(repoRoot, canonRoot, rawPath)
		if rel == "" {
			continue
		}
		h := max(1, hits)
		st := out[rel]
		if sLine == clampOnceSentinelLine && eLine == clampOnceSentinelLine {
			st.clampHits += h
		} else if h > 1 {
			st.repeatReads += h - 1
		}
		totalEvents += h
		out[rel] = st
	}
	return out, totalEvents
}

func queryHiddenCouplingPairs(db *sql.DB, repoRoot string, minCount int, astLinked map[[2]string]bool) ([]HiddenCouplingHint, int) {
	rows, err := db.Query(`SELECT file_a, file_b, kind, count FROM co_activity WHERE count >= ? ORDER BY count DESC, file_a, file_b`, minCount)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()
	canonRoot := CanonicalizePath("", repoRoot)
	best := map[[2]string]HiddenCouplingHint{}
	for rows.Next() {
		var a, b, kind string
		var cnt int
		if rows.Scan(&a, &b, &kind, &cnt) != nil {
			continue
		}
		ra, rb := normalizeRepoRel(repoRoot, canonRoot, a), normalizeRepoRel(repoRoot, canonRoot, b)
		if ra == "" || rb == "" || ra == rb || isExcludedASTPath(ra) || isExcludedASTPath(rb) {
			continue
		}
		if harness.SecretShaped(path.Base(ra)) || harness.SecretShaped(path.Base(rb)) {
			continue
		}
		if ra > rb {
			ra, rb = rb, ra
		}
		key := [2]string{ra, rb}
		cur, exists := best[key]
		if !exists || cnt > cur.Count || (cnt == cur.Count && kind == "co_modified" && cur.Kind != "co_modified") {
			best[key] = HiddenCouplingHint{
				FileA: printable(redact.Redact(ra)),
				FileB: printable(redact.Redact(rb)),
				Kind:  printable(kind),
				Count: cnt,
			}
		}
	}
	var hidden []HiddenCouplingHint
	for key, h := range best {
		if !astLinked[key] {
			hidden = append(hidden, h)
		}
	}
	sortHiddenCouplingHints(hidden)
	return hidden, len(best)
}

func sortHiddenCouplingHints(hidden []HiddenCouplingHint) {
	sort.Slice(hidden, func(i, j int) bool {
		if hidden[i].Count != hidden[j].Count {
			return hidden[i].Count > hidden[j].Count
		}
		if hidden[i].Kind != hidden[j].Kind {
			return hidden[i].Kind == "co_modified"
		}
		if hidden[i].FileA != hidden[j].FileA {
			return hidden[i].FileA < hidden[j].FileA
		}
		return hidden[i].FileB < hidden[j].FileB
	})
}

func normalizeRepoRel(repoRoot, canonRoot, p string) string {
	if repoRoot != "" {
		if rel := relPathInRepo(repoRoot, p); rel != "" {
			return rel
		}
	}
	if canonRoot != "" {
		if rel := relPathInRepo(canonRoot, p); rel != "" {
			return rel
		}
	}
	return relPathInRepo("", p)
}

func roundSignal4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
