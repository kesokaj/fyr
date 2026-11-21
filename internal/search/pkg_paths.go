package search

import (
	"database/sql"
	"io"
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

const vendoredExtractPrefix = "__vendored__/"

func normalizeASTPath(p string) string {
	norm := strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if norm == "" {
		return ""
	}
	norm = strings.TrimPrefix(path.Clean(norm), "./")
	if norm == "" || norm == "." {
		return ""
	}
	return norm
}

func isExcludedASTPath(p string) bool {
	return isExcludedASTPathOpt(p, false)
}

func isExcludedASTPathOpt(p string, includeVendored bool) bool {
	norm := normalizeASTPath(p)
	if norm == "" {
		return true
	}
	if !includeVendored && isVendoredSkillPath(norm) {
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

// IsVendoredSkillPath reports whether p is inside a skill's vendored
// references/ or assets/ subtree while excluding SKILL.md and scripts/.
func IsVendoredSkillPath(p string) bool {
	norm := normalizeASTPath(p)
	return norm != "" && isVendoredSkillPath(norm)
}

func vendoredExtractAlias(p string) (string, string, bool) {
	norm := normalizeASTPath(p)
	if norm == "" || !isVendoredSkillPath(norm) || isExcludedASTPathOpt(norm, true) {
		return "", "", false
	}
	return norm, vendoredExtractPrefix + norm, true
}

func stripVendoredExtractPrefix(p string) string {
	if strings.HasPrefix(p, "./"+vendoredExtractPrefix) {
		return "./" + strings.TrimPrefix(p, "./"+vendoredExtractPrefix)
	}
	return strings.TrimPrefix(p, vendoredExtractPrefix)
}

// isVendoredSkillPath excludes third-party reference docs/code and assets
// shipped under skills/<name>/{references,assets}/... or
// .agents/skills/<name>/{references,assets}/... while preserving SKILL.md and scripts/.
func isVendoredSkillPath(norm string) bool {
	segs := strings.Split(norm, "/")
	start := 0
	if len(segs) >= 5 && segs[0] == ".agents" {
		start = 1
	}
	rest := segs[start:]
	return len(rest) >= 4 && rest[0] == "skills" && rest[1] != "" && (rest[2] == "references" || rest[2] == "assets") && rest[3] != ""
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
	q := `SELECT project, path FROM files WHERE project IN ` + inSQL + ` AND (path = 'go.mod' OR path LIKE '%/go.mod') ORDER BY length(path), path`
	rows, err := db.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var proj, relPath string
		if rows.Scan(&proj, &relPath) == nil && !isExcludedASTPath(relPath) {
			readGoModAt(proj, relPath, out)
		}
	}
	return out
}

func readGoModAt(root, relPath string, out map[string]string) {
	if root == "" || relPath == "" {
		return
	}
	rel := filepath.Clean(filepath.FromSlash(relPath))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer r.Close()
	fi, err := r.Lstat(rel)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return
	}
	f, err := r.Open(rel)
	if err != nil {
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil || len(data) > maxFileBytes {
		return
	}
	if mod := parseGoModModule(string(data)); mod != "" {
		if _, exists := out[mod]; !exists {
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
