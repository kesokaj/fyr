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

func populateVetDeltas(res *VetResult, rawPrompt, cleanPrompt string, slice SliceResult, files []string, prev *VetHistoryRow) {
	if res == nil {
		return
	}
	res.Bypassed = strings.HasPrefix(rawPrompt, "!")
	if (!res.Bypassed && (res.Intent == IntentApproval || res.Intent == IntentTrivial)) || res.Intent == IntentCorrection {
		return
	}
	if prev != nil && shouldCarryForwardVet(cleanPrompt, res.Intent, slice, prev) && prev.RawPrompt != "" && prev.RawPrompt != rawPrompt {
		res.PriorTurnContext = fmt.Sprintf("turn %d: %s", prev.TurnNum, collapseOneLine(prev.RawPrompt, 180))
	}
	if len(files) > 0 {
		res.TargetFiles = append(res.TargetFiles, files[:min(6, len(files))]...)
	}
	if res.Intent != IntentQuestion && res.Gate != GateVetFirst && stringInList(res.MissingAnchors, "verification") {
		res.SuggestedVerify = inferConcreteVerifyCommand(files)
	}
}

func stringInList(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

// SynthesizeImprovedPrompt generates a compact delta-only [VET_IMPROVED_PROMPT]
// record for vet_history and fyr-process.md when non-redundant deltas exist
// and 45 <= Score < 80, follow-up to VET_FIRST applies, or conflicts exist.
func SynthesizeImprovedPrompt(_ string, res VetResult, slice SliceResult, brainCtx TurnBrainContext, prev *VetHistoryRow) string {
	if res.Score < 45 || res.Gate != GateEnrichAndProceed || res.Intent == IntentQuestion || res.Intent == IntentCorrection {
		return ""
	}
	if res.Score >= 80 && (prev == nil || prev.Gate != GateVetFirst) && len(brainCtx.Conflicts) == 0 {
		return ""
	}
	var lines []string
	if prior := strings.TrimSpace(res.PriorTurnContext); prior != "" {
		lines = append(lines, "prior: "+prior)
	}
	if len(res.TargetFiles) > 0 {
		lines = append(lines, "targets: "+strings.Join(res.TargetFiles, ", "))
	} else if spans := formatImprovedPromptTargetSpans(slice); len(spans) > 0 {
		lines = append(lines, "targets: "+strings.Join(spans, ", "))
	}
	if verify := strings.TrimSpace(res.SuggestedVerify); verify != "" {
		lines = append(lines, "verify: "+verify)
	}
	if c := formatImprovedPromptConstraints(brainCtx); c != "" {
		lines = append(lines, "constraints: "+c)
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("[VET_IMPROVED_PROMPT]\nscore: %d/100\n%s", res.Score, strings.Join(lines, "\n"))
}

func formatImprovedPromptTargetSpans(slice SliceResult) []string {
	var spans []string
	for _, line := range strings.Split(slice.Capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") && (!strings.HasPrefix(line, "[Hop 1] ") || strings.Contains(line, " snippet ")) {
			continue
		}
		rest := line[strings.IndexByte(line, ']')+2:]
		if cut := strings.Index(rest, " snippet — "); cut >= 0 {
			rest = rest[:cut]
		}
		rest = collapseOneLine(strings.TrimSpace(strings.TrimSuffix(rest, "[EXTRACTED]")), 120)
		if rest != "" {
			spans = append(spans, rest)
			if len(spans) >= 5 {
				break
			}
		}
	}
	return spans
}

func formatImprovedPromptConstraints(brainCtx TurnBrainContext) string {
	var parts []string
	for _, adr := range brainCtx.ADRs[:min(2, len(brainCtx.ADRs))] {
		parts = append(parts, fmt.Sprintf("%s (%s)", adr.ID, collapseOneLine(adr.Title, 60)))
	}
	for _, bug := range brainCtx.Bugs[:min(2, len(brainCtx.Bugs))] {
		parts = append(parts, fmt.Sprintf("%s (%s)", bug.ID, collapseOneLine(bug.Title, 60)))
	}
	for _, c := range brainCtx.Conflicts[:min(2, len(brainCtx.Conflicts))] {
		parts = append(parts, collapseOneLine(c, 80))
	}
	return strings.Join(parts, "; ")
}

func inferConcreteVerifyCommand(files []string) string {
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".go" {
			dir := filepath.ToSlash(filepath.Dir(f))
			if dir == "." || dir == "" {
				return "go test -race ./..."
			}
			return fmt.Sprintf("go test -race ./%s/...", strings.TrimPrefix(dir, "./"))
		}
		if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			return "npm test"
		}
	}
	return ""
}

func collapseOneLine(s string, maxLen int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	if maxLen > 0 && len(collapsed) > maxLen {
		return collapsed[:maxLen] + "..."
	}
	return collapsed
}
