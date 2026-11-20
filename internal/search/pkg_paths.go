package search

import (
	"database/sql"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func canonicalPkgDir(filePath string) string {
	p := strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/")
	p = strings.TrimPrefix(path.Clean(p), "./")
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	if p == "" || p == "." || p == ".." {
		return "."
	}
	d := path.Dir(p)
	if d == "" || d == "/" {
		return "."
	}
	return d
}

func topLevelLayer(p string) string {
	clean := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"), "./")
	if clean == "" || clean == "." {
		return "root"
	}
	first, _, hasSlash := strings.Cut(clean, "/")
	if !hasSlash && strings.Contains(clean, ".") {
		return "root"
	}
	return first
}

func isCodePath(p string) bool {
	return codeExt[strings.ToLower(path.Ext(p))]
}

func isDocPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".mdx":
		return true
	}
	return false
}

func isTSSource(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		return true
	}
	return false
}

func isTestPath(p string) bool {
	low := strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
	base := path.Base(low)
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") || strings.HasPrefix(base, "test_") {
		return true
	}
	for _, suf := range []string{".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx", ".test.js", ".spec.js", ".test.jsx", ".spec.jsx"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return strings.Contains(low, "/__tests__/") || strings.HasPrefix(low, "__tests__/")
}

var excludedASTSegments = map[string]bool{
	"vendor": true, "node_modules": true, "testdata": true,
	"dist": true, "build": true, ".next": true,
	"coverage": true, "third_party": true, "__pycache__": true,
}

func isExcludedASTPath(p string) bool {
	norm := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"), "./")
	if norm == "" {
		return true
	}
	for _, seg := range strings.Split(norm, "/") {
		if excludedASTSegments[seg] {
			return true
		}
	}
	lowBase := strings.ToLower(path.Base(norm))
	for _, suf := range []string{".pb.go", ".pb.gw.go", "_gen.go", ".gen.go", "_generated.go", ".min.js", ".min.css", ".bundle.js", ".d.ts"} {
		if strings.HasSuffix(lowBase, suf) {
			return true
		}
	}
	return false
}

func isGeneratedGoText(text string) bool {
	for i, line := range splitLines(text) {
		if i > 40 {
			break
		}
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "package ") {
			return false
		}
		if strings.HasPrefix(trim, "// Code generated ") && strings.HasSuffix(trim, " DO NOT EDIT.") {
			return true
		}
	}
	return false
}

func loadGoModules(sc *scope) map[string]string {
	if sc == nil {
		return map[string]string{}
	}
	var db *sql.DB
	if sc.st != nil {
		db = sc.st.db
	}
	return loadGoModulesFromDB(db, sc.projects)
}

func loadGoModulesFromDB(db *sql.DB, projects []string) map[string]string {
	out := map[string]string{}
	for _, root := range projects {
		readGoModAt(root, "go.mod", out)
	}
	if db == nil || len(projects) == 0 {
		return out
	}
	inSQL, args := projectsInClause(projects)
	q := `SELECT project, path FROM files WHERE project IN ` + inSQL + ` AND (path = 'go.mod' OR path LIKE '%/go.mod')`
	rows, err := db.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var proj, relPath string
		if rows.Scan(&proj, &relPath) == nil {
			readGoModAt(proj, relPath, out)
		}
	}
	return out
}

func readGoModAt(root, relPath string, out map[string]string) {
	if root == "" || relPath == "" {
		return
	}
	full := filepath.Join(root, filepath.FromSlash(relPath))
	fi, err := os.Lstat(full)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return
	}
	if data, err := os.ReadFile(full); err == nil {
		if mod := parseGoModModule(string(data)); mod != "" {
			out[mod] = canonicalPkgDir(relPath)
		}
	}
}

func parseGoModModule(text string) string {
	for _, line := range splitLines(text) {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

func resolveImportTarget(fromPath, toPkg string, goMods map[string]string, pkgSet map[string]bool, fileByStem map[string]string) (string, string, bool) {
	if toPkg == "" {
		return "", "", false
	}
	if isGoSource(fromPath) {
		pkg, ok := resolveGoImportTarget(toPkg, goMods, pkgSet)
		return pkg, "", ok
	}
	if isTSSource(fromPath) {
		return resolveTSImportTarget(toPkg, pkgSet, fileByStem)
	}
	if pkgSet[toPkg] {
		return toPkg, "", true
	}
	return "", "", false
}

func resolveGoImportTarget(toPkg string, goMods map[string]string, pkgSet map[string]bool) (string, bool) {
	if len(goMods) == 0 {
		return "", false
	}
	bestMod, bestRel := "", ""
	for mod, modDir := range goMods {
		if (toPkg == mod || strings.HasPrefix(toPkg, mod+"/")) && len(mod) > len(bestMod) {
			bestMod, bestRel = mod, modDir
		}
	}
	if bestMod == "" {
		return "", false
	}
	sub := strings.TrimPrefix(strings.TrimPrefix(toPkg, bestMod), "/")
	cand := bestRel
	if sub != "" {
		if bestRel == "" || bestRel == "." {
			cand = sub
		} else {
			cand = bestRel + "/" + sub
		}
	}
	if cand == "" {
		cand = "."
	}
	return cand, pkgSet[cand]
}

func resolveTSImportTarget(toPkg string, pkgSet map[string]bool, fileByStem map[string]string) (string, string, bool) {
	if !strings.HasPrefix(toPkg, "./") && !strings.HasPrefix(toPkg, "../") {
		return "", "", false
	}
	clean := strings.TrimPrefix(path.Clean(toPkg), "./")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", false
	}
	if f, ok := fileByStem[clean]; ok {
		return canonicalPkgDir(f), f, true
	}
	stem := trimTSExtension(clean)
	if f, ok := fileByStem[stem]; ok {
		return canonicalPkgDir(f), f, true
	}
	if f, ok := fileByStem[stem+"/index"]; ok {
		return canonicalPkgDir(f), f, true
	}
	if pkgSet[clean] {
		return clean, "", true
	}
	return "", "", false
}

func trimTSExtension(p string) string {
	low := strings.ToLower(p)
	for _, ext := range []string{".tsx", ".ts", ".jsx", ".js", ".mjs"} {
		if strings.HasSuffix(low, ext) {
			return p[:len(p)-len(ext)]
		}
	}
	return p
}
