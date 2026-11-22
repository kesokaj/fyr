package search

import (
	"context"
	"database/sql"
	"path"
	"slices"
	"strings"
)

var boundedFileExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".sh": true, ".md": true, ".mdx": true, ".sql": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".css": true,
	".html": true, ".svg": true, ".proto": true, ".txt": true, ".db": true,
}

func querySeedSymbolsLikeDB(db *sql.DB, projects []string, includeDocs bool, terms []string) ([]RankedSymbol, error) {
	return querySeedSymbolsLikeDBCtx(context.Background(), db, projects, includeDocs, terms, maxBoundedSeedNames)
}

func querySeedSymbolsLikeDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, terms []string, maxNames int) ([]RankedSymbol, error) {
	return querySeedSymbolsLikeMinLenDBCtx(ctx, db, projects, includeDocs, terms, maxNames, 4)
}

func querySeedSymbolsLikeMinLenDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, terms []string, maxNames, minLikeLen int) ([]RankedSymbol, error) {
	if len(projects) == 0 || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if maxNames <= 0 {
		maxNames = maxBoundedSeedNames
	}
	exact, long, dotMethods, hasQualDot := collectBoundedLikeTermsWithMin(terms, minLikeLen)
	if len(exact) == 0 && len(long) == 0 {
		return nil, nil
	}
	matched, seen, err := matchSymbolNamesFromIndexCtx(ctx, db, includeDocs, exact, long)
	if err == nil && hasQualDot && len(matched) < maxNames && ctx.Err() == nil {
		matched, err = matchQualSymbolNamesFromDBCtx(ctx, db, projects, includeDocs, exact, long, dotMethods, seen, matched, maxNames)
	}
	if err != nil || len(matched) == 0 {
		return nil, err
	}
	return queryBoundedMatchedSymbolsDBCtx(ctx, db, projects, includeDocs, matched, maxNames)
}

func queryBoundedMatchedSymbolsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, names []string, maxNames int) ([]RankedSymbol, error) {
	const batchSize = 200
	var out []RankedSymbol
	keptNames := map[string]bool{}
	for i := 0; i < len(names) && len(keptNames) < maxNames; i += batchSize {
		end := min(i+batchSize, len(names))
		syms, err := queryExactSymbolsByNameDBCtx(ctx, db, projects, includeDocs, names[i:end])
		if err != nil {
			return nil, err
		}
		for _, s := range syms {
			if !keptNames[s.Name] && len(keptNames) >= maxNames {
				continue
			}
			keptNames[s.Name] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func collectBoundedLikeTerms(terms []string) (exact, long, dotMethods []string, hasQualDot bool) {
	return collectBoundedLikeTermsWithMin(terms, 4)
}

func collectBoundedLikeTermsWithMin(terms []string, minLikeLen int) (exact, long, dotMethods []string, hasQualDot bool) {
	if minLikeLen < 4 {
		minLikeLen = 4
	}
	seenMethod := map[string]bool{}
	for _, tl := range expandBoundedSymbolTerms(terms) {
		if strings.Contains(tl, "/") || len(tl) < 2 {
			continue
		}
		isFileLike := boundedFileExts[strings.ToLower(path.Ext(tl))]
		if len(exact) < maxBoundedSymbolTerms {
			exact = append(exact, tl)
		}
		if len(tl) >= minLikeLen && len(long) < maxBoundedSymbolTerms && !isFileLike {
			long = append(long, tl)
		}
		if strings.Contains(tl, ".") && !isFileLike {
			hasQualDot = true
			if _, after, ok := strings.Cut(tl, "."); ok && len(after) >= 2 && !seenMethod[after] {
				seenMethod[after] = true
				dotMethods = append(dotMethods, after)
			}
		}
	}
	return exact, long, dotMethods, hasQualDot
}

func boundedLikeDocFilterSQL(includeDocs bool) string {
	if includeDocs {
		return ""
	}
	return ` AND sy.kind != 'heading' AND lower(f.path) NOT LIKE '%.md' AND lower(f.path) NOT LIKE '%.mdx'`
}

// matchSymbolNamesFromIndexCtx scans the covering index symbols_name directly
// with SQL predicate pushdown (zero table lookups, zero files JOIN, zero temp
// B-tree sort, and zero Go-side marshaling of non-matching symbol rows);
// project/doc filtering and maxNames capping run in queryBoundedMatchedSymbolsDBCtx.
func matchSymbolNamesFromIndexCtx(ctx context.Context, db *sql.DB, includeDocs bool, exact, long []string) ([]string, map[string]bool, error) {
	if len(long) >= 2 {
		return scanSymbolNamesCoveringGoCtx(ctx, db, includeDocs, exact, long)
	}
	q, args := buildSymbolNameMatchSQL(includeDocs, exact, long)
	if q == "" {
		return nil, map[string]bool{}, nil
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var matched []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, nil, err
		}
		if !seen[name] {
			seen[name] = true
			matched = append(matched, name)
		}
	}
	return matched, seen, rows.Err()
}

func scanSymbolNamesCoveringGoCtx(ctx context.Context, db *sql.DB, includeDocs bool, exact, long []string) ([]string, map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM symbols`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	exactSet := make(map[string]bool, len(exact))
	for _, e := range exact {
		exactSet[e] = true
	}
	seen := map[string]bool{}
	var matched []string
	var prev string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, nil, err
		}
		if name == prev || (!includeDocs && strings.IndexByte(name, ' ') >= 0) {
			continue
		}
		prev = name
		low := strings.ToLower(name)
		if exactSet[low] || slices.ContainsFunc(long, func(l string) bool { return strings.Contains(low, l) }) {
			if !seen[name] {
				seen[name] = true
				matched = append(matched, name)
				if len(matched) >= 1024 {
					break
				}
			}
		}
	}
	return matched, seen, rows.Err()
}

func buildSymbolNameMatchSQL(includeDocs bool, exact, long []string) (string, []any) {
	var preds []string
	var args []any
	var exactOnly []string
	for _, e := range exact {
		if !slices.Contains(long, e) {
			exactOnly = append(exactOnly, e)
		}
	}
	if len(exactOnly) > 0 {
		eSQL, eArgs := projectsInClause(exactOnly)
		preds = append(preds, `lower(name) IN `+eSQL)
		args = append(args, eArgs...)
	}
	for _, l := range long {
		preds = append(preds, `lower(name) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeSQLLike(l)+"%")
	}
	if len(preds) == 0 {
		return "", nil
	}
	where := `(` + strings.Join(preds, ` OR `) + `)`
	if !includeDocs {
		where = `instr(name, ' ') = 0 AND ` + where
	}
	return `SELECT DISTINCT name FROM symbols WHERE ` + where + ` LIMIT 1024`, args
}

func matchQualSymbolNamesFromDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, exact, long, dotMethods []string, seen map[string]bool, matched []string, maxNames int) ([]string, error) {
	if len(projects) == 0 || ctx.Err() != nil {
		return matched, ctx.Err()
	}
	pSQL, pArgs := projectsInClause(projects)
	q := `SELECT DISTINCT f.path, sy.recv, sy.name FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project IN ` + pSQL + ` AND sy.recv != ''` + boundedLikeDocFilterSQL(includeDocs)
	args := pArgs
	if len(dotMethods) > 0 && len(dotMethods) <= 8 {
		var likes []string
		for _, m := range dotMethods {
			likes = append(likes, `lower(sy.name) LIKE ? ESCAPE '\'`)
			args = append(args, "%"+escapeSQLLike(m)+"%")
		}
		q += ` AND (` + strings.Join(likes, ` OR `) + `)`
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if seen == nil {
		seen = map[string]bool{}
	}
	for rows.Next() && len(matched) < maxNames {
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

const maxBoundedSymbolTerms = 24

func filterBoundedQueryTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		tl := strings.ToLower(t)
		if !strings.ContainsAny(tl, "/.") && ftsFallbackStopwords[tl] {
			continue
		}
		out = append(out, t)
		if len(out) >= maxBoundedQueryTerms {
			break
		}
	}
	return out
}

func expandBoundedSymbolTerms(terms []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(tok string) bool {
		if tok == "" || seen[tok] {
			return len(out) < maxBoundedSymbolTerms
		}
		seen[tok] = true
		out = append(out, tok)
		return len(out) < maxBoundedSymbolTerms
	}
	for _, t := range filterBoundedQueryTerms(terms) {
		if !addExpandedBoundedTerm(t, add) {
			break
		}
	}
	return out
}

func addExpandedBoundedTerm(raw string, add func(string) bool) bool {
	tl := strings.ToLower(raw)
	if strings.Contains(tl, "/") {
		base := path.Base(strings.TrimRight(tl, "/"))
		stem := strings.TrimSuffix(base, path.Ext(base))
		if len(stem) >= 4 && !ftsFallbackStopwords[stem] && !add(stem) {
			return false
		}
		tl = stem
	} else if !add(tl) {
		return false
	}
	if strings.ContainsAny(tl, "-.") {
		parts := strings.FieldsFunc(tl, func(r rune) bool { return r == '-' || r == '.' })
		for _, p := range parts {
			if len(p) >= 4 && !ftsFallbackStopwords[p] && !add(p) {
				return false
			}
		}
	}
	return true
}
