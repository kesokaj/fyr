package search

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

func buildHealthyHookTurn(o Options, sc *scope, dir, convID, cleanPrompt string, state SessionState, dirtySummary string, fastPath bool) ([]string, []string, SliceResult) {
	var sections, banners []string
	if state.Compacted {
		if o.BrainReprime != nil {
			if rep := strings.TrimSpace(o.BrainReprime(dir, o.Home)); rep != "" {
				sections = append(sections, rep)
			}
		}
		banners = append(banners, fmt.Sprintf("♻️ compacted (gen %d): re-primed memory & capsule", state.GenerationID))
	}
	if fastPath && dirtySummary != "" {
		sections = append(sections, dirtySummary)
		banners = append(banners, "⚡ >15 dirty files: serving cached AST snapshot")
	}
	var slice SliceResult
	if ClassifyIntent(cleanPrompt, 0) != IntentApproval || state.Compacted {
		sliceOpts := o
		sliceOpts.Dir = dir
		sliceOpts.Session = convID
		slice = sc.buildVetSlice(sliceOpts, cleanPrompt, state, true)
		if slice.Capsule != "" {
			banners = append(banners, fmt.Sprintf("🧠 %d pivots", slice.Pivots))
		}
	}
	if state.EstTokens >= hookKneeTokens {
		sections = append(sections, fmt.Sprintf("[CONTEXT_KNEE_WARNING] Session context is ~%dk tokens (>= 180K knee); wrap up current step and run /fyr-handoff or /compact.", state.EstTokens/1000))
		banners = append(banners, fmt.Sprintf("⚠️ %dk tokens (>= 180K knee)", state.EstTokens/1000))
	}
	return sections, banners, slice
}

func buildHookEnvelope(o Options, sc *scope, dir, convID string, in HookInput, state SessionState, rawPrompt, cleanPrompt string, slice SliceResult, sections, banners []string) (string, string) {
	if rawPrompt == "" && len(sections) == 0 && slice.Capsule == "" && len(banners) == 0 {
		return "", ""
	}
	turnNum := state.TurnCount
	var prev *VetHistoryRow
	var recentSkills map[string]bool
	var recentPivots []string
	if sc != nil && sc.st != nil {
		prev, _ = sc.st.lastVetHistory(convID, turnNum)
		if !state.Compacted {
			recentSkills, recentPivots, _ = sc.st.recentSuggestedSkills(convID, turnNum, 3)
		}
	}
	intent := ClassifyIntent(cleanPrompt, slice.SymbolHits)
	var brainCtx TurnBrainContext
	if o.BrainTurnContext != nil {
		brainCtx = o.BrainTurnContext(dir, o.Home, cleanPrompt, slice.Files)
	}
	var catalog []SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	vetRes := vetPromptCoreWithHistory(rawPrompt, intent, slice, brainCtx, catalog, prev, recentSkills, recentPivots)
	if sc != nil && sc.st != nil {
		filterSessionDedupTurn(sc.st, convID, state.GenerationID, turnNum, &vetRes, &brainCtx)
	}
	if nudge := suggestSessionModelNudge(sc, convID, state.GenerationID, turnNum, in.ModelName, vetRes); nudge != "" {
		banners = append(banners, nudge)
	}
	sysMsg, banner := BuildTurnEnvelope(&vetRes, slice, brainCtx, sections, banners)
	if sc != nil && sc.st != nil && rawPrompt != "" {
		pivotFiles := extractPromptAndSliceFiles(cleanPrompt, slice.Files)
		_ = sc.st.recordVetHistory(convID, turnNum, rawPrompt, vetRes, pivotFiles)
	}
	writeHookProcessArtifact(o.Home, sc, dir, in, inferProcessStage(turnNum, vetRes), vetRes.MatchedRecords)
	return sysMsg, banner
}

func inferProcessStage(turnNum int, res VetResult) string {
	switch {
	case res.Gate == GateVetFirst:
		return "Vet"
	case res.Intent == IntentRelease:
		return "Verify"
	case res.Intent == IntentFeature && res.Score < 65:
		return "Plan"
	case turnNum <= 1 && res.Intent == IntentQuestion:
		return "Resume"
	default:
		return "Build"
	}
}

func writeHookProcessArtifact(home string, sc *scope, dir string, in HookInput, stage string, records []MatchedRecord) {
	if hooksDisabled(dir) {
		return
	}
	target := resolveProcessArtifactPath(in)
	if target == "" {
		return
	}
	var rows []VetHistoryRow
	if sc != nil && sc.st != nil {
		rows, _ = sc.st.listConversationVetHistory(in.ConversationID, 20)
	} else {
		root, err := checkoutRoot(dir)
		if err != nil {
			return
		}
		st, err := openStore(projectDB(root), io.Discard)
		if err != nil {
			return
		}
		defer st.close()
		rows, _ = st.listConversationVetHistory(in.ConversationID, 20)
	}
	content := RenderProcessArtifactForHome(home, in.ConversationID, stage, rows, records)
	if waveMD := readLatestWorkWaveMarkdown(home); waveMD != "" {
		content += "\n" + waveMD
	}
	_ = writeProcessArtifactAtomic(target, []byte(content))
}

type workStatusSnapshot struct {
	Stamp     string               `json:"stamp"`
	UpdatedAt string               `json:"updated_at"`
	Workers   []workWorkerSnapshot `json:"workers"`
}

type workWorkerSnapshot struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Model        string `json:"model"`
	StepCount    int    `json:"step_count"`
	Commits      int    `json:"commits"`
	BaseCheck    string `json:"base_check"`
	JudgeModel   string `json:"judge_model"`
	JudgeVerdict string `json:"judge_verdict"`
	LastStep     string `json:"last_step"`
}

func readLatestWorkWaveMarkdown(home string) string {
	snap, ok := loadLatestWorkStatus(home)
	if !ok {
		return ""
	}
	return renderWorkWaveMarkdown(snap)
}

func loadLatestWorkStatus(home string) (workStatusSnapshot, bool) {
	if strings.TrimSpace(home) == "" {
		return workStatusSnapshot{}, false
	}
	workDir := filepath.Join(home, harness.StateDir, "work")
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return workStatusSnapshot{}, false
	}
	var stamps []string
	for _, e := range entries {
		if e.IsDir() {
			if _, serr := os.Stat(filepath.Join(workDir, e.Name(), "status.json")); serr == nil {
				stamps = append(stamps, e.Name())
			}
		}
	}
	if len(stamps) == 0 {
		return workStatusSnapshot{}, false
	}
	sort.Strings(stamps)
	newest := stamps[len(stamps)-1]
	data, err := os.ReadFile(filepath.Join(workDir, newest, "status.json"))
	if err != nil {
		return workStatusSnapshot{}, false
	}
	var snap workStatusSnapshot
	if err := json.Unmarshal(data, &snap); err != nil || len(snap.Workers) == 0 {
		return workStatusSnapshot{}, false
	}
	return snap, true
}

func renderWorkWaveMarkdown(snap workStatusSnapshot) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Active / Latest `fyr work` Wave (`%s`)\n\n", Printable(snap.Stamp))
	if snap.UpdatedAt != "" {
		fmt.Fprintf(&sb, "- **Updated:** `%s`\n\n", Printable(snap.UpdatedAt))
	}
	sb.WriteString("| Worker | State | Model | Base-Check | Judge | Steps | Commits | Last Step |\n|---|---|---|---|---|---:|---:|---|\n")
	for _, w := range snap.Workers {
		bc := w.BaseCheck
		if bc == "" {
			bc = "—"
		}
		jg := w.JudgeVerdict
		if jg != "" && w.JudgeModel != "" {
			jg = fmt.Sprintf("%s (%s)", jg, w.JudgeModel)
		} else if jg == "" {
			jg = "—"
		}
		last := w.LastStep
		if last == "" {
			last = "—"
		}
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | `%s` | `%s` | %d | %d | %s |\n",
			sanitizeMDCell(w.Name), sanitizeMDCell(w.State), sanitizeMDCell(w.Model), sanitizeMDCell(bc), sanitizeMDCell(jg), w.StepCount, w.Commits, sanitizeMDCell(last))
	}
	return sb.String()
}

func sanitizeMDCell(s string) string {
	clean := Printable(s)
	r := strings.NewReplacer("|", "/", "`", "'", "\n", " ")
	return strings.TrimSpace(r.Replace(clean))
}

func resolveProcessArtifactPath(in HookInput) string {
	if p := strings.TrimSpace(in.ArtifactDirectoryPath); p != "" {
		clean := filepath.Clean(p)
		if isUnderBrainDir(clean) {
			return filepath.Join(clean, "fyr-process.md")
		}
	}
	if tp := strings.TrimSpace(in.TranscriptPath); tp != "" {
		cur := filepath.Dir(filepath.Clean(tp))
		for i := 0; i < 6 && cur != "/" && cur != "."; i++ {
			if filepath.Base(filepath.Dir(cur)) == "brain" {
				return filepath.Join(cur, "fyr-process.md")
			}
			cur = filepath.Dir(cur)
		}
	}
	return ""
}

func isUnderBrainDir(dir string) bool {
	if filepath.Base(filepath.Dir(dir)) == "brain" {
		return true
	}
	return strings.Contains(filepath.ToSlash(dir), "/brain/")
}

func writeProcessArtifactAtomic(target string, data []byte) error {
	parent := filepath.Dir(target)
	if st, err := os.Lstat(parent); err != nil {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	} else if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("artifact dir is not a regular directory")
	}
	if st, err := os.Lstat(target); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink target")
	}
	tmp := target + ".tmp"
	if st, err := os.Lstat(tmp); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink tmp")
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// RenderProcessArtifact formats the live `<appDataDir>/brain/<conv-id>/fyr-process.md`
// Markdown artifact showing the fyr workflow stages, active trail/model routing,
// and chronological turn-by-turn vet_history + [VET_IMPROVED_PROMPT] synthesis.
func RenderProcessArtifact(convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	return RenderProcessArtifactForHome("", convID, stage, rows, records)
}

// RenderProcessArtifactForHome formats fyr-process.md using cached <home>/.config/fyr/models.json
// picks when present (< 1 ms, never spawning agy) and role-family policy otherwise.
func RenderProcessArtifactForHome(home, convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	var sb strings.Builder
	sb.WriteString("# `fyr` Turn Telemetry\n\n")
	fmt.Fprintf(&sb, "- **Conversation:** `%s`\n", convID)
	fmt.Fprintf(&sb, "- **Active Stage:** **%s**\n", stage)
	sb.WriteString(formatModelRoutingLine(home))
	if len(records) > 0 {
		var ids []string
		for _, r := range records {
			ids = append(ids, "`"+r.ID+"`")
		}
		fmt.Fprintf(&sb, "- **Active Trail Records:** %s\n", strings.Join(ids, ", "))
	}
	sb.WriteString("\n## Pipeline Stage\n\n```mermaid\nflowchart LR\n")
	sb.WriteString("  S1[\"Resume\"] --> S2[\"Vet\"] --> S3[\"Plan\"] --> S4[\"Build\"] --> S5[\"Verify\"] --> S6[\"Handoff\"]\n")
	sb.WriteString("```\n\n")
	sb.WriteString("## Turn-by-Turn `fyr vet` History\n\n")
	if len(rows) == 0 {
		sb.WriteString("_No turns recorded yet._\n")
		return sb.String()
	}
	sb.WriteString("| Turn | Intent | Gate | Score | Skills | Pivots |\n|---|---|---|---|---|---|\n")
	for _, r := range rows {
		skills := strings.Join(r.SuggestedSkills, ", ")
		if skills == "" {
			skills = "—"
		}
		pivots := strings.Join(r.PivotFiles, ", ")
		if pivots == "" {
			pivots = "—"
		}
		fmt.Fprintf(&sb, "| %d | `%s` | `%s` | `%d/100` | %s | %s |\n", r.TurnNum, r.Intent, r.Gate, r.Score, skills, pivots)
	}
	for _, r := range rows {
		if strings.TrimSpace(r.ImprovedPrompt) != "" {
			fmt.Fprintf(&sb, "\n### Turn %d Synthesized `[VET_IMPROVED_PROMPT]`\n\n```text\n%s\n```\n", r.TurnNum, strings.TrimSpace(r.ImprovedPrompt))
		}
	}
	return sb.String()
}

func formatModelRoutingLine(home string) string {
	const policy = "- **Cross-Family Model Routing:** Builder → Gemini (Carbon/pro, Flash) · Judge / Verifier / Reviewer → Anthropic (Opus/pro, Sonnet/flash; auto-fallback to Gemini when Anthropic quota `< 20%` weekly or `< 30%` 5-hour)\n"
	if home == "" {
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
		Picks map[string]struct {
			Model          string `json:"model"`
			FallbackReason string `json:"fallback_reason"`
		} `json:"picks"`
	}
	if json.Unmarshal(data, &cached) != nil || len(cached.Picks) == 0 {
		return policy
	}
	bPro := sanitizeMDCell(cached.Picks["builder:pro"].Model)
	bFlash := sanitizeMDCell(cached.Picks["builder:flash"].Model)
	jPro := sanitizeMDCell(cached.Picks["judge:pro"].Model)
	jFlash := sanitizeMDCell(cached.Picks["judge:flash"].Model)
	if bPro == "" || jPro == "" {
		return policy
	}
	bPart := "`" + bPro + "`"
	if bFlash != "" && bFlash != bPro {
		bPart += " / `" + bFlash + "`"
	}
	jPart := "`" + jPro + "`"
	if jFlash != "" && jFlash != jPro {
		jPart += " / `" + jFlash + "`"
	}
	suffix := "auto-fallback to Gemini when Anthropic quota `< 20%` weekly or `< 30%` 5-hour"
	if fb := sanitizeMDCell(cached.Picks["judge:pro"].FallbackReason); fb != "" {
		suffix = fb
	}
	return fmt.Sprintf("- **Cross-Family Model Routing:** Builder %s · Judge / Verifier / Reviewer %s (%s)\n", bPart, jPart, suffix)
}
