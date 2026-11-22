package search

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
		if active[f.path] {
			oids[f.oid] = true
		}
	}
	hasExplicit := false
	for _, f := range matched {
		if fileMatchesBoundedTerms(f.path, filteredTerms) {
			hasExplicit = true
			if len(oids) < maxSeedFiles {
				oids[f.oid] = true
			}
		}
	}
	return oids, hasExplicit, nil
}

func scanSeedFileCandidatesCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, active map[string]bool, terms []string) ([]indexedFileMeta, error) {
	if len(projects) == 1 {
		rows, err := db.QueryContext(ctx, `SELECT path, oid FROM files WHERE project = ?`, projects[0])
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []indexedFileMeta
		for rows.Next() {
			var fp, oid string
			if err := rows.Scan(&fp, &oid); err != nil {
				return nil, err
			}
			if (active[fp] || fileMatchesBoundedTerms(fp, terms)) && keepScannedFile(fp, includeDocs, false) {
				out = append(out, indexedFileMeta{project: projects[0], path: fp, oid: oid, pkg: canonicalPkgDir(fp)})
			}
		}
		return out, rows.Err()
	}
	pSQL, pArgs := projectsInClause(projects)
	rows, err := db.QueryContext(ctx, `SELECT project, path, oid FROM files WHERE project IN `+pSQL+` ORDER BY path`, pArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []indexedFileMeta
	for rows.Next() {
		var m indexedFileMeta
		if err := rows.Scan(&m.project, &m.path, &m.oid); err != nil {
			return nil, err
		}
		if (active[m.path] || fileMatchesBoundedTerms(m.path, terms)) && keepScannedFile(m.path, includeDocs, false) {
			m.pkg = canonicalPkgDir(m.path)
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

func querySeedSymbolsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, seedOIDs map[string]bool, hasExplicitFileSeed bool, terms []string, maxNames int) ([]RankedSymbol, error) {
	var merged []RankedSymbol
	if len(seedOIDs) > 0 {
		byOID, err := querySymbolsByOIDsDBCtx(ctx, db, projects, includeDocs, sortedMapKeys(seedOIDs))
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
		minLikeLen = hookMinIdentLen
	}
	likeSyms, err := querySeedSymbolsLikeMinLenDBCtx(ctx, db, projects, includeDocs, terms, maxNames, minLikeLen)
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
