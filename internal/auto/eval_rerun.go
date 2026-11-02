package auto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// EvalRerunRow is one suite row selected by the eval-rerun triggers (catalog 13–15).
type EvalRerunRow struct {
	ID     string
	Runs   int
	Reason string
}

type trialMeta struct {
	harness   string
	cli       string
	startedAt int64
}

type trialState struct {
	latest       map[string]trialMeta
	unstable     map[string]bool
	gradedOnHead map[string]int
	latestCLI    string
}

var hexCommitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func (r *run) currentAgyVersion() string {
	if r.o.AgyVersion != nil {
		return strings.TrimSpace(r.o.AgyVersion())
	}
	cx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cx, "agy", "--version")
	cmd.Env = append(sandbox.WithoutGit(os.Environ()), "PATH="+r.o.path())
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}

func (r *run) triggerEvalRerun(root string, p *Project) *Task {
	if !r.isHarnessCheckout(root) {
		return nil
	}
	suite, err := readCheckoutSuite(r.o.Home, root)
	if err != nil {
		return nil
	}
	ts := r.readEvalTrialState(root)
	reruns := r.collectEvalReruns(root, p, suite, ts)
	if len(reruns) == 0 {
		return nil
	}
	return &Task{
		Class:      ClassEvalRerun,
		Level:      LevelHarness,
		Outcome:    OutcomeEval,
		DraftsOnly: true,
		Count:      len(reruns),
		Reruns:     reruns,
	}
}

func sameDir(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ia, ib)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// readCheckoutSuite is the suite `fyr eval` reads in root: the tracked file
// when root has one (ADR-500), else the GUIDE-eval record of root's scope.
func readCheckoutSuite(home, root string) ([]eval.Row, error) {
	if rows, found, err := eval.ReadTrackedSuite(root, false); found || err != nil {
		return rows, err
	}
	recs, ok, err := brain.ReadScope(home, root)
	if err != nil || !ok {
		return nil, fmt.Errorf("no trail in %s", root)
	}
	for _, rec := range recs {
		if rec.ID == "GUIDE-eval" {
			return eval.ParseSuite(rec.Body)
		}
	}
	return nil, fmt.Errorf("no GUIDE-eval in %s", root)
}

func (r *run) collectEvalReruns(root string, p *Project, suite []eval.Row, ts trialState) []EvalRerunRow {
	targets := listHarnessTargets(root)
	curCLI := r.currentAgyVersion()
	curValid := curCLI != "" && !strings.HasPrefix(curCLI, "unknown")
	var out []EvalRerunRow
	for _, row := range suite {
		runs, reasons := 0, []string(nil)
		if tr, ok := ts.latest[row.ID]; ok && rowTargetChanged(root, row, targets, tr, p.LastEvalRerun) {
			runs = max(runs, 3)
			reasons = append(reasons, "skill-or-agent-changed")
		}
		if ts.unstable[row.ID] && ts.gradedOnHead[row.ID] < 5 {
			if tr, ok := ts.latest[row.ID]; !ok || p.LastEvalRerun.IsZero() || tr.startedAt > p.LastEvalRerun.UnixMilli() {
				runs = max(runs, 5)
				reasons = append(reasons, "unstable")
			}
		}
		rowCLI := ts.latest[row.ID].cli
		if rowCLI == "" {
			rowCLI = p.LastEvalCLI
		}
		if curValid && row.Kind == eval.KindRegression && rowCLI != "" && !strings.HasPrefix(rowCLI, "unknown") && rowCLI != curCLI && p.LastEvalCLI != curCLI {
			runs = max(runs, 1)
			reasons = append(reasons, "cli-changed")
		}
		if runs > 0 {
			out = append(out, EvalRerunRow{ID: row.ID, Runs: runs, Reason: strings.Join(reasons, ",")})
		}
	}
	return out
}

type harnessTarget struct {
	name, relPath string
}

func listHarnessTargets(root string) []harnessTarget {
	var out []harnessTarget
	for _, sub := range []struct{ dir, file string }{{"skills", "SKILL.md"}, {"agents", "agent.md"}} {
		entries, err := os.ReadDir(filepath.Join(root, sub.dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "google-catalog" {
				continue
			}
			rel := filepath.Join(sub.dir, e.Name())
			if _, err := os.Stat(filepath.Join(root, rel, sub.file)); err == nil {
				out = append(out, harnessTarget{name: e.Name(), relPath: rel})
			}
		}
	}
	return out
}

func rowTargetChanged(root string, row eval.Row, targets []harnessTarget, tr trialMeta, lastRerun time.Time) bool {
	for _, tgt := range targets {
		if !rowNamesTarget(row, tgt.name) {
			continue
		}
		if targetChangedSinceTrial(root, tgt.relPath, tr, lastRerun) {
			return true
		}
	}
	return false
}

func rowNamesTarget(r eval.Row, name string) bool {
	if name == "" {
		return false
	}
	if r.Agent == name || r.ID == name || strings.HasPrefix(r.ID, name+"-") {
		return true
	}
	return mentionsWord(r.Prompt, name) || mentionsWord(r.Prompt2, name)
}

func mentionsWord(text, word string) bool {
	for start := 0; start < len(text); {
		idx := strings.Index(text[start:], word)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(word)
		beforeOK := pos == 0 || !isIdentByte(text[pos-1])
		afterOK := end == len(text) || !isIdentByte(text[end])
		if beforeOK && afterOK {
			return true
		}
		start = pos + 1
	}
	return false
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

func targetChangedSinceTrial(root, relPath string, tr trialMeta, lastRerun time.Time) bool {
	commit, _, _ := strings.Cut(strings.TrimSpace(tr.harness), " ")
	if hexCommitRe.MatchString(commit) && trail.Command(root, "cat-file", "-e", commit+"^{commit}").Run() == nil {
		return trail.Command(root, "diff", "--quiet", commit, "HEAD", "--", relPath).Run() != nil
	}
	out, err := trail.Command(root, "log", "-1", "--format=%ct", "--", relPath).Output()
	if err != nil {
		return false
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || sec <= 0 {
		return false
	}
	if !lastRerun.IsZero() && sec < lastRerun.Unix() {
		return false
	}
	return sec*1000 > tr.startedAt
}

func (r *run) readEvalTrialState(root string) trialState {
	ts := trialState{
		latest:       map[string]trialMeta{},
		unstable:     map[string]bool{},
		gradedOnHead: map[string]int{},
	}
	headKey := gitShortCommit(root)
	homes := []string{r.o.Home}
	if eh := r.o.evalHome(); eh != "" && eh != r.o.Home {
		homes = append(homes, eh)
	}
	for _, h := range homes {
		r.mergeHomeTrials(h, headKey, &ts)
	}
	return ts
}

func (r *run) mergeHomeTrials(home, headKey string, ts *trialState) {
	mp := metrics.Path(home)
	if _, err := os.Lstat(mp); err != nil {
		return
	}
	tmp, err := os.MkdirTemp(filepath.Dir(mp), "auto-metrics-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	s, err := metrics.Snapshot(mp, tmp)
	if err != nil {
		return
	}
	defer s.Close()
	if trials, err := s.SuiteTrials(); err == nil {
		scanTrialRows(trials, headKey, ts)
	}
	if ev, err := s.Evals(metrics.EvalOptions{Now: r.o.now()}); err == nil {
		for _, row := range ev.Rows {
			if slices.Contains(row.Flags, "unstable") {
				ts.unstable[row.Row] = true
			}
		}
	}
}

func scanTrialRows(trials []metrics.Trial, headKey string, ts *trialState) {
	for _, tr := range trials {
		if tr.CLI != "" {
			ts.latestCLI = tr.CLI
		}
		if tr.Status != "pass" && tr.Status != "fail" {
			continue
		}
		rowID := strings.TrimPrefix(tr.Label, "eval=")
		started := tr.StartedAt.UnixMilli()
		if prev, ok := ts.latest[rowID]; !ok || started >= prev.startedAt {
			ts.latest[rowID] = trialMeta{harness: tr.Harness, cli: tr.CLI, startedAt: started}
		}
		commit, _, _ := strings.Cut(strings.TrimSpace(tr.Harness), " ")
		if headKey != "" && commit == headKey {
			ts.gradedOnHead[rowID]++
		}
	}
}

func gitShortCommit(dir string) string {
	out, err := trail.Command(dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
