package isolate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// WorkerOptions configures an isolated background worker run (`fyr auto`).
type WorkerOptions struct {
	Root, Home, RealHome, Self, Repo string
	Timeout                          time.Duration
	Model                            string
	Keep                             bool
	MaxWorkers                       int
	RAMGB, CPUs, DiskGB              int
	Out                              io.Writer
	Now                              func() time.Time
	StopGrace                        time.Duration
	Runner, RunnerReason             string
	Auto, ACP                        bool
	TestRO, TestRW                   []string
	TestUnconfined                   bool
}

const (
	DefaultTimeout = 30 * time.Minute

	ReasonTimedOut     = "timed out"
	ReasonKilled       = "killed"
	ReasonNoCommits    = "no commits"
	ReasonNoHandoff    = "no handoff"
	ReasonTwoHandoffs  = "more than one handoff"
	ReasonOutside      = "changed outside its Files"
	ReasonRewrote      = "rewrote history"
	ReasonLeadChanged  = "lead checkout changed"
	ReasonBranchFailed = "branch"
	ReasonDiskLimit    = "disk limit"
	ReasonJudgeFailed  = "judge failed"
)

var (
	minTimeout    = time.Minute
	timeoutUnit   = time.Minute
	diskUnitBytes = int64(1 << 30)
	wideGrant     = []string{"command(*)", "read_file(*)", "write_file(*)"}
	safetyReasons = append(slices.Clone(IntegrityReasons), ReasonCredentials, ReasonRealConfig, ReasonLeadChanged)
)

// WorkerRecord is one record from a worker's branch trail export.
type WorkerRecord struct {
	ID, Kind, Status, Date, Title, Updated, Body string
}

// WorkerResult is one worker's outcome from RunWorker.
type WorkerResult struct {
	Name, Verdict        string
	Reasons              []string
	AgyExit              int
	Model                string
	Seconds, SetupSec    int
	AgySec, CheckSec     int
	Base, Head           string
	Commits              int
	Dir                  string
	Handoff              *WorkerRecord
	Changes              []WorkerRecord
	Drafts               int
	DraftsDir            string
	Runner, RunnerReason string
	Notes                []string
}

type prepared struct {
	k      Kickoff
	base   string
	reflog []refEntry
	zone   zone
	tok    string
	agy    string
}

type worker struct {
	o           WorkerOptions
	p           prepared
	b           *Box
	stamp       string
	notes       bytes.Buffer
	ran         bool
	start       time.Time
	collected   bool
	baseRecords map[string]WorkerRecord
	dest        *os.Root
	res         WorkerResult
	seed        *waveSeed
}

// SandboxRoot returns `<home>/.config/fyr/sandbox/<project>`.
func SandboxRoot(home, root string) string { return ProjectRoot(home, "sandbox", root) }

// RunWorker executes one or more Kickoff files in isolated worker sandboxes.
func RunWorker(ctx context.Context, o WorkerOptions, files ...string) ([]WorkerResult, error) {
	if err := initWorkerOptions(&o, files); err != nil {
		return nil, err
	}
	ps, err := preflightWorker(ctx, &o, files)
	if err != nil {
		return nil, err
	}
	stamp := fmt.Sprintf("%s-%d", o.Now().Format("2006-01-02-150405"), os.Getpid())
	builtSelf, cleanSelf := PrepareSelf(ctx, o.Repo, o.Self)
	defer cleanSelf()
	o.Self = builtSelf
	sbProj := SandboxRoot(o.Home, o.Root)
	root := filepath.Join(sbProj, stamp)
	if !o.Keep {
		defer cleanRunDirs(root, sbProj, o.Home)
	}
	seed := &waveSeed{}
	ws := make([]*worker, len(ps))
	for i, p := range ps {
		ws[i] = newWorker(o, p, stamp, filepath.Join(root, p.k.Name), seed)
	}
	return dispatchWorkers(ctx, o.MaxWorkers, ws), nil
}

func cleanRunDirs(root, sbProj, home string) {
	_ = os.Remove(root)
	_ = os.Remove(sbProj)
	_ = os.Remove(Root(home, "sandbox"))
}

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
		if *p == "" {
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
	ks, err := parseAllKickoffs(files, o.Auto)
	if err != nil {
		return nil, err
	}
	if err := branchesFree(o.Root, ks); err != nil {
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
	if err := leadClean(o.Root); err != nil {
		return err
	}
	if err := CheckRoot(o.RealHome, o.Repo, SandboxRoot(o.Home, o.Root)); err != nil {
		return err
	}
	return CheckWorkDir(o.Home)
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

func dispatchWorkers(ctx context.Context, maxWorkers int, ws []*worker) []WorkerResult {
	res := make([]WorkerResult, len(ws))
	sem := make(chan struct{}, maxWorkers)
	var collect sync.Mutex
	var wg sync.WaitGroup
	for i, w := range ws {
		wg.Add(1)
		go func(idx int, wrk *worker) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				res[idx] = wrk.run(ctx, &collect)
			case <-ctx.Done():
				wrk.b.Note("run cancelled before worker started")
				wrk.b.Fail(ReasonKilled)
				wrk.res.Dir = ""
				res[idx] = wrk.result()
			}
		}(i, w)
	}
	wg.Wait()
	return res
}

func newWorker(o WorkerOptions, p prepared, stamp, dir string, seed *waveSeed) *worker {
	model := p.k.Model
	if model == "" {
		model = o.Model
	}
	grace := min(5*time.Minute, o.Timeout/5)
	pt := max(1, int((o.Timeout-grace)/time.Minute))
	w := &worker{o: o, p: p, stamp: stamp, seed: seed}
	w.b = New(Config{
		Dir: dir, Source: o.Root, RealHome: o.RealHome, Repo: o.Repo, Self: o.Self,
		Grants: wideGrant, GitName: "agy-work", GitEmail: "agy-work@localhost",
		Model: model, Zone: p.zone.name, Timeout: o.Timeout, SkipStores: true,
		PrintTimeout: fmt.Sprintf("%dm", pt), StopGrace: o.StopGrace,
		Prefix: "auto: " + p.k.Name, Out: &w.notes, ACP: o.ACP,
		TestRO: o.TestRO, TestRW: o.TestRW, TestUnconfined: o.TestUnconfined,
	})
	w.res = WorkerResult{Name: p.k.Name, AgyExit: -1, Model: model, Base: p.base, Dir: dir, Runner: o.Runner, RunnerReason: o.RunnerReason}
	return w
}

func (w *worker) run(ctx context.Context, collect *sync.Mutex) WorkerResult {
	w.start = time.Now()
	if !w.b.Make() {
		w.res.SetupSec = stageSeconds(w.start, time.Now())
		w.res.Seconds = w.res.SetupSec
		return w.result()
	}
	ok := w.prepare(ctx)
	setupEnd := time.Now()
	w.res.SetupSec = stageSeconds(w.start, setupEnd)
	checkStart := setupEnd
	if ok {
		w.res.AgyExit = w.b.Session(ctx, w.p.agy, w.p.tok, w.p.k.Text, "out.txt")
		w.ran = w.b.Started()
		checkStart = time.Now()
		w.res.AgySec = stageSeconds(setupEnd, checkStart)
		w.b.After()
	}
	w.finishChecks(ctx, collect)
	w.res.CheckSec = stageSeconds(checkStart, time.Now())
	w.res.Seconds = stageSeconds(w.start, time.Now())
	return w.result()
}

func (w *worker) finishChecks(ctx context.Context, collect *sync.Mutex) {
	w.b.ExitChecks()
	w.checkDisk()
	if w.b.TimedOut() {
		w.b.Fail(ReasonTimedOut)
	}
	if ctx.Err() != nil {
		w.b.Fail(ReasonKilled)
	}
	if w.p.k.Judge != "" && w.ran && !w.unsafe() && len(w.b.Reasons()) == 0 {
		runWorkerJudge(ctx, w)
	}
	collect.Lock()
	w.collect(ctx)
	collect.Unlock()
	w.remove()
}

func (w *worker) prepare(ctx context.Context) bool {
	if !w.b.Clone(ctx) || !w.branch() {
		return false
	}
	w.b.Fingerprint()
	if !w.b.Install(ctx) || !w.b.Settings() {
		return false
	}
	w.seedRecords(ctx)
	w.b.Baseline()
	return ctx.Err() == nil
}

func (w *worker) branch() bool {
	for _, args := range [][]string{{"remote", "remove", "origin"}, {"checkout", "-q", "-b", w.p.k.Branch()}} {
		if out, err := trail.Command(w.b.App(), args...).CombinedOutput(); err != nil {
			w.b.Note("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			w.b.Fail(ReasonBranchFailed)
			return false
		}
	}
	return true
}

func (w *worker) seedRecords(ctx context.Context) {
	fyrBin, err := w.leadFyrBin()
	if err != nil {
		return
	}
	trailFile := filepath.Join(w.b.App(), trailRel)
	if _, err := os.Stat(trailFile); err == nil {
		runSeedSync(ctx, fyrBin, w.b.App(), w.b.Home())
		return
	}
	blob, recs, ok := w.seed.load(ctx, fyrBin, w.b.App(), w.o.Root, w.o.Home, w.o.RealHome)
	if !ok || os.MkdirAll(filepath.Dir(trailFile), 0o755) != nil {
		return
	}
	if os.WriteFile(trailFile, blob, 0o600) == nil {
		runSeedSync(ctx, fyrBin, w.b.App(), w.b.Home())
	}
	cleanSeedTrail(trailFile)
	w.baseRecords = recs
}

func (w *worker) checkDisk() {
	if w.o.DiskGB <= 0 || w.unsafe() || !w.b.Intact() {
		return
	}
	limit := int64(w.o.DiskGB) * diskUnitBytes
	var used int64
	err := filepath.WalkDir(w.res.Dir, func(_ string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.Type().IsRegular() {
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			used += info.Size()
		}
		return nil
	})
	if err != nil || used > limit {
		w.b.Fail(ReasonDiskLimit)
	}
}

func (w *worker) unsafe() bool {
	for _, r := range safetyReasons {
		if w.b.Has(r) {
			return true
		}
	}
	return false
}

func (w *worker) remove() {
	if w.o.Keep || w.unsafe() || (w.ran && !w.collected) {
		return
	}
	if w.b.Remove() {
		w.res.Dir = ""
	}
}

func (w *worker) result() WorkerResult {
	w.res.Reasons = w.b.Reasons()
	w.res.Verdict = "PASS"
	if len(w.res.Reasons) > 0 {
		w.res.Verdict = "FAIL"
	}
	if w.notes.Len() > 0 {
		w.res.Notes = strings.Split(strings.TrimRight(w.notes.String(), "\n"), "\n")
	}
	return w.res
}

func stageSeconds(from, to time.Time) int {
	return max(0, int(to.Sub(from).Seconds()))
}

func headCommit(root string) (string, error) {
	out, err := trail.Command(root, "rev-parse", "--verify", "-q", "HEAD^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %v", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}
