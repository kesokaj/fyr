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
		banners = append(banners, fmt.Sprintf("compacted (gen %d): re-primed memory & capsule", state.GenerationID))
	}
	if fastPath && dirtySummary != "" {
		sections = append(sections, dirtySummary)
		banners = append(banners, ">15 dirty files: serving cached AST snapshot")
	}
	var slice SliceResult
	if ClassifyIntent(cleanPrompt, 0) != IntentApproval || state.Compacted {
		sliceOpts := o
		sliceOpts.Dir = dir
		sliceOpts.Session = convID
		slice = sc.buildVetSlice(sliceOpts, cleanPrompt, state, true)
		if slice.Capsule != "" {
			banners = append(banners, fmt.Sprintf("%d pivots", slice.Pivots))
		}
	}
	if state.EstTokens >= hookKneeTokens {
		sections = append(sections, fmt.Sprintf("[CONTEXT_KNEE_WARNING] Session context is ~%dk tokens (>= 180K knee); wrap up current step and run /fyr-handoff or /compact.", state.EstTokens/1000))
		banners = append(banners, fmt.Sprintf("%dk tokens (>= 180K knee)", state.EstTokens/1000))
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
			if isUnderBrainDir(cur) {
				return filepath.Join(cur, "fyr-process.md")
			}
			cur = filepath.Dir(cur)
		}
	}
	return ""
}

func isUnderBrainDir(dir string) bool {
	if !filepath.IsAbs(dir) {
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
	if st, err := os.Lstat(brainDir); err == nil && (st.Mode()&os.ModeSymlink != 0 || !st.IsDir()) {
		return false
	}
	if st, err := os.Lstat(dir); err == nil && (st.Mode()&os.ModeSymlink != 0 || !st.IsDir()) {
		return false
	}
	return true
}

func writeProcessArtifactAtomic(target string, data []byte) error {
	parent := filepath.Dir(target)
	brainDir := filepath.Dir(parent)
	if st, err := os.Lstat(brainDir); err == nil && (st.Mode()&os.ModeSymlink != 0 || !st.IsDir()) {
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

// RenderProcessArtifact formats the live `<appDataDir>/brain/<conv-id>/fyr-process.md`
// Markdown artifact showing the fyr workflow stages, active trail/model routing,
// and chronological turn-by-turn vet_history + [VET_IMPROVED_PROMPT] synthesis.
func RenderProcessArtifact(convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	return RenderProcessArtifactForHome("", convID, stage, rows, records)
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
