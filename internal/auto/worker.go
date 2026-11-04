package auto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/work"
)

// workerTimeout is the 30-minute per-worker budget (R3 3.2b, D1 row 7).
const workerTimeout = 30 * time.Minute

// chooseRunner returns RunnerDocker when fyr work's docker preflight passes,
// and RunnerHost with the reason when it does not, setting wo.Runner and
// wo.RunnerReason (D2, D17).
func chooseRunner(wo *work.Options, preflight func(work.Options) error) (runner, why string) {
	if preflight == nil {
		preflight = work.DockerPreflight
	}
	if err := preflight(*wo); err == nil {
		wo.Runner, wo.RunnerReason = work.RunnerDocker, ""
		return work.RunnerDocker, "docker (fyr work docker preflight passed)"
	} else {
		_, reason := work.Check(work.Options{Runner: work.RunnerHost, RunnerReason: err.Error()})
		wo.Runner, wo.RunnerReason = work.RunnerHost, reason
		return work.RunnerHost, "host (" + reason + ")"
	}
}

// runModelTask picks the highest due model task for root (rotating p.LastClass
// to the end), files any rubric ask, and runs at most one worker.
func (r *run) runModelTask(root string, p *Project, findings []Finding) (string, []Finding) {
	if r.workerRan {
		return "worker already ran in this sweep", nil
	}
	t := r.pickProjectTask(root, p, findings)
	if t == nil {
		return "no model task due", nil
	}
	var extra []Finding
	var askID string
	if len(t.Denied) > 0 {
		id, f := r.fileAsk(root, *t)
		askID = id
		extra = append(extra, f)
	}
	if !t.DraftsOnly && len(t.Kept) == 0 {
		p.LastClass = t.Class
		return fmt.Sprintf("ask %s (%s needs rubric-denied %s)", askID, t.Class, strings.Join(t.Denied, ", ")), extra
	}
	if t.Class == ClassEvalRerun {
		v, ef := r.execEvalRerun(root, p, *t)
		return v, append(extra, ef...)
	}
	v, wf := r.execWorker(root, p, *t)
	return v, append(extra, wf...)
}

func (r *run) fileAsk(root string, t Task) (string, Finding) {
	denied := strings.Join(t.Denied, ", ")
	body := fmt.Sprintf("# auto ask: %s in %s\n\nClass `%s` at level `%s` needed rubric-denied target(s): `%s`.\n",
		t.Class, oneLine(filepath.Base(root)), t.Class, t.Level, oneLine(denied))
	id, err := brain.Add(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.AddInput{
		Kind:    "proposal",
		Title:   fmt.Sprintf("auto ask: %s in %s (%s)", t.Class, oneLine(filepath.Base(root)), oneLine(denied)),
		Project: root,
		Body:    strings.NewReader(redact.Redact(body)),
	})
	if err != nil {
		r.failed = true
		return "failed", Finding{"ask", "failed", t.Class, oneLine(err.Error())}
	}
	r.st.OpenAsks++
	return id, Finding{"ask", t.Class, denied, "queued proposal " + id + " (rubric-denied)"}
}

func (r *run) execWorker(root string, p *Project, t Task) (string, []Finding) {
	r.workerRan = true
	cfgBefore, cfgErr := readBounded(ConfigPath(r.o.Home), configLimit)
	if today := r.o.now().Format("2006-01-02"); r.st.Date < today {
		r.st.Date, r.st.ModelRuns = today, 0
	}
	r.st.ModelRuns++
	if err := saveState(r.o.Home, r.st); err != nil {
		r.failed = true
		return "worker state error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	stateHash, err := fileSHA256(filepath.Join(dir(r.o.Home), "runs.json"))
	if err != nil {
		r.failed = true
		return "worker state error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	k, err := WriteKickoff(r.o.Home, r.o.now(), t)
	if err != nil {
		r.failed = true
		return "worker kickoff error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	wo := r.workOptions(root)
	var runnerWhy string
	_, runnerWhy = chooseRunner(&wo, r.o.DockerPreflight)
	if err := r.prepareLearningWorker(root, p, t, k.Path); err != nil {
		r.failed = true
		return "worker input error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	defer cleanLearningInput(root)
	snapBefore, snapErr := takeCheckoutSnapshot(root)
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	resList, runErr := r.o.workRun()(sig, wo, k.Path)
	stop()
	var res work.Result
	if len(resList) > 0 {
		res = resList[0]
	}
	return r.finishWorker(root, p, t, k, wo.Runner, runnerWhy, cfgBefore, cfgErr, stateHash, snapBefore, snapErr, res, runErr)
}

func (r *run) workOptions(root string) work.Options {
	return work.Options{
		Root:       root,
		Home:       r.o.Home,
		RealHome:   r.o.realHome(),
		Self:       r.o.self(),
		Repo:       r.o.Repo,
		Version:    r.o.Version,
		Timeout:    workerTimeout,
		MaxWorkers: 1,
		Out:        io.Discard,
		Now:        r.o.Now,
		Auto:       true,
	}
}

func (r *run) finishWorker(root string, p *Project, t Task, k Kickoff, runner, runnerWhy string, cfgBefore []byte, cfgErr error, stateHash string, snapBefore checkoutSnapshot, snapErr error, res work.Result, runErr error) (string, []Finding) {
	defer cleanProjectDrafts(root, res.DraftsDir)
	if err := verifyAutoDirs(r.o.Home); err != nil {
		r.failed, r.halted = true, true
		msg := "runner " + runnerWhy + "; FAIL (" + oneLine(err.Error()) + "); branch " + k.Branch + " kept"
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	if slices.Contains(res.Reasons, sandbox.ReasonRealConfig) || configTampered(r.o.Home, cfgBefore, cfgErr) {
		disableConfig(r.o.Home, r.cfg)
		_ = saveState(r.o.Home, r.st)
		r.failed, r.halted = true, true
		msg := "runner " + runnerWhy + "; auto disabled: its config changed during a run"
		return "auto disabled: its config changed during a run", []Finding{{"worker", "config-changed", k.Branch, msg}}
	}
	if gotHash, err := fileSHA256(filepath.Join(dir(r.o.Home), "runs.json")); err != nil || gotHash != stateHash {
		_ = saveState(r.o.Home, r.st)
		r.failed = true
		msg := "runner " + runnerWhy + "; FAIL (runs.json changed during the run); branch " + k.Branch + " kept"
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	if runErr != nil || res.Verdict != "PASS" {
		r.failed = true
		if slices.Contains(res.Reasons, work.ReasonLeadChanged) {
			msg := fmt.Sprintf("runner %s; FAIL (checkout moved); branch %s kept", runnerWhy, k.Branch)
			return "checkout moved; branch " + k.Branch + " kept", []Finding{{"worker", t.Class, k.Branch, msg}}
		}
		why := strings.Join(res.Reasons, "; ")
		if runErr != nil {
			why = oneLine(runErr.Error())
		}
		msg := fmt.Sprintf("runner %s; FAIL (%s); branch %s kept", runnerWhy, why, k.Branch)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	draftFindings, err := r.collectAutoDrafts(root, k.Name, t.Class, res)
	if err != nil {
		r.failed = true
		msg := fmt.Sprintf("runner %s; FAIL (drafts: %s); branch %s kept", runnerWhy, oneLine(err.Error()), k.Branch)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	cleanProjectDrafts(root, res.DraftsDir)
	p.LastClass = t.Class
	r.recordLearningProgress(p, t)
	v, bf := r.finishPassBranch(root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
	return v, append(draftFindings, bf...)
}

func (r *run) recordLearningProgress(p *Project, t Task) {
	now := r.o.now()
	switch t.Class {
	case ClassMemoryConsolidation:
		p.LastMemoryConsolidation = now
	case ClassRetroLite:
		p.LastRetroLite = now
		p.PendingMemoryCandidates = t.Count
	case ClassMemoryCandidates:
		p.LastMemoryCandidates = now
		p.PendingMemoryCandidates = 0
	case ClassMiniAudit:
		p.LastMiniAudit = now
	}
}

func (r *run) finishPassBranch(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res work.Result) (string, []Finding) {
	if t.Outcome == OutcomeEvalGate {
		return r.evalGateBranch(root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
	}
	if t.Outcome == OutcomeApplied {
		if r.o.Apply == nil {
			return r.applyBranch(root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
		}
		applied, err := r.o.Apply(root, k.Branch, t.Class)
		if err != nil {
			r.failed = true
			msg := fmt.Sprintf("runner %s; FAIL (%s); branch %s kept", runnerWhy, oneLine(err.Error()), k.Branch)
			return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
		}
		if applied {
			if err := deleteBranch(root, k.Branch); err != nil {
				r.failed = true
				msg := fmt.Sprintf("runner %s; FAIL (branch delete: %s); branch %s kept", runnerWhy, oneLine(err.Error()), k.Branch)
				return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
			}
			msg := fmt.Sprintf("runner %s; PASS; applied, branch %s deleted", runnerWhy, k.Branch)
			return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
		}
	}
	msg := fmt.Sprintf("runner %s; PASS; branch %s kept", runnerWhy, k.Branch)
	return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
}

// collectAutoDrafts copies auto-drafts.jsonl from the worker's DraftsDir into
// ~/.config/fyr/auto/drafts/<name>.jsonl (D26 S1) and returns any D39 finding
// lines for the project's report.
func (r *run) collectAutoDrafts(root, name, class string, res work.Result) ([]Finding, error) {
	if res.Drafts == 0 || res.DraftsDir == "" {
		return nil, nil
	}
	rel := filepath.Join(filepath.FromSlash(res.DraftsDir), "auto-drafts.jsonl")
	lead, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer lead.Close()
	f, err := lead.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("auto-drafts.jsonl unreadable or over 1 MiB")
	}
	if err := ensureSubdir(r.o.Home, "drafts"); err != nil {
		return nil, err
	}
	dest := filepath.Join(dir(r.o.Home), "drafts", name+".jsonl")
	if err := harness.WriteAtomic(dest, data, 0o600); err != nil {
		return nil, err
	}
	return r.processDraftLines(root, class, data), nil
}

// cleanProjectDrafts removes the .agents/scratch/work/<stamp>/<name> directory
// work.Run created in root, and empty parents up to .agents/scratch/work.
func cleanProjectDrafts(root, draftsDir string) {
	if draftsDir == "" {
		return
	}
	lead, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer lead.Close()
	rel := filepath.Clean(filepath.FromSlash(draftsDir))
	_ = lead.Remove(filepath.Join(rel, "auto-drafts.jsonl"))
	for _, dir := range []string{rel, filepath.Dir(rel), filepath.Join(".agents", "scratch", "work"), filepath.Join(".agents", "scratch")} {
		_ = lead.Remove(dir)
	}
}

// verifyAutoDirs re-checks auto/ and its subdirectories after a worker so a
// directory replaced by a symlink during a host run is refused before any
// post-worker write (D26 S5).
func verifyAutoDirs(home string) error {
	if err := ensureDir(home); err != nil {
		return err
	}
	for _, sub := range []string{"kickoffs", "drafts", "inputs"} {
		p := filepath.Join(dir(home), sub)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", p)
		}
	}
	return nil
}

func configTampered(home string, before []byte, beforeErr error) bool {
	after, afterErr := readBounded(ConfigPath(home), configLimit)
	if (beforeErr != nil) != (afterErr != nil) {
		return true
	}
	return !bytes.Equal(before, after)
}

func disableConfig(home string, c Config) {
	if c.Level == "" {
		c = defaults()
	}
	c.Enabled = false
	_ = save(home, c)
}

func fileSHA256(path string) (string, error) {
	data, err := readBounded(path, stateLimit)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

// deleteBranch deletes a worker branch after an applied PASS, with hooks,
// replace refs, grafts, commit-graph and GPG signing off (§ The Kickoff and the branch).
func deleteBranch(root, branch string) error {
	_, err := applyGit(root, nil, "branch", "-D", branch)
	return err
}
