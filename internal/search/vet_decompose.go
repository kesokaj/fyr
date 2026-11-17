package search

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const maxSubtaskSlices = 6

// SubtaskSlice is one deterministic, pairwise-disjoint unit of work emitted
// on PreInvocation for COORDINATE_SUBTASKS turns (SPEC-fyr-pre-hook-coordinator).
type SubtaskSlice struct {
	ID     string   `json:"id"`
	Role   string   `json:"role"`
	Goal   string   `json:"goal"`
	Files  []string `json:"files,omitempty"`
	Skills []string `json:"skills,omitempty"`
	Verify string   `json:"verify,omitempty"`
}

var numberedMarkerRe = regexp.MustCompile(`(?:^|[\s:;])(?:\((\d+)\)|(\d+)[.)])\s+`)

func stripAnySandboxPreamble(prompt string) string {
	s := strings.TrimSpace(StripSandboxPreamble(strings.TrimSpace(prompt)))
	low := strings.ToLower(s)
	if strings.HasPrefix(low, strings.ToLower(sandboxPreamblePrefix)) {
		if idx := strings.Index(low, strings.ToLower(sandboxPreambleEnd)); idx >= 0 {
			return strings.TrimSpace(s[idx+len(sandboxPreambleEnd):])
		}
	}
	return s
}

func isProjectThisCloneLine(line string) bool {
	const prefix = "project: this clone"
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	if len(line) == len(prefix) {
		return true
	}
	switch line[len(prefix)] {
	case ' ', '\t', ',', '.', ';', ':':
		return true
	default:
		return false
	}
}

func isKickoffHeaderMetaLine(line string) bool {
	for _, p := range []string{"project:", "session:", "plan:", "model:", "class:", "level:", "never:", "done when:"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func hasWorkerTopHeader(lines []string) bool {
	limit := min(len(lines), 8)
	for i := 0; i < limit; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			return false
		}
		switch {
		case strings.HasPrefix(line, "files:"):
			return true
		case isProjectThisCloneLine(line):
			return true
		case strings.HasPrefix(line, "standing approvals:"),
			strings.HasPrefix(line, "standing approvals (closed set):"):
			return true
		case i == 0 && line == "this is a kickoff written by fyr auto.":
			return true
		case isKickoffHeaderMetaLine(line):
			continue
		default:
			return false
		}
	}
	return false
}

func hasWorkerCapsuleSection(lines []string) bool {
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "[fyr_worker_capsule]" &&
			strings.HasPrefix(strings.TrimSpace(lines[i+1]), "trust: ") {
			return true
		}
	}
	return false
}

// isWorkerBriefPrompt reports whether prompt (original or lowercased) is an
// isolated worker brief or autonomous Kickoff so hooks never route workers into
// COORDINATE_SUBTASKS, fyr-swarm, or [COORDINATOR_GUARD] (D25, SPEC-fyr-pre-hook-coordinator).
func isWorkerBriefPrompt(prompt string) bool {
	s := stripAnySandboxPreamble(prompt)
	if s == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(s, "\r\n", "\n"))
	lines := strings.Split(low, "\n")
	return hasWorkerTopHeader(lines) || hasWorkerCapsuleSection(lines)
}

// DecomposeSubtasks deterministically splits multi-clause or multi-package
// coordinator prompts into <= 6 pairwise-disjoint SubtaskSlice entries.
func DecomposeSubtasks(cleanPrompt string, intent IntentMode, slice SliceResult, files []string, catalog []SkillMeta) []SubtaskSlice {
	trimmed := strings.TrimSpace(StripSandboxPreamble(strings.TrimSpace(cleanPrompt)))
	if shouldSkipDecompose(trimmed, intent, slice.SymbolHits) {
		return nil
	}
	if intent == "" {
		intent = ClassifyIntent(trimmed, slice.SymbolHits)
	}
	knownFiles := dedupKnownFiles(extractPromptAndSliceFiles(trimmed, append(append([]string{}, slice.Files...), files...)))
	if clauses := splitDecomposeClauses(trimmed); len(clauses) >= 2 {
		return buildClauseSubtasks(clauses, slice, knownFiles, catalog)
	}
	return buildPackageSubtasks(trimmed, intent, slice, knownFiles, catalog)
}

func dedupKnownFiles(raw []string) []string {
	var normed, out []string
	seen, hasFull := map[string]bool{}, map[string]bool{}
	for _, r := range raw {
		if n := normalizeSlashPath(r); n != "" && !seen[n] {
			seen[n] = true
			normed = append(normed, n)
			if strings.Contains(n, "/") && !isDirPathToken(n) {
				hasFull[filepath.Base(n)] = true
			}
		}
	}
	for _, n := range normed {
		if (!strings.Contains(n, "/") && !isDirPathToken(n) && hasFull[n]) ||
			(isDirPathToken(n) && len(filesUnderDir(n, normed)) > 0) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func shouldSkipDecompose(trimmed string, intent IntentMode, symbolHits int) bool {
	low := strings.ToLower(trimmed)
	if trimmed == "" || strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "/") ||
		strings.HasPrefix(low, "run /") || isWorkerBriefPrompt(trimmed) {
		return true
	}
	if intent == "" {
		intent = ClassifyIntent(trimmed, symbolHits)
	}
	return intent == IntentApproval || intent == IntentTrivial || intent == IntentQuestion || intent == IntentCorrection
}

func splitDecomposeClauses(trimmed string) []string {
	if numbered := splitNumberedClauses(trimmed); len(numbered) >= 2 {
		return numbered
	}
	if semi := splitSemicolonActionClauses(trimmed); len(semi) >= 2 {
		return semi
	}
	return nil
}

func splitNumberedClauses(prompt string) []string {
	type marker struct{ start, content int }
	var markers []marker
	nextNum := 1
	for _, loc := range numberedMarkerRe.FindAllStringSubmatchIndex(prompt, -1) {
		if insideBackticks(prompt, loc[0]) {
			continue
		}
		if parseMarkerDigits(prompt, loc) == nextNum {
			markers = append(markers, marker{start: loc[0], content: loc[1]})
			nextNum++
		}
	}
	if len(markers) < 2 {
		return nil
	}
	var out []string
	for i, m := range markers {
		end := len(prompt)
		if i+1 < len(markers) {
			end = markers[i+1].start
		}
		clause := strings.TrimSpace(strings.TrimRight(prompt[m.content:end], "; "))
		if len(strings.Fields(clause)) >= 2 && !isConstraintClause(strings.ToLower(clause)) {
			out = append(out, clause)
		}
	}
	return out
}

func parseMarkerDigits(prompt string, loc []int) int {
	for _, pair := range [][2]int{{loc[2], loc[3]}, {loc[4], loc[5]}} {
		if pair[0] >= 0 && pair[1] > pair[0] {
			if n, err := strconv.Atoi(prompt[pair[0]:pair[1]]); err == nil {
				return n
			}
		}
	}
	return 0
}

func insideBackticks(s string, pos int) bool {
	open := false
	for i := 0; i < pos && i < len(s); i++ {
		if s[i] == '`' {
			open = !open
		}
	}
	return open
}

func splitSemicolonActionClauses(prompt string) []string {
	if !strings.Contains(prompt, ";") {
		return nil
	}
	var out []string
	start, inTick := 0, false
	flush := func(seg string) {
		if c := strings.TrimSpace(seg); len(strings.Fields(c)) >= 2 && !isConstraintClause(strings.ToLower(c)) {
			out = append(out, c)
		}
	}
	for i := 0; i < len(prompt); i++ {
		switch prompt[i] {
		case '`':
			inTick = !inTick
		case ';':
			if !inTick {
				flush(prompt[start:i])
				start = i + 1
			}
		}
	}
	flush(prompt[start:])
	return out
}

func buildClauseSubtasks(clauses []string, slice SliceResult, knownFiles []string, catalog []SkillMeta) []SubtaskSlice {
	if len(clauses) > maxSubtaskSlices {
		clauses = append(append([]string{}, clauses[:maxSubtaskSlices-1]...), strings.Join(clauses[maxSubtaskSlices-1:], "; "))
	}
	used, symFiles := map[string]bool{}, extractCapsuleSymbolFiles(slice.Capsule)
	out := make([]SubtaskSlice, 0, len(clauses))
	for i, clause := range clauses {
		assigned := claimDisjointFiles(matchClauseFiles(clause, knownFiles, symFiles), used)
		role := inferClauseRole(clause, assigned)
		cIntent := ClassifyIntent(clause, max(1, len(assigned)))
		out = append(out, SubtaskSlice{
			ID:     fmt.Sprintf("subtask-%d", i+1),
			Role:   role,
			Goal:   collapseOneLine(clause, 180),
			Files:  assigned,
			Skills: selectTopSkillNames(clause, cIntent, assigned, catalog),
			Verify: inferSliceVerify(role, assigned),
		})
	}
	return out
}

func matchClauseFiles(clause string, knownFiles []string, symFiles map[string][]string) []string {
	var cands []string
	for _, tok := range extractPromptAndSliceFiles(clause, nil) {
		norm := normalizeSlashPath(tok)
		if isDirPathToken(norm) {
			if under := filesUnderDir(norm, knownFiles); len(under) > 0 {
				cands = append(cands, under...)
				continue
			}
		}
		cands = append(cands, resolveUniqueFileMatch(norm, knownFiles))
	}
	if len(cands) == 0 && len(symFiles) > 0 {
		for sym, paths := range symFiles {
			_, short, hasDot := strings.Cut(sym, ".")
			if (len(sym) >= 4 && strings.Contains(clause, sym)) || (hasDot && len(short) >= 4 && strings.Contains(clause, short)) {
				cands = append(cands, paths...)
			}
		}
		sort.Strings(cands)
	}
	return cands
}

func resolveUniqueFileMatch(tok string, knownFiles []string) string {
	if strings.Contains(tok, "/") {
		return tok
	}
	var match string
	for _, kf := range knownFiles {
		if n := normalizeSlashPath(kf); filepath.Base(n) == tok {
			if match != "" && match != n {
				return tok
			}
			match = n
		}
	}
	if match != "" {
		return match
	}
	return tok
}

func filesUnderDir(dir string, knownFiles []string) []string {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	var out []string
	for _, kf := range knownFiles {
		if n := normalizeSlashPath(kf); strings.HasPrefix(n, prefix) && !isDirPathToken(n) {
			out = append(out, n)
		}
	}
	return out
}

func extractCapsuleSymbolFiles(capsule string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") && !strings.HasPrefix(line, "[Hop 1] ") {
			continue
		}
		if path, sym := parseCapsuleLinePathSym(line); path != "" && sym != "" {
			out[sym] = append(out[sym], path)
		}
	}
	return out
}

func parseCapsuleLinePathSym(line string) (string, string) {
	rest := line[strings.IndexByte(line, ']')+2:]
	spec, after, ok := strings.Cut(rest, " (")
	if !ok {
		return "", ""
	}
	colon := strings.LastIndexByte(spec, ':')
	sym, _, okSym := strings.Cut(after, ")")
	if colon <= 0 || !okSym {
		return "", ""
	}
	return normalizeSlashPath(spec[:colon]), strings.TrimSpace(sym)
}

func buildPackageSubtasks(trimmed string, intent IntentMode, slice SliceResult, knownFiles []string, catalog []SkillMeta) []SubtaskSlice {
	if !hasCodeOrDirTarget(knownFiles) {
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

func hop0DirRanks(capsule string) map[string]int {
	ranks, idx := map[string]int{}, 0
	for _, line := range strings.Split(capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") {
			continue
		}
		if path, _ := parseCapsuleLinePathSym(line); path != "" {
			dir := packageDirKey(path)
			if _, exists := ranks[dir]; !exists {
				ranks[dir] = idx
				idx++
			}
		}
	}
	return ranks
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
