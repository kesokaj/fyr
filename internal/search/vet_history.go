package search

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// VetHistoryRow is one turn's persisted vetting state in .agents/scratch/search.db.
type VetHistoryRow struct {
	ConversationID  string
	TurnNum         int
	Intent          IntentMode
	Gate            VetGate
	Score           int
	RawPrompt       string
	ImprovedPrompt  string
	PresentAnchors  []string
	MissingAnchors  []string
	SuggestedSkills []string
	PivotFiles      []string
	CreatedAt       int64
}

var canonicalVetAnchors = []string{
	"target", "goal", "scope", "inputs_outputs", "constraints", "verification",
}

var vetAnchorWeights = map[string]int{
	"target":         25,
	"goal":           15,
	"scope":          15,
	"inputs_outputs": 15,
	"constraints":    15,
	"verification":   15,
}

func (st *store) recordVetHistory(convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string) error {
	if st == nil || st.db == nil {
		return nil
	}
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	if turnNum <= 0 {
		turnNum = 1
	}
	var skillNames []string
	for _, s := range res.Skills {
		if s.Name != "" {
			skillNames = append(skillNames, s.Name)
		}
	}
	_, err := st.db.Exec(
		`INSERT OR REPLACE INTO vet_history(conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		convID,
		turnNum,
		string(res.Intent),
		string(res.Gate),
		res.Score,
		strings.TrimSpace(rawPrompt),
		res.ImprovedPrompt,
		strings.Join(res.PresentAnchors, ","),
		strings.Join(res.MissingAnchors, ","),
		strings.Join(skillNames, ","),
		strings.Join(pivotFiles, ","),
		nowFunc().Unix(),
	)
	return err
}

func (st *store) lastVetHistory(convID string, beforeTurn int) (*VetHistoryRow, error) {
	if st == nil || st.db == nil {
		return nil, nil
	}
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	query := `SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, created_at FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT 1`
	args := []any{convID}
	if beforeTurn > 0 {
		query = `SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, created_at FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT 1`
		args = []any{convID, beforeTurn}
	}
	row, err := scanVetHistoryRow(st.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

func (st *store) recentSuggestedSkills(convID string, beforeTurn, window int) (map[string]bool, []string, error) {
	if st == nil || st.db == nil {
		return nil, nil, nil
	}
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	if window <= 0 {
		window = 3
	}
	query := `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT ?`
	args := []any{convID, window}
	if beforeTurn > 0 {
		query = `SELECT suggested_skills, pivot_files FROM vet_history WHERE conversation_id = ? AND turn_num < ? ORDER BY turn_num DESC LIMIT ?`
		args = []any{convID, beforeTurn, window}
	}
	rows, err := st.db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	skills := map[string]bool{}
	pivotsSeen := map[string]bool{}
	var pivots []string
	for rows.Next() {
		var sCSV, pCSV string
		if err := rows.Scan(&sCSV, &pCSV); err != nil {
			continue
		}
		for _, s := range splitCSV(sCSV) {
			skills[s] = true
		}
		for _, p := range splitCSV(pCSV) {
			if !pivotsSeen[p] {
				pivotsSeen[p] = true
				pivots = append(pivots, p)
			}
		}
	}
	return skills, pivots, rows.Err()
}

func (st *store) listConversationVetHistory(convID string, limit int) ([]VetHistoryRow, error) {
	if st == nil || st.db == nil {
		return nil, nil
	}
	if strings.TrimSpace(convID) == "" {
		convID = "default"
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := st.db.Query(
		`SELECT conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, created_at FROM vet_history WHERE conversation_id = ? ORDER BY turn_num ASC LIMIT ?`,
		convID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VetHistoryRow
	for rows.Next() {
		r, err := scanVetHistoryRow(rows)
		if err != nil {
			return out, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVetHistoryRow(sc rowScanner) (*VetHistoryRow, error) {
	var r VetHistoryRow
	var intent, gate, presentCSV, missingCSV, skillsCSV, pivotsCSV string
	if err := sc.Scan(
		&r.ConversationID, &r.TurnNum, &intent, &gate, &r.Score,
		&r.RawPrompt, &r.ImprovedPrompt, &presentCSV, &missingCSV,
		&skillsCSV, &pivotsCSV, &r.CreatedAt,
	); err != nil {
		return nil, err
	}
	r.Intent = IntentMode(intent)
	r.Gate = VetGate(gate)
	r.PresentAnchors = splitCSV(presentCSV)
	r.MissingAnchors = splitCSV(missingCSV)
	r.SuggestedSkills = splitCSV(skillsCSV)
	r.PivotFiles = splitCSV(pivotsCSV)
	return &r, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ComputeVetScore computes a deterministic 0..100 prompt quality score across
// the 6 anchors (target 25, goal 15, scope 15, inputs_outputs 15, constraints
// 15 or -40 on conflict, verification 15) and carries forward anchors from prev
// when prev was VET_FIRST or the current turn is a follow-up.
func ComputeVetScore(prompt string, intent IntentMode, slice SliceResult, brainCtx TurnBrainContext, prev *VetHistoryRow) (int, []string, []string) {
	present, _ := EvaluateAnchors(prompt, slice, brainCtx)
	if shouldCarryForwardVet(prompt, intent, slice, prev) {
		present = mergeAnchorLists(present, prev.PresentAnchors)
	}
	missing := computeMissingAnchors(present)
	score := 0
	for _, a := range present {
		if a == "constraints" && len(brainCtx.Conflicts) > 0 {
			continue
		}
		score += vetAnchorWeights[a]
	}
	if len(brainCtx.Conflicts) > 0 {
		score -= 40
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, present, missing
}

func shouldCarryForwardVet(prompt string, intent IntentMode, slice SliceResult, prev *VetHistoryRow) bool {
	if prev == nil || intent == IntentTrivial {
		return false
	}
	if prev.Gate == GateVetFirst {
		return true
	}
	if intent == IntentFeature {
		if prev.Intent == IntentQuestion {
			return false
		}
		return sharesPivotSurface(prompt, slice.Files, prev.PivotFiles)
	}
	if len(strings.Fields(prompt)) <= 12 {
		return true
	}
	return sharesPivotSurface(prompt, slice.Files, prev.PivotFiles)
}

func sharesPivotSurface(prompt string, curFiles, prevFiles []string) bool {
	if len(prevFiles) == 0 {
		return false
	}
	low := strings.ToLower(prompt)
	for _, pf := range prevFiles {
		if slices.Contains(curFiles, pf) {
			return true
		}
		base := strings.ToLower(filepath.Base(pf))
		if base != "" && strings.Contains(low, base) {
			return true
		}
	}
	return false
}

func mergeAnchorLists(cur, prev []string) []string {
	seen := map[string]bool{}
	for _, a := range cur {
		seen[a] = true
	}
	for _, a := range prev {
		seen[a] = true
	}
	var out []string
	for _, a := range canonicalVetAnchors {
		if seen[a] {
			out = append(out, a)
		}
	}
	return out
}

func computeMissingAnchors(present []string) []string {
	var missing []string
	for _, a := range canonicalVetAnchors {
		if !slices.Contains(present, a) {
			missing = append(missing, a)
		}
	}
	return missing
}

func filterSuggestedSkills(skills []SkillMeta, curFiles []string, recentSkills map[string]bool, recentPivots []string) []SkillMeta {
	if len(skills) == 0 || len(recentSkills) == 0 {
		return skills
	}
	if hasNewFileSurface(curFiles, recentPivots) {
		return skills
	}
	var out []SkillMeta
	for _, s := range skills {
		if !recentSkills[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

func hasNewFileSurface(curFiles, recentPivots []string) bool {
	if len(curFiles) == 0 || len(recentPivots) == 0 {
		return false
	}
	for _, f := range curFiles {
		if !slices.Contains(recentPivots, f) {
			return true
		}
	}
	return false
}

// SynthesizeImprovedPrompt generates the deterministic [VET_IMPROVED_PROMPT]
// block in < 2 ms when res.Score >= 45 and the turn is executable.
func SynthesizeImprovedPrompt(prompt string, res VetResult, slice SliceResult, brainCtx TurnBrainContext, prev *VetHistoryRow) string {
	if res.Score < 45 || res.Gate != GateEnrichAndProceed || res.Intent == IntentQuestion || res.Intent == IntentCorrection {
		return ""
	}
	task := collapseOneLine(StripSandboxPreamble(strings.TrimSpace(prompt)), 280)
	if prev != nil && shouldCarryForwardVet(prompt, res.Intent, slice, prev) && prev.RawPrompt != "" && prev.RawPrompt != strings.TrimSpace(prompt) {
		task = fmt.Sprintf("%s (follow-up turn %d: %s)", collapseOneLine(prev.RawPrompt, 180), prev.TurnNum, task)
	}
	targets := formatImprovedPromptTargets(prompt, slice)
	files := extractPromptAndSliceFiles(prompt, slice.Files)
	scopeStr := "project"
	if len(files) > 0 {
		scopeStr = strings.Join(files[:min(6, len(files))], ", ")
	}
	constraints := formatImprovedPromptConstraints(brainCtx)
	verifyCmd := inferVerifyCommand(files)

	var b strings.Builder
	b.WriteString("[VET_IMPROVED_PROMPT]\n")
	fmt.Fprintf(&b, "score: %d/100\n", res.Score)
	fmt.Fprintf(&b, "task: %s\n", task)
	fmt.Fprintf(&b, "targets: %s\n", targets)
	fmt.Fprintf(&b, "scope: %s\n", scopeStr)
	fmt.Fprintf(&b, "constraints: %s\n", constraints)
	fmt.Fprintf(&b, "verify: %s", verifyCmd)
	return b.String()
}

func formatImprovedPromptTargets(prompt string, slice SliceResult) string {
	var spans []string
	for _, line := range strings.Split(slice.Capsule, "\n") {
		if (!strings.HasPrefix(line, "[Hop 0] ") && !strings.HasPrefix(line, "[Hop 1] ")) || strings.Contains(line, " snippet ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimSuffix(line[strings.IndexByte(line, ']')+2:], "[EXTRACTED]"))
		if rest != "" {
			spans = append(spans, rest)
			if len(spans) >= 5 {
				break
			}
		}
	}
	if len(spans) > 0 {
		return strings.Join(spans, ", ")
	}
	files := extractPromptAndSliceFiles(prompt, slice.Files)
	if len(files) > 0 {
		return strings.Join(files[:min(5, len(files))], ", ")
	}
	return "none"
}

func formatImprovedPromptConstraints(brainCtx TurnBrainContext) string {
	var parts []string
	for _, adr := range brainCtx.ADRs[:min(2, len(brainCtx.ADRs))] {
		parts = append(parts, fmt.Sprintf("%s (%s)", adr.ID, collapseOneLine(adr.Title, 60)))
	}
	for _, bug := range brainCtx.Bugs[:min(2, len(brainCtx.Bugs))] {
		parts = append(parts, fmt.Sprintf("%s (%s)", bug.ID, collapseOneLine(bug.Title, 60)))
	}
	for _, mem := range brainCtx.Memories[:min(1, len(brainCtx.Memories))] {
		parts = append(parts, fmt.Sprintf("%s (%s)", mem.ID, collapseOneLine(mem.Title, 60)))
	}
	if len(parts) == 0 {
		return "stdlib-first; preserve existing tests"
	}
	return strings.Join(parts, "; ")
}

func inferVerifyCommand(files []string) string {
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".go" {
			dir := filepath.ToSlash(filepath.Dir(f))
			if dir == "." || dir == "" {
				return "go test -race ./..."
			}
			return fmt.Sprintf("go test -race ./%s/...", strings.TrimPrefix(dir, "./"))
		}
		if ext == ".ts" || ext == ".tsx" {
			return "npm test"
		}
	}
	return "go test -race ./..."
}

func collapseOneLine(s string, maxLen int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	if maxLen > 0 && len(collapsed) > maxLen {
		return collapsed[:maxLen] + "..."
	}
	return collapsed
}
