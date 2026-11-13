package work

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// prepared is what the refusals resolved, for one worker; every field but
// k and toBuild is the run's.
type prepared struct {
	k                Kickoff
	agy, tok         string
	base             string     // the checkout's HEAD at the start, recorded once (D36)
	reflog           []refEntry // the newest reflog entries of HEAD and its branch then (BUG-198)
	gitName, gitMail string
	zone             zone        // the lead's, the worker's TZ (refusal 22)
	docker           *dockerPlan // nil on the host runner, and under Check's buildPredict when toBuild is set
	toBuild          string      // Check's buildPredict: the image a run builds first (ADR-504a)
}

// preflight is refusals 1-22 of SPEC-fyr-work before anything is made,
// for every file of the run, in that order: the limit after the sandbox
// root's check, so no unapproved root is read, then session's refusals.
func preflight(ctx context.Context, o *Options, files []string) ([]prepared, error) {
	o.build = buildFirst
	var p prepared
	if err := leadClean(o.Root); err != nil {
		return nil, err
	}
	ks, err := parseAll(files, o.Auto)
	if err != nil {
		return nil, err
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
	if err := checkWorkRoots(*o, work); err != nil {
		return nil, err
	}
	if err := o.fillConfig(); err != nil {
		return nil, err
	}
	if err := atLimit(work, o.MaxWorkers); err != nil {
		return nil, err
	}
	if o.Runner != "" {
		if err := CheckRunner(o.Runner); err != nil {
			return nil, err
		}
	}
	if err := p.session(ctx, o, ks); err != nil {
		return nil, err
	}
	ps := make([]prepared, len(ks))
	for i, k := range ks {
		ps[i] = p
		ps[i].k = k
	}
	return ps, nil
}

func checkWorkRoots(o Options, work string) error {
	for _, r := range []string{work, SandboxRoot(o.Home, o.Root)} {
		if err := sandbox.CheckRoot(o.RealHome, o.Repo, r); err != nil {
			return err
		}
	}
	return nil
}

// parseAll is refusal 3 for every file, in order.
func parseAll(files []string, auto bool) ([]Kickoff, error) {
	var ks []Kickoff
	for _, file := range files {
		k, err := parseKickoff(file, auto)
		if err != nil {
			return nil, err
		}
		ks = append(ks, k)
	}
	return ks, nil
}

// session is refusals 8-11 and 21-23, what the sessions need: offline
// refusals first, before the login check, checkModels and every docker call
// (workers D145, G4); then ChooseRunner turns --runner auto into docker or
// the host (D5, ADR-504), and the probe's plan and token are kept, so docker
// is checked once; SyncWorkToken runs last, after every refusal passed. The
// runner chosen is left on o, for the stamp line and the workers.
func (p *prepared) session(ctx context.Context, o *Options, ks []Kickoff) error {
	var err error
	if o.Model != "" && !modelRe.MatchString(o.Model) {
		return fmt.Errorf("--model %s: not a model ID", o.Model)
	}
	if o.Timeout < minTimeout || o.Timeout%timeoutUnit != 0 {
		return fmt.Errorf("--timeout %s: give whole minutes, at least %s", o.Timeout, minTimeout)
	}
	if p.zone, err = runZone(o.Runner); err != nil {
		return err
	}
	if p.base, err = head(o.Root); err != nil {
		return err
	}
	if p.reflog, err = reflogStart(o.Root); err != nil {
		return err
	}
	if err = gitFloor(o.Runner); err != nil {
		return err
	}
	var probe prepared
	if ChooseRunner(o, probe.dockerPreflight); o.Runner != RunnerDocker {
		return p.hostSession(ctx, o, ks)
	}
	p.docker, p.tok = probe.docker, probe.tok
	return p.dockerSession(ctx, o, ks)
}

// hostSession is the host runner's part of session: the login, the host's
// agy, then the model check.
func (p *prepared) hostSession(ctx context.Context, o *Options, ks []Kickoff) error {
	var err error
	if p.tok, err = sandbox.CheckLogin(o.RealHome, hostOS); err != nil {
		return err
	}
	if p.agy, err = exec.LookPath("agy"); err != nil {
		return errors.New("agy not found on PATH")
	}
	return checkModels(ctx, o, ks)
}

// dockerSession is the docker runner's part of session. After --runner
// auto's probe (p.docker set) only the model check is left; an explicit
// --runner docker, which ChooseRunner never probes, refuses here in order:
// a development build, the login, the model check, then dockerRefusals.
// The work token is synced last.
func (p *prepared) dockerSession(ctx context.Context, o *Options, ks []Kickoff) error {
	probed := p.docker != nil
	var err error
	if !probed {
		if err = develBuild(o.Version); err != nil {
			return err
		}
		if p.tok, err = sandbox.CheckWorkLogin(o.Home, o.RealHome, hostOS); err != nil {
			return err
		}
	}
	if err = checkModels(ctx, o, ks); err != nil {
		return err
	}
	if !probed {
		if err = p.dockerRefusals(*o); err != nil {
			return err
		}
	}
	p.tok, err = sandbox.SyncWorkToken(o.Home, o.RealHome, hostOS)
	return err
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
