package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
)

func buildHealthyHookTurn(o Options, sc *scope, dir, convID, sessionModel, rawPrompt, cleanPrompt string, state SessionState, dirtySummary string, fastPath bool) ([]string, TurnBannerMeta, SliceResult) {
	var sections []string
	var meta TurnBannerMeta
	if state.Compacted {
		if o.BrainReprime != nil {
			if rep := strings.TrimSpace(o.BrainReprime(dir, o.Home)); rep != "" {
				sections = append(sections, rep)
			}
		}
		meta.CompactedGeneration = max(1, state.GenerationID)
	}
	if fastPath && dirtySummary != "" {
		sections = append(sections, dirtySummary)
		meta.DirtyFastPath = true
	}
	var slice SliceResult
	bypassed := strings.HasPrefix(strings.TrimSpace(rawPrompt), "!")
	initIntent := ClassifyIntent(cleanPrompt, 1)
	skipHeavy := !state.Compacted && !bypassed && (initIntent == IntentApproval || initIntent == IntentTrivial)
	if !skipHeavy {
		sliceOpts := o
		sliceOpts.Dir = dir
		sliceOpts.Session = convID
		slice = sc.buildVetSlice(sliceOpts, cleanPrompt, state, true)
		if slice.Capsule != "" {
			meta.PivotCount = slice.Pivots
		}
	}
	if warnFull, warnShort := dedupContextKneeWarning(o.Home, sc, convID, sessionModel, state); warnFull != "" {
		sections = append(sections, warnFull)
		meta.ContextKneeWarning = warnShort
	}
	return sections, meta, slice
}

func dedupContextKneeWarning(home string, sc *scope, convID, sessionModel string, state SessionState) (string, string) {
	knee := models.ContextKneeTokensForHome(home, sessionModel)
	if knee <= 0 || int64(state.EstTokens) < knee {
		return "", ""
	}
	step := max(knee/2, int64(100_000))
	bucket := (int64(state.EstTokens) - knee) / step
	var st *store
	if sc != nil {
		st = sc.st
	}
	if !markSessionSentinelOnce(st, convID, state.GenerationID, state.TurnCount, fmt.Sprintf("__knee_warn__:%d:%d", knee, bucket)) {
		return "", ""
	}
	kneeLabel := fmt.Sprintf("%d tokens", knee)
	if knee >= 1000 {
		kneeLabel = fmt.Sprintf("%dK", (knee+500)/1000)
	}
	full := fmt.Sprintf("[CONTEXT_KNEE_WARNING] Session context is ~%dk tokens (>= %s 60%% model knee) — %s (or /compact).", state.EstTokens/1000, kneeLabel, contextKneeHandoffCue)
	short := fmt.Sprintf("%dk tokens (>= %s 60%% model knee) — %s", state.EstTokens/1000, kneeLabel, contextKneeHandoffCue)
	return full, short
}

func buildHookEnvelopeWithModelTimed(ctx context.Context, o Options, cat modelsCatalogFile, hasCat bool, sc *scope, dir, convID string, modelInfo resolvedModelInfo, in HookInput, state SessionState, rawPrompt, cleanPrompt string, slice SliceResult, sections []string, meta TurnBannerMeta, start time.Time) (string, string) {
	if (ctx != nil && ctx.Err() != nil) || (rawPrompt == "" && len(sections) == 0 && slice.Capsule == "" && !meta.hasSignals()) {
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
	bypassed := strings.HasPrefix(strings.TrimSpace(rawPrompt), "!")
	skipBrainAndSkills := !state.Compacted && !bypassed && (intent == IntentApproval || intent == IntentTrivial)
	var brainCtx TurnBrainContext
	if !skipBrainAndSkills && hasBrainTurnContext(o) {
		bctx := ctx
		if bctx == nil {
			bctx = context.Background()
		}
		brainCtx = invokeBrainTurnContext(bctx, o, dir, cleanPrompt, slice.Files)
	}
	var catalog []SkillMeta
	if !skipBrainAndSkills && o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	vetRes := vetPromptCoreWithHistory(rawPrompt, intent, slice, brainCtx, catalog, prev, recentSkills, recentPivots)
	vetRes.SessionModel, vetRes.SessionModelDisplay, vetRes.SessionThinking = modelInfo.ID, modelInfo.Display, modelInfo.Thinking
	if sc != nil && sc.st != nil {
		filterSessionDedupTurn(sc.st, convID, state.GenerationID, turnNum, &vetRes, &brainCtx)
	}
	if nudge := suggestSessionModelNudgeForHome(o.Home, sc, convID, state.GenerationID, turnNum, modelInfo.ID, vetRes); nudge != "" {
		meta.ModelTip = strings.TrimSpace(strings.TrimPrefix(nudge, "model tip: "))
	}
	meta.ExtraSections = sections
	meta.SessionModelResolved = true
	return finalizeHookEnvelope(ctx, o.Home, cat, hasCat, sc, dir, convID, turnNum, rawPrompt, cleanPrompt, in, state, vetRes, slice, brainCtx, meta, sections, start)
}

func finalizeHookEnvelope(ctx context.Context, home string, cat modelsCatalogFile, hasCat bool, sc *scope, dir, convID string, turnNum int, rawPrompt, cleanPrompt string, in HookInput, state SessionState, vetRes VetResult, slice SliceResult, brainCtx TurnBrainContext, meta TurnBannerMeta, sections []string, start time.Time) (string, string) {
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	sysMsg, banner := BuildTurnEnvelopeWithMeta(&vetRes, slice, brainCtx, meta)
	if vetRes.Gate == GatePassThrough && !vetRes.Bypassed && !state.Compacted && len(sections) == 0 && len(vetRes.Conflicts) == 0 {
		sysMsg, banner = "", ""
	}
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	if sc != nil && sc.st != nil && rawPrompt != "" {
		durMS := 0
		if !start.IsZero() {
			durMS = int(time.Since(start).Milliseconds())
		}
		pivotFiles := extractPromptAndSliceFiles(cleanPrompt, slice.Files)
		wctx := ctx
		if wctx == nil {
			wctx = context.Background()
		}
		_ = sc.st.recordVetHistoryWithMetricsCtx(wctx, convID, turnNum, StripSandboxPreamble(strings.TrimSpace(rawPrompt)), vetRes, pivotFiles, vetTurnMetrics{
			durationMS:    durMS,
			injectedBytes: len(sysMsg),
		})
	}
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	writeHookProcessArtifactWithCatalog(home, cat, hasCat, sc, dir, in, inferProcessStage(turnNum, vetRes), vetRes.MatchedRecords)
	return sysMsg, banner
}

func inferProcessStage(turnNum int, res VetResult) string {
	switch {
	case res.Gate == GateVetFirst:
		return "VET"
	case res.Intent == IntentRelease:
		return "VERIFY"
	case res.Intent == IntentFeature && res.Score < 65:
		return "PLAN"
	case turnNum <= 1 && res.Intent == IntentQuestion:
		return "RESUME"
	default:
		return "BUILD"
	}
}

func writeHookProcessArtifact(home string, sc *scope, dir string, in HookInput, stage string, records []MatchedRecord) {
	cat, hasCat := loadModelsCatalog(home)
	writeHookProcessArtifactWithCatalog(home, cat, hasCat, sc, dir, in, stage, records)
}

func writeHookProcessArtifactWithCatalog(home string, cat modelsCatalogFile, hasCat bool, sc *scope, dir string, in HookInput, stage string, records []MatchedRecord) {
	if strings.TrimSpace(home) == "" || hooksDisabledForHome(home, dir) {
		return
	}
	target := resolveProcessArtifactPathForHome(home, in)
	if target == "" {
		return
	}
	var rows []VetHistoryRow
	if sc != nil && sc.st != nil {
		rows, _ = sc.st.listConversationVetHistory(in.ConversationID, 20)
	} else {
		root, _, ok := findGitRootFast(dir)
		if !ok {
			return
		}
		st, err := openStore(projectDB(root), io.Discard)
		if err != nil {
			return
		}
		defer st.close()
		rows, _ = st.listConversationVetHistory(in.ConversationID, 20)
	}
	content := renderProcessTelemetryMarkdownWithCatalog(home, cat, hasCat, in.ConversationID, stage, rows, records)
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
			p := filepath.Join(workDir, e.Name(), "status.json")
			if st, serr := os.Lstat(p); serr == nil && st.Mode().IsRegular() && st.Size() > 0 && st.Size() <= 4<<20 {
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

func resolveProcessArtifactPathForHome(home string, in HookInput) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	if p := strings.TrimSpace(in.ArtifactDirectoryPath); p != "" {
		clean := filepath.Clean(p)
		if isUnderBrainDirForHome(home, clean) {
			return filepath.Join(clean, "fyr-process.md")
		}
	}
	if tp := strings.TrimSpace(in.TranscriptPath); tp != "" {
		cur := filepath.Dir(filepath.Clean(tp))
		for i := 0; i < 6 && cur != "/" && cur != "."; i++ {
			if isUnderBrainDirForHome(home, cur) {
				return filepath.Join(cur, "fyr-process.md")
			}
			cur = filepath.Dir(cur)
		}
	}
	return ""
}

func isUnderBrainDirForHome(home, dir string) bool {
	if strings.TrimSpace(home) == "" || !filepath.IsAbs(dir) {
		return false
	}
	base := filepath.Base(dir)
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return false
	}
	brainDir := filepath.Dir(dir)
	if filepath.Base(brainDir) != "brain" {
		return false
	}
	if !matchesAnchoredBrainRoot(home, brainDir) {
		return false
	}
	for _, p := range []string{brainDir, dir} {
		if !isRegularDirOrAbsent(p) {
			return false
		}
	}
	return true
}

func matchesAnchoredBrainRoot(home, brainDir string) bool {
	cleanBrain := filepath.Clean(brainDir)
	absHome, err := filepath.Abs(strings.TrimSpace(home))
	if err != nil {
		return false
	}
	matched := cleanBrain == filepath.Join(absHome, ".gemini", "antigravity-cli", "brain")
	if !matched {
		if evalHome, err := filepath.EvalSymlinks(absHome); err == nil {
			matched = cleanBrain == filepath.Join(evalHome, ".gemini", "antigravity-cli", "brain")
		}
	}
	if !matched {
		return false
	}
	agyDir := filepath.Dir(cleanBrain)
	geminiDir := filepath.Dir(agyDir)
	return isRegularDirOrAbsent(geminiDir) && isRegularDirOrAbsent(agyDir)
}

func isRegularDirOrAbsent(p string) bool {
	st, err := os.Lstat(p)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return st.Mode()&os.ModeSymlink == 0 && st.IsDir()
}

func writeProcessArtifactAtomic(target string, data []byte) error {
	parent := filepath.Dir(target)
	brainDir := filepath.Dir(parent)
	if !isRegularDirOrAbsent(brainDir) {
		return fmt.Errorf("brain dir is not a regular directory")
	}
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
	f, err := os.CreateTemp(parent, ".fyr-process-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// RenderProcessArtifactForHome formats fyr-process.md using cached <home>/.config/fyr/models.json
// picks when present (< 1 ms, never spawning agy) and role-family policy otherwise.
func RenderProcessArtifactForHome(home, convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	return renderProcessTelemetryMarkdown(home, convID, stage, rows, records)
}

func safeBacktickFence(text string) string {
	maxRun, run := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	n := 3
	if maxRun >= n {
		n = maxRun + 1
	}
	return strings.Repeat("`", n)
}
