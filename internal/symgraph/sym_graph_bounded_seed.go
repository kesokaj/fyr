package symgraph

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strconv"
	"strings"
)

func querySeedFileOIDsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, active map[string]bool, terms []string, maxSeedFiles int) (map[string]bool, bool, error) {
	filteredTerms := filterBoundedQueryTerms(terms)
	if len(active) == 0 && len(filteredTerms) == 0 {
		return map[string]bool{}, false, nil
	}
	matched, err := scanSeedFileCandidatesCtx(ctx, db, projects, includeDocs, active, filteredTerms)
	if err != nil {
		return nil, false, err
	}
	oids := map[string]bool{}
	for _, f := range matched {
		if active[f.Path] {
			oids[f.OID] = true
		}
	}
	hasExplicit := false
	for _, f := range matched {
		if fileMatchesBoundedTerms(f.Path, filteredTerms) {
			hasExplicit = true
			if len(oids) < maxSeedFiles {
				oids[f.OID] = true
			}
		}
	}
	return oids, hasExplicit, nil
}

func scanSeedFileCandidatesCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, active map[string]bool, terms []string) ([]FileMeta, error) {
	if len(projects) == 1 {
		return scanSingleProjectSeedFilesCtx(ctx, db, projects[0], includeDocs, active, terms)
	}
	return scanMultiProjectSeedFilesCtx(ctx, db, projects, includeDocs, active, terms)
}

func scanSingleProjectSeedFilesCtx(ctx context.Context, db *sql.DB, project string, includeDocs bool, active map[string]bool, terms []string) ([]FileMeta, error) {
	rows, err := db.QueryContext(ctx, `SELECT path, oid FROM files WHERE project = ?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileMeta
	for rows.Next() {
		var fp, oid string
		if err := rows.Scan(&fp, &oid); err != nil {
			return nil, err
		}
		if keepSeedCandidate(fp, includeDocs, active, terms) {
			out = append(out, FileMeta{Project: project, Path: fp, OID: oid, Pkg: CanonicalPkgDir(fp)})
		}
	}
	return out, rows.Err()
}

func scanMultiProjectSeedFilesCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, active map[string]bool, terms []string) ([]FileMeta, error) {
	pSQL, pArgs := projectsInClause(projects)
	rows, err := db.QueryContext(ctx, `SELECT project, path, oid FROM files WHERE project IN `+pSQL+` ORDER BY path`, pArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileMeta
	for rows.Next() {
		var m FileMeta
		if err := rows.Scan(&m.Project, &m.Path, &m.OID); err != nil {
			return nil, err
		}
		if keepSeedCandidate(m.Path, includeDocs, active, terms) {
			m.Pkg = CanonicalPkgDir(m.Path)
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

func keepSeedCandidate(fp string, includeDocs bool, active map[string]bool, terms []string) bool {
	return (active[fp] || fileMatchesBoundedTerms(fp, terms)) && keepScannedFile(fp, includeDocs, false)
}

func querySeedSymbolsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, seedOIDs map[string]bool, hasExplicitFileSeed bool, terms []string, maxNames int) ([]RankedSymbol, error) {
	var merged []RankedSymbol
	if len(seedOIDs) > 0 {
		byOID, err := querySymbolsByOIDsDBCtx(ctx, db, projects, includeDocs, SortedMapKeys(seedOIDs))
		if err != nil {
			return nil, err
		}
		merged = append(merged, byOID...)
	}
	if exactNames := collectExactSymbolCandidates(terms); len(exactNames) > 0 {
		byName, err := queryExactSymbolsByNameDBCtx(ctx, db, projects, includeDocs, exactNames)
		if err != nil {
			return nil, err
		}
		merged = append(merged, byName...)
	}
	merged = mergeAndSortSeedSymbols(merged)
	if hasStrongIdentSeed(merged, terms) || (hasExplicitFileSeed && len(merged) > 0) || ctx.Err() != nil {
		return merged, ctx.Err()
	}
	minLikeLen := 4
	if maxNames <= maxBoundedHookSeedNames {
		minLikeLen = HookMinIdentLen
	}
	likeSyms, err := QuerySeedSymbolsLikeMinLenDBCtx(ctx, db, projects, includeDocs, terms, maxNames, minLikeLen)
	if err != nil {
		return nil, err
	}
	return mergeAndSortSeedSymbols(append(merged, likeSyms...)), nil
}

func collectExactSymbolCandidates(terms []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if len(s) >= 2 && !seen[s] && len(out) < maxBoundedSymbolTerms {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, t := range filterBoundedQueryTerms(terms) {
		if !strings.Contains(t, "/") {
			add(t)
			if dot := strings.LastIndexByte(t, '.'); dot >= 0 && dot+1 < len(t) {
				add(t[dot+1:])
			}
		}
	}
	return out
}

func mergeAndSortSeedSymbols(in []RankedSymbol) []RankedSymbol {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]RankedSymbol, 0, len(in))
	for _, s := range in {
		if k := s.OID + "\x00" + s.Name + "\x00" + s.Recv + "\x00" + strconv.Itoa(s.StartLine); !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b RankedSymbol) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.StartLine, b.StartLine),
			cmp.Compare(a.EndLine, b.EndLine), cmp.Compare(a.Recv, b.Recv), cmp.Compare(a.Name, b.Name))
	})
	for i := range out {
		out[i].Idx = i
	}
	return out
}
