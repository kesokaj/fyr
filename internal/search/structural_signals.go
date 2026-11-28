package search

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"

	"github.com/kesokaj/fyr/internal/symgraph"
)

const defaultHiddenCouplingMinCount = 3

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
	hints := queryHiddenCouplingPairs(db, root, minCount, astLinked)
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
	for _, f := range g.Files {
		if !isCodePath(f.Path) || isTestPath(f.Path) || isDocPath(f.Path) {
			continue
		}
		sig.TotalCodeFiles++
		if f.LineCount > 350 {
			sig.LargeFilesOver350++
		}
		if f.Pkg != "" {
			prodPkgs[f.Pkg] = true
		}
	}
	_, pkgDeps := g.SummarizePackages()
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
		sig.GodNodeClampHits += st.ClampHits
		sig.GodNodeRepeatReads += st.RepeatReads
	}
	if totalReadEvents > 0 && (sig.GodNodeClampHits+sig.GodNodeRepeatReads) > 0 {
		sig.GodNodeChurnRate = roundSignal4(float64(sig.GodNodeClampHits+sig.GodNodeRepeatReads) / float64(totalReadEvents))
	}
	astLinked := collectUndirectedASTFilePairs(g)
	hc := summarizeHiddenCoupling(db, repoRoot, defaultHiddenCouplingMinCount, astLinked)
	sig.HiddenCouplingPairs = hc.totalPairs
	sig.HiddenCouplingEditPairs = hc.editPairs
	sig.HiddenCouplingReadPairs = hc.readPairs
	if hc.totalCoPairs > 0 {
		sig.HiddenCouplingRate = roundSignal4(float64(hc.totalPairs) / float64(hc.totalCoPairs))
	}
	return sig
}

func computeGodNodeFilesAndHubShare(g *symGraph) (map[string]bool, float64) {
	ranked := g.Rank("", nil)
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
	return g.CollectUndirectedASTFilePairs()
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
			st.ClampHits += h
		} else if h > 1 {
			st.RepeatReads += h - 1
		}
		totalEvents += h
		out[rel] = st
	}
	return out, totalEvents
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
	return symgraph.RoundSignal4(v)
}
