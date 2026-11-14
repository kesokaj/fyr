package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
)

// PreInvocationBanner renders Surface 1 (PreInvocation hook banner) in
// Hybrid Example A: 2-space pretty-printed lowerCamelCase JSON with
// UPPER_SNAKE_CASE enums and omitempty on optional/empty fields.
type PreInvocationBanner struct {
	Surface                  string   `json:"surface"`
	Intent                   string   `json:"intent,omitempty"`
	Gate                     string   `json:"gate,omitempty"`
	Score                    *int     `json:"score,omitempty"`
	Skills                   []string `json:"skills,omitempty"`
	SecuritySubagentRequired bool     `json:"securitySubagentRequired,omitempty"`
	MatchedRecordCount       int      `json:"matchedRecordCount,omitempty"`
	PivotCount               int      `json:"pivotCount,omitempty"`
	CompactedGeneration      int      `json:"compactedGeneration,omitempty"`
	DirtyFastPath            bool     `json:"dirtyFastPath,omitempty"`
	TokenKneeWarning         string   `json:"tokenKneeWarning,omitempty"`
	Conflict                 string   `json:"conflict,omitempty"`
	Tip                      string   `json:"tip,omitempty"`
	ModelTip                 string   `json:"modelTip,omitempty"`
	StoreError               string   `json:"storeError,omitempty"`
	Notices                  []string `json:"notices,omitempty"`
}

// LedgerNotice renders Surface 2 (PreToolUse [UNCHANGED] / [MODIFIED] notices)
// as a fixed sentinel line followed by 2-space pretty-printed lowerCamelCase JSON.
type LedgerNotice struct {
	Status         string   `json:"status"`
	Path           string   `json:"path"`
	StartLine      int      `json:"startLine,omitempty"`
	EndLine        int      `json:"endLine,omitempty"`
	Sha256         string   `json:"sha256,omitempty"`
	PreviousSha256 string   `json:"previousSha256,omitempty"`
	CurrentSha256  string   `json:"currentSha256,omitempty"`
	HitCount       int      `json:"hitCount,omitempty"`
	StampConsumed  bool     `json:"stampConsumed,omitempty"`
	Message        string   `json:"message"`
	Skeleton       []string `json:"skeleton,omitempty"`
}

// ProcessTelemetry renders Surface 3 (fyr-process.md telemetry artifact) as a
// clean Markdown heading + 2-space pretty-printed lowerCamelCase JSON payload.
type ProcessTelemetry struct {
	ConversationID string                 `json:"conversationId"`
	Stage          string                 `json:"stage"`
	ActiveRecords  []string               `json:"activeRecords,omitempty"`
	ModelRouting   ProcessModelRouting    `json:"modelRouting"`
	Turns          []ProcessTurnTelemetry `json:"turns,omitempty"`
	LatestWorkWave *ProcessWorkWave       `json:"latestWorkWave,omitempty"`
}

// ProcessModelRouting describes cached role picks or fallback policy in ProcessTelemetry.
type ProcessModelRouting struct {
	Policy         string `json:"policy,omitempty"`
	BuilderPro     string `json:"builderPro,omitempty"`
	BuilderFlash   string `json:"builderFlash,omitempty"`
	JudgePro       string `json:"judgePro,omitempty"`
	JudgeFlash     string `json:"judgeFlash,omitempty"`
	FallbackReason string `json:"fallbackReason,omitempty"`
	UpdateTime     string `json:"updateTime,omitempty"`
}

// ProcessTurnTelemetry records one turn's vet state inside ProcessTelemetry.
type ProcessTurnTelemetry struct {
	TurnNum int      `json:"turnNum"`
	Intent  string   `json:"intent"`
	Gate    string   `json:"gate"`
	Score   int      `json:"score"`
	Skills  []string `json:"skills,omitempty"`
	Pivots  []string `json:"pivots,omitempty"`
}

// ProcessWorkWave describes the newest active/completed fyr work wave.
type ProcessWorkWave struct {
	Stamp      string              `json:"stamp"`
	UpdateTime string              `json:"updateTime,omitempty"`
	Workers    []ProcessWorkWorker `json:"workers,omitempty"`
}

// ProcessWorkWorker describes one worker status row inside ProcessWorkWave.
type ProcessWorkWorker struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Model        string `json:"model,omitempty"`
	BaseCheck    string `json:"baseCheck,omitempty"`
	JudgeVerdict string `json:"judgeVerdict,omitempty"`
	JudgeModel   string `json:"judgeModel,omitempty"`
	StepCount    int    `json:"stepCount,omitempty"`
	Commits      int    `json:"commits,omitempty"`
	LastStep     string `json:"lastStep,omitempty"`
}

func safeBacktickFenceWithLang(text, lang string) string {
	body := strings.TrimRight(text, "\n")
	fence := safeBacktickFence(body)
	return fence + lang + "\n" + body + "\n" + fence
}

func marshalPrettyJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimRight(buf.String(), "\n")
}

func formatPreInvocationBanner(res VetResult, extraBanners []string) string {
	score := res.Score
	b := PreInvocationBanner{
		Surface:                  "PRE_INVOCATION",
		Intent:                   normalizeUpperSnakeEnum(string(res.Intent)),
		Gate:                     normalizeUpperSnakeEnum(string(res.Gate)),
		Score:                    &score,
		SecuritySubagentRequired: res.SecurityRequired,
		MatchedRecordCount:       len(res.MatchedRecords),
	}
	for _, s := range res.Skills {
		if name := strings.TrimSpace(printable(s.Name)); name != "" {
			b.Skills = append(b.Skills, name)
		}
	}
	if res.Gate == GateVetFirst {
		if len(res.Conflicts) > 0 {
			b.Conflict = printable(res.Conflicts[0])
		} else if len(res.MissingAnchors) > 0 {
			b.Tip = fmt.Sprintf("missing anchors (%s) — prefix `!` to bypass vetting", printable(strings.Join(res.MissingAnchors, ", ")))
		}
	}
	for _, extra := range extraBanners {
		applyExtraBanner(&b, extra)
	}
	return safeBacktickFenceWithLang(marshalPrettyJSON(b), "json")
}

func applyExtraBanner(b *PreInvocationBanner, raw string) {
	clean := strings.TrimSpace(printable(raw))
	if clean == "" {
		return
	}
	var gen int
	switch {
	case strings.HasPrefix(clean, "compacted (gen "):
		if _, err := fmt.Sscanf(clean, "compacted (gen %d):", &gen); err == nil && gen > 0 {
			b.CompactedGeneration = gen
			return
		}
	case strings.Contains(clean, ">15 dirty files: serving cached AST snapshot"):
		b.DirtyFastPath = true
		return
	case strings.HasSuffix(clean, " pivots"):
		trimmed := strings.TrimSpace(strings.TrimSuffix(clean, " pivots"))
		if n, err := strconv.Atoi(trimmed); err == nil && n > 0 {
			b.PivotCount = n
			return
		}
	case strings.Contains(clean, "tokens (>= 180K knee)"):
		b.TokenKneeWarning = clean
		return
	case strings.HasPrefix(clean, "model tip: "):
		b.ModelTip = strings.TrimSpace(strings.TrimPrefix(clean, "model tip: "))
		return
	case strings.HasPrefix(clean, "store unavailable ("):
		b.StoreError = clean
		return
	}
	b.Notices = append(b.Notices, clean)
}

func storeDegradedNotice(err error) string {
	return fmt.Sprintf("store unavailable (%s) — capsule/memory skipped; run /fyr-index-project or fyr index", oneLineErr(err))
}

func formatStoreDegradedBanner(err error) string {
	b := PreInvocationBanner{
		Surface:    "PRE_INVOCATION",
		Gate:       "STORE_DEGRADED",
		StoreError: storeDegradedNotice(err),
	}
	return safeBacktickFenceWithLang(marshalPrettyJSON(b), "json")
}

func splitSkeletonLines(skel string) []string {
	var out []string
	for _, line := range strings.Split(skel, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			out = append(out, printable(line))
		}
	}
	return out
}

func formatUnchangedLedgerNotice(relPath string, start, end int, sumHex string, hitCount int, skel string) string {
	n := LedgerNotice{
		Status:        "UNCHANGED",
		Path:          printable(relPath),
		StartLine:     start,
		EndLine:       end,
		Sha256:        sumHex[:min(12, len(sumHex))],
		HitCount:      hitCount,
		StampConsumed: true,
		Message:       "already in context; stamp consumed (retry to force re-read)",
		Skeleton:      splitSkeletonLines(skel),
	}
	return "[UNCHANGED]\n" + marshalPrettyJSON(n)
}

func formatModifiedLedgerNotice(relPath, prevSHA, curSHA, skel string) string {
	n := LedgerNotice{
		Status:         "MODIFIED",
		Path:           printable(relPath),
		PreviousSha256: prevSHA[:min(12, len(prevSHA))],
		CurrentSha256:  curSHA[:min(12, len(curSHA))],
		Message:        "changed on disk since last read; re-read before editing",
		Skeleton:       splitSkeletonLines(skel),
	}
	return "[MODIFIED]\n" + marshalPrettyJSON(n)
}

func normalizeProcessStage(stage string) string {
	clean := strings.TrimSpace(printable(stage))
	if len(clean) >= 3 && clean[0] >= '0' && clean[0] <= '9' && clean[1] == '.' && clean[2] == ' ' {
		clean = strings.TrimSpace(clean[3:])
	}
	enum := normalizeUpperSnakeEnum(clean)
	if enum == "" {
		return "BUILD"
	}
	return enum
}

func normalizeUpperSnakeEnum(s string) string {
	clean := strings.TrimSpace(printable(s))
	if clean == "" {
		return ""
	}
	var b strings.Builder
	prevUnderscore := true
	for _, r := range clean {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
			prevUnderscore = false
		case !prevUnderscore:
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.TrimRight(b.String(), "_")
}

type modelRoutingPick struct {
	Model          string `json:"model"`
	FallbackReason string `json:"fallback_reason"`
}

func firstModelFallbackReason(picks map[string]modelRoutingPick) string {
	if fb := strings.TrimSpace(printable(picks["judge:pro"].FallbackReason)); fb != "" {
		return fb
	}
	keys := make([]string, 0, len(picks))
	for k := range picks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if fb := strings.TrimSpace(printable(picks[k].FallbackReason)); fb != "" {
			return fb
		}
	}
	return ""
}

func buildProcessModelRouting(home string) ProcessModelRouting {
	const defaultPolicy = "Builder -> Gemini (Carbon/pro, Flash); Judge / Verifier / Reviewer -> Anthropic (Opus/pro, Sonnet/flash; fallback < 20% weekly or < 30% 5h -> Gemini)"
	policy := ProcessModelRouting{Policy: defaultPolicy}
	if strings.TrimSpace(home) == "" {
		return policy
	}
	p := filepath.Join(home, harness.StateDir, "models.json")
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > 1<<20 {
		return policy
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return policy
	}
	var cached struct {
		UpdatedAt string                      `json:"updated_at"`
		Picks     map[string]modelRoutingPick `json:"picks"`
	}
	if json.Unmarshal(data, &cached) != nil || len(cached.Picks) == 0 {
		return policy
	}
	routing := ProcessModelRouting{
		BuilderPro:     printable(cached.Picks["builder:pro"].Model),
		BuilderFlash:   printable(cached.Picks["builder:flash"].Model),
		JudgePro:       printable(cached.Picks["judge:pro"].Model),
		JudgeFlash:     printable(cached.Picks["judge:flash"].Model),
		UpdateTime:     printable(cached.UpdatedAt),
		FallbackReason: firstModelFallbackReason(cached.Picks),
	}
	if routing.BuilderPro == "" && routing.JudgePro == "" && routing.BuilderFlash == "" && routing.JudgeFlash == "" {
		return policy
	}
	return routing
}

func buildProcessWorkWave(home string) *ProcessWorkWave {
	snap, ok := loadLatestWorkStatus(home)
	if !ok {
		return nil
	}
	wave := &ProcessWorkWave{
		Stamp:      printable(snap.Stamp),
		UpdateTime: printable(snap.UpdatedAt),
		Workers:    make([]ProcessWorkWorker, 0, len(snap.Workers)),
	}
	for _, w := range snap.Workers {
		wave.Workers = append(wave.Workers, ProcessWorkWorker{
			Name:         printable(w.Name),
			State:        normalizeUpperSnakeEnum(w.State),
			Model:        printable(w.Model),
			BaseCheck:    normalizeUpperSnakeEnum(w.BaseCheck),
			JudgeVerdict: normalizeUpperSnakeEnum(w.JudgeVerdict),
			JudgeModel:   printable(w.JudgeModel),
			StepCount:    w.StepCount,
			Commits:      w.Commits,
			LastStep:     printable(w.LastStep),
		})
	}
	return wave
}

func buildProcessTurns(rows []VetHistoryRow) []ProcessTurnTelemetry {
	var turns []ProcessTurnTelemetry
	for _, r := range rows {
		turn := ProcessTurnTelemetry{
			TurnNum: r.TurnNum,
			Intent:  normalizeUpperSnakeEnum(string(r.Intent)),
			Gate:    normalizeUpperSnakeEnum(string(r.Gate)),
			Score:   r.Score,
		}
		for _, s := range r.SuggestedSkills {
			if name := strings.TrimSpace(printable(s)); name != "" {
				turn.Skills = append(turn.Skills, name)
			}
		}
		for _, p := range r.PivotFiles {
			if pv := strings.TrimSpace(printable(p)); pv != "" {
				turn.Pivots = append(turn.Pivots, pv)
			}
		}
		turns = append(turns, turn)
	}
	return turns
}

func appendProcessImprovedPrompts(sb *strings.Builder, rows []VetHistoryRow) {
	wrotePromptHeader := false
	for _, r := range rows {
		prompt := strings.TrimSpace(r.ImprovedPrompt)
		if prompt == "" {
			continue
		}
		if !wrotePromptHeader {
			sb.WriteString("\n## Synthesized Improved Prompts\n")
			wrotePromptHeader = true
		}
		fmt.Fprintf(sb, "\n### Turn %d\n\n%s\n", r.TurnNum, safeBacktickFenceWithLang(prompt, "text"))
	}
}

func renderProcessTelemetryMarkdown(home, convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	id := strings.TrimSpace(printable(convID))
	if id == "" {
		id = "default"
	}
	payload := ProcessTelemetry{
		ConversationID: id,
		Stage:          normalizeProcessStage(stage),
		ModelRouting:   buildProcessModelRouting(home),
		LatestWorkWave: buildProcessWorkWave(home),
		Turns:          buildProcessTurns(rows),
	}
	for _, r := range records {
		if recID := strings.TrimSpace(printable(r.ID)); recID != "" {
			payload.ActiveRecords = append(payload.ActiveRecords, recID)
		}
	}

	var sb strings.Builder
	sb.WriteString("# fyr Turn Telemetry\n\n")
	sb.WriteString(safeBacktickFenceWithLang(marshalPrettyJSON(payload), "json"))
	sb.WriteByte('\n')
	appendProcessImprovedPrompts(&sb, rows)
	return sb.String()
}
