package vet

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func buildPackageSubtasks(trimmed string, intent IntentMode, slice SliceResult, knownFiles []string, catalog []SkillMeta) []SubtaskSlice {
	if IsDiagnosticReadOnlyPrompt(trimmed) {
		return nil
	}
	explicitFiles := extractExplicitPromptTargets(trimmed, knownFiles)
	if !hasCodeOrDirTarget(explicitFiles) {
		return nil
	}
	if !hasDecomposeEditVerb(trimmed) && inferClauseRole(trimmed, explicitFiles) == "explorer" {
		return nil
	}
	groups, order := groupFilesByPkg(explicitFiles, slice.Capsule)
	if len(order) < 2 {
		return nil
	}
	return buildPartitionSubtasks(trimmed, intent, slice, order, groups, catalog)
}

func buildPartitionSubtasks(trimmed string, intent IntentMode, slice SliceResult, order []string, groups map[string][]string, catalog []SkillMeta) []SubtaskSlice {
	used := map[string]bool{}
	out := make([]SubtaskSlice, 0, min(maxSubtaskSlices, len(order)))
	baseGoal := CollapseOneLine(trimmed, 120)
	for _, sp := range partitionPackageGroups(order, groups, slice.PkgCommunities) {
		assigned := claimDisjointFiles(sp.files, used)
		if len(assigned) == 0 {
			continue
		}
		role := inferClauseRole(trimmed, assigned)
		out = append(out, SubtaskSlice{
			ID:     fmt.Sprintf("subtask-%d", len(out)+1),
			Role:   role,
			Goal:   fmt.Sprintf("%s (%s)", baseGoal, sp.label),
			Files:  assigned,
			Skills: selectTopSkillNames(trimmed, intent, assigned, catalog),
			Verify: InferSliceVerify(role, assigned),
		})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// extractExplicitPromptTargets resolves only file/dir targets named in prompt
// itself (expanding explicit directory tokens and unique bare basenames via
// knownFiles, never pulling in unrelated 1-hop AST neighbor packages).
func extractExplicitPromptTargets(trimmed string, knownFiles []string) []string {
	var resolved []string
	for _, tok := range ExtractPromptAndSliceFiles(trimmed, nil) {
		norm := NormalizeSlashPath(tok)
		if norm == "" {
			continue
		}
		if IsDirPathToken(norm) {
			if under := filesUnderDir(norm, knownFiles); len(under) > 0 {
				resolved = append(resolved, under...)
				continue
			}
			resolved = append(resolved, norm)
			continue
		}
		resolved = append(resolved, resolveUniqueFileMatch(norm, knownFiles))
	}
	return dedupKnownFiles(resolved)
}

// countDistinctExplicitPackages counts distinct package directories explicitly
// referenced by action clauses (plus any lead-in before numbered markers),
// ignoring dropped constraint clauses, unrelated 1-hop AST neighbors, and
// unresolved bare filenames paired with an explicit target directory.
func countDistinctExplicitPackages(prompt string, clauses, knownFiles []string) int {
	knownSet := buildNormalizedFileSet(knownFiles)
	dirs := map[string]bool{}
	hasUnresolvedBare := false
	for _, text := range explicitPackageTexts(prompt, clauses) {
		if collectExplicitPackageDirs(text, knownFiles, knownSet, dirs) {
			hasUnresolvedBare = true
		}
	}
	if hasUnresolvedBare && len(dirs) == 0 {
		return 1
	}
	return len(dirs)
}

func buildNormalizedFileSet(files []string) map[string]bool {
	out := make(map[string]bool, len(files))
	for _, f := range files {
		if n := NormalizeSlashPath(f); n != "" {
			out[n] = true
		}
	}
	return out
}

func explicitPackageTexts(prompt string, clauses []string) []string {
	if len(clauses) < 2 {
		return []string{prompt}
	}
	texts := make([]string, 0, len(clauses)+1)
	if loc := numberedMarkerRe.FindStringIndex(prompt); loc != nil && loc[0] > 0 {
		texts = append(texts, prompt[:loc[0]])
	}
	return append(texts, clauses...)
}

func collectExplicitPackageDirs(text string, knownFiles []string, knownSet, dirs map[string]bool) bool {
	hasUnresolvedBare := false
	for _, target := range extractExplicitPromptTargets(text, knownFiles) {
		dir := PackageDirKey(target)
		if dir == "" {
			continue
		}
		if isUnresolvedBareTarget(dir, target, knownSet) {
			hasUnresolvedBare = true
			continue
		}
		dirs[dir] = true
	}
	return hasUnresolvedBare
}

func isUnresolvedBareTarget(dir, target string, knownSet map[string]bool) bool {
	return dir == "." && !strings.Contains(target, "/") && !IsDirPathToken(target) && !knownSet[target]
}

func hasDiagnosticReadOnlyVerb(s string) bool {
	stripped := strings.ToLower(FilePathRe.ReplaceAllString(" "+s+" ", " "))
	return ContainsAnyWord(stripped, "check", "checks", "inspect", "inspects", "profile", "profiles", "profiling", "measure", "measures", "measuring", "verify", "verifies", "audit", "audits")
}

// IsDiagnosticReadOnlyPrompt reports whether trimmed requests read-only inspection or lookup.
func IsDiagnosticReadOnlyPrompt(trimmed string) bool {
	if hasDecomposeEditVerb(trimmed) {
		return false
	}
	return hasDiagnosticReadOnlyVerb(trimmed) || hasLookupCues(strings.ToLower(trimmed))
}

type pkgPartition struct {
	label string
	files []string
}

type commBucket struct {
	dirs  []string
	files []string
}

func groupFilesByPkg(knownFiles []string, capsule string) (map[string][]string, []string) {
	groups := map[string][]string{}
	for _, raw := range knownFiles {
		if n := NormalizeSlashPath(raw); n != "" {
			dir := PackageDirKey(n)
			groups[dir] = append(groups[dir], n)
		}
	}
	ranks, order := hop0DirRanks(capsule), make([]string, 0, len(groups))
	for dir := range groups {
		order = append(order, dir)
	}
	sort.Slice(order, func(i, j int) bool {
		ri, oki := ranks[order[i]]
		rj, okj := ranks[order[j]]
		if oki != okj {
			return oki
		}
		if oki && ri != rj {
			return ri < rj
		}
		return order[i] < order[j]
	})
	return groups, order
}

func partitionPackageGroups(order []string, groups map[string][]string, pkgComms map[string]int) []pkgPartition {
	if len(order) <= maxSubtaskSlices {
		out := make([]pkgPartition, 0, len(order))
		for _, dir := range order {
			out = append(out, pkgPartition{label: dir, files: groups[dir]})
		}
		return out
	}
	if len(pkgComms) > 0 {
		if coalesced := coalesceOverflowByCommunity(order, groups, pkgComms); len(coalesced) >= 2 {
			return coalesced
		}
	}
	return partitionOverflowTail(order, groups)
}

func partitionOverflowTail(order []string, groups map[string][]string) []pkgPartition {
	out := make([]pkgPartition, 0, maxSubtaskSlices)
	for _, dir := range order[:maxSubtaskSlices-1] {
		out = append(out, pkgPartition{label: dir, files: groups[dir]})
	}
	var overflowFiles []string
	for _, dir := range order[maxSubtaskSlices-1:] {
		overflowFiles = append(overflowFiles, groups[dir]...)
	}
	return append(out, pkgPartition{label: strings.Join(order[maxSubtaskSlices-1:], ", "), files: overflowFiles})
}

func coalesceOverflowByCommunity(order []string, groups map[string][]string, pkgComms map[string]int) []pkgPartition {
	buckets := buildCommunityBuckets(order, groups, pkgComms)
	if len(buckets) < 2 {
		return nil
	}
	limit := min(len(buckets), maxSubtaskSlices)
	if len(buckets) > maxSubtaskSlices {
		limit = maxSubtaskSlices - 1
	}
	out := make([]pkgPartition, 0, min(maxSubtaskSlices, len(buckets)))
	for i := 0; i < limit; i++ {
		out = append(out, pkgPartition{label: strings.Join(buckets[i].dirs, ", "), files: buckets[i].files})
	}
	if len(buckets) > maxSubtaskSlices {
		out = append(out, mergeOverflowBuckets(buckets[maxSubtaskSlices-1:]))
	}
	return out
}

func buildCommunityBuckets(order []string, groups map[string][]string, pkgComms map[string]int) []commBucket {
	idxByComm := map[int]int{}
	var buckets []commBucket
	nextUnknown := -1
	for _, dir := range order {
		cid, ok := pkgComms[dir]
		if !ok {
			cid = nextUnknown
			nextUnknown--
		}
		if bIdx, exists := idxByComm[cid]; exists {
			buckets[bIdx].dirs = append(buckets[bIdx].dirs, dir)
			buckets[bIdx].files = append(buckets[bIdx].files, groups[dir]...)
		} else {
			idxByComm[cid] = len(buckets)
			buckets = append(buckets, commBucket{dirs: []string{dir}, files: append([]string(nil), groups[dir]...)})
		}
	}
	return buckets
}

func mergeOverflowBuckets(rest []commBucket) pkgPartition {
	var dirs, files []string
	for _, b := range rest {
		dirs = append(dirs, b.dirs...)
		files = append(files, b.files...)
	}
	return pkgPartition{label: strings.Join(dirs, ", "), files: files}
}

func claimDisjointFiles(cands []string, used map[string]bool) []string {
	var out []string
	for _, f := range cands {
		if n := NormalizeSlashPath(f); n != "" && !used[n] {
			used[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NormalizeSlashPath converts p to clean slash-separated repo-relative form.
func NormalizeSlashPath(p string) string {
	return strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./"), "/")
}

// IsDirPathToken reports whether p looks like a directory token rather than a file path.
func IsDirPathToken(p string) bool {
	base := filepath.Base(p)
	return filepath.Ext(p) == "" && base != "Dockerfile" && base != "Makefile"
}

// PackageDirKey returns the package directory key for file or directory path p.
func PackageDirKey(p string) string {
	if IsDirPathToken(p) {
		return p
	}
	if d := filepath.ToSlash(filepath.Dir(p)); d != "" {
		return d
	}
	return "."
}

func hasCodeOrDirTarget(files []string) bool {
	for _, f := range files {
		if ext := strings.ToLower(filepath.Ext(f)); ext != ".md" && ext != ".txt" {
			return true
		}
	}
	return false
}

func inferClauseRole(clause string, clauseFiles []string) string {
	stripped := strings.ToLower(FilePathRe.ReplaceAllString(" "+clause+" ", " "))
	switch {
	case ContainsAnyWord(stripped, "review", "reviews", "audit", "audits"):
		return "reviewer"
	case ContainsAnyWord(stripped, "security", "auth", "authn", "authz", "oauth", "oauth2", "oidc", "jwt", "secret", "secrets", "crypto", "cryptography", "hmac", "redact"):
		return "security"
	case ContainsAnyWord(stripped, "doc", "docs", "documentation", "readme", "spec", "specs", "sdd", "tdd"):
		return "documentation"
	case !hasDecomposeEditVerb(clause) && ContainsAnyWord(stripped, "find", "search", "trace", "explain", "check", "checks", "inspect", "inspects", "profile", "profiles", "profiling", "measure", "measures", "measuring", "verify", "verifies"):
		return "explorer"
	case len(clauseFiles) > 0 && allMarkdownFiles(clauseFiles) && !ContainsAnyWord(stripped, "implement", "fix", "add", "build", "create", "refactor", "test"):
		return "documentation"
	default:
		return "builder"
	}
}

func allMarkdownFiles(files []string) bool {
	for _, f := range files {
		if strings.ToLower(filepath.Ext(f)) != ".md" {
			return false
		}
	}
	return len(files) > 0
}

func selectTopSkillNames(clause string, intent IntentMode, clauseFiles []string, catalog []SkillMeta) []string {
	skills, _ := MatchSkills(clause, intent, clauseFiles, catalog)
	var out []string
	for _, s := range skills {
		if s.Name != "" {
			out = append(out, s.Name)
			if len(out) >= 2 {
				break
			}
		}
	}
	return out
}

// InferSliceVerify selects a safe verification command for role and clauseFiles.
func InferSliceVerify(role string, clauseFiles []string) string {
	if role == "explorer" && len(clauseFiles) == 0 {
		return ""
	}
	if goVerify := inferGoDirsVerify(clauseFiles); goVerify != "" {
		return goVerify
	}
	for _, f := range clauseFiles {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".ts", ".tsx", ".js", ".jsx":
			return "npm test"
		case ".sh":
			if isSafeVerifyPath(f) {
				return "bash -n " + f
			}
		case ".md":
			return "fyr check"
		}
	}
	return ""
}

func inferGoDirsVerify(clauseFiles []string) string {
	seen := map[string]bool{}
	var targets []string
	for _, f := range clauseFiles {
		if !isGoVerifyCandidate(f) {
			continue
		}
		target := formatGoVerifyTarget(f)
		if target != "" && !seen[target] {
			seen[target] = true
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return ""
	}
	return "go test -race " + strings.Join(targets, " ")
}

func isGoVerifyCandidate(f string) bool {
	if strings.ToLower(filepath.Ext(f)) == ".go" {
		return true
	}
	return IsDirPathToken(f) && (strings.HasPrefix(f, "internal/") || strings.HasPrefix(f, "cmd/") || strings.HasPrefix(f, "pkg/"))
}

func formatGoVerifyTarget(f string) string {
	dir := PackageDirKey(f)
	if dir == "." || dir == "" {
		return "./..."
	}
	cleanDir := strings.TrimPrefix(dir, "./")
	if !isSafeVerifyPath(cleanDir) {
		return ""
	}
	return "./" + cleanDir + "/..."
}

func isSafeVerifyPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '/' || c == '.' || c == '_' || c == '-' || c == '@' || c == '+'
		if !ok {
			return false
		}
	}
	return true
}
