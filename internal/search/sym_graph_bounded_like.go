package search

import (
	"database/sql"
	"slices"
	"strings"
)

func querySeedSymbolsLikeDB(db *sql.DB, projects []string, includeDocs bool, terms []string) ([]RankedSymbol, error) {
	if len(projects) == 0 {
		return nil, nil
	}
	exact, long, hasDot := collectBoundedLikeTerms(terms)
	if len(exact) == 0 && len(long) == 0 {
		return nil, nil
	}
	matched, seen, err := matchSymbolNamesFromIndex(db, projects, includeDocs, exact, long)
	if err == nil && hasDot && len(matched) < maxBoundedSeedNames {
		matched, err = matchQualSymbolNamesFromDB(db, projects, includeDocs, exact, long, seen, matched)
	}
	if err != nil || len(matched) == 0 {
		return nil, err
	}
	return queryExactSymbolsByNameDB(db, projects, includeDocs, matched)
}

func collectBoundedLikeTerms(terms []string) (exact, long []string, hasDot bool) {
	for _, tl := range expandBoundedSymbolTerms(terms) {
		if strings.Contains(tl, "/") || len(tl) < 2 {
			continue
		}
		if len(exact) < maxBoundedSymbolTerms {
			exact = append(exact, tl)
			hasDot = hasDot || strings.Contains(tl, ".")
		}
		if len(tl) >= 4 && len(long) < maxBoundedSymbolTerms {
			long = append(long, tl)
			hasDot = hasDot || strings.Contains(tl, ".")
		}
	}
	return exact, long, hasDot
}

func boundedLikeDocFilterSQL(includeDocs bool) string {
	if includeDocs {
		return ""
	}
	return ` AND sy.kind != 'heading' AND lower(f.path) NOT LIKE '%.md' AND lower(f.path) NOT LIKE '%.mdx'`
}

func matchSymbolNamesFromIndex(db *sql.DB, projects []string, includeDocs bool, exact, long []string) ([]string, map[string]bool, error) {
	if len(projects) == 0 {
		return nil, nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	q := `SELECT DISTINCT f.path, sy.name FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project IN ` + pSQL + boundedLikeDocFilterSQL(includeDocs) + ` ORDER BY f.path, sy.name`
	rows, err := db.Query(q, pArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var matched []string
	for rows.Next() && len(matched) < maxBoundedSeedNames {
		var fPath, name string
		if err := rows.Scan(&fPath, &name); err != nil {
			return nil, nil, err
		}
		if isExcludedASTPath(fPath) || (!includeDocs && isDocPath(fPath)) {
			continue
		}
		matched = addBoundedLikeMatch(seen, matched, name, name, exact, long)
	}
	return matched, seen, rows.Err()
}

func matchQualSymbolNamesFromDB(db *sql.DB, projects []string, includeDocs bool, exact, long []string, seen map[string]bool, matched []string) ([]string, error) {
	if len(projects) == 0 {
		return matched, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	q := `SELECT DISTINCT f.path, sy.recv, sy.name FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project IN ` + pSQL + ` AND sy.recv != ''` + boundedLikeDocFilterSQL(includeDocs) + ` ORDER BY f.path, sy.recv, sy.name`
	rows, err := db.Query(q, pArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if seen == nil {
		seen = map[string]bool{}
	}
	for rows.Next() && len(matched) < maxBoundedSeedNames {
		var fPath, recv, name string
		if err := rows.Scan(&fPath, &recv, &name); err != nil {
			return nil, err
		}
		if isExcludedASTPath(fPath) || (!includeDocs && isDocPath(fPath)) {
			continue
		}
		matched = addBoundedLikeMatch(seen, matched, name, recv+"."+name, exact, long)
	}
	return matched, rows.Err()
}

func addBoundedLikeMatch(seen map[string]bool, matched []string, name, cand string, exact, long []string) []string {
	if seen[name] {
		return matched
	}
	low := strings.ToLower(cand)
	if slices.Contains(exact, low) || slices.ContainsFunc(long, func(l string) bool { return strings.Contains(low, l) }) {
		seen[name] = true
		return append(matched, name)
	}
	return matched
}
