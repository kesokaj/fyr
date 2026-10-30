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

// prepared is what the refusals resolved, for one worker; every field but
// k is the run's.
type prepared struct {
	k                Kickoff
	agy, tok         string
	base             string // the checkout's HEAD at the start, recorded once (D36)
	gitName, gitMail string
	docker           *dockerPlan // nil on the host runner
}

// preflight is refusals 1-10 of § Interfaces before anything is made, for
// every file of the run, in that order but for the limit (5), which is
// counted after the sandbox root's check (7), so no unapproved root is read.
func preflight(o Options, files []string) ([]prepared, error) {
	var p prepared
	if err := leadClean(o.Root); err != nil {
		return nil, err
	}
	var ks []Kickoff
	for _, file := range files {
		k, err := ParseKickoff(file)
		if err != nil {
			return nil, err
		}
		ks = append(ks, k)
	}
	if err := branchesFree(o.Root, ks); err != nil {
		return nil, err
	}
	// D25: the worker commits as the operator; without an identity every
	// commit of a two-hour session would fail.
	if p.gitName, p.gitMail = gitConfig(o.Root, "user.name"), gitConfig(o.Root, "user.email"); p.gitName == "" || p.gitMail == "" {
		return nil, fmt.Errorf("%s has no git user.name and user.email; the worker commits as them (D25)", o.Root)
	}
	if err := disjoint(ks); err != nil {
		return nil, err
	}
	work := sandbox.Root(o.Home, "work")
	if err := sandbox.CheckRoot(o.RealHome, o.Repo, work); err != nil {
		return nil, err
	}
	// After CheckRoot, so nothing under an unapproved root is read (the
	// Phase 4 security review).
	if err := atLimit(work, o.MaxWorkers); err != nil {
		return nil, err
	}
	switch o.Runner {
	case "", RunnerHost, RunnerDocker:
	default:
		return nil, fmt.Errorf("--runner %q: give %s or %s", o.Runner, RunnerHost, RunnerDocker)
	}
	if err := p.session(o); err != nil {
		return nil, err
	}
	ps := make([]prepared, len(ks))
	for i, k := range ks {
		ps[i] = p
		ps[i].k = k
	}
	return ps, nil
}

// session is refusals 8-10, what the sessions need, then the base.
func (p *prepared) session(o Options) error {
	p.tok = filepath.Join(o.RealHome, sandbox.TokenRel)
	if st, err := os.Lstat(p.tok); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("no Antigravity login at %s", p.tok)
	}
	var err error
	// In docker mode the container's agy is a linux one (dockerRefusals).
	if o.Runner != RunnerDocker {
		if p.agy, err = exec.LookPath("agy"); err != nil {
			return errors.New("agy not found on PATH")
		}
	}
	if strings.HasPrefix(o.Model, "-") {
		return fmt.Errorf("--model %s: a model name, not a flag", o.Model)
	}
	if o.Timeout < minTimeout || o.Timeout%timeoutUnit != 0 {
		return fmt.Errorf("--timeout %s: give whole minutes, at least %s", o.Timeout, minTimeout)
	}
	if p.base, err = head(o.Root); err != nil || o.Runner != RunnerDocker {
		return err
	}
	return p.dockerRefusals(o)
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

// branchesFree is the rest of refusal 4: no name twice, git takes each
// branch name, and the checkout has no branch of it yet.
func branchesFree(root string, ks []Kickoff) error {
	seen := map[string]string{}
	for _, k := range ks {
		if first, ok := seen[k.Name]; ok {
			return fmt.Errorf("the Kickoffs %s and %s share the name %s; each worker needs its own branch", first, k.Path, k.Name)
		}
		seen[k.Name] = k.Path
		if err := trail.Command(root, "check-ref-format", "--branch", k.Branch()).Run(); err != nil {
			return fmt.Errorf("git refuses the branch name %s", k.Branch())
		}
		if trail.Command(root, "rev-parse", "--verify", "-q", "refs/heads/"+k.Branch()).Run() == nil {
			return fmt.Errorf("the branch %s already exists in %s; merge or delete it first", k.Branch(), root)
		}
	}
	return nil
}

// disjoint is refusal 6 (D17): no entry of one Kickoff's Files: equals, or
// is a directory prefix of, an entry of another's, so two workers never
// change one path or one record.
func disjoint(ks []Kickoff) error {
	for i, a := range ks {
		for _, b := range ks[i+1:] {
			for _, ea := range a.Files {
				for _, eb := range b.Files {
					if intersect(ea, eb) {
						return fmt.Errorf("the Kickoffs %s and %s share scope: %s and %s; give each worker its own files (D17)", a.Path, b.Path, ea, eb)
					}
				}
			}
		}
	}
	return nil
}

// intersect: equal entries, or one a directory entry the other lies under.
func intersect(a, b string) bool {
	under := func(dir, p string) bool { return strings.HasSuffix(dir, "/") && strings.HasPrefix(p, dir) }
	return a == b || under(a, b) || under(b, a)
}
