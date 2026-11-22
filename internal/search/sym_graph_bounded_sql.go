package search

import (
	"context"
	"database/sql"
	"strings"
)

func queryFilesByPathsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, paths []string) ([]indexedFileMeta, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	fSQL, fArgs := projectsInClause(paths)
	q := `SELECT f.project, f.path, f.oid, 0 FROM files f WHERE f.project IN ` + pSQL + ` AND f.path IN ` + fSQL + ` ORDER BY f.path`
	return scanIndexedFileMetaCtx(ctx, db, q, append(pArgs, fArgs...), includeDocs)
}

func queryFilesByOIDsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, oids []string) ([]indexedFileMeta, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	oSQL, oArgs := projectsInClause(oids)
	q := `SELECT f.project, f.path, f.oid, 0 FROM files f WHERE f.project IN ` + pSQL + ` AND f.oid IN ` + oSQL + ` ORDER BY f.path`
	return scanIndexedFileMetaCtx(ctx, db, q, append(pArgs, oArgs...), includeDocs)
}

func scanIndexedFileMetaCtx(ctx context.Context, db *sql.DB, q string, args []any, includeDocs bool) ([]indexedFileMeta, error) {
	rows, err := db.QueryContext(ctx, q, args...)
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
		if !keepScannedFile(m.path, includeDocs, false) {
			continue
		}
		m.pkg = canonicalPkgDir(m.path)
		out = append(out, m)
	}
	return out, rows.Err()
}

func escapeSQLLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func queryExactSymbolsByNameDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, names []string) ([]RankedSymbol, error) {
	return querySymbolsWhereInDBCtx(ctx, db, projects, includeDocs, "sy.name", names)
}

func querySymbolsByOIDsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, oids []string) ([]RankedSymbol, error) {
	return querySymbolsWhereInDBCtx(ctx, db, projects, includeDocs, "sy.oid", oids)
}

func querySymbolsWhereInDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, col string, vals []string) ([]RankedSymbol, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	vSQL, vArgs := projectsInClause(vals)
	q := `SELECT f.project, f.path, sy.oid, sy.name, sy.recv, sy.kind, sy.pkg, sy.start_line, sy.end_line, sy.sig, sy.exported, sy.body_hash
		FROM symbols sy CROSS JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + pSQL + ` AND ` + col + ` IN ` + vSQL + `
		ORDER BY f.path, sy.start_line, sy.end_line, sy.recv, sy.name`
	return scanSymbolNodeRowsCtx(ctx, db, q, append(pArgs, vArgs...), includeDocs)
}

func scanSymbolNodeRowsCtx(ctx context.Context, db *sql.DB, q string, args []any, includeDocs bool) ([]RankedSymbol, error) {
	rows, err := db.QueryContext(ctx, q, args...)
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

func querySymRefsByOIDsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, oids []string) ([]rawSymRef, error) {
	return querySymRefsWhereInDBCtx(ctx, db, projects, includeDocs, "r.oid", oids, "ORDER BY f.path, r.line, r.from_sym, r.to_name")
}

func queryInboundSymRefsDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, names []string, limit int) ([]rawSymRef, error) {
	if limit <= 0 {
		return nil, nil
	}
	return querySymRefsWhereInDBCtx(ctx, db, projects, includeDocs, "r.to_name", names, "ORDER BY f.path, r.line LIMIT ?", limit)
}

func querySymRefsWhereInDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, col string, vals []string, tail string, tailArgs ...any) ([]rawSymRef, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	vSQL, vArgs := projectsInClause(vals)
	q := `SELECT f.path, r.oid, r.from_sym, r.to_pkg, r.to_name, r.kind, r.confidence, r.weight, r.line
		FROM sym_refs r CROSS JOIN files f ON f.oid = r.oid
		WHERE f.project IN ` + pSQL + ` AND ` + col + ` IN ` + vSQL + ` ` + tail
	return scanRawSymRefRowsCtx(ctx, db, q, append(append(pArgs, vArgs...), tailArgs...), includeDocs)
}

func queryOIDsBySymbolNamesDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	pSQL, pArgs := projectsInClause(projects)
	nSQL, nArgs := projectsInClause(names)
	q := `SELECT DISTINCT f.path, sy.oid FROM symbols sy CROSS JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + pSQL + ` AND sy.name IN ` + nSQL
	rows, err := db.QueryContext(ctx, q, append(pArgs, nArgs...)...)
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
		if !isExcludedASTPath(fPath) && (includeDocs || !isDocPath(fPath)) && !seen[oid] {
			seen[oid] = true
			out = append(out, oid)
		}
	}
	return out, rows.Err()
}

func scanRawSymRefRowsCtx(ctx context.Context, db *sql.DB, q string, args []any, includeDocs bool) ([]rawSymRef, error) {
	rows, err := db.QueryContext(ctx, q, args...)
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
	for _, group := range [][]rawSymRef{a, b} {
		for _, r := range group {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

func partitionPrioritySymNames(syms []RankedSymbol, terms []string, limit int) (priority, other []string) {
	seen := map[string]bool{}
	for _, matchFn := range []func(RankedSymbol, []string) bool{matchesPromptSymbolName, matchesPromptTerms} {
		for _, s := range syms {
			if s.Name != "" && !seen[s.Name] && matchFn(s, terms) {
				seen[s.Name] = true
				priority = append(priority, s.Name)
				if limit > 0 && len(priority) >= limit {
					return priority, nil
				}
			}
		}
		if len(priority) > 0 {
			break
		}
	}
	for _, s := range syms {
		if s.Name != "" && !seen[s.Name] && (limit <= 0 || len(priority)+len(other) < limit) {
			seen[s.Name] = true
			other = append(other, s.Name)
		}
	}
	return priority, other
}

func collectDistinctOutTargetNames(refs []rawSymRef, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range refs {
		if r.toName != "" && r.toName != "<import>" && !seen[r.toName] {
			seen[r.toName] = true
			out = append(out, r.toName)
			if limit > 0 && len(out) >= limit {
				break
			}
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
