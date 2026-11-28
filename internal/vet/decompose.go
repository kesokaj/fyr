package vet

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

var (
	numberedMarkerRe  = regexp.MustCompile(`(?:^|[\s:;])(?:\((\d+)\)|(\d+)[.)])\s+`)
	blanketReadOnlyRe = regexp.MustCompile(
		`\b(?:(?:do not|don't)\s+(?:edit|modify)|without\s+editing)\s+(?:anything\b|files\b|any\s+files?\b|any(?:\s*(?:$|[.,;!?:)]|\band\b)))` +
			`|\b(?:do not|don't)\s+(?:edit|modify)\s*(?:$|[.,;!?:)]|\band\b)` +
			`|\bwithout\s+editing\s*(?:$|[.,;!?:)])` +
			`|\b(?:change|edit|modify)\s+nothing\b`,
	)
)

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
	knownFiles := dedupKnownFiles(ExtractPromptAndSliceFiles(trimmed, append(append([]string{}, slice.Files...), files...)))
	if clauses := splitDecomposeClauses(trimmed); len(clauses) >= 2 {
		if !HasExplicitMultiPhaseOrParallelCues(strings.ToLower(trimmed)) &&
			countDistinctExplicitPackages(trimmed, clauses, slice.Files) < 2 {
			return nil
		}
		return buildClauseSubtasks(clauses, slice, knownFiles, catalog)
	}
	return buildPackageSubtasks(trimmed, intent, slice, knownFiles, catalog)
}

func dedupKnownFiles(raw []string) []string {
	var normed, out []string
	seen, hasFull := map[string]bool{}, map[string]bool{}
	for _, r := range raw {
		n := NormalizeSlashPath(r)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		normed = append(normed, n)
		if strings.Contains(n, "/") && !IsDirPathToken(n) {
			hasFull[filepath.Base(n)] = true
		}
	}
	for _, n := range normed {
		if (!strings.Contains(n, "/") && !IsDirPathToken(n) && hasFull[n]) ||
			(IsDirPathToken(n) && len(filesUnderDir(n, normed)) > 0) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func isBlanketReadOnlyPrompt(low string) bool {
	return blanketReadOnlyRe.MatchString(low)
}

func shouldSkipDecompose(trimmed string, intent IntentMode, symbolHits int) bool {
	low := strings.ToLower(trimmed)
	if trimmed == "" || strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "/") ||
		strings.HasPrefix(low, "run /") || IsWorkerBriefPrompt(trimmed) || isBlanketReadOnlyPrompt(low) ||
		IsDiagnosticReadOnlyPrompt(trimmed) {
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
			Goal:   CollapseOneLine(clause, 180),
			Files:  assigned,
			Skills: selectTopSkillNames(clause, cIntent, assigned, catalog),
			Verify: InferSliceVerify(role, assigned),
		})
	}
	return out
}

func matchClauseFiles(clause string, knownFiles []string, symFiles map[string][]string) []string {
	cands := matchClauseExplicitFiles(clause, knownFiles)
	if len(cands) == 0 && len(symFiles) > 0 {
		return matchClauseSymbolFiles(clause, symFiles)
	}
	return cands
}

func matchClauseExplicitFiles(clause string, knownFiles []string) []string {
	var cands []string
	for _, tok := range ExtractPromptAndSliceFiles(clause, nil) {
		norm := NormalizeSlashPath(tok)
		if IsDirPathToken(norm) {
			if under := filesUnderDir(norm, knownFiles); len(under) > 0 {
				cands = append(cands, under...)
				continue
			}
		}
		cands = append(cands, resolveUniqueFileMatch(norm, knownFiles))
	}
	return cands
}

func matchClauseSymbolFiles(clause string, symFiles map[string][]string) []string {
	var cands []string
	for sym, paths := range symFiles {
		if matchesCapsuleSymbol(clause, sym) {
			cands = append(cands, paths...)
		}
	}
	sort.Strings(cands)
	return cands
}

func matchesCapsuleSymbol(clause, sym string) bool {
	if len(sym) >= 4 && strings.Contains(clause, sym) {
		return true
	}
	_, short, hasDot := strings.Cut(sym, ".")
	return hasDot && len(short) >= 4 && strings.Contains(clause, short)
}

func resolveUniqueFileMatch(tok string, knownFiles []string) string {
	if strings.Contains(tok, "/") {
		return tok
	}
	var match string
	for _, kf := range knownFiles {
		if n := NormalizeSlashPath(kf); filepath.Base(n) == tok {
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
		if n := NormalizeSlashPath(kf); strings.HasPrefix(n, prefix) && !IsDirPathToken(n) {
			out = append(out, n)
		}
	}
	return out
}

func hasDecomposeEditVerb(trimmed string) bool {
	stripped := strings.ToLower(FilePathRe.ReplaceAllString(" "+trimmed+" ", " "))
	return ContainsAnyWord(stripped, "implement", "fix", "add", "update", "refactor", "replace", "remove", "delete", "rename", "migrate", "write", "build", "create", "change", "modify")
}
