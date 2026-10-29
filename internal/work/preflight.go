package work

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// prepared is what the refusals resolved, for the worker.
type prepared struct {
	k                Kickoff
	agy, tok         string
	base             string // the checkout's HEAD at the start, recorded once (D36)
	gitName, gitMail string
}

// preflight is refusals 1-4 and 7-10 of § Interfaces, in that order, before
// anything is made; refusals 5 and 6 come with several files (Phase 4).
func preflight(o Options, file string) (prepared, error) {
	var p prepared
	if err := leadClean(o.Root); err != nil {
		return p, err
	}
	k, err := ParseKickoff(file)
	if err != nil {
		return p, err
	}
	p.k = k
	if err := branchFree(o.Root, k.Branch()); err != nil {
		return p, err
	}
	// D25: the worker commits as the operator; without an identity every
	// commit of a two-hour session would fail.
	if p.gitName, p.gitMail = gitConfig(o.Root, "user.name"), gitConfig(o.Root, "user.email"); p.gitName == "" || p.gitMail == "" {
		return p, fmt.Errorf("%s has no git user.name and user.email; the worker commits as them (D25)", o.Root)
	}
	if err := sandbox.CheckRoot(o.RealHome, o.Repo, sandbox.Root(o.Home, "work")); err != nil {
		return p, err
	}
	p.tok = filepath.Join(o.RealHome, sandbox.TokenRel)
	if st, err := os.Lstat(p.tok); err != nil || !st.Mode().IsRegular() {
		return p, fmt.Errorf("no Antigravity login at %s", p.tok)
	}
	if p.agy, err = exec.LookPath("agy"); err != nil {
		return p, errors.New("agy not found on PATH")
	}
	if strings.HasPrefix(o.Model, "-") {
		return p, fmt.Errorf("--model %s: a model name, not a flag", o.Model)
	}
	if o.Timeout < minTimeout || o.Timeout%timeoutUnit != 0 {
		return p, fmt.Errorf("--timeout %s: give whole minutes, at least %s", o.Timeout, minTimeout)
	}
	p.base, err = head(o.Root)
	return p, err
}

// leadClean is refusals 1 and 2: root is a checkout's top level and its
// tree matches HEAD, since the clone takes only HEAD (D13). trail.Dirty
// answers without `git status` and its filters (BUG-164).
func leadClean(root string) error {
	if err := sandbox.OutsideCheckout(root); err == nil {
		return fmt.Errorf("%s is not a git checkout", root)
	}
	dirty, err := trail.Dirty(root)
	if err != nil {
		return fmt.Errorf("%s: %v", root, err)
	}
	if dirty {
		return fmt.Errorf("%s has uncommitted or untracked changes; commit them first: a worker clones HEAD only (D13)", root)
	}
	return nil
}

// branchFree is the rest of refusal 4: git takes the branch name, and the
// checkout has no branch of it yet.
func branchFree(root, branch string) error {
	if err := trail.Command(root, "check-ref-format", "--branch", branch).Run(); err != nil {
		return fmt.Errorf("git refuses the branch name %s", branch)
	}
	if trail.Command(root, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Run() == nil {
		return fmt.Errorf("the branch %s already exists in %s; merge or delete it first", branch, root)
	}
	return nil
}
