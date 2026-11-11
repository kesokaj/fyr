package work

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// WorkerStatus is one worker's live state in `status.json`.
type WorkerStatus struct {
	Name         string   `json:"name"`
	State        string   `json:"state"` // "queued", "running", "PASS", "FAIL"
	Model        string   `json:"model"`
	Branch       string   `json:"branch"`
	Files        []string `json:"files"`
	StepCount    int      `json:"step_count"`
	LastTool     string   `json:"last_tool,omitempty"`
	LastStep     string   `json:"last_step,omitempty"`
	Subagents    []string `json:"subagents,omitempty"`
	BaseCheck    string   `json:"base_check,omitempty"`
	JudgeModel   string   `json:"judge_model,omitempty"`
	JudgeVerdict string   `json:"judge_verdict,omitempty"`
	ElapsedSec   int      `json:"elapsed_sec"`
	SetupSec     int      `json:"setup_s"`
	AgySec       int      `json:"agy_s"`
	CheckSec     int      `json:"check_s"`
	Commits      int      `json:"commits,omitempty"`
	Reasons      []string `json:"reasons,omitempty"`
}

// WaveStatus is a `/fyr-work` wave's live status board persisted to
// `<home>/.config/fyr/work/<stamp>/status.json`.
type WaveStatus struct {
	Stamp       string         `json:"stamp"`
	ProjectRoot string         `json:"project_root,omitempty"`
	UpdatedAt   string         `json:"updated_at"`
	Workers     []WorkerStatus `json:"workers"`
}

type progressTracker struct {
	mu           sync.Mutex
	out          io.Writer
	statusPath   string
	statusMDPath string
	now          func() time.Time
	starts       map[string]time.Time
	index        map[string]int
	wave         WaveStatus
}

func newProgressTracker(o Options, stamp string, ps []prepared) *progressTracker {
	nowFn := o.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	var projectRoot string
	if strings.TrimSpace(o.Root) != "" {
		projectRoot = filepath.Clean(o.Root)
	}
	t := &progressTracker{
		out:          o.Progress,
		statusPath:   filepath.Join(sandbox.Root(o.Home, "work"), stamp, "status.json"),
		statusMDPath: o.StatusMD,
		now:          nowFn,
		starts:       make(map[string]time.Time, len(ps)),
		index:        make(map[string]int, len(ps)),
		wave: WaveStatus{
			Stamp:       stamp,
			ProjectRoot: projectRoot,
			Workers:     make([]WorkerStatus, len(ps)),
		},
	}
	for i, p := range ps {
		model := p.k.Model
		if model == "" {
			model = o.Model
		}
		if model == "" {
			model = "default"
		}
		t.index[p.k.Name] = i
		t.wave.Workers[i] = WorkerStatus{
			Name:   p.k.Name,
			State:  "queued",
			Model:  model,
			Branch: p.k.Branch(),
			Files:  slices.Clone(p.k.Files),
		}
	}
	t.persistLocked()
	return t
}

func (t *progressTracker) onStart(name string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.index[name]
	if !ok {
		return
	}
	t.starts[name] = t.now()
	w := &t.wave.Workers[idx]
	w.State = "running"
	t.persistLocked()
	files := strings.Join(w.Files, ", ")
	if files == "" {
		files = "(drafts)"
	}
	t.emitLine(fmt.Sprintf("work: [%s] ▶ started  model %s  branch %s  files: %s",
		w.Name, w.Model, w.Branch, files))
}

func (t *progressTracker) onEvent(name string, ev sandbox.ACPEvent) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.index[name]
	if !ok {
		return
	}
	w := &t.wave.Workers[idx]
	w.ElapsedSec = t.elapsedLocked(name)
	switch ev.Kind {
	case "subagent":
		w.StepCount++
		w.LastTool = "invoke_subagent"
		w.LastStep = ev.Summary
		if ev.Summary != "" && !slices.Contains(w.Subagents, ev.Summary) {
			w.Subagents = append(w.Subagents, ev.Summary)
		}
		t.persistLocked()
		t.emitLine(fmt.Sprintf("work: [%s] 🤖 step %d (%s)  subagent %s",
			w.Name, w.StepCount, fmtMMSS(w.ElapsedSec), ev.Summary))
	case "tool_call", "tool_update":
		if ev.Kind == "tool_update" && ev.StepIndex <= w.StepCount {
			return
		}
		w.StepCount++
		w.LastTool = ev.ToolName
		w.LastStep = strings.TrimSpace(ev.ToolName + " " + ev.Summary)
		t.persistLocked()
		t.emitLine(fmt.Sprintf("work: [%s] ⚙ step %d (%s)  %s",
			w.Name, w.StepCount, fmtMMSS(w.ElapsedSec), w.LastStep))
	}
}

func (t *progressTracker) onBaseCheck(name, stage, status string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.index[name]
	if !ok {
		return
	}
	w := &t.wave.Workers[idx]
	w.ElapsedSec = t.elapsedLocked(name)
	w.BaseCheck = status
	w.LastTool = "base-check"
	w.LastStep = fmt.Sprintf("base-check %s: %s", stage, status)
	t.persistLocked()
	t.emitLine(fmt.Sprintf("work: [%s] 🧪 base-check %s (%s)  %s",
		w.Name, stage, fmtMMSS(w.ElapsedSec), status))
}

func (t *progressTracker) onJudge(name, model, verdict, reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.index[name]
	if !ok {
		return
	}
	w := &t.wave.Workers[idx]
	w.ElapsedSec = t.elapsedLocked(name)
	w.JudgeModel = model
	w.JudgeVerdict = verdict
	w.LastTool = "judge"
	step := fmt.Sprintf("judge (%s): %s", model, verdict)
	if reason != "" {
		step += " — " + reason
	}
	w.LastStep = step
	t.persistLocked()
	t.emitLine(fmt.Sprintf("work: [%s] ⚖ %s (%s)",
		w.Name, step, fmtMMSS(w.ElapsedSec)))
}

func (t *progressTracker) onStage(name, stage string, sec int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.index[name]
	if !ok {
		return
	}
	w := &t.wave.Workers[idx]
	w.ElapsedSec = t.elapsedLocked(name)
	switch stage {
	case "setup":
		w.SetupSec = max(0, sec)
	case "agy":
		w.AgySec = max(0, sec)
	}
	t.persistLocked()
}

func (t *progressTracker) onFinish(name string, res Result) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.index[name]
	if !ok {
		return
	}
	w := &t.wave.Workers[idx]
	w.State = res.Verdict
	w.ElapsedSec = res.Seconds
	w.SetupSec = max(0, res.SetupSec)
	w.AgySec = max(0, res.AgySec)
	w.CheckSec = max(0, res.CheckSec)
	w.Commits = res.Commits
	w.Reasons = slices.Clone(res.Reasons)
	t.persistLocked()
	t.emitLine(fmt.Sprintf("work: [%s] ✓ finished %s (%s, %d steps, %d commits)",
		w.Name, w.State, fmtMMSS(w.ElapsedSec), w.StepCount, w.Commits))
}

func (t *progressTracker) elapsedLocked(name string) int {
	st, ok := t.starts[name]
	if !ok {
		return 0
	}
	return max(0, int(t.now().Sub(st).Seconds()))
}

func (t *progressTracker) persistLocked() {
	t.wave.UpdatedAt = t.now().UTC().Format(time.RFC3339)
	if t.statusMDPath != "" {
		writeStatusMD(t.statusMDPath, t.wave)
	}
	if t.statusPath == "" {
		return
	}
	b, err := json.MarshalIndent(t.wave, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(t.statusPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".status-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	_ = tmp.Chmod(0o600)
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(name, t.statusPath) != nil {
		_ = os.Remove(name)
	}
}

func (t *progressTracker) emitLine(s string) {
	if t.out == nil {
		return
	}
	clean := sandbox.Printable(s)
	runes := []rune(clean)
	if len(runes) > 120 {
		clean = string(runes[:117]) + "..."
	}
	_, _ = fmt.Fprintln(t.out, clean)
}

func fmtMMSS(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

// FormatStatusBoard reads the newest `<home>/.config/fyr/work/<stamp>/status.json`
// and renders a compact status table for `fyr work --status`.
func FormatStatusBoard(home string) (string, error) {
	workDir := sandbox.Root(home, "work")
	entries, err := os.ReadDir(workDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "work: no active or recent waves\n", nil
		}
		return "", err
	}
	var stamps []string
	for _, e := range entries {
		if e.IsDir() && stampRe.MatchString(e.Name()) {
			if _, serr := os.Stat(filepath.Join(workDir, e.Name(), "status.json")); serr == nil {
				stamps = append(stamps, e.Name())
			}
		}
	}
	if len(stamps) == 0 {
		return "work: no active or recent waves\n", nil
	}
	sort.Strings(stamps)
	newest := stamps[len(stamps)-1]
	data, err := os.ReadFile(filepath.Join(workDir, newest, "status.json"))
	if err != nil {
		return "", err
	}
	var wave WaveStatus
	if err := json.Unmarshal(data, &wave); err != nil {
		return "", err
	}
	return renderWaveBoard(wave), nil
}

func renderWaveBoard(wave WaveStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "work: stamp %s  updated %s\n", sandbox.Printable(wave.Stamp), sandbox.Printable(wave.UpdatedAt))
	fmt.Fprintln(&b, "WORKER\tSTATE\tELAPSED\tSTEPS\tCOMMITS\tMODEL\tLAST STEP")
	for _, w := range wave.Workers {
		last := w.LastStep
		if last == "" && len(w.Reasons) > 0 {
			last = strings.Join(w.Reasons, "; ")
		}
		if last == "" {
			last = "-"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			sandbox.Printable(w.Name),
			sandbox.Printable(w.State),
			fmtMMSS(w.ElapsedSec),
			w.StepCount,
			w.Commits,
			sandbox.Printable(w.Model),
			sandbox.Printable(last))
	}
	return b.String()
}
