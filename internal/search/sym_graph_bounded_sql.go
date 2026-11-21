package search

import (
	"database/sql"
	"strings"
)

func querySeedSymbolsDB(db *sql.DB, projects []string, includeDocs bool, seedOIDs map[string]bool, terms []string) ([]RankedSymbol, error) {
	predSQL, predArgs := buildSeedSymbolPredicate(seedOIDs, terms)
	if predSQL == "" {
		return nil, nil
	}
	inSQL, args := projectsInClause(projects)
	args = append(args, predArgs...)
	q := `SELECT DISTINCT f.project, f.path, sy.oid, sy.name, sy.recv, sy.kind, sy.pkg, sy.start_line, sy.end_line, sy.sig, sy.exported, sy.body_hash
		FROM symbols sy JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + inSQL + ` AND (` + predSQL + `)
		ORDER BY f.path, sy.start_line, sy.end_line, sy.recv, sy.name`
	return scanSymbolNodeRows(db, q, args, includeDocs)
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
		tl := strings.ToLower(t)
		if !add(tl) {
			break
		}
		if strings.ContainsAny(tl, "/-.") {
			parts := strings.FieldsFunc(tl, func(r rune) bool { return r == '/' || r == '-' || r == '.' })
			for _, p := range parts {
				if len(p) >= 4 && !ftsFallbackStopwords[p] && !add(p) {
					return out
				}
			}
		}
	}
	return out
}

func buildSeedSymbolPredicate(seedOIDs map[string]bool, terms []string) (string, []any) {
	var parts []string
	var args []any
	if len(seedOIDs) > 0 {
		inSQL, oidArgs := projectsInClause(sortedMapKeys(seedOIDs))
		parts = append(parts, `sy.oid IN `+inSQL)
		args = append(args, oidArgs...)
	}
	var exact, long []string
	for _, tl := range expandBoundedSymbolTerms(terms) {
		if len(tl) >= 2 && len(exact) < maxBoundedSymbolTerms {
			exact = append(exact, tl)
		}
		if len(tl) >= 4 && len(long) < maxBoundedSymbolTerms {
			long = append(long, tl)
		}
	}
	if len(exact) > 0 {
		inSQL, exArgs := projectsInClause(exact)
		parts = append(parts, `lower(sy.name) IN `+inSQL)
		args = append(args, exArgs...)
	}
	for _, tl := range long {
		pat := "%" + escapeSQLike(tl) + "%"
		parts = append(parts, `(lower(sy.name) LIKE ? ESCAPE '\' OR lower(sy.recv || '.' || sy.name) LIKE ? ESCAPE '\')`)
		args = append(args, pat, pat)
	}
	return strings.Join(parts, " OR "), args
}

func querySymbolsByOIDsDB(db *sql.DB, projects []string, includeDocs bool, oids []string) ([]RankedSymbol, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	oSQL, oArgs := projectsInClause(oids)
	q := `SELECT f.project, f.path, sy.oid, sy.name, sy.recv, sy.kind, sy.pkg, sy.start_line, sy.end_line, sy.sig, sy.exported, sy.body_hash
		FROM symbols sy JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + pSQL + ` AND sy.oid IN ` + oSQL + `
		ORDER BY f.path, sy.start_line, sy.end_line, sy.recv, sy.name`
	return scanSymbolNodeRows(db, q, append(pArgs, oArgs...), includeDocs)
}

func scanSymbolNodeRows(db *sql.DB, q string, args []any, includeDocs bool) ([]RankedSymbol, error) {
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

func querySymRefsByOIDsDB(db *sql.DB, projects []string, includeDocs bool, oids []string) ([]rawSymRef, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	oSQL, oArgs := projectsInClause(oids)
	q := `SELECT f.path, r.oid, r.from_sym, r.to_pkg, r.to_name, r.kind, r.confidence, r.weight, r.line
		FROM sym_refs r JOIN files f ON f.oid = r.oid
		WHERE f.project IN ` + pSQL + ` AND r.oid IN ` + oSQL + `
		ORDER BY f.path, r.line, r.from_sym, r.to_name`
	return scanRawSymRefRows(db, q, append(pArgs, oArgs...), includeDocs)
}

func queryInboundSymRefsDB(db *sql.DB, projects []string, includeDocs bool, names []string, limit int) ([]rawSymRef, error) {
	if len(names) == 0 || limit <= 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	nSQL, nArgs := projectsInClause(names)
	q := `SELECT f.path, r.oid, r.from_sym, r.to_pkg, r.to_name, r.kind, r.confidence, r.weight, r.line
		FROM sym_refs r JOIN files f ON f.oid = r.oid
		WHERE f.project IN ` + pSQL + ` AND r.to_name IN ` + nSQL + `
		ORDER BY f.path, r.line LIMIT ?`
	args := append(append(pArgs, nArgs...), limit)
	return scanRawSymRefRows(db, q, args, includeDocs)
}

func queryOIDsBySymbolNamesDB(db *sql.DB, projects []string, includeDocs bool, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	nSQL, nArgs := projectsInClause(names)
	q := `SELECT DISTINCT f.path, sy.oid FROM symbols sy JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + pSQL + ` AND sy.name IN ` + nSQL
	rows, err := db.Query(q, append(pArgs, nArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var fPath, oid string
		if err := rows.Scan(&fPath, &oid); err != nil {
			return nil, err
		}
		if isExcludedASTPath(fPath) || (!includeDocs && isDocPath(fPath)) || seen[oid] {
			continue
		}
		seen[oid] = true
		out = append(out, oid)
	}
	return out, rows.Err()
}

func scanRawSymRefRows(db *sql.DB, q string, args []any, includeDocs bool) ([]rawSymRef, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawSymRef
	for rows.Next() {
		var r rawSymRef
		if err := rows.Scan(&r.path, &r.oid, &r.fromSym, &r.toPkg, &r.toName, &r.kind, &r.confidence, &r.weight, &r.line); err != nil {
			return nil, err
		}
		if isExcludedASTPath(r.path) || (!includeDocs && (isDocPath(r.path) || r.kind == "doc_ref")) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func mergeUniqueRawRefs(a, b []rawSymRef) []rawSymRef {
	seen := make(map[rawSymRef]bool, len(a)+len(b))
	out := make([]rawSymRef, 0, len(a)+len(b))
	for _, r := range a {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, r := range b {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func partitionPrioritySymNames(syms []RankedSymbol, terms []string, limit int) (priority, other []string) {
	seen := map[string]bool{}
	for _, s := range syms {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		if matchesPromptTerms(s, terms) {
			seen[s.Name] = true
			priority = append(priority, s.Name)
			if limit > 0 && len(priority) >= limit {
				return priority, nil
			}
		}
	}
	rem := limit - len(priority)
	for _, s := range syms {
		if s.Name == "" || seen[s.Name] || (limit > 0 && rem <= 0) {
			continue
		}
		seen[s.Name] = true
		other = append(other, s.Name)
		rem--
	}
	return priority, other
}

func collectDistinctOutTargetNames(refs []rawSymRef, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range refs {
		if r.toName == "" || r.toName == "<import>" || seen[r.toName] {
			continue
		}
		seen[r.toName] = true
		out = append(out, r.toName)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		if v {
			out[k] = true
		}
	}
	return out
}

func addBoundedOID(dst map[string]bool, oid string, limit int) bool {
	if oid == "" || dst[oid] || (limit > 0 && len(dst) >= limit) {
		return false
	}
	dst[oid] = true
	return true
}

func escapeSQLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
