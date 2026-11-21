package isolate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

func initWorkerOptions(o *WorkerOptions, files []string) error {
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxWorkers <= 0 {
		o.MaxWorkers = 1
	}
	if len(files) == 0 {
		return errors.New("no Kickoff file")
	}
	for _, p := range []*string{&o.Root, &o.Home, &o.RealHome} {
		if *p == "" || (p == &o.Root && o.DraftsOnly && *p == globalWorkspace) {
			continue
		}
		abs, err := filepath.Abs(*p)
		if err != nil {
			return err
		}
		*p = abs
	}
	return nil
}

func preflightWorker(ctx context.Context, o *WorkerOptions, files []string) ([]prepared, error) {
	if err := checkWorkerRoots(*o); err != nil {
		return nil, err
	}
	ks, err := parseAllKickoffs(files, o.Auto || o.DraftsOnly)
	if err != nil {
		return nil, err
	}
	if o.DraftsOnly {
		err = kickoffNamesUnique(ks)
	} else {
		err = branchesFree(o.Root, ks)
	}
	if err != nil {
		return nil, err
	}
	if err := disjoint(ks); err != nil {
		return nil, err
	}
	base, z, rf, err := preflightSessionState(ctx, o, ks)
	if err != nil {
		return nil, err
	}
	return buildPrepared(ctx, o, ks, base, z, rf)
}

func preflightSessionState(ctx context.Context, o *WorkerOptions, ks []Kickoff) (string, zone, []refEntry, error) {
	if o.Model != "" && !modelRe.MatchString(o.Model) {
		return "", zone{}, nil, fmt.Errorf("--model %s: not a model ID", o.Model)
	}
	if o.Timeout < minTimeout || o.Timeout%timeoutUnit != 0 {
		return "", zone{}, nil, fmt.Errorf("--timeout %s: give whole minutes, at least %s", o.Timeout, minTimeout)
	}
	runner, why, err := ChooseRunner(o.Runner, isolateAvailFor(*o))
	if err != nil {
		return "", zone{}, nil, err
	}
	o.Runner, o.RunnerReason = runner, why
	z, err := runZone(o.Runner)
	if err != nil {
		return "", zone{}, nil, err
	}
	if o.DraftsOnly {
		return "", z, nil, nil
	}
	base, err := headCommit(o.Root)
	if err != nil {
		return "", zone{}, nil, err
	}
	rf, err := reflogStart(o.Root)
	if err != nil {
		return "", zone{}, nil, err
	}
	return base, z, rf, nil
}

func isolateAvailFor(o WorkerOptions) func() error {
	if o.TestUnconfined {
		return func() error { return nil }
	}
	return CheckAvailable
}

func buildPrepared(ctx context.Context, o *WorkerOptions, ks []Kickoff, base string, z zone, rf []refEntry) ([]prepared, error) {
	var shared prepared
	shared.base, shared.reflog, shared.zone = base, rf, z
	var err error
	if _, err = CheckWorkLogin(o.Home, o.RealHome, hostOS); err != nil {
		return nil, err
	}
	if shared.agy, err = exec.LookPath("agy"); err != nil {
		return nil, errors.New("agy not found on PATH")
	}
	if shared.tok, err = SyncWorkToken(o.Home, o.RealHome, hostOS); err != nil {
		return nil, err
	}
	if err := checkModels(ctx, o, ks); err != nil {
		return nil, err
	}
	out := make([]prepared, len(ks))
	for i, k := range ks {
		p := shared
		p.k = k
		out[i] = p
	}
	return out, nil
}

func checkWorkerRoots(o WorkerOptions) error {
	if o.DraftsOnly {
		if o.Root != "" && o.Root != globalWorkspace {
			if st, err := os.Stat(o.Root); err != nil || !st.IsDir() {
				return fmt.Errorf("%s is not a directory", o.Root)
			}
		}
	} else if err := leadClean(o.Root); err != nil {
		return err
	}
	if err := CheckRoot(o.RealHome, o.Repo, SandboxRoot(o.Home, o.Root)); err != nil {
		return err
	}
	return CheckWorkDir(o.Home)
}

func kickoffNamesUnique(ks []Kickoff) error {
	seen := map[string]string{}
	for _, k := range ks {
		if prev, ok := seen[k.Name]; ok {
			return fmt.Errorf("the Kickoffs %s and %s share the name %s", prev, k.Path, k.Name)
		}
		seen[k.Name] = k.Path
	}
	return nil
}

func leadClean(root string) error {
	if err := OutsideCheckout(root); err == nil {
		return fmt.Errorf("%s is not a git checkout", root)
	}
	dirty, err := trail.Dirty(root)
	if err != nil {
		return fmt.Errorf("%s: %v", root, err)
	}
	if dirty {
		return fmt.Errorf("%s has uncommitted or untracked changes; commit them first", root)
	}
	return nil
}

func parseAllKickoffs(files []string, auto bool) ([]Kickoff, error) {
	ks := make([]Kickoff, 0, len(files))
	for _, f := range files {
		k, err := parseKickoff(f, auto)
		if err != nil {
			return nil, err
		}
		ks = append(ks, k)
	}
	return ks, nil
}

func branchesFree(root string, ks []Kickoff) error {
	seen := map[string]string{}
	for _, k := range ks {
		if prev, ok := seen[k.Name]; ok {
			return fmt.Errorf("the Kickoffs %s and %s share the name %s", prev, k.Path, k.Name)
		}
		seen[k.Name] = k.Path
		if err := trail.Command(root, "check-ref-format", "--branch", k.Branch()).Run(); err != nil {
			return fmt.Errorf("git refuses branch %s", k.Branch())
		}
		if trail.Command(root, "rev-parse", "--verify", "-q", "refs/heads/"+k.Branch()).Run() == nil {
			return fmt.Errorf("branch %s already exists in %s", k.Branch(), root)
		}
	}
	return nil
}

func disjoint(ks []Kickoff) error {
	for i, a := range ks {
		for _, b := range ks[i+1:] {
			for _, ea := range a.Files {
				for _, eb := range b.Files {
					if ea == eb || (strings.HasSuffix(ea, "/") && strings.HasPrefix(eb, ea)) || (strings.HasSuffix(eb, "/") && strings.HasPrefix(ea, eb)) {
						return fmt.Errorf("Kickoffs %s and %s overlap on %s and %s", a.Path, b.Path, ea, eb)
					}
				}
			}
		}
	}
	return nil
}

func headCommit(root string) (string, error) {
	out, err := trail.Command(root, "rev-parse", "--verify", "-q", "HEAD^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %v", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}
