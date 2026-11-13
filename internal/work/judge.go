package work

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

var (
	baseCheckTimeout = 5 * time.Minute
	judgeTimeout     = 10 * time.Minute
)

const maxJudgeDiffBytes = 48 << 10

// runPreBaseCheck runs the Kickoff's Base-Check: command on HEAD before the
// builder session starts. It must fail (non-zero exit) on HEAD to prove the
// check is falsifiable; if it exits 0, the worker fails with
// ReasonBaseCheckPassed and skips the builder session.
func (w *worker) runPreBaseCheck(ctx context.Context) bool {
	if w.p.k.BaseCheck == "" {
		return true
	}
	if !w.b.Intact() {
		return false
	}
	_, err := runBounded(w.baseCheckCmd(), baseCheckTimeout)
	if err == nil {
		w.b.Note("Base-Check %q passed on HEAD before the worker started (not falsifiable)", w.p.k.BaseCheck)
		w.b.Fail(ReasonBaseCheckPassed)
		w.tracker.onBaseCheck(w.p.k.Name, "pre", "passed-on-HEAD (FAIL)")
		return false
	}
	cleanBaseCheckArtifacts(w.b.App())
	w.b.Note("Base-Check %q failed on HEAD as expected before build", w.p.k.BaseCheck)
	w.tracker.onBaseCheck(w.p.k.Name, "pre", "pre-ok")
	return ctx.Err() == nil
}

// runPostBaseCheck runs the Kickoff's Base-Check: command after After(),
// ExitChecks(), fetch(), and scope() have verified sandbox integrity and
// file-scope compliance. It must exit 0; otherwise the worker fails with
// ReasonBaseCheckFailed.
func (w *worker) runPostBaseCheck() {
	if w.p.k.BaseCheck == "" || len(w.b.Reasons()) > 0 || !w.b.Intact() {
		return
	}
	out, err := runBounded(w.baseCheckCmd(), baseCheckTimeout)
	w.baseCheckOut = strings.TrimSpace(string(out))
	if err != nil {
		w.b.Note("Base-Check %q failed after build: %v: %s", w.p.k.BaseCheck, err, lastLine(string(out)))
		w.b.Fail(ReasonBaseCheckFailed)
		w.tracker.onBaseCheck(w.p.k.Name, "post", "FAIL")
		return
	}
	w.tracker.onBaseCheck(w.p.k.Name, "post", "PASS")
}

func (w *worker) baseCheckCmd() *exec.Cmd {
	cmd := exec.Command("sh", "-c", w.p.k.BaseCheck)
	cmd.Dir = w.b.App()
	var env []string
	for _, kv := range sandbox.FyrEnv() {
		k, _, _ := strings.Cut(kv, "=")
		if k != "HOME" && k != "PWD" && k != "OLDPWD" {
			env = append(env, kv)
		}
	}
	binDir := filepath.Join(w.b.Home(), ".local", "bin")
	pathVal := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	var finalEnv []string
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != "PATH" {
			finalEnv = append(finalEnv, kv)
		}
	}
	cmd.Env = append(finalEnv, "HOME="+w.b.Home(), "PWD="+w.b.App(), "PATH="+pathVal)
	return cmd
}

func cleanBaseCheckArtifacts(app string) {
	_ = trail.Command(app, "checkout", "-q", "--", ".").Run()
	_ = trail.Command(app, "clean", "-fdq", "-e", ".agents/scratch").Run()
}

// runJudge evaluates the Kickoff's Judge: criterion in an isolated read-only
// cross-family agy session over git diff base..HEAD and verification output,
// with zero access to the builder's transcript or reasoning.
func (w *worker) runJudge() {
	if w.p.k.Judge == "" || len(w.b.Reasons()) > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), judgeTimeout)
	defer cancel()

	agy := w.p.agy
	if agy == "" {
		agy, _ = exec.LookPath("agy")
	}
	if agy == "" {
		w.b.Note("judge: agy not found on PATH")
		w.b.Fail(ReasonJudgeFailed)
		return
	}
	judgeModel, fallbackReason, _ := ResolveRoleModel(ctx, agy, "judge", "opus", w.p.k.JudgeModel)
	if fallbackReason != "" {
		w.b.Note("judge model: %s (%s)", judgeModel, fallbackReason)
	}
	w.tracker.onJudge(w.p.k.Name, judgeModel, "running", "")
	prompt := w.buildJudgePrompt(judgeModel)
	report, err := execJudgeAgy(ctx, agy, judgeModel, prompt)
	w.writeJudgeArtifacts(judgeModel, report, err)
	if err != nil {
		w.b.Note("judge (%s) execution failed: %v", judgeModel, err)
		w.b.Fail(ReasonJudgeFailed)
		w.tracker.onJudge(w.p.k.Name, judgeModel, "FAIL", err.Error())
		return
	}
	passed, reason := parseJudgeVerdict(report)
	if !passed {
		w.b.Note("judge (%s): FAIL — %s", judgeModel, reason)
		w.b.Fail(ReasonJudgeFailed)
		w.tracker.onJudge(w.p.k.Name, judgeModel, "FAIL", reason)
		return
	}
	w.b.Note("judge (%s): PASS — %s", judgeModel, reason)
	w.tracker.onJudge(w.p.k.Name, judgeModel, "PASS", reason)
}

func (w *worker) buildJudgePrompt(judgeModel string) string {
	ref := w.res.Head
	if ref == "" {
		ref = "refs/heads/" + w.p.k.Branch()
	}
	logOut, _ := branchOutput(leadGit(w.o.Root, "log", "--oneline", w.p.base+".."+ref), w.o.Runner)
	diffOut, _ := branchOutput(leadGit(w.o.Root, "diff", "--no-ext-diff", w.p.base+".."+ref), w.o.Runner)
	if len(diffOut) > maxJudgeDiffBytes {
		diffOut = append(diffOut[:maxJudgeDiffBytes], []byte("\n... [diff truncated]\n")...)
	}
	var sb strings.Builder
	sb.WriteString("[FYR_JUDGE] You are an independent cross-family Judge evaluating a worker's committed diff against its Judge criterion.\n")
	sb.WriteString("You have ZERO access to the builder's transcript or reasoning; evaluate strictly from the criterion, diff, and verification output below.\n\n")
	fmt.Fprintf(&sb, "Worker: %s\nBuilder-Model: %s\nJudge-Model: %s\n", w.p.k.Name, w.res.Model, judgeModel)
	fmt.Fprintf(&sb, "Files-Scope: %s\n", strings.Join(w.p.k.Files, ", "))
	fmt.Fprintf(&sb, "Judge-Criterion: %s\n\n", w.p.k.Judge)
	fmt.Fprintf(&sb, "## Commits (%s..%s)\n```\n%s\n```\n\n", short(w.p.base), short(ref), strings.TrimSpace(string(logOut)))
	if w.baseCheckOut != "" {
		fmt.Fprintf(&sb, "## Verification / Base-Check Output\n```\n%s\n```\n\n", w.baseCheckOut)
	}
	fmt.Fprintf(&sb, "## Diff\n```diff\n%s\n```\n\n", strings.TrimSpace(string(diffOut)))
	sb.WriteString("Write a concise review report and end with a final line in the exact format:\nVERDICT: PASS — <one-line reason>\nor\nVERDICT: FAIL — <one-line reason>\n")
	return sb.String()
}

func execJudgeAgy(run context.Context, agy, model, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(run, judgeTimeout)
	defer cancel()
	args := []string{"-p", prompt}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, agy, args...)
	cmd.Dir = "/"
	cmd.Env = rootEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &capped{over: errModelsOver}, &capped{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	switch {
	case stdout.full:
		err = errModelsOver
	case run.Err() != nil:
		err = errors.New("the run ended before it answered")
	case ctx.Err() == context.DeadlineExceeded:
		err = fmt.Errorf("no answer in %s", judgeTimeout)
	}
	if err != nil {
		if l := lastLine(stderr.String()); l != "" {
			return stdout.String(), fmt.Errorf("%v: %s", err, l)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

func parseJudgeVerdict(report string) (bool, string) {
	lines := strings.Split(strings.TrimSpace(report), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if rest, ok := strings.CutPrefix(line, "VERDICT:"); ok {
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, "PASS") {
				reason := strings.TrimSpace(strings.TrimPrefix(rest, "PASS"))
				reason = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(reason, "—"), "-"))
				if reason == "" {
					reason = "criterion satisfied"
				}
				return true, reason
			}
			if strings.HasPrefix(rest, "FAIL") {
				reason := strings.TrimSpace(strings.TrimPrefix(rest, "FAIL"))
				reason = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(reason, "—"), "-"))
				if reason == "" {
					reason = "criterion not satisfied"
				}
				return false, reason
			}
		}
	}
	return false, "missing VERDICT: PASS|FAIL line in judge output"
}

func (w *worker) writeJudgeArtifacts(judgeModel, report string, execErr error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Cross-Family Judge Review — `%s`\n\n", w.p.k.Name)
	fmt.Fprintf(&sb, "- **Criterion:** %s\n", w.p.k.Judge)
	fmt.Fprintf(&sb, "- **Builder Model:** `%s`\n", w.res.Model)
	fmt.Fprintf(&sb, "- **Judge Model:** `%s`\n\n", judgeModel)
	if execErr != nil {
		fmt.Fprintf(&sb, "- **Execution Error:** `%v`\n\n", execErr)
	}
	sb.WriteString(strings.TrimSpace(report))
	sb.WriteString("\n")
	data := []byte(sb.String())

	if dest, err := w.leadDir(); err == nil {
		if err := writeDraft(dest, "review-judge.md", data); err == nil {
			w.res.Drafts++
			if w.res.DraftsDir == "" {
				w.res.DraftsDir = filepath.ToSlash(filepath.Join(".agents", "scratch", "work", w.stamp, w.p.k.Name)) + "/"
			}
		}
	}
}
