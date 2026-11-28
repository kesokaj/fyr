package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/vet"
)

// ArtifactDeps supplies store and environment callbacks for telemetry artifact writes.
type ArtifactDeps struct {
	HooksDisabled    func(home, dir string) bool
	RecordVetHistory func(ctx context.Context, convID string, turnNum int, rawPrompt string, res vet.Result, pivotFiles []string, m vet.TurnMetrics) error
	ListVetHistory   func(dir, convID string, limit int) ([]vet.HistoryRow, bool)
}

// DedupContextKneeWarning emits a once-per-bucket context-knee warning when state crosses the model knee.
func DedupContextKneeWarning(home, convID, sessionModel string, state readledger.SessionState, markOnce func(convID string, genID, turnNum int, key string) bool) (string, string) {
	knee := models.ContextKneeTokensForHome(home, sessionModel)
	if knee <= 0 || int64(state.EstTokens) < knee {
		return "", ""
	}
	step := max(knee/2, int64(100_000))
	bucket := (int64(state.EstTokens) - knee) / step
	if markOnce != nil && !markOnce(convID, state.GenerationID, state.TurnCount, fmt.Sprintf("__knee_warn__:%d:%d", knee, bucket)) {
		return "", ""
	}
	kneeLabel := fmt.Sprintf("%d tokens", knee)
	if knee >= 1000 {
		kneeLabel = fmt.Sprintf("%dK", (knee+500)/1000)
	}
	full := fmt.Sprintf("[CONTEXT_KNEE_WARNING] Session context is ~%dk tokens (>= %s 60%% model knee) — %s (or /compact).", state.EstTokens/1000, kneeLabel, ContextKneeHandoffCue)
	short := fmt.Sprintf("%dk tokens (>= %s 60%% model knee) — %s", state.EstTokens/1000, kneeLabel, ContextKneeHandoffCue)
	return full, short
}

// FinalizeHookEnvelope renders the banner, persists vet history, and writes the process artifact.
func FinalizeHookEnvelope(ctx context.Context, home string, cat ModelsCatalogFile, hasCat bool, deps ArtifactDeps, dir, convID string, turnNum int, rawPrompt, cleanPrompt string, in ConvInput, state readledger.SessionState, vetRes vet.Result, slice vet.SliceResult, brainCtx vet.TurnBrainContext, meta TurnBannerMeta, sections []string, start time.Time) (string, string) {
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	sysMsg, banner := BuildTurnEnvelopeWithMeta(&vetRes, slice, brainCtx, meta)
	if vetRes.Gate == vet.GatePassThrough && !vetRes.Bypassed && !state.Compacted && len(sections) == 0 && len(vetRes.Conflicts) == 0 {
		sysMsg, banner = "", ""
	}
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	if deps.RecordVetHistory != nil && rawPrompt != "" {
		durMS := 0
		if !start.IsZero() {
			durMS = int(time.Since(start).Milliseconds())
		}
		pivotFiles := vet.ExtractPromptAndSliceFiles(cleanPrompt, slice.Files)
		wctx := ctx
		if wctx == nil {
			wctx = context.Background()
		}
		_ = deps.RecordVetHistory(wctx, convID, turnNum, vet.StripSandboxPreamble(strings.TrimSpace(rawPrompt)), vetRes, pivotFiles, vet.TurnMetrics{
			DurationMS:    durMS,
			InjectedBytes: len(sysMsg),
		})
	}
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	WriteHookProcessArtifactWithCatalog(home, cat, hasCat, deps, dir, in, inferProcessStage(turnNum, vetRes), vetRes.MatchedRecords)
	return sysMsg, banner
}

func inferProcessStage(turnNum int, res vet.Result) string {
	switch {
	case res.Gate == vet.GateVetFirst:
		return "VET"
	case res.Intent == vet.IntentRelease:
		return "VERIFY"
	case res.Intent == vet.IntentFeature && res.Score < 65:
		return "PLAN"
	case turnNum <= 1 && res.Intent == vet.IntentQuestion:
		return "RESUME"
	default:
		return "BUILD"
	}
}

// WriteHookProcessArtifact loads the model catalog and refreshes fyr-process.md.
func WriteHookProcessArtifact(home string, deps ArtifactDeps, dir string, in ConvInput, stage string, records []vet.MatchedRecord) {
	cat, hasCat := LoadModelsCatalog(home)
	WriteHookProcessArtifactWithCatalog(home, cat, hasCat, deps, dir, in, stage, records)
}

// WriteHookProcessArtifactWithCatalog writes fyr-process.md using an already-loaded model catalog.
func WriteHookProcessArtifactWithCatalog(home string, cat ModelsCatalogFile, hasCat bool, deps ArtifactDeps, dir string, in ConvInput, stage string, records []vet.MatchedRecord) {
	if strings.TrimSpace(home) == "" || (deps.HooksDisabled != nil && deps.HooksDisabled(home, dir)) {
		return
	}
	target := ResolveProcessArtifactPathForHome(home, in)
	if target == "" {
		return
	}
	var rows []vet.HistoryRow
	if deps.ListVetHistory != nil {
		var ok bool
		rows, ok = deps.ListVetHistory(dir, in.ConversationID, 20)
		if !ok {
			return
		}
	}
	content := RenderProcessTelemetryMarkdownWithCatalog(home, cat, hasCat, in.ConversationID, stage, rows, records)
	_ = WriteProcessArtifactAtomic(target, []byte(content))
}

// WorkStatusSnapshot captures the latest wave status.json snapshot under ~/.config/fyr/work/.
type WorkStatusSnapshot struct {
	Stamp     string               `json:"stamp"`
	UpdatedAt string               `json:"updated_at"`
	Workers   []WorkWorkerSnapshot `json:"workers"`
}

// WorkWorkerSnapshot captures one worker entry in WorkStatusSnapshot.
type WorkWorkerSnapshot struct {
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

// LoadLatestWorkStatus reads the newest valid wave status.json from home.
func LoadLatestWorkStatus(home string) (WorkStatusSnapshot, bool) {
	if strings.TrimSpace(home) == "" {
		return WorkStatusSnapshot{}, false
	}
	workDir := filepath.Join(home, harness.StateDir, "work")
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return WorkStatusSnapshot{}, false
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
		return WorkStatusSnapshot{}, false
	}
	sort.Strings(stamps)
	newest := stamps[len(stamps)-1]
	data, err := os.ReadFile(filepath.Join(workDir, newest, "status.json"))
	if err != nil {
		return WorkStatusSnapshot{}, false
	}
	var snap WorkStatusSnapshot
	if err := json.Unmarshal(data, &snap); err != nil || len(snap.Workers) == 0 {
		return WorkStatusSnapshot{}, false
	}
	return snap, true
}

// ResolveProcessArtifactPathForHome returns the anchored fyr-process.md path under home's brain dir.
func ResolveProcessArtifactPathForHome(home string, in ConvInput) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	if p := strings.TrimSpace(in.ArtifactDirectoryPath); p != "" {
		clean := filepath.Clean(p)
		if IsUnderBrainDirForHome(home, clean) {
			return filepath.Join(clean, "fyr-process.md")
		}
	}
	if tp := strings.TrimSpace(in.TranscriptPath); tp != "" {
		cur := filepath.Dir(filepath.Clean(tp))
		for i := 0; i < 6 && cur != "/" && cur != "."; i++ {
			if IsUnderBrainDirForHome(home, cur) {
				return filepath.Join(cur, "fyr-process.md")
			}
			cur = filepath.Dir(cur)
		}
	}
	return ""
}

// IsUnderBrainDirForHome verifies dir is a direct regular subdir of home/.gemini/antigravity-cli/brain.
func IsUnderBrainDirForHome(home, dir string) bool {
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
		if !IsRegularDirOrAbsent(p) {
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
	return IsRegularDirOrAbsent(geminiDir) && IsRegularDirOrAbsent(agyDir)
}

// IsRegularDirOrAbsent reports whether p is absent or a non-symlink directory.
func IsRegularDirOrAbsent(p string) bool {
	st, err := os.Lstat(p)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return st.Mode()&os.ModeSymlink == 0 && st.IsDir()
}

// WriteProcessArtifactAtomic safely writes data to target via temp file + rename without following symlinks.
func WriteProcessArtifactAtomic(target string, data []byte) error {
	parent := filepath.Dir(target)
	brainDir := filepath.Dir(parent)
	if !IsRegularDirOrAbsent(brainDir) {
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
