package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

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
