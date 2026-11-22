package auto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// workerTimeout is the 30-minute per-worker budget (R3 3.2b, D1 row 7).
const workerTimeout = 30 * time.Minute

// chooseRunner resolves the worker runner through isolate.ChooseRunner with
// --runner auto, failing closed if no isolated runner is available (PRD R8,
// R10; ADR-522). It sets wo.Runner and wo.RunnerReason (D2, D17).
func chooseRunner(wo *isolate.WorkerOptions, isolateAvail func() error) (runner, why string, err error) {
	wo.Runner = isolate.RunnerAuto
	runner, why, err = isolate.ChooseRunner(isolate.RunnerAuto, isolateAvail)
	if err != nil {
		wo.Runner, wo.RunnerReason = "", ""
		return "", "", err
	}
	wo.Runner, wo.RunnerReason = runner, why
	return runner, why, nil
}

// withRunner ends a worker's report line with the runner and why: last,
// because a refusal can carry the image's build line and a project's line
// is cut at lineLimit, so the verdict and the branch come first (BUG-506).
// Every line that names the runner is built here (sweep_line_test.go).
func withRunner(line, runnerWhy string) string { return line + "; runner " + runnerWhy }

// runModelTask picks the highest due model task for root (rotating p.LastClass
// to the end), files any rubric ask, and runs at most one worker (allowing
// DraftsOnly tasks on dirty, non-git, or GlobalWorkspace roots while enforcing
// clean git checkouts for non-DraftsOnly tasks).
func (r *run) runModelTask(root string, p *Project, findings []Finding) (string, []Finding) {
	if r.workerRan {
		return "worker already ran in this sweep", nil
	}
	if cd := r.cooldownStop(); cd != "" {
		return cd, nil
	}
	endPass := r.beginProjectPass(root)
	defer endPass()
	t, stopMsg := r.selectModelTask(root, p, findings)
	if t == nil {
		return stopMsg, nil
	}
	if !t.DraftsOnly {
		if why := workerCheckoutStop(root); why != "" {
			return "worker skipped: " + why, nil
		}
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
	v, wf := r.execWorker(root, p, *t)
	return v, append(extra, wf...)
}

func workerCheckoutStop(root string) string {
	if root == "" || root == search.GlobalWorkspace {
		return "global workspace has no git checkout"
	}
	if _, _, err := trail.Roots(root); err != nil {
		if errors.Is(err, trail.ErrNotCheckout) {
			return "not a git checkout"
		}
		return oneLine(err.Error())
	}
	if why := gateClean(root); why != "" {
		return strings.TrimPrefix(why, "G8 clean: ")
	}
	return ""
}

func (r *run) fileAsk(root string, t Task) (string, Finding) {
	denied := strings.Join(t.Denied, ", ")
	body := fmt.Sprintf("# auto ask: %s in %s\n\nClass `%s` needed rubric-denied target(s): `%s`.\n",
		t.Class, oneLine(filepath.Base(root)), t.Class, oneLine(denied))
	id, created, err := r.upsertProposal(proposalSpec{
		Project:    root,
		Title:      fmt.Sprintf("auto ask: %s in %s (%s)", t.Class, oneLine(filepath.Base(root)), oneLine(denied)),
		Body:       body,
		UpdateBody: fmt.Sprintf("Class `%s` again requested rubric-denied target(s): `%s`.", t.Class, oneLine(denied)),
		CustomKey:  rubricAskKey(root, t.Class, denied),
	})
	if err != nil {
		r.failed = true
		return "failed", Finding{"ask", "failed", t.Class, oneLine(err.Error())}
	}
	if created {
		r.st.OpenAsks++
	}
	return id, Finding{"ask", t.Class, denied, "queued proposal " + id + " (rubric-denied)"}
}

func (r *run) execWorker(root string, p *Project, t Task) (string, []Finding) {
	r.workerRan, r.projectWorkerRan = true, true
	cfgBefore, cfgErr := readBounded(ConfigPath(r.o.Home), configLimit)
	stateHash, err := r.recordWorkerStart()
	if err != nil {
		r.failed = true
		return "worker state error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	t.Root = root
	k, err := WriteKickoff(r.o.Home, r.o.now(), t)
	if err != nil {
		r.failed = true
		return "worker kickoff error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	wo := r.workOptions(root, t)
	_, runnerWhy, err := chooseRunner(&wo, r.o.IsolateAvailable)
	if err != nil {
		r.failed = true
		return "worker runner error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	learningCutoff, err := r.prepareLearningWorker(root, p, t, k.Path)
	r.finishPassMetrics()
	if err != nil {
		r.failed = true
		return "worker input error: " + oneLine(err.Error()), []Finding{{"worker", "failed", t.Class, oneLine(err.Error())}}
	}
	defer cleanLearningInput(root)
	var snapBefore checkoutSnapshot
	var snapErr error
	if !t.DraftsOnly {
		snapBefore, snapErr = takeCheckoutSnapshot(root)
	}
	sig, stop := signal.NotifyContext(r.context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	resList, runErr := r.o.workRun()(sig, wo, k.Path)
	var res isolate.WorkerResult
	if len(resList) > 0 {
		res = resList[0]
	}
	return r.finishWorker(sig, root, p, t, k, wo.Runner, runnerWhy, cfgBefore, cfgErr, stateHash, snapBefore, snapErr, res, runErr, learningCutoff)
}

func (r *run) recordWorkerStart() (string, error) {
	now := r.o.now()
	if today := now.Format("2006-01-02"); r.st.Date < today {
		r.st.Date, r.st.ModelRuns = today, 0
	}
	r.st.ModelRuns++
	r.st.LastModelRun = now
	if err := saveState(r.o.Home, r.st); err != nil {
		return "", err
	}
	return fileSHA256(filepath.Join(dir(r.o.Home), "runs.json"))
}

func (r *run) workOptions(root string, t Task) isolate.WorkerOptions {
	return isolate.WorkerOptions{
		Root:           root,
		Home:           r.o.Home,
		RealHome:       r.o.realHome(),
		Self:           r.o.self(),
		Repo:           r.o.Repo,
		Timeout:        workerTimeout,
		Model:          r.pickWorkerModel(t),
		DraftsOnly:     t.DraftsOnly,
		MaxWorkers:     1,
		Out:            io.Discard,
		Now:            r.o.Now,
		Auto:           true,
		TestRO:         r.o.TestRO,
		TestRW:         r.o.TestRW,
		TestUnconfined: r.o.TestUnconfined,
	}
}

func (r *run) pickWorkerModel(t Task) string {
	cat, ok, err := models.LoadCatalog(r.o.Home)
	if err != nil || !ok {
		return ""
	}
	role, tier := "builder", "pro"
	if t.DraftsOnly {
		role, tier = "mechanical", "flash"
	}
	pick, err := models.PickFromCatalog(cat, role, tier)
	if err != nil {
		return ""
	}
	return pick.Model
}

func (r *run) finishWorker(ctx context.Context, root string, p *Project, t Task, k Kickoff, runner, runnerWhy string, cfgBefore []byte, cfgErr error, stateHash string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult, runErr error, learningCutoff time.Time) (string, []Finding) {
	defer r.cleanWorkerDrafts(root, res.DraftsDir)
	if err := verifyAutoDirs(r.o.Home); err != nil {
		r.failed, r.halted = true, true
		msg := withRunner("FAIL ("+oneLine(err.Error())+"); branch "+k.Branch+" kept", runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	if slices.Contains(res.Reasons, isolate.ReasonRealConfig) || configTampered(r.o.Home, cfgBefore, cfgErr) {
		return r.handleConfigTamper(k.Branch, runnerWhy)
	}
	if gotHash, err := fileSHA256(filepath.Join(dir(r.o.Home), "runs.json")); err != nil || gotHash != stateHash {
		detail := "runs.json changed during the run"
		if err := saveState(r.o.Home, r.st); err != nil {
			detail += "; warning: could not restore state: " + oneLine(err.Error())
		}
		r.failed = true
		msg := withRunner("FAIL ("+detail+"); branch "+k.Branch+" kept", runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	if runErr != nil || res.Verdict != "PASS" {
		r.failed = true
		if slices.Contains(res.Reasons, isolate.ReasonLeadChanged) {
			return r.checkoutMovedFail(runnerWhy, t.Class, k.Branch)
		}
		why := strings.Join(res.Reasons, "; ")
		if runErr != nil {
			why = oneLine(runErr.Error())
		}
		msg := withRunner(fmt.Sprintf("FAIL (%s); branch %s kept", why, k.Branch), runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	draftFindings, err := r.collectAutoDrafts(root, k.Name, t.Class, res)
	if err != nil {
		r.failed = true
		msg := withRunner(fmt.Sprintf("FAIL (drafts: %s); branch %s kept", oneLine(err.Error()), k.Branch), runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	r.cleanWorkerDrafts(root, res.DraftsDir)
	p.LastClass = t.Class
	r.recordLearningProgress(p, t, learningCutoff)
	v, bf := r.finishPassBranch(ctx, root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
	return v, append(draftFindings, bf...)
}

func (r *run) recordLearningProgress(p *Project, t Task, cutoff time.Time) {
	mark := cutoff
	if mark.IsZero() {
		mark = r.o.now()
	}
	switch t.Class {
	case ClassMemoryConsolidation:
		p.LastMemoryConsolidation = r.o.now()
	case ClassRetroLite:
		p.LastRetroLite = mark
		p.PendingMemoryCandidates = t.Count
	case ClassMemoryCandidates:
		p.LastMemoryCandidates = mark
		p.PendingMemoryCandidates = 0
	}
}

func (r *run) finishPassBranch(ctx context.Context, root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult) (string, []Finding) {
	if t.Outcome == OutcomeEvalGate {
		return r.evalGateBranch(ctx, root, t, k, runner, runnerWhy, snapBefore, snapErr, res)
	}
	msg := withRunner(fmt.Sprintf("PASS; branch %s kept", k.Branch), runnerWhy)
	return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
}

// deleteBranch deletes a worker branch after an applied PASS, with hooks,
// replace refs, grafts, commit-graph and GPG signing off (§ The Kickoff and the branch).
func deleteBranch(root, branch string) error {
	_, err := applyGit(root, nil, "branch", "-D", "--", branch)
	return err
}
