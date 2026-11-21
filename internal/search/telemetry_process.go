package search

import (
	"fmt"
	"sort"
	"strings"
)

// ProcessTelemetry holds structured turn, routing, and work-wave telemetry
// rendered into Surface 3 (fyr-process.md) as clean Markdown tables.
type ProcessTelemetry struct {
	ConversationID string
	Stage          string
	ActiveRecords  []string
	ModelRouting   ProcessModelRouting
	Turns          []ProcessTurnTelemetry
	LatestWorkWave *ProcessWorkWave
}

// ProcessModelRouting describes cached role picks or fallback policy in ProcessTelemetry.
type ProcessModelRouting struct {
	Policy         string
	BuilderPro     string
	BuilderFlash   string
	JudgePro       string
	JudgeFlash     string
	FallbackReason string
	UpdateTime     string
}

// ProcessTurnTelemetry records one turn's vet state inside ProcessTelemetry.
type ProcessTurnTelemetry struct {
	TurnNum       int
	Intent        string
	Gate          string
	Score         int
	DurationMS    int
	TimedOut      bool
	InjectedBytes int
	Skills        []string
	Pivots        []string
}

// ProcessWorkWave describes the newest active/completed fyr work wave.
type ProcessWorkWave struct {
	Stamp      string
	UpdateTime string
	Workers    []ProcessWorkWorker
}

// ProcessWorkWorker describes one worker status row inside ProcessWorkWave.
type ProcessWorkWorker struct {
	Name         string
	State        string
	Model        string
	BaseCheck    string
	JudgeVerdict string
	JudgeModel   string
	StepCount    int
	Commits      int
	LastStep     string
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

func buildProcessModelRoutingFromCatalog(cat modelsCatalogFile, hasCat bool) ProcessModelRouting {
	const defaultPolicy = "Builder -> Gemini (pro, flash); Judge / Verifier / Reviewer -> Anthropic (Opus/pro, Sonnet/flash; fallback < 20% weekly or < 30% 5h -> Gemini)"
	policy := ProcessModelRouting{Policy: defaultPolicy}
	if !hasCat || len(cat.Picks) == 0 {
		return policy
	}
	routing := ProcessModelRouting{
		BuilderPro:     printable(cat.Picks["builder:pro"].Model),
		BuilderFlash:   printable(cat.Picks["builder:flash"].Model),
		JudgePro:       printable(cat.Picks["judge:pro"].Model),
		JudgeFlash:     printable(cat.Picks["judge:flash"].Model),
		UpdateTime:     printable(cat.UpdatedAt),
		FallbackReason: firstModelFallbackReason(cat.Picks),
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
			TurnNum:       r.TurnNum,
			Intent:        normalizeUpperSnakeEnum(string(r.Intent)),
			Gate:          normalizeUpperSnakeEnum(string(r.Gate)),
			Score:         r.Score,
			DurationMS:    r.DurationMS,
			TimedOut:      r.TimedOut,
			InjectedBytes: r.InjectedBytes,
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
		prompt := printableMultiline(r.ImprovedPrompt)
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
	cat, hasCat := loadModelsCatalog(home)
	return renderProcessTelemetryMarkdownWithCatalog(home, cat, hasCat, convID, stage, rows, records)
}

func renderProcessTelemetryMarkdownWithCatalog(home string, cat modelsCatalogFile, hasCat bool, convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	id := strings.TrimSpace(printable(convID))
	if id == "" {
		id = "default"
	}
	payload := ProcessTelemetry{
		ConversationID: id,
		Stage:          normalizeProcessStage(stage),
		ModelRouting:   buildProcessModelRoutingFromCatalog(cat, hasCat),
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
	fmt.Fprintf(&sb, "- **Conversation:** %s\n", mdCodeSpan(payload.ConversationID))
	fmt.Fprintf(&sb, "- **Stage:** %s\n", mdCodeSpan(payload.Stage))
	if len(payload.ActiveRecords) > 0 {
		fmt.Fprintf(&sb, "- **Active Records:** %s\n", mdCodeList(payload.ActiveRecords))
	}
	appendProcessModelRouting(&sb, payload.ModelRouting)
	appendProcessWorkWaveSection(&sb, payload.LatestWorkWave)
	appendProcessTurnsSection(&sb, payload.Turns)
	appendProcessImprovedPrompts(&sb, rows)
	return sb.String()
}

func appendProcessModelRouting(sb *strings.Builder, mr ProcessModelRouting) {
	sb.WriteString("\n## Model Routing\n\n")
	if mr.Policy != "" {
		fmt.Fprintf(sb, "- **Policy:** %s\n", collapseOneLine(printable(mr.Policy), 320))
		return
	}
	sb.WriteString("| Role | Model |\n|---|---|\n")
	for _, row := range [][2]string{
		{"Builder (Pro)", mr.BuilderPro},
		{"Builder (Flash)", mr.BuilderFlash},
		{"Judge (Pro)", mr.JudgePro},
		{"Judge (Flash)", mr.JudgeFlash},
	} {
		if row[1] != "" {
			fmt.Fprintf(sb, "| %s | %s |\n", row[0], mdCodeCell(row[1]))
		}
	}
	if mr.UpdateTime != "" {
		fmt.Fprintf(sb, "\n- **Updated:** %s\n", mdCodeSpan(mr.UpdateTime))
	}
	if mr.FallbackReason != "" {
		fmt.Fprintf(sb, "- **Fallback:** %s\n", collapseOneLine(printable(mr.FallbackReason), 220))
	}
}

func appendProcessWorkWaveSection(sb *strings.Builder, wave *ProcessWorkWave) {
	if wave == nil {
		return
	}
	sb.WriteString("\n## Latest Work Wave\n\n")
	fmt.Fprintf(sb, "- **Stamp:** %s", mdCodeSpan(wave.Stamp))
	if wave.UpdateTime != "" {
		fmt.Fprintf(sb, " · **Updated:** %s", mdCodeSpan(wave.UpdateTime))
	}
	sb.WriteByte('\n')
	if len(wave.Workers) == 0 {
		return
	}
	sb.WriteString("\n| Worker | State | Model | Base | Judge | Steps | Commits | Last Step |\n|---|---|---|---|---|---:|---:|---|\n")
	for _, w := range wave.Workers {
		judge := w.JudgeVerdict
		if w.JudgeModel != "" {
			judge += " (" + w.JudgeModel + ")"
		}
		fmt.Fprintf(sb, "| %s | %s | %s | %s | %s | %d | %d | %s |\n",
			mdTableCell(w.Name), mdTableCell(w.State), mdCodeCell(w.Model),
			mdTableCell(w.BaseCheck), mdTableCell(judge), w.StepCount, w.Commits, mdTableCell(w.LastStep))
	}
}

func appendProcessTurnsSection(sb *strings.Builder, turns []ProcessTurnTelemetry) {
	if len(turns) == 0 {
		return
	}
	sb.WriteString("\n## Turns\n\n| Turn | Intent | Gate | Score | Duration | Injected | Skills | Pivots |\n|---:|---|---|---:|---:|---:|---|---|\n")
	for _, t := range turns {
		dur := fmt.Sprintf("%d ms", t.DurationMS)
		if t.TimedOut {
			dur += " (timeout)"
		}
		fmt.Fprintf(sb, "| %d | %s | %s | %d/100 | %s | %d B | %s | %s |\n",
			t.TurnNum, mdTableCell(t.Intent), mdTableCell(t.Gate), t.Score,
			dur, t.InjectedBytes, mdTableCodeList(t.Skills), mdTableCodeList(t.Pivots))
	}
}

func mdCodeSpan(s string) string {
	clean := collapseOneLine(printable(s), 220)
	if clean == "" {
		return ""
	}
	maxRun, cur := 0, 0
	for i := 0; i < len(clean); i++ {
		if clean[i] == '`' {
			cur++
			if cur > maxRun {
				maxRun = cur
			}
		} else {
			cur = 0
		}
	}
	fence := strings.Repeat("`", maxRun+1)
	if strings.HasPrefix(clean, "`") || strings.HasSuffix(clean, "`") {
		return fence + " " + clean + " " + fence
	}
	return fence + clean + fence
}

var tablePipeReplacer = strings.NewReplacer(`\|`, `\\\|`, "|", `\|`)

func mdTableCell(s string) string {
	clean := collapseOneLine(printable(s), 220)
	if clean == "" {
		return "-"
	}
	return tablePipeReplacer.Replace(clean)
}

func mdCodeCell(s string) string {
	clean := collapseOneLine(printable(s), 220)
	if clean == "" {
		return "-"
	}
	return tablePipeReplacer.Replace(mdCodeSpan(clean))
}

func mdCodeList(items []string) string {
	var parts []string
	for _, item := range items {
		if span := mdCodeSpan(item); span != "" {
			parts = append(parts, span)
		}
	}
	return strings.Join(parts, ", ")
}

func mdTableCodeList(items []string) string {
	var parts []string
	for _, item := range items {
		if cell := mdCodeCell(item); cell != "-" {
			parts = append(parts, cell)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
