package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/gate"
)

const (
	DefaultSessionTimeout = 30 * time.Minute
	maxSessionOutputBytes = 1 << 20
	killGrace             = 3 * time.Second
)

// SessionRequest holds inputs for one isolated fresh `agy -p` phase session.
type SessionRequest struct {
	RepoDir, Home, PlanID, PhaseID, AgyPath, Model, Prompt, PrintTimeout string
	Timeout                                                              time.Duration
}

// SessionResult captures exit status, output, attribution, and duration of a phase session.
type SessionResult struct {
	ExitCode        int
	Stdout, Stderr  string
	Duration        time.Duration
	WorkerModel     string
	WorkerSessionID string
}

// SessionRunner executes one fresh phase session; injectable for tests.
type SessionRunner func(ctx context.Context, req SessionRequest) (SessionResult, error)

// BuildAgyArgs builds argv for one fresh `agy -p` session (never `--dangerously-skip-permissions`).
func BuildAgyArgs(req SessionRequest) []string {
	pt := strings.TrimSpace(req.PrintTimeout)
	if pt == "" {
		t := req.Timeout
		if t <= 0 {
			t = DefaultSessionTimeout
		}
		pt = t.String()
	}
	args := []string{"-p", req.Prompt, "--disable-slash-commands", "--output-format", "json", "--print-timeout", pt}
	if m := strings.TrimSpace(req.Model); m != "" && isValidModelID(m) {
		args = append(args, "--model", m)
	}
	return args
}

// RunAgySession executes one isolated `agy -p` subprocess in req.RepoDir with a
// bounded timeout and process-group cleanup.
func RunAgySession(ctx context.Context, req SessionRequest) (SessionResult, error) {
	if m := strings.TrimSpace(req.Model); m != "" && !isValidModelID(m) {
		return SessionResult{ExitCode: -1}, fmt.Errorf("invalid model ID %q", m)
	}
	if len(req.Prompt) >= MaxKickoffBytes {
		return SessionResult{ExitCode: -1}, fmt.Errorf("kickoff prompt too large: %d bytes (limit < %d)", len(req.Prompt), MaxKickoffBytes)
	}
	bin := strings.TrimSpace(req.AgyPath)
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("agy"); err != nil {
			return SessionResult{ExitCode: -1}, fmt.Errorf("agy not found on PATH: %w", err)
		}
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultSessionTimeout
	}
	cmd := exec.Command(bin, BuildAgyArgs(req)...)
	cmd.Dir = req.RepoDir
	cmd.Env = relaySessionEnv(req)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	res, err := execSessionCmd(ctx, cmd, timeout)
	res.WorkerModel, res.WorkerSessionID = parseSessionAttribution(res.Stdout, req.Model)
	return res, err
}

type agySessionEnvelope struct {
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id"`
	Model          string `json:"model"`
	ModelID        string `json:"model_id"`
}

func parseSessionAttribution(stdout, fallbackModel string) (model, sessionID string) {
	model = strings.TrimSpace(fallbackModel)
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return model, ""
	}
	if m, s, ok := decodeSessionEnvelope(trimmed); ok {
		return firstNonEmptyStr(m, model), s
	}
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") {
			if m, s, ok := decodeSessionEnvelope(line); ok {
				return firstNonEmptyStr(m, model), s
			}
		}
	}
	return model, ""
}

func decodeSessionEnvelope(raw string) (model, sessionID string, ok bool) {
	var env agySessionEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return "", "", false
	}
	model = firstNonEmptyStr(env.Model, env.ModelID)
	sessionID = firstNonEmptyStr(env.ConversationID, env.SessionID)
	return model, sessionID, model != "" || sessionID != ""
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func relaySessionEnv(req SessionRequest) []string {
	env := append(withoutGitRepoVars(os.Environ()), "FYR_RELAY=1")
	if req.PlanID != "" {
		env = append(env, "FYR_RELAY_PLAN="+req.PlanID)
	}
	if req.PhaseID != "" {
		env = append(env, "FYR_RELAY_PHASE="+req.PhaseID)
	}
	return env
}

func execSessionCmd(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (SessionResult, error) {
	start := time.Now()
	stdout, stderr := &cappedBuf{limit: maxSessionOutputBytes}, &cappedBuf{limit: maxSessionOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = killGrace
	if err := cmd.Start(); err != nil {
		return SessionResult{ExitCode: -1, Duration: time.Since(start)}, err
	}
	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	err := awaitSession(ctx, done, pgid, timeout)
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		err = nil
	}
	return SessionResult{ExitCode: cmdExitCode(err), Stdout: stdout.buf.String(), Stderr: stderr.buf.String(), Duration: time.Since(start)}, err
}

func awaitSession(ctx context.Context, done <-chan error, pgid int, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return err
	case <-timer.C:
		killProcessGroup(done, pgid)
		return fmt.Errorf("agy session timed out after %s", timeout)
	case <-ctx.Done():
		killProcessGroup(done, pgid)
		return ctx.Err()
	}
}

func killProcessGroup(done <-chan error, pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(killGrace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
}

func cmdExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

type cappedBuf struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuf) Write(p []byte) (int, error) {
	if rem := c.limit - c.buf.Len(); rem > 0 {
		c.buf.Write(p[:min(len(p), rem)])
	}
	return len(p), nil
}

var droppedGitVars = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true, "GIT_OBJECT_DIRECTORY": true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_COMMON_DIR": true, "GIT_PREFIX": true,
	"GIT_SUPER_PREFIX": true, "GIT_GRAFT_FILE": true, "GIT_NAMESPACE": true,
	"GIT_IMPLICIT_WORK_TREE": true, "GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true,
}

func withoutGitRepoVars(env []string) []string {
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !droppedGitVars[k] {
			out = append(out, kv)
		}
	}
	return append(out, "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
}

func gitBytes(ctx context.Context, root string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = root, withoutGitRepoVars(os.Environ()), &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

func gitDirtyEntries(ctx context.Context, root string) ([]string, error) {
	out, err := gitBytes(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{"?? " + gate.StampRelPath: true, "?? .agents/scratch/stop-nudge.stamp": true}
	var dirty []string
	for _, raw := range bytes.Split(out, []byte{0}) {
		if s := string(raw); s != "" && !allowed[s] {
			dirty = append(dirty, s)
		}
	}
	return dirty, nil
}

func gitHead(ctx context.Context, root string) (string, error) {
	out, err := gitBytes(ctx, root, "rev-parse", "HEAD")
	return strings.TrimSpace(string(out)), err
}

func gitLines(ctx context.Context, root string, args ...string) ([]string, error) {
	out, err := gitBytes(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	return lines, nil
}

func gitTags(ctx context.Context, root string) (map[string]bool, error) {
	list, err := gitLines(ctx, root, "tag", "--list")
	if err != nil {
		return nil, err
	}
	tags := make(map[string]bool, len(list))
	for _, t := range list {
		tags[t] = true
	}
	return tags, nil
}

func newTagsBetween(before, after map[string]bool) []string {
	var added []string
	for t := range after {
		if !before[t] {
			added = append(added, t)
		}
	}
	sort.Strings(added)
	return added
}

func gitDiffNames(ctx context.Context, root, before, after string) ([]string, error) {
	return gitLines(ctx, root, "diff", "--name-only", "--no-renames", before+".."+after)
}
