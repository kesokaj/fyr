// Package work runs one Kickoff as a worker: an unattended agy session in
// its own sandbox, on its own clone and branch, fetched back into the lead's
// checkout by path and reported as the result contract
// (PLAN-2026-10-29-workers § Interfaces § fyr work; SPEC-fyr-work). It
// never opens a sandbox's brain store: records travel only through the
// branch's committed trail.jsonl (ADR-063).
package work

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// Options is one invocation.
type Options struct {
	Root      string        // the lead's checkout, its top level
	Home      string        // fyr's home: the sandboxes are under sandbox.Root(Home, "work")
	RealHome  string        // $HOME: the token, check c's fingerprint
	Self      string        // the fyr that installs (os.Executable)
	Repo      string        // the install source; "" is the harness embedded in Self
	Timeout   time.Duration // agy is killed after this; 0 is DefaultTimeout
	Model     string        // agy --model; "" passes none
	Keep      bool          // keep the sandbox of a fetched worker too
	Out       io.Writer
	Now       func() time.Time
	StopGrace time.Duration // SIGTERM to SIGKILL on ctx's end; 0 is the sandbox's 5s
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
	ReasonTimedOut     = "timed out"
	ReasonKilled       = "killed"
	ReasonNoCommits    = "no commits"
	ReasonNoHandoff    = "no handoff"
	ReasonTwoHandoffs  = "more than one handoff"
	ReasonOutside      = "changed outside its Files"
	ReasonRewrote      = "rewrote history"
	ReasonLeadChanged  = "lead checkout changed"
	ReasonBranchFailed = "branch"
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
	Seconds       int
	Base, Head    string // Head is "" when the branch was not fetched
	Commits       int
	Dir           string   // the sandbox, "" once removed
	Handoff       *Record  // the one handoff the branch added
	Changes       []Record // the change records it added, by ID
	Drafts        int      // files copied to DraftsDir
	DraftsDir     string   // repo-relative, ending in /
	Notes         []string // the sandbox's notes, each a printable line
}

// Run refuses (an error, nothing made) or runs the Kickoff at file as one
// worker, printing the stamp line first and the result contract last.
func Run(ctx context.Context, o Options, file string) (Result, error) {
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	// Once, so the fetch (git -C Root) and the drafts copy (os.Root) read
	// the same clone (security review of Phase 2, finding 8).
	for _, p := range []*string{&o.Root, &o.Home, &o.RealHome} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			return Result{}, err
		}
		*p = abs
	}
	p, err := preflight(o, file)
	if err != nil {
		return Result{}, err
	}
	stamp := fmt.Sprintf("%s-%d", o.Now().Format("2006-01-02-150405"), os.Getpid())
	root := filepath.Join(sandbox.Root(o.Home, "work"), stamp)
	fmt.Fprintln(o.Out, sandbox.Printable(fmt.Sprintf("work: stamp %s  1 workers  %s/", stamp, root)))
	w := newWorker(o, p, stamp, filepath.Join(root, p.k.Name))
	res := w.run(ctx)
	for _, l := range res.Block() {
		fmt.Fprintln(o.Out, l)
	}
	return res, nil
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
