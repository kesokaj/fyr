package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// suiteID is the guide record that holds a project's suite (plan fyr-eval D2).
const suiteID = "GUIDE-eval"

// evalSelf is the fyr each sandbox installs and lists records with; a test
// replaces it, since the test binary is not a fyr.
var evalSelf = os.Executable

// evalRun is eval.Run; a test wraps it to land a signal before the first row.
var evalRun = eval.Run

func init() {
	verbs["eval"] = verb{flags: func(c *ctx) {
		c.fs.StringVar(&c.evalFixture, "fixture", "", "the git repository `DIR` each row clones (default: this checkout)")
		c.fs.BoolVar(&c.evalList, "list", false, "print the suite's rows (id, agent or -, named or wide, version, kind) and run nothing")
		c.fs.IntVar(&c.evalRuns, "runs", 1, "run each named row `N` times, 1 to 25, each trial in its own sandbox")
		c.fs.String("suite-file", "", "read the suite from `PATH` outside every checkout; labels rows eval=holdout/<id>")
	}, positional: true, readsRepo: true, run: runEval}
}

func evalSuiteFile(c *ctx) (string, bool) {
	set := false
	c.fs.Visit(func(f *flag.Flag) { set = set || f.Name == "suite-file" })
	if fn := c.fs.Lookup("suite-file"); fn != nil {
		return fn.Value.String(), set
	}
	return "", set
}

func loadEvalSuite(c *ctx, root string, atHead bool) ([]eval.Row, error) {
	if path, set := evalSuiteFile(c); set {
		return eval.ReadSuiteFile(path)
	}
	return readSuite(root, atHead)
}

func labelHoldout(c *ctx, rows []eval.Row, results []eval.Result) {
	if _, set := evalSuiteFile(c); !set {
		return
	}
	for i := range rows {
		rows[i].ID = "holdout/" + rows[i].ID
	}
	for i := range results {
		results[i].Row = "holdout/" + results[i].Row
	}
}

// runEval serves `fyr eval` (spec SPEC-fyr-eval, ADR-065). Every refusal
// comes before anything is created, in the plan's step-0 order (D20); the
// --home refusal (ledger D37) comes right after the usage errors, before
// every refusal that reads the tree: not a checkout, no install source, the sandbox root, the suite and its HEAD copy,
// the rows, then the login and agy (inside eval.Run). The install source is
// resolved here rather than by the verb table's source flag, so its error is
// a refusal like the others (exit 2) and --list needs none.
func runEval(c *ctx) error {
	names := c.fs.Args()
	if err := evalUsage(c, names); err != nil {
		return err
	}
	if why := evalHomeRefused(c.homeFlag, c.home); why != "" && !c.evalList {
		return c.evalRefuse("%s", why)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	_, root, err := trail.Roots(wd)
	if err != nil {
		return c.evalRefuse("%s is not a git checkout", wd)
	}
	if c.evalList {
		return listEval(c, root)
	}
	o, src, rows, err := evalSetup(c, root, names)
	if err != nil {
		return err
	}
	// Before the first trial: every trial installs from the source as it is
	// now, so its key names the harness the trials ran.
	key := harnessKey(src)

	// A hangup too: its default would kill fyr before the token link is
	// removed, and agy, in its own process group, would run on unattended.
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	results, err := evalRun(sig, o, rows)
	if err != nil {
		return c.evalRefuse("%v", err)
	}
	stop()
	if len(results) == 0 {
		// Interrupted before the first row: nothing ran, so nothing is
		// recorded, and the export stays as the operator left it.
		fmt.Fprintln(c.out, "eval: interrupted before the first row; no record written")
		return exitCode(1)
	}
	labelHoldout(c, rows, results)
	return recordEval(c, root, o, src, key, rows, results)
}

// evalUsage is the usage errors, exit 2, before any other refusal.
func evalUsage(c *ctx, names []string) error {
	runsSet := false
	c.fs.Visit(func(f *flag.Flag) { runsSet = runsSet || f.Name == "runs" })
	switch {
	case c.evalList && len(names) > 0:
		fmt.Fprintln(c.out, "fyr eval: --list takes no row")
		return exitCode(2)
	case c.evalList && runsSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no --runs")
		return exitCode(2)
	case !c.evalList && len(names) == 0:
		return usageErr(c.out, "eval", "")
	case c.evalRuns < 1 || c.evalRuns > eval.MaxRuns:
		return usageErr(c.out, "eval", fmt.Sprintf("fyr eval: --runs %d: want 1 to %d", c.evalRuns, eval.MaxRuns))
	}
	return nil
}

// evalRefuse prints an eval refusal and returns exit 2.
func (c *ctx) evalRefuse(format string, a ...any) error {
	fmt.Fprintln(c.out, search.Printable("eval: "+fmt.Sprintf(format, a...)))
	return exitCode(2)
}

// listEval serves `fyr eval --list`: the working export's rows, each with
// its version and kind (ADR-085).
func listEval(c *ctx, root string) error {
	suite, err := loadEvalSuite(c, root, false)
	if err != nil {
		return c.evalRefuse("%v", err)
	}
	for _, r := range suite {
		agent, grants := r.Agent, "named"
		if agent == "" {
			agent = "-"
		}
		if _, wide := r.Wide(); wide {
			grants = "wide"
		}
		fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("%s  %s  %s  v%d  %s", r.ID, agent, grants, r.Version, r.Kind)))
	}
	return nil
}

// evalSetup makes every refusal that comes before a row runs, in the plan's
// step-0 order, and returns the run's options, its install source and its
// rows.
func evalSetup(c *ctx, root string, names []string) (eval.Options, harness.Source, []eval.Row, error) {
	fail := func(err error) (eval.Options, harness.Source, []eval.Row, error) {
		return eval.Options{}, harness.Source{}, nil, err
	}
	src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
	if err != nil {
		return fail(c.evalRefuse("%v", err))
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		return fail(c.evalRefuse("no home directory: %v", err))
	}
	self, err := evalSelf()
	if err != nil {
		return fail(c.evalRefuse("the running fyr: %v", err))
	}
	fixture := c.evalFixture
	if fixture != "" {
		if fixture, err = filepath.Abs(fixture); err != nil {
			return fail(c.evalRefuse("--fixture: %v", err))
		}
		if err := trail.Command(fixture, "rev-parse", "--git-dir").Run(); err != nil {
			return fail(c.evalRefuse("--fixture %s is not a git repository", fixture))
		}
	}
	o := eval.Options{Root: root, Home: c.home, RealHome: realHome, Fixture: fixture, Self: self, Repo: src.Dir, Runs: c.evalRuns, Out: c.out}
	if err := eval.Preflight(o); err != nil {
		return fail(c.evalRefuse("%v", err))
	}
	suite, err := loadEvalSuite(c, root, true)
	if err != nil {
		return fail(c.evalRefuse("%v", err))
	}
	rows, err := eval.SelectRows(suite, names)
	if err != nil {
		return fail(c.evalRefuse("%v", err))
	}
	return o, src, rows, nil
}

// readSuite parses GUIDE-eval from root's trail export. With atHead it also
// refuses a suite that differs from HEAD's, so a run always names the commit
// its scenarios came from (D3); a testing record an earlier run wrote leaves
// the export dirty without blocking the next run. --list runs nothing and
// reads the working export alone (D19).
func readSuite(root string, atHead bool) ([]eval.Row, error) {
	recs, ok, err := brain.ReadTrail(root)
	if err != nil {
		return nil, err
	}
	work, found := findRecord(recs, suiteID)
	if !ok || !found {
		return nil, fmt.Errorf("no %s record in %s", suiteID, filepath.Join(root, ".agents", "trail.jsonl"))
	}
	if atHead {
		notCommitted := fmt.Errorf("%s is not committed; commit it first", suiteID)
		out, err := trail.Command(root, "show", "HEAD:.agents/trail.jsonl").Output()
		if err != nil {
			return nil, notCommitted
		}
		headRecs, err := brain.ParseTrail(bytes.NewReader(out))
		if err != nil {
			return nil, fmt.Errorf("HEAD's trail.jsonl: %v", err)
		}
		head, found := findRecord(headRecs, suiteID)
		if !found {
			return nil, notCommitted
		}
		if head.Body != work.Body || head.Updated != work.Updated {
			return nil, fmt.Errorf("%s has uncommitted changes; commit them first", suiteID)
		}
	}
	return eval.ParseSuite(work.Body)
}

func findRecord(recs []brain.Record, id string) (brain.Record, bool) {
	for _, r := range recs {
		if r.ID == id {
			return r, true
		}
	}
	return brain.Record{}, false
}

// ingestRow reads one trial's sandbox home into the metrics store under the
// label eval=<row>, its status as the outcome and its run on every session. A row that failed before its
// sandbox home existed has nothing to read; a sandbox that already existed is
// another invocation's and is never read as this one's; a sandbox the agent
// replaced may lead anywhere, the real home included; a link in place of
// the home, or of a directory on the way to its conversations, is never
// followed, whatever the reasons say (IN35 of the 2026-10-27 audit, and the
// security review of its fix).
func ingestRow(home string, r eval.Result) error {
	// Before any store opens: the row may have linked --home's state to the
	// real one since the start check (D37's security review).
	if real, shared := sharesRealState(home); shared {
		return fmt.Errorf("not ingested: --home now shares the real state of %s", real)
	}
	sb := filepath.Join(r.Dir, "home")
	// A row that left processes running is not read either: one that ignored
	// SIGTERM could swap a directory between this check and the ingest
	// (security review of the fix, round 2). Nor is one the machine could not
	// write, which read sandbox replaced until PLAN.md item 48.
	if !realDirs(sb, ".gemini", "antigravity-cli", "conversations") || sandbox.Compromised(r.Reasons) {
		return nil
	}
	outcome := strings.ToLower(r.Status)
	// An unregistered clone still has its sessions ingested, without record
	// events; the refusal is said after the ingest, as a failed ingest.
	regErr := registerClone(home, r.Dir, eval.ProcsChecked)
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	var log bytes.Buffer
	res, err := metrics.Ingest(s, metrics.Options{Home: home, Source: sb, Label: "eval=" + r.Row, Outcome: outcome, Run: r.Run}, &log)
	if err != nil {
		return err
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%s failed: %s", strings.Join(res.Failed, ", "), strings.TrimSpace(log.String()))
	}
	if regErr != nil {
		return fmt.Errorf("the clone was not registered, so its sessions have no record events: %w", regErr)
	}
	return nil
}

// realDirs reports whether dir, and each of parts below it in turn, is a
// directory and not a link. A missing part below dir is fine: there is
// nothing to read there, and ingest finds nothing.
func realDirs(dir string, parts ...string) bool {
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return false
	}
	for _, p := range parts {
		dir = filepath.Join(dir, p)
		st, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err != nil || !st.IsDir() {
			return false
		}
	}
	return true
}
