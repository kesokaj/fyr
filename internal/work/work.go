// Package work runs Kickoffs as workers, several at once up to a
// machine-wide limit: each an unattended agy session in its own sandbox, on
// its own clone and branch, fetched back into the lead's checkout by path
// and reported as the result contract
// (PLAN-2026-10-29-workers § Interfaces § fyr work; SPEC-fyr-work). It
// never links the brain store (ADR-063): it reads a sandbox's records through
// `fyr record export`, or the branch's committed trail.jsonl, and writes each
// handoff and change record a worker added as files under the lead's
// .agents/scratch/work/<stamp>/<name>/records/, for the lead to store with
// `fyr record add --title-file` (BUG-500, ADR-501). It writes no store.
package work

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// Options is one invocation.
type Options struct {
	Root     string        // the lead's checkout, its top level
	Home     string        // fyr's home: the sandboxes are under SandboxRoot(Home, Root), the locks and pid files under sandbox.Root(Home, "work")
	RealHome string        // $HOME: the token, check c's fingerprint
	Self     string        // the fyr that installs (os.Executable)
	Repo     string        // the install source; "" is the harness embedded in Self
	Timeout  time.Duration // agy is killed after this; 0 is DefaultTimeout
	Model    string        // agy --model for a worker whose Kickoff has no Model: line; "" passes none
	Keep     bool          // keep the sandbox of a fetched worker too
	// MaxWorkers is the machine-wide limit, 1 to MaxMaxWorkers; 0 loads
	// config.json or DefaultMaxWorkers (D30, D10)
	MaxWorkers   int
	RAMGB        int // sandbox memory limit in GB, MinRAMGB to MaxRAMGB; 0 loads config.json or DefaultRAMGB
	CPUs         int // sandbox CPU limit in cores, MinCPUs to MaxCPUs; 0 loads config.json or DefaultCPUs
	DiskGB       int // sandbox disk limit in GB, MinDiskGB to MaxDiskGB; 0 loads config.json or DefaultDiskGB
	Out          io.Writer
	Now          func() time.Time
	StopGrace    time.Duration // SIGTERM to SIGKILL on ctx's end; 0 is the sandbox's 5s
	Runner       string        // RunnerAuto, RunnerHost ("" too) or RunnerDocker; Run makes RunnerAuto one of the others (ChooseRunner)
	RunnerReason string        // why RunnerHost was chosen; "" is "--runner host"
	Image        string        // the docker runner's image ref; "" is DefaultImage
	AgyLinux     string        // the docker runner's linux agy; "" is the host's own on linux
	Version      string        // this fyr's build version, which the image must hold
	Auto         bool          // fyr auto's worker: an empty Files: line is a drafts-only scope, and auto-drafts.jsonl is copied with the drafts
	ACP          bool          // drive host workers via `agy acp --stdio --process-per-session`
	Progress     io.Writer     // optional live subagent-style progress writer (stderr in CLI; nil suppresses)
	StatusMD     string        // optional Markdown artifact file updated atomically on each progress event (D7)
	build        buildMode     // a missing or stale DefaultImage: Run builds it (buildFirst), Check predicts the build (buildPredict), DockerPreflight neither (buildNever)
}

// DefaultTimeout is --timeout's default (D45).
const DefaultTimeout = 120 * time.Minute

// The --timeout bounds (D37), variables so a test can run a short worker.
var (
	minTimeout  = 10 * time.Minute
	timeoutUnit = time.Minute
)

// The FAIL reasons work adds to the sandbox's (§ Interfaces, D38).
const (
	ReasonTimedOut        = "timed out"
	ReasonKilled          = "killed"
	ReasonNoCommits       = "no commits"
	ReasonNoHandoff       = "no handoff"
	ReasonTwoHandoffs     = "more than one handoff"
	ReasonOutside         = "changed outside its Files"
	ReasonRewrote         = "rewrote history"
	ReasonLeadChanged     = "lead checkout changed"
	ReasonBranchFailed    = "branch"
	ReasonBaseCheckPassed = "base-check passed on HEAD"
	ReasonBaseCheckFailed = "base-check failed"
	ReasonJudgeFailed     = "judge failed"
)

// Record is one record the worker's branch added, as its trail.jsonl line
// holds it.
type Record struct {
	ID, Kind, Status, Date, Title, Updated, Body string
}

// Result is one worker's outcome; Block is its result contract.
type Result struct {
	Name, Verdict string // "PASS" | "FAIL"
	Reasons       []string
	AgyExit       int
	Model         string // the model agy ran on, "" its default
	Seconds       int
	Base, Head    string // Head is "" when the branch was not fetched
	Commits       int
	Dir           string   // the sandbox, "" once removed
	Handoff       *Record  // the one handoff the branch added
	Changes       []Record // the change records it added, by ID
	Drafts        int      // files copied to DraftsDir
	DraftsDir     string   // repo-relative, ending in /
	Records       int      // handoff and change records written to RecordsDir
	RecordsDir    string   // repo-relative, ending in /: <ID>.title and <ID>.md each
	Runner        string   // RunnerHost ("" too) or RunnerDocker; never RunnerAuto, which Run resolved
	RunnerReason  string   // why RunnerHost was chosen; "" is "--runner host"
	Notes         []string // the sandbox's notes, each a printable line
}

// Run refuses (an error, nothing made) or runs the Kickoff files as
// workers, at most MaxWorkers at once across the machine (D30): it prints
// the stamp line first, then each worker's result contract whole, in the
// order of files, as soon as it and every file before it ended (D19). The
// results are in that order too.
func Run(ctx context.Context, o Options, files ...string) ([]Result, error) {
	if err := initRunOptions(&o, files); err != nil {
		return nil, err
	}
	ps, err := preflight(ctx, &o, files)
	if err != nil {
		return nil, err
	}
	stamp := fmt.Sprintf("%s-%d", o.Now().Format("2006-01-02-150405"), os.Getpid())
	if err := o.makeNetwork(stamp); err != nil {
		return nil, err
	}
	work := sandbox.Root(o.Home, "work")
	r, err := openRun(work, stamp, o.MaxWorkers)
	if err != nil {
		o.sweepRun(stamp)
		return nil, fmt.Errorf("the run's pid file: %v", err)
	}
	sbProj := SandboxRoot(o.Home, o.Root)
	root := filepath.Join(sbProj, stamp)
	builtSelf, cleanSelf := sandbox.PrepareSelf(ctx, o.Repo, o.Self)
	o.Self = builtSelf
	defer closeRunSandboxes(r, root, sbProj, o.Home, cleanSelf)
	fmt.Fprintln(o.Out, sandbox.Printable(fmt.Sprintf("work: stamp %s  %d workers  %s/  %s", stamp, len(ps), root, RunnerLabel(o.Runner, o.RunnerReason))))
	tracker := newProgressTracker(o, stamp, ps)
	ws := make([]*worker, len(ps))
	for i, p := range ps {
		ws[i] = newWorker(o, p, stamp, filepath.Join(root, p.k.Name), tracker)
	}
	res := dispatch(ctx, r, ws, o.Out)
	o.sweepRun(stamp)
	return res, nil
}

func initRunOptions(o *Options, files []string) error {
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxWorkers < 0 || o.MaxWorkers > MaxMaxWorkers {
		return fmt.Errorf("--max-workers %d: give 1 to %d", o.MaxWorkers, MaxMaxWorkers)
	}
	if len(files) == 0 {
		return errors.New("no Kickoff file")
	}
	// Once, so the fetch (git -C Root) and the drafts copy (os.Root) read
	// the same clone (security review of Phase 2, finding 8).
	for _, p := range []*string{&o.Root, &o.Home, &o.RealHome} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			return err
		}
		*p = abs
	}
	return nil
}

func closeRunSandboxes(r *run, root, sbProj, home string, cleanSelf func()) {
	cleanSelf()
	r.close()
	_ = os.Remove(root)
	_ = os.Remove(sbProj)
	_ = os.Remove(sandbox.Root(home, "sandbox"))
}

// dispatch starts each worker as a slot frees, in file order, and prints
// the result contracts in that order. Once ctx ends, the workers still
// waiting never start and fail killed.
func dispatch(ctx context.Context, r *run, ws []*worker, out io.Writer) []Result {
	type ended struct {
		i   int
		res Result
	}
	var (
		collect sync.Mutex // one worker at a time fetches into the lead
		results = make([]*Result, len(ws))
		done    = make(chan ended)
		stop    = ctx.Done() // nil once handled; never closes for a Background ctx
		stopped bool
		tick    = time.NewTicker(slotPoll)
	)
	defer tick.Stop()
	next, printed := 0, 0
	for printed < len(ws) {
		// ctx.Err too: a select may take a worker's end over ctx's.
		for next < len(ws) && !stopped && ctx.Err() == nil {
			ok, err := r.take()
			if err != nil {
				// No slot can be counted, so none is taken: fail closed.
				ws[next].b.Note("the worker limit: %v", err)
				res := ws[next].unstarted(sandbox.ReasonSetup)
				results[next] = &res
				next++
				continue
			}
			if !ok {
				break
			}
			go func(i int) { done <- ended{i, ws[i].run(ctx, &collect)} }(next)
			next++
		}
		select {
		case e := <-done:
			r.give()
			results[e.i] = &e.res
		case <-stop:
			stop, stopped = nil, true
			next = neverStarted(ws, results, next)
		case <-tick.C:
		}
		printed = printReady(out, results, printed)
	}
	all := make([]Result, len(ws))
	for i, res := range results {
		all[i] = *res
	}
	return all
}

// neverStarted fails killed every worker from next on, still waiting for
// a slot when the run ended, and returns len(ws).
func neverStarted(ws []*worker, results []*Result, next int) int {
	for ; next < len(ws); next++ {
		ws[next].b.Note("the run ended while it waited for a slot; never started")
		res := ws[next].unstarted(ReasonKilled)
		results[next] = &res
	}
	return next
}

// printReady prints the blocks from printed on whose worker, and every
// one before it, ended, and returns the next to print.
func printReady(out io.Writer, results []*Result, printed int) int {
	for ; printed < len(results) && results[printed] != nil; printed++ {
		for _, l := range results[printed].Block() {
			fmt.Fprintln(out, l)
		}
	}
	return printed
}

// head is HEAD's commit in root.
func head(root string) (string, error) {
	out, err := trail.Command(root, "rev-parse", "--verify", "-q", "HEAD^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %v", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitConfig is `git config <key>` in root, "" when unset.
func gitConfig(root, key string) string {
	out, _ := trail.Command(root, "config", "--get", key).Output()
	return strings.TrimSpace(string(out))
}
