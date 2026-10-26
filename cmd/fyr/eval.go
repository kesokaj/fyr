package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// suiteID is the guide record that holds a project's suite (plan fyr-eval D2).
const suiteID = "GUIDE-eval"

// evalSelf is the fyr each sandbox installs and lists records with; a test
// replaces it, since the test binary is not a fyr.
var evalSelf = os.Executable

func init() {
	verbs["eval"] = verb{flags: func(c *ctx) {
		c.fs.StringVar(&c.evalFixture, "fixture", "", "eval: the git repository each row clones (default: this checkout)")
		c.fs.BoolVar(&c.evalList, "list", false, "eval: print the suite's rows, run nothing")
	}, positional: true, run: runEval}
}

// runEval serves `fyr eval` (spec SPEC-fyr-eval, ADR-065). Every refusal
// comes before anything is created, in the plan's step-0 order (D20): not a
// checkout, no install source, the sandbox root, the suite and its HEAD copy,
// the rows, then the login and agy (inside eval.Run). The install source is
// resolved here rather than by the verb table's source flag, so its error is
// a refusal like the others (exit 2) and --list needs none.
func runEval(c *ctx) error {
	names := c.fs.Args()
	switch {
	case c.evalList && len(names) > 0:
		fmt.Fprintln(c.out, "fyr eval: --list takes no row")
		return exitCode(2)
	case !c.evalList && len(names) == 0:
		fmt.Fprintln(c.out, "usage: fyr eval [--fixture DIR] <row>...  |  fyr eval --list")
		return exitCode(2)
	}
	refuse := func(format string, a ...any) error {
		fmt.Fprintln(c.out, search.Printable("eval: "+fmt.Sprintf(format, a...)))
		return exitCode(2)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	_, root, err := trail.Roots(wd)
	if err != nil {
		return refuse("%s is not a git checkout", wd)
	}
	if c.evalList {
		suite, err := readSuite(root, false)
		if err != nil {
			return refuse("%v", err)
		}
		for _, r := range suite {
			agent, grants := r.Agent, "named"
			if agent == "" {
				agent = "-"
			}
			if _, wide := r.Wide(); wide {
				grants = "wide"
			}
			fmt.Fprintln(c.out, search.Printable(r.ID+"  "+agent+"  "+grants))
		}
		return nil
	}

	src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
	if err != nil {
		return refuse("%v", err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		return refuse("no home directory: %v", err)
	}
	self, err := evalSelf()
	if err != nil {
		return refuse("the running fyr: %v", err)
	}
	fixture := c.evalFixture
	if fixture != "" {
		if fixture, err = filepath.Abs(fixture); err != nil {
			return refuse("--fixture: %v", err)
		}
		if err := trail.Command(fixture, "rev-parse", "--git-dir").Run(); err != nil {
			return refuse("--fixture %s is not a git repository", fixture)
		}
	}
	o := eval.Options{Root: root, Home: c.home, RealHome: realHome, Fixture: fixture, Self: self, Repo: src.Dir, Out: c.out}
	if err := eval.Preflight(o); err != nil {
		return refuse("%v", err)
	}
	suite, err := readSuite(root, true)
	if err != nil {
		return refuse("%v", err)
	}
	rows, err := eval.SelectRows(suite, names)
	if err != nil {
		return refuse("%v", err)
	}

	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	results, err := eval.Run(sig, o, rows)
	if err != nil {
		return refuse("%v", err)
	}
	stop()

	failed := false
	// After the verdict, never before: the store stamps a label's outcome
	// first-write-wins (D6), and an interrupted invocation still ingests the
	// rows it started (D22).
	for _, r := range results {
		if r.Verdict != "PASS" {
			failed = true
		}
		if err := ingestRow(c.home, r); err != nil {
			fmt.Fprintln(c.out, search.Printable("eval: "+r.Row+" ingest: "+err.Error()))
			failed = true
		}
	}
	pass := 0
	for _, r := range results {
		if r.Verdict == "PASS" {
			pass++
		}
	}
	head := gitShort(root)
	body := evalBody(root, o, src, results)
	id, err := brain.Add(brain.Options{Home: c.home, Dir: root}, brain.AddInput{
		Kind: "testing", Status: "closed",
		Title: fmt.Sprintf("Eval — %d of %d PASS at %s", pass, len(results), head),
		Body:  strings.NewReader(body),
	})
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("eval: record: "+err.Error()))
		return exitCode(1)
	}
	fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("eval: %d of %d PASS; record %s", pass, len(results), id)))
	if failed || len(results) < len(rows) {
		return exitCode(1)
	}
	return nil
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

// ingestRow reads one sandbox home into the metrics store under the label
// eval=<row>, its verdict as the outcome. A row that failed before its
// sandbox home existed has nothing to read; a sandbox that already existed is
// another invocation's and is never read as this one's.
func ingestRow(home string, r eval.Result) error {
	sb := filepath.Join(r.Dir, "home")
	if st, err := os.Stat(sb); err != nil || !st.IsDir() || slices.Contains(r.Reasons, eval.ReasonExists) {
		return nil
	}
	outcome := "pass"
	if r.Verdict != "PASS" {
		outcome = "fail"
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	var log bytes.Buffer
	res, err := metrics.Ingest(s, metrics.Options{Home: home, Source: sb, Label: "eval=" + r.Row, Outcome: outcome}, &log)
	if err != nil {
		return err
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%s failed: %s", strings.Join(res.Failed, ", "), strings.TrimSpace(log.String()))
	}
	return nil
}

// evalBody is the testing record's body: what ran against what, and one
// table row per result. No output of the CLI or a check goes in it.
func evalBody(root string, o eval.Options, src harness.Source, results []eval.Result) string {
	var b strings.Builder
	fixture := o.Fixture
	if fixture == "" {
		fixture = root
	}
	source := "embedded " + src.Version
	if src.Dir != "" {
		source = src.Dir
		if out, err := trail.Command(src.Dir, "status", "--porcelain").Output(); err == nil && len(bytes.TrimSpace(out)) > 0 {
			source += " (dirty)"
		}
	}
	fmt.Fprintf(&b, "# fyr eval — %s\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- fyr: %s\n", buildVersion())
	fmt.Fprintf(&b, "- project: %s at %s\n", root, gitShort(root))
	fmt.Fprintf(&b, "- fixture: %s at %s\n", fixture, gitShort(fixture))
	fmt.Fprintf(&b, "- install source: %s\n", source)
	fmt.Fprintf(&b, "- agy: %s\n", agyVersion())
	if runtime.GOOS != "linux" {
		b.WriteString("- the leftover-process check did not run: no /proc here (plan fyr-eval O1)\n")
	}
	b.WriteString("\n| row | verdict | reasons | agy exit | seconds | sandbox |\n|---|---|---|---|---|---|\n")
	for _, r := range results {
		reasons := strings.Join(r.Reasons, "; ")
		if reasons == "" {
			reasons = "-"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %d | `%s` |\n", r.Row, r.Verdict, strings.ReplaceAll(reasons, "|", `\|`), r.AgyExit, r.Seconds, r.Dir)
	}
	return b.String()
}

// gitShort is dir's HEAD as a short hash, or "unknown".
func gitShort(dir string) string {
	out, err := trail.Command(dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// agyVersion is the first line of `agy --version`, bounded so a CLI that
// hangs cannot hold the record back.
func agyVersion() string {
	cx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cx, "agy", "--version").Output()
	if err != nil && len(out) == 0 {
		if errors.Is(cx.Err(), context.DeadlineExceeded) {
			return "unknown (agy --version timed out)"
		}
		return "unknown"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}
