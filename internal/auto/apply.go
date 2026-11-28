package auto

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/trail"
)

// checkoutSnapshot records the checkout state in step 2 and before the worker.
type checkoutSnapshot = trail.CheckoutSnapshot

var autoSnapshotOpts = trail.SnapshotOptions{
	IncludeReflog: true,
	FullBranchRef: true,
	HardenConfig:  true,
}

func takeCheckoutSnapshot(root string) (checkoutSnapshot, error) {
	if stop := gateClean(root); stop != "" {
		return checkoutSnapshot{}, errors.New(stop)
	}
	return trail.CaptureCheckoutSnapshot(context.Background(), root, autoSnapshotOpts)
}

// applyGit runs git in dir with hooks, replace refs, commit-graph, grafts and
// GPG signing disabled (§ The apply step, § The Kickoff and the branch).
func applyGit(dir string, extraEnv []string, args ...string) (string, error) {
	out, err := applyGitBytes(dir, extraEnv, args...)
	return strings.TrimSpace(string(out)), err
}

func applyGitBytes(dir string, extraEnv []string, args ...string) ([]byte, error) {
	baseArgs := []string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "core.commitGraph=false", "-c", "commit.gpgsign=false", "-c", "tag.gpgSign=false"}
	cmd := trail.Command(dir, append(baseArgs, args...)...)
	cmd.Env = append(append(cmd.Env, "GIT_GRAFT_FILE="+os.DevNull), extraEnv...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		stderr := ""
		if errors.As(err, &ee) {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return out, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, stderr)
	}
	return out, nil
}

func (r *run) applyFail(runnerWhy, branch, why string) (string, []Finding) {
	r.failed = true
	msg := withRunner(fmt.Sprintf("FAIL (%s); branch %s kept", oneLine(why), branch), runnerWhy)
	return msg, []Finding{{"worker", "failed", branch, msg}}
}

func (r *run) checkoutMovedFail(runnerWhy, class, branch string) (string, []Finding) {
	r.failed = true
	msg := withRunner(fmt.Sprintf("FAIL (checkout moved); branch %s kept", branch), runnerWhy)
	return "checkout moved; branch " + branch + " kept", []Finding{{"worker", class, branch, msg}}
}

// readGitBlob returns the blob bytes at rev:path, or (nil, nil) if the path
// does not exist in rev's tree. Any git or object read error fails closed.
func readGitBlob(root, rev, path string) ([]byte, error) {
	spec := rev + ":" + path
	if _, err := applyGitBytes(root, nil, "cat-file", "-e", "--end-of-options", spec); err != nil {
		out, lsErr := applyGit(root, nil, "ls-tree", "-z", rev, "--", path)
		if lsErr == nil && out == "" {
			return nil, nil
		}
		return nil, err
	}
	return applyGitBytes(root, nil, "show", "--end-of-options", spec)
}

// applyBranch runs § The apply step (steps 1–7).
func (r *run) applyBranch(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res isolate.WorkerResult) (string, []Finding) {
	if r.o.Apply != nil {
		return r.applyWithHook(root, t, k, runnerWhy)
	}
	if !r.again(root) {
		msg := withRunner(fmt.Sprintf("stopped before apply (%s); branch %s kept", r.verdict, k.Branch), runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	snap, err := takeCheckoutSnapshot(root)
	if err != nil || snapErr != nil || snap != snapBefore || (res.Base != "" && res.Base != snap.Head) {
		return r.checkoutMovedFail(runnerWhy, t.Class, k.Branch)
	}
	agentsBytes, err := readGitBlob(root, snap.Head, "AGENTS.md")
	if err != nil {
		return r.applyFail(runnerWhy, k.Branch, "read AGENTS.md: "+err.Error())
	}
	tests := parseAutoTests(string(agentsBytes))
	if len(tests) == 0 {
		id, askF := r.fileNoTestAsk(root, t, k.Branch)
		msg := withRunner(fmt.Sprintf("PASS (no # auto: test line; proposal %s); branch %s kept", id, k.Branch), runnerWhy)
		return msg, []Finding{askF, {"worker", t.Class, k.Branch, msg}}
	}
	if err := verifyBranchForApply(root, snap.Head, k.Branch, t.Class); err != nil {
		return r.applyFail(runnerWhy, k.Branch, err.Error())
	}
	return r.applyInWorktree(root, t, k, runner, runnerWhy, snap, tests, res)
}

func (r *run) applyWithHook(root string, t Task, k Kickoff, runnerWhy string) (string, []Finding) {
	applied, err := r.o.Apply(root, k.Branch, t.Class)
	if err != nil {
		r.failed = true
		msg := withRunner(fmt.Sprintf("FAIL (%s); branch %s kept", oneLine(err.Error()), k.Branch), runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	if applied {
		if err := deleteBranch(root, k.Branch); err != nil {
			r.failed = true
			msg := withRunner(fmt.Sprintf("FAIL (branch delete: %s); branch %s kept", oneLine(err.Error()), k.Branch), runnerWhy)
			return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
		}
		r.appliedSHA = "applied-mock"
		r.appliedAny = true
		r.appliedSignalID, r.appliedReportDate = t.SignalID, t.ReportDate
		msg := withRunner(fmt.Sprintf("PASS; applied, branch %s deleted", k.Branch), runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	msg := withRunner(fmt.Sprintf("PASS; branch %s kept", k.Branch), runnerWhy)
	return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
}

func (r *run) fileNoTestAsk(root string, t Task, branch string) (string, Finding) {
	body := fmt.Sprintf("# auto ask: %s in %s\n\nBranch `%s` passed for class `%s`, but `AGENTS.md` has no `# auto: test` command under `## Setup commands`. Mark a test command with `# auto: test` or merge `%s` by hand.\n",
		t.Class, oneLine(filepath.Base(root)), branch, t.Class, branch)
	id, created, err := r.upsertProposal(proposalSpec{
		Project: root, Body: body, CustomKey: noTestAskKey(root, t.Class),
		Title:      fmt.Sprintf("auto ask: %s in %s (no # auto: test line, branch %s)", t.Class, oneLine(filepath.Base(root)), branch),
		UpdateBody: fmt.Sprintf("Latest passing branch `%s` for class `%s` (still missing `# auto: test` in `AGENTS.md`).", branch, t.Class),
	})
	if err != nil {
		r.failed = true
		return "failed", Finding{"ask", "failed", t.Class, oneLine(err.Error())}
	}
	if created {
		r.st.OpenAsks++
	}
	return id, Finding{"ask", t.Class, branch, "queued proposal " + id + " (no # auto: test line)"}
}

func (r *run) applyInWorktree(root string, t Task, k Kickoff, runner, runnerWhy string, snap checkoutSnapshot, tests []string, res isolate.WorkerResult) (string, []Finding) {
	wtDir := filepath.Join(dir(r.o.Home), "wt-"+k.Name)
	_ = trail.RemoveWorktree(context.Background(), root, wtDir, true)
	defer func() { _ = trail.RemoveWorktree(context.Background(), root, wtDir, true) }()
	if _, err := applyGit(root, nil, "worktree", "add", "--detach", "--", wtDir, snap.Head); err != nil {
		return r.applyFail(runnerWhy, k.Branch, "worktree add: "+err.Error())
	}
	squashSHA, err := r.squashInWorktree(root, wtDir, t, k, res)
	if err != nil {
		return r.applyFail(runnerWhy, k.Branch, err.Error())
	}
	if err := r.verifyWorktree(root, wtDir, k.Name, runner, tests); err != nil {
		return r.applyFail(runnerWhy, k.Branch, err.Error())
	}
	if !r.again(root) {
		msg := withRunner(fmt.Sprintf("stopped before apply (%s); branch %s kept", r.verdict, k.Branch), runnerWhy)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	if gateClean(root) != "" || trail.CheckoutUnchanged(context.Background(), root, snap, autoSnapshotOpts) != nil {
		return r.checkoutMovedFail(runnerWhy, t.Class, k.Branch)
	}
	if _, err := applyGit(root, nil, "merge", "--ff-only", "--end-of-options", squashSHA); err != nil {
		return r.applyFail(runnerWhy, k.Branch, "merge --ff-only: "+err.Error())
	}
	r.appliedSHA = shortSHA(squashSHA)
	r.appliedAny = true
	r.appliedSignalID, r.appliedReportDate = t.SignalID, t.ReportDate
	if appliedTouchesSkills(root, snap.Head, squashSHA) {
		// D4 / PRD R6,R9: never run real-home `fyr install` automatically; surface reminder in `fyr auto status`.
		r.installPending = true
		r.st.InstallPending = true
		r.st.InstallPendingSHA = r.appliedSHA
	}
	if err := deleteBranch(root, k.Branch); err != nil {
		return r.applyFail(runnerWhy, k.Branch, "branch delete: "+err.Error())
	}
	msg := withRunner(fmt.Sprintf("PASS; applied %s (revert: git revert %s), branch %s deleted", r.appliedSHA, r.appliedSHA, k.Branch), runnerWhy)
	return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
}

func appliedTouchesSkills(root, base, head string) bool {
	out, err := applyGit(root, nil, "diff", "--name-only", "--end-of-options", base, head, "--")
	if err != nil {
		return false
	}
	for _, raw := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(filepath.ToSlash(raw)), "skills/") {
			return true
		}
	}
	return false
}

func (r *run) squashInWorktree(root, wtDir string, t Task, k Kickoff, res isolate.WorkerResult) (string, error) {
	if _, err := applyGit(wtDir, nil, "merge", "--squash", "--end-of-options", "refs/heads/"+k.Branch); err != nil {
		return "", fmt.Errorf("merge --squash: %w", err)
	}
	userName := cmp.Or(applyGitConfig(root, "user.name"), "fyr auto")
	userEmail := cmp.Or(applyGitConfig(root, "user.email"), "auto@localhost")
	reportID := r.reserveReport(root, filepath.Base(root))
	handoffID := "none"
	if res.Handoff != nil && res.Handoff.ID != "" {
		handoffID = res.Handoff.ID
	}
	subject := redact.Redact(fmt.Sprintf("auto: %s — %s", t.Class, commitOneLine(t, res)))
	body := redact.Redact(formatApplyCommitBody(reportID, handoffID, t))
	env := []string{"GIT_AUTHOR_NAME=" + userName, "GIT_AUTHOR_EMAIL=" + userEmail, "GIT_COMMITTER_NAME=" + userName, "GIT_COMMITTER_EMAIL=" + userEmail}
	if _, err := applyGit(wtDir, env, "commit", "-m", subject, "-m", body); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return applyGit(wtDir, nil, "rev-parse", "--verify", "HEAD")
}

func formatApplyCommitBody(reportID, handoffID string, t Task) string {
	body := fmt.Sprintf("Applied by fyr auto (report %s, handoff %s).", reportID, handoffID)
	if attr := formatTriggerProse(t.SignalID, t.ReportDate); attr != "" {
		body += "\n" + attr
	}
	return body + "\nrevert: git revert HEAD"
}

func applyGitConfig(root, key string) string {
	v, _ := applyGit(root, nil, "config", "--get", "--", key)
	return v
}

func commitOneLine(t Task, res isolate.WorkerResult) string {
	if res.Handoff != nil && strings.TrimSpace(res.Handoff.Title) != "" {
		return oneLine(res.Handoff.Title)
	}
	if len(t.Kept) > 0 {
		return oneLine(strings.Join(t.Kept, ", "))
	}
	return t.Class
}

func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }
