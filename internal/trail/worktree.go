package trail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrCheckoutChanged marks a checkout whose HEAD, branch, status, or reflog
// mutated concurrently between capture and verification.
var ErrCheckoutChanged = errors.New("checkout changed concurrently during operation")

// CheckoutSnapshot captures a checkout's observable state so callers can detect
// concurrent mutations before merging or applying changes.
type CheckoutSnapshot struct {
	Branch     string
	Head       string
	StatusHash string
	HeadLog    string
	BranchLog  string
}

// SnapshotOptions configures CaptureCheckoutSnapshot and CheckoutUnchanged.
type SnapshotOptions struct {
	IncludeStatus bool
	IncludeReflog bool
	FullBranchRef bool
	HardenConfig  bool
}

var worktreeDroppedGitVars = map[string]bool{
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
	"GIT_QUARANTINE_PATH":              true,
	"GIT_SHALLOW_FILE":                 true,
	"GIT_REPLACE_REF_BASE":             true,
	"GIT_NO_REPLACE_OBJECTS":           true,
}

func withoutWorktreeGitVars(env []string, hardenConfig bool) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if worktreeDroppedGitVars[k] {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
	if hardenConfig {
		out = append(out, "GIT_GRAFT_FILE="+os.DevNull)
	}
	return out
}

func worktreeGitBytes(ctx context.Context, root string, hardenConfig bool, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	full := args
	if hardenConfig {
		prefix := []string{
			"--no-optional-locks", "--no-replace-objects",
			"-c", "core.fsmonitor=false",
			"-c", "log.showSignature=false",
			"-c", "core.hooksPath=/dev/null",
			"-c", "core.commitGraph=false",
			"-c", "commit.gpgsign=false",
			"-c", "tag.gpgSign=false",
		}
		full = append(prefix, args...)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = root
	cmd.Env = withoutWorktreeGitVars(os.Environ(), hardenConfig)
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

func worktreeGitLine(ctx context.Context, root string, hardenConfig bool, args ...string) (string, error) {
	out, err := worktreeGitBytes(ctx, root, hardenConfig, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CaptureCheckoutSnapshot records HEAD, branch, and optional status/reflog state
// for root under opts.
func CaptureCheckoutSnapshot(ctx context.Context, root string, opts SnapshotOptions) (CheckoutSnapshot, error) {
	head, err := worktreeGitLine(ctx, root, opts.HardenConfig, "rev-parse", "--verify", "HEAD")
	if err != nil || head == "" {
		return CheckoutSnapshot{}, errors.New("cannot read HEAD")
	}
	branch, err := captureSnapshotBranch(ctx, root, opts)
	if err != nil {
		return CheckoutSnapshot{}, err
	}
	snap := CheckoutSnapshot{Branch: branch, Head: head}
	if opts.IncludeStatus {
		hash, err := captureSnapshotStatusHash(ctx, root, opts.HardenConfig)
		if err != nil {
			return CheckoutSnapshot{}, err
		}
		snap.StatusHash = hash
	}
	if opts.IncludeReflog {
		headLog, branchLog, err := captureSnapshotReflogs(ctx, root, branch, opts.HardenConfig)
		if err != nil {
			return CheckoutSnapshot{}, err
		}
		snap.HeadLog, snap.BranchLog = headLog, branchLog
	}
	return snap, nil
}

func captureSnapshotBranch(ctx context.Context, root string, opts SnapshotOptions) (string, error) {
	args := []string{"symbolic-ref", "-q", "HEAD"}
	if !opts.FullBranchRef {
		args = []string{"symbolic-ref", "--short", "-q", "HEAD"}
	}
	branch, err := worktreeGitLine(ctx, root, opts.HardenConfig, args...)
	if err != nil || branch == "" {
		if opts.IncludeReflog {
			return "", errors.New("HEAD is detached")
		}
		return "", nil
	}
	return branch, nil
}

func captureSnapshotStatusHash(ctx context.Context, root string, hardenConfig bool) (string, error) {
	st, err := worktreeGitBytes(ctx, root, hardenConfig, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(st)
	return hex.EncodeToString(sum[:]), nil
}

func captureSnapshotReflogs(ctx context.Context, root, branch string, hardenConfig bool) (string, string, error) {
	headLog, err := worktreeGitLine(ctx, root, hardenConfig, "reflog", "show", "-n", "1", "--date=iso-strict", "--format=%H %gd %gs", "HEAD", "--")
	if err != nil {
		return "", "", errors.New("cannot read HEAD reflog")
	}
	branchLog, err := worktreeGitLine(ctx, root, hardenConfig, "reflog", "show", "-n", "1", "--date=iso-strict", "--format=%H %gd %gs", branch, "--")
	if err != nil {
		return "", "", errors.New("cannot read branch reflog")
	}
	return headLog, branchLog, nil
}

// CheckoutUnchanged verifies that root's current snapshot equals before.
// Concurrent mutations return ErrCheckoutChanged.
func CheckoutUnchanged(ctx context.Context, root string, before CheckoutSnapshot, opts SnapshotOptions) error {
	now, err := CaptureCheckoutSnapshot(ctx, root, opts)
	if err != nil {
		return err
	}
	if now != before {
		return ErrCheckoutChanged
	}
	return nil
}

// RemoveWorktree forcefully removes linked worktree wtDir from mainRoot and
// prunes stale metadata on failure. It refuses empty paths and mainRoot itself.
func RemoveWorktree(ctx context.Context, mainRoot, wtDir string, hardenConfig bool) error {
	if strings.TrimSpace(mainRoot) == "" || strings.TrimSpace(wtDir) == "" || SameDir(mainRoot, wtDir) {
		return fmt.Errorf("trail: refuse to remove worktree %q for %q", wtDir, mainRoot)
	}
	_, err := worktreeGitBytes(ctx, mainRoot, hardenConfig, "worktree", "remove", "--force", "--", wtDir)
	if err != nil {
		if hardenConfig {
			_ = os.RemoveAll(wtDir)
		}
		_, _ = worktreeGitBytes(ctx, mainRoot, hardenConfig, "worktree", "prune")
		return err
	}
	return nil
}
