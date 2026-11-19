package search

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func buildPackageSubtasks(trimmed string, intent IntentMode, slice SliceResult, knownFiles []string, catalog []SkillMeta) []SubtaskSlice {
	low := strings.ToLower(trimmed)
	isReadOnlyLookup := !hasDecomposeEditVerb(trimmed) && (inferClauseRole(trimmed, knownFiles) == "explorer" || hasLookupCues(low))
	if !hasCodeOrDirTarget(knownFiles) || isReadOnlyLookup {
		return nil
	}
	groups, order := groupFilesByPkg(knownFiles, slice.Capsule)
	if len(order) < 2 {
		return nil
	}
	used, out, baseGoal := map[string]bool{}, make([]SubtaskSlice, 0, min(maxSubtaskSlices, len(order))), collapseOneLine(trimmed, 120)
	for _, sp := range partitionPackageGroups(order, groups) {
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
			Verify: inferSliceVerify(role, assigned),
		})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

type pkgPartition struct {
	label string
	files []string
}

func groupFilesByPkg(knownFiles []string, capsule string) (map[string][]string, []string) {
	groups := map[string][]string{}
	for _, raw := range knownFiles {
		if n := normalizeSlashPath(raw); n != "" {
			dir := packageDirKey(n)
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

func partitionPackageGroups(order []string, groups map[string][]string) []pkgPartition {
	if len(order) <= maxSubtaskSlices {
		out := make([]pkgPartition, 0, len(order))
		for _, dir := range order {
			out = append(out, pkgPartition{label: dir, files: groups[dir]})
		}
		return out
	}
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

func claimDisjointFiles(cands []string, used map[string]bool) []string {
	var out []string
	for _, f := range cands {
		if n := normalizeSlashPath(f); n != "" && !used[n] {
			used[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeSlashPath(p string) string {
	return strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./"), "/")
}

func isDirPathToken(p string) bool {
	base := filepath.Base(p)
	return filepath.Ext(p) == "" && base != "Dockerfile" && base != "Makefile"
}

func packageDirKey(p string) string {
	if isDirPathToken(p) {
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
	stripped := strings.ToLower(fileTokenRe.ReplaceAllString(" "+clause+" ", " "))
	switch {
	case containsAnyWord(stripped, "review", "reviews", "audit", "audits"):
		return "reviewer"
	case containsAnyWord(stripped, "security", "auth", "authn", "authz", "oauth", "oauth2", "oidc", "jwt", "secret", "secrets", "crypto", "cryptography", "hmac", "redact"):
		return "security"
	case containsAnyWord(stripped, "doc", "docs", "documentation", "readme", "spec", "specs", "sdd", "tdd"):
		return "documentation"
	case containsAnyWord(stripped, "find", "search", "trace", "explain"):
		return "explorer"
	case len(clauseFiles) > 0 && allMarkdownFiles(clauseFiles) && !containsAnyWord(stripped, "implement", "fix", "add", "build", "create", "refactor", "test"):
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

func inferSliceVerify(role string, clauseFiles []string) string {
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
			return "bash -n " + f
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
		if strings.ToLower(filepath.Ext(f)) != ".go" && !(isDirPathToken(f) && (strings.HasPrefix(f, "internal/") || strings.HasPrefix(f, "cmd/") || strings.HasPrefix(f, "pkg/"))) {
			continue
		}
		dir, target := packageDirKey(f), "./..."
		if dir != "." && dir != "" {
			target = "./" + strings.TrimPrefix(dir, "./") + "/..."
		}
		if !seen[target] {
			seen[target] = true
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return ""
	}
	return "go test -race " + strings.Join(targets, " ")
}
