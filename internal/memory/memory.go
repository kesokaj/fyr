// Package memory resolves a project's root and its OLD memory store,
// .agents/memory/, for `fyr dream`, which reads it until brain plan Phase 4.
// `fyr memory` itself writes and reads memory records in the brain store
// since Phase 2 (internal/brain, D14); `fyr record import` migrates the old
// store.
package memory

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// refusal is a store the command will not touch, or input it will not take;
// the caller exits 2 with its message.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error { return &refusal{msg: fmt.Sprintf(format, a...)} }

// IsRefusal reports whether err is a refusal.
func IsRefusal(err error) bool {
	var r *refusal
	return errors.As(err, &r)
}

// Root is the main worktree's top level for the checkout that holds dir, so
// every worktree of a repository shares one store (D26). The resolution and
// its checks are trail.Roots'; here a failure is a refusal.
func Root(dir string) (string, error) {
	main, _, err := trail.Roots(dir)
	if err != nil {
		return "", refuse("%s", err)
	}
	return main, nil
}

// trusted returns the store path of root after the checks every command
// makes: the store is the project's own directory, never a link a commit
// could carry and never content git tracks (memory-loop spec, ADR-043).
func trusted(root string) (string, error) {
	agents := filepath.Join(root, ".agents")
	store := filepath.Join(agents, "memory")
	for _, p := range []string{agents, store} {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", refuse("%s is a symlink: the memory store must be the project's own directory", p)
		}
	}
	out, err := storeTracked(root).Output()
	if err != nil {
		return "", refuse("cannot ask git what it tracks under .agents/memory: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return "", refuse("git tracks files under .agents/memory: the store is the operator's own, never the repository's")
	}
	return store, nil
}

// Store returns root's store path after the trust checks every reader
// makes; `fyr dream` reads a project's memory through it.
func Store(root string) (string, error) { return trusted(root) }

// storeTracked is the git call that asks what the repository tracks under
// .agents/memory. The dream timer runs it in every registered project, so it
// goes through trail.Command: no index lock, no fsmonitor hook, no signature
// program, no lazy fetch (security review of the v2.0.0 gate, 2026-10-01;
// BUG-164, BUG-165).
func storeTracked(root string) *exec.Cmd {
	return trail.Command(root, "ls-files", "--", ".agents/memory")
}
