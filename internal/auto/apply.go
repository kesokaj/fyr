package auto

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/work"
)

// checkoutSnapshot records the checkout state in step 2 and before the worker.
type checkoutSnapshot struct {
	head      string
	branch    string
	headLog   string
	branchLog string
}

func takeCheckoutSnapshot(root string) (checkoutSnapshot, error) {
	if stop := gateClean(root); stop != "" {
		return checkoutSnapshot{}, errors.New(stop)
	}
	head, err := applyGit(root, nil, "rev-parse", "--verify", "HEAD")
	if err != nil || head == "" {
		return checkoutSnapshot{}, errors.New("cannot read HEAD")
	}
	branch, err := applyGit(root, nil, "symbolic-ref", "-q", "HEAD")
	if err != nil || branch == "" {
		return checkoutSnapshot{}, errors.New("HEAD is detached")
	}
	headLog, err := applyGit(root, nil, "reflog", "show", "-n", "1", "--date=iso-strict", "--format=%H %gd %gs", "HEAD", "--")
	if err != nil {
		return checkoutSnapshot{}, errors.New("cannot read HEAD reflog")
	}
	branchLog, err := applyGit(root, nil, "reflog", "show", "-n", "1", "--date=iso-strict", "--format=%H %gd %gs", branch, "--")
	if err != nil {
		return checkoutSnapshot{}, errors.New("cannot read branch reflog")
	}
	return checkoutSnapshot{head: head, branch: branch, headLog: headLog, branchLog: branchLog}, nil
}

func checkoutUnchanged(root string, want checkoutSnapshot) bool {
	got, err := takeCheckoutSnapshot(root)
	return err == nil && got == want
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

// parseAutoTests extracts command lines under `## Setup commands` in AGENTS.md
// that sit inside a fenced code block and end with `# auto: test`.
func parseAutoTests(agentsMD string) []string {
	var cmds []string
	inSetup, fence := false, ""
	for _, raw := range strings.Split(agentsMD, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if fence == "" {
			switch {
			case strings.HasPrefix(line, "## "):
				inSetup = (line == "## Setup commands")
			case strings.HasPrefix(line, "# "):
				inSetup = false
			case inSetup && (strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")):
				fence = line[:3]
			}
			continue
		}
		if strings.HasPrefix(line, fence) {
			fence = ""
			continue
		}
		if rawCmd, ok := strings.CutSuffix(line, "# auto: test"); ok {
			cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawCmd), "$ "))
			if cmd != "" && !strings.HasPrefix(cmd, "#") {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

func (r *run) applyFail(runnerWhy, branch, why string) (string, []Finding) {
	r.failed = true
	msg := fmt.Sprintf("runner %s; FAIL (%s); branch %s kept", runnerWhy, oneLine(why), branch)
	return msg, []Finding{{"worker", "failed", branch, msg}}
}

func (r *run) checkoutMovedFail(runnerWhy, class, branch string) (string, []Finding) {
	r.failed = true
	msg := fmt.Sprintf("runner %s; FAIL (checkout moved); branch %s kept", runnerWhy, branch)
	return "checkout moved; branch " + branch + " kept", []Finding{{"worker", class, branch, msg}}
}

// applyBranch runs § The apply step (steps 1–7) for an OutcomeApplied task.
func (r *run) applyBranch(root string, t Task, k Kickoff, runner, runnerWhy string, snapBefore checkoutSnapshot, snapErr error, res work.Result) (string, []Finding) {
	if !r.again(root) {
		msg := fmt.Sprintf("runner %s; stopped before apply (%s); branch %s kept", runnerWhy, r.verdict, k.Branch)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	snap, err := takeCheckoutSnapshot(root)
	if err != nil || snapErr != nil || snap != snapBefore || (res.Base != "" && res.Base != snap.head) {
		return r.checkoutMovedFail(runnerWhy, t.Class, k.Branch)
	}
	agentsBytes, _ := applyGitBytes(root, nil, "show", snap.head+":AGENTS.md")
	tests := parseAutoTests(string(agentsBytes))
	if len(tests) == 0 {
		id, askF := r.fileNoTestAsk(root, t, k.Branch)
		msg := fmt.Sprintf("runner %s; PASS (no # auto: test line; proposal %s); branch %s kept", runnerWhy, id, k.Branch)
		return msg, []Finding{askF, {"worker", t.Class, k.Branch, msg}}
	}
	if err := verifyBranchForApply(root, snap.head, k.Branch, t.Class); err != nil {
		return r.applyFail(runnerWhy, k.Branch, err.Error())
	}
	return r.applyInWorktree(root, t, k, runner, runnerWhy, snap, tests, res)
}

func (r *run) fileNoTestAsk(root string, t Task, branch string) (string, Finding) {
	body := fmt.Sprintf("# auto ask: %s in %s\n\nBranch `%s` passed for class `%s`, but `AGENTS.md` has no `# auto: test` command under `## Setup commands`. Mark a test command with `# auto: test` or merge `%s` by hand.\n",
		t.Class, oneLine(filepath.Base(root)), branch, t.Class, branch)
	id, err := brain.Add(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.AddInput{
		Kind:    "proposal",
		Title:   fmt.Sprintf("auto ask: %s in %s (no # auto: test line, branch %s)", t.Class, oneLine(filepath.Base(root)), branch),
		Project: root,
		Body:    strings.NewReader(redact.Redact(body)),
	})
	if err != nil {
		r.failed = true
		return "failed", Finding{"ask", "failed", t.Class, oneLine(err.Error())}
	}
	r.st.OpenAsks++
	return id, Finding{"ask", t.Class, branch, "queued proposal " + id + " (no # auto: test line)"}
}

func (r *run) applyInWorktree(root string, t Task, k Kickoff, runner, runnerWhy string, snap checkoutSnapshot, tests []string, res work.Result) (string, []Finding) {
	wtDir := filepath.Join(dir(r.o.Home), "wt-"+k.Name)
	removeWorktree(root, wtDir)
	defer removeWorktree(root, wtDir)
	if _, err := applyGit(root, nil, "worktree", "add", "--detach", wtDir, snap.head); err != nil {
		return r.applyFail(runnerWhy, k.Branch, "worktree add: "+err.Error())
	}
	squashSHA, err := r.squashInWorktree(root, wtDir, t, k, res)
	if err != nil {
		return r.applyFail(runnerWhy, k.Branch, err.Error())
	}
	if err := r.verifyWorktree(wtDir, runner, tests); err != nil {
		return r.applyFail(runnerWhy, k.Branch, err.Error())
	}
	if !r.again(root) {
		msg := fmt.Sprintf("runner %s; stopped before apply (%s); branch %s kept", runnerWhy, r.verdict, k.Branch)
		return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
	}
	if !checkoutUnchanged(root, snap) {
		return r.checkoutMovedFail(runnerWhy, t.Class, k.Branch)
	}
	if _, err := applyGit(root, nil, "merge", "--ff-only", squashSHA); err != nil {
		return r.applyFail(runnerWhy, k.Branch, "merge --ff-only: "+err.Error())
	}
	r.appliedSHA = shortSHA(squashSHA)
	if err := deleteBranch(root, k.Branch); err != nil {
		return r.applyFail(runnerWhy, k.Branch, "branch delete: "+err.Error())
	}
	msg := fmt.Sprintf("runner %s; PASS; applied %s (revert: git revert %s), branch %s deleted", runnerWhy, r.appliedSHA, r.appliedSHA, k.Branch)
	return msg, []Finding{{"worker", t.Class, k.Branch, msg}}
}

func (r *run) squashInWorktree(root, wtDir string, t Task, k Kickoff, res work.Result) (string, error) {
	if _, err := applyGit(wtDir, nil, "merge", "--squash", "refs/heads/"+k.Branch); err != nil {
		return "", fmt.Errorf("merge --squash: %w", err)
	}
	userName := cmp.Or(gitConfigValue(root, "user.name"), "fyr auto")
	userEmail := cmp.Or(gitConfigValue(root, "user.email"), "auto@localhost")
	reportID := r.reserveReport(root, filepath.Base(root))
	handoffID := "none"
	if res.Handoff != nil && res.Handoff.ID != "" {
		handoffID = res.Handoff.ID
	}
	subject := fmt.Sprintf("auto: %s — %s", t.Class, commitOneLine(t, res))
	body := fmt.Sprintf("Applied by fyr auto (report %s, handoff %s).\nrevert: git revert HEAD", reportID, handoffID)
	env := []string{"GIT_AUTHOR_NAME=" + userName, "GIT_AUTHOR_EMAIL=" + userEmail, "GIT_COMMITTER_NAME=" + userName, "GIT_COMMITTER_EMAIL=" + userEmail}
	if _, err := applyGit(wtDir, env, "commit", "-m", subject, "-m", body); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return applyGit(wtDir, nil, "rev-parse", "--verify", "HEAD")
}

func gitConfigValue(root, key string) string {
	v, _ := applyGit(root, nil, "config", "--get", key)
	return v
}

func commitOneLine(t Task, res work.Result) string {
	if res.Handoff != nil && strings.TrimSpace(res.Handoff.Title) != "" {
		return oneLine(res.Handoff.Title)
	}
	if len(t.Kept) > 0 {
		return oneLine(strings.Join(t.Kept, ", "))
	}
	return t.Class
}

func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

func (r *run) verifyWorktree(wtDir, runner string, tests []string) error {
	for _, cmdLine := range tests {
		if err := r.o.testRunner()(wtDir, runner, cmdLine); err != nil {
			return fmt.Errorf("marked test %q: %w", cmdLine, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(wtDir, "PRD.md")); err == nil {
		rep, cerr := check.Run(wtDir, r.o.Vocabulary)
		if cerr != nil {
			return fmt.Errorf("fyr check: %w", cerr)
		}
		if len(rep.Findings) > 0 {
			return fmt.Errorf("fyr check: %d findings", len(rep.Findings))
		}
	}
	return nil
}

func removeWorktree(root, wtDir string) {
	_, _ = applyGit(root, nil, "worktree", "remove", "--force", wtDir)
	_ = os.RemoveAll(wtDir)
	_, _ = applyGit(root, nil, "worktree", "prune")
}

// verifyBranchForApply checks that refs/heads/<branch> descends from base with
// at least one commit and touches no rubric-denied path, instruction file,
// symlink, submodule or denied trail record.
func verifyBranchForApply(root, base, branch, class string) error {
	ref := "refs/heads/" + branch
	tip, err := applyGit(root, nil, "rev-parse", "--verify", ref)
	if err != nil || tip == "" {
		return fmt.Errorf("missing branch %s", branch)
	}
	if _, err := applyGit(root, nil, "merge-base", "--is-ancestor", base, ref); err != nil {
		return fmt.Errorf("branch %s does not descend from base", branch)
	}
	countStr, err := applyGit(root, nil, "rev-list", "--count", base+".."+ref)
	if n, nerr := strconv.Atoi(countStr); err != nil || nerr != nil || n < 1 {
		return fmt.Errorf("branch %s has no commits past base", branch)
	}
	rawDiff, err := applyGitBytes(root, nil, "diff", "--raw", "-z", "--no-renames", base, ref)
	if err != nil {
		return err
	}
	return checkRawDiff(root, base, ref, class, rawDiff)
}

func checkRawDiff(root, base, ref, class string, rawDiff []byte) error {
	spec, _ := lookupClass(class)
	parts := strings.Split(string(rawDiff), "\x00")
	trailChanged := false
	for i := 0; i+1 < len(parts); i += 2 {
		meta, path := parts[i], parts[i+1]
		if meta == "" || path == "" {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) >= 5 && fields[4] == "D" {
			return fmt.Errorf("branch deletes file %s", path)
		}
		if len(fields) >= 2 && (fields[1] == "120000" || fields[1] == "160000") {
			return fmt.Errorf("branch adds symlink or submodule %s", path)
		}
		if Denied(path) || (!spec.Instructions && Instruction(path)) {
			return fmt.Errorf("branch touches rubric-denied path %s", path)
		}
		trailChanged = trailChanged || path == ".agents/trail.jsonl"
	}
	if trailChanged {
		return verifyTrailForApply(root, base, ref)
	}
	return nil
}

func verifyTrailForApply(root, base, ref string) error {
	baseTrail, _ := applyGitBytes(root, nil, "show", base+":.agents/trail.jsonl")
	refTrail, err := applyGitBytes(root, nil, "show", ref+":.agents/trail.jsonl")
	if err != nil {
		return fmt.Errorf("cannot read branch trail: %w", err)
	}
	var baseRecs []brain.Record
	if len(bytes.TrimSpace(baseTrail)) > 0 {
		if baseRecs, err = brain.ParseTrail(bytes.NewReader(baseTrail)); err != nil {
			return fmt.Errorf("invalid base trail: %w", err)
		}
	}
	refRecs, err := brain.ParseTrail(bytes.NewReader(refTrail))
	if err != nil {
		return fmt.Errorf("invalid trail record on branch: %w", err)
	}
	baseByID := make(map[string]brain.Record, len(baseRecs))
	for _, r := range baseRecs {
		baseByID[r.ID] = r
	}
	refByID := make(map[string]brain.Record, len(refRecs))
	for _, r := range refRecs {
		refByID[r.ID] = r
	}
	for _, b := range baseRecs {
		if _, ok := refByID[b.ID]; !ok {
			return fmt.Errorf("branch deletes record %s", b.ID)
		}
	}
	for _, r := range refRecs {
		if b, ok := baseByID[r.ID]; (!ok || r != b) && (deniedTrailRecord(r) || (ok && deniedTrailRecord(b))) {
			return fmt.Errorf("branch touches rubric-denied record %s", r.ID)
		}
	}
	return nil
}

func deniedTrailRecord(r brain.Record) bool {
	return Denied(r.ID) || strings.EqualFold(r.Kind, "adr") || (strings.EqualFold(r.Kind, "plan") && strings.Contains(r.Body, deniedBox))
}
