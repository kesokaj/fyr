// Package gate implements deterministic verification, worktree gating,
// landing, and session close for fyr build control.
package gate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// ErrRefused marks precondition or input refusals (wrong branch, dirty tree,
// concurrent mutation, missing/stale stamp).
var ErrRefused = errors.New("refused")

// ErrFailed marks a failing deterministic gate or verification check.
var ErrFailed = errors.New("failed")

// Refusal wraps ErrRefused with a human-readable cause.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return "refused: " + r.Reason }
func (r *Refusal) Unwrap() error { return ErrRefused }

// Failure wraps ErrFailed with a human-readable cause.
type Failure struct{ Reason string }

func (f *Failure) Error() string { return "failed: " + f.Reason }
func (f *Failure) Unwrap() error { return ErrFailed }

func refusef(format string, args ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, args...)}
}

func failf(format string, args ...any) error {
	return &Failure{Reason: fmt.Sprintf(format, args...)}
}

var droppedGitVars = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR":                   true,
	"GIT_PREFIX":                       true,
	"GIT_SUPER_PREFIX":                 true,
	"GIT_GRAFT_FILE":                   true,
	"GIT_NAMESPACE":                    true,
	"GIT_IMPLICIT_WORK_TREE":           true,
	"GIT_CONFIG_PARAMETERS":            true,
	"GIT_CONFIG_COUNT":                 true,
}

func withoutGitRepoVars(env []string) []string {
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if droppedGitVars[k] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
}

func gitCmd(ctx context.Context, root string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = withoutGitRepoVars(os.Environ())
	return cmd
}

func gitBytes(ctx context.Context, root string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := gitCmd(ctx, root, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git %s: %w", args[0], err)
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return stdout.Bytes(), nil
}

func gitLine(ctx context.Context, root string, args ...string) (string, error) {
	out, err := gitBytes(ctx, root, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type checkoutSnapshot struct {
	branch     string
	head       string
	statusHash string
}

func takeSnapshot(ctx context.Context, root string) (checkoutSnapshot, error) {
	head, err := gitLine(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return checkoutSnapshot{}, err
	}
	branch, err := gitLine(ctx, root, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		branch = ""
	}
	st, err := gitBytes(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return checkoutSnapshot{}, err
	}
	sum := sha256.Sum256(st)
	return checkoutSnapshot{
		branch:     branch,
		head:       head,
		statusHash: hex.EncodeToString(sum[:]),
	}, nil
}

func checkoutUnchanged(ctx context.Context, root string, before checkoutSnapshot) error {
	now, err := takeSnapshot(ctx, root)
	if err != nil {
		return err
	}
	if now.head != before.head || now.branch != before.branch || now.statusHash != before.statusHash {
		return refusef("checkout changed concurrently during operation")
	}
	return nil
}

func checkScratchNoSymlinks(root string) error {
	for _, rel := range []string{"", ".agents", filepath.Join(".agents", "scratch")} {
		p := root
		if rel != "" {
			p = filepath.Join(root, rel)
		}
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return refusef("refusing symlinked path %s", p)
		}
		if !info.IsDir() {
			return refusef("expected directory at %s", p)
		}
	}
	return nil
}

func hasForbiddenTrailer(commitBody string) (string, bool) {
	for _, raw := range strings.Split(commitBody, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if strings.HasPrefix(line, "claude-session:") {
			return "Claude-Session", true
		}
		if strings.HasPrefix(line, "co-authored-by:") {
			return "Co-Authored-By", true
		}
	}
	return "", false
}

func gitConfigValue(ctx context.Context, root, key string) string {
	v, err := gitLine(ctx, root, "config", "--get", "--", key)
	if err != nil {
		return ""
	}
	return v
}

func removeWorktree(ctx context.Context, mainRoot, wtDir string) error {
	_, err := gitBytes(ctx, mainRoot, "worktree", "remove", "--force", "--", wtDir)
	if err != nil {
		_, _ = gitBytes(ctx, mainRoot, "worktree", "prune")
		return err
	}
	return nil
}

func verifyCommitRef(ctx context.Context, root, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", refusef("invalid ref %q", ref)
	}
	return gitLine(ctx, root, "rev-parse", "--verify", ref+"^{commit}")
}

func checkoutDirty(ctx context.Context, root string) (bool, error) {
	dirty, err := trail.Dirty(root)
	if err != nil || !dirty {
		return dirty, err
	}
	if _, err := gitBytes(ctx, root, "diff", "--quiet"); err != nil {
		return true, nil
	}
	if _, err := gitBytes(ctx, root, "diff", "--cached", "--quiet"); err != nil {
		return true, nil
	}
	out, err := gitBytes(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return true, err
	}
	return hasNonStampEntries(out), nil
}

func hasNonStampEntries(porcelainZ []byte) bool {
	allowed := map[string]bool{
		"?? " + StampRelPath:                  true,
		"?? .agents/scratch/stop-nudge.stamp": true,
	}
	for _, entry := range bytes.Split(porcelainZ, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		if !allowed[string(entry)] {
			return true
		}
	}
	return false
}
