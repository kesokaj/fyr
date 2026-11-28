package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// recordEval stores every trial and ingests its sandbox, then writes the
// testing record and the summary line; any trial not PASS, failed trial
// write or ingest, or unrun trial is exit 1.
func recordEval(c *runtimeCtx, root string, o eval.Options, src harness.Source, key string, rows []eval.Row, results []eval.Result) error {
	cli := agyVersion()
	byID := map[string]eval.Row{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	failed := recordEvalTrials(c, o.Model, key, cli, byID, results)
	// Asked again here: a row's agent ran for minutes with command(*) and
	// could have linked --home's state to the real one (D37's security review).
	if why := evalHomeRefused(c.homeFlag, c.home); why != "" {
		fmt.Fprintln(c.out, search.Printable("eval: record: not written: "+why))
		return exitCode(1)
	}
	tl := tallyOf(results)
	tl.fromSuite(byID, trialExpects(c.home, results))
	runs := max(o.Runs, 1)
	runModels := trialModels(c.home, results)
	if len(runModels) == 0 && o.Model != "" {
		runModels = []string{o.Model}
	}
	rec := evalRecord{root: root, o: o, src: src, cli: cli, key: key, model: modelName(runModels), runs: runs, results: results, tally: tl}
	id, err := brain.Add(brain.Options{Home: c.home, Dir: root}, brain.AddInput{
		Kind: "testing", Status: "closed",
		Title: fmt.Sprintf("Eval — %d of %d PASS at %s%s", tl.pass, tl.graded, gitShort(root), titleSuffix(runs, tl.errors)),
		Body:  strings.NewReader(rec.body()),
	})
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("eval: record: "+err.Error()))
		return exitCode(1)
	}
	fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("eval: %d of %d PASS, %s; record %s", tl.pass, tl.graded, errorsText(tl.errors), id)))
	if failed || len(results) < len(rows)*runs {
		return exitCode(1)
	}
	return nil
}

// recordEvalTrials ingests each started trial (so sessions.model can populate
// trials.policy_model when --model was not set) and records its trials row,
// returning true when any trial failed or failed to ingest/record.
func recordEvalTrials(c *runtimeCtx, explicitModel, key, cli string, byID map[string]eval.Row, results []eval.Result) bool {
	failed := false
	for _, r := range results {
		if r.Status != eval.StatusPass {
			failed = true
		}
		if err := ingestRow(c.home, r); err != nil {
			fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("eval: %s#%d ingest: %v", r.Row, r.Trial, err)))
			failed = true
		}
		t := trialOf(r, byID[r.Row], key, cli)
		t.PolicyModel = trialText(trialPolicyModel(c.home, explicitModel, r))
		if err := recordTrial(c.home, t); err != nil {
			fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("eval: %s#%d trial: %v", r.Row, r.Trial, err)))
			failed = true
		}
	}
	return failed
}

// tally is a run's counts: graded trials (PASS and FAIL) and their passes,
// the ERROR trials apart, and the same per row, in the order first run.
type tally struct {
	pass, graded, errors int
	rows                 []rowTally
}

// rowTally is one row's n graded trials, c passed, and its errors, with the
// row's kind and its trials' expect match once fromSuite has run.
type rowTally struct {
	row          string
	n, c, errors int
	kind         string
	expect       metrics.ExpectMatch
}

func tallyOf(results []eval.Result) tally {
	var tl tally
	at := map[string]int{}
	for _, r := range results {
		i, ok := at[r.Row]
		if !ok {
			i = len(tl.rows)
			at[r.Row] = i
			tl.rows = append(tl.rows, rowTally{row: r.Row})
		}
		rt := &tl.rows[i]
		switch r.Status {
		case eval.StatusError:
			tl.errors++
			rt.errors++
		case eval.StatusPass:
			tl.pass++
			rt.c++
			fallthrough
		default:
			tl.graded++
			rt.n++
		}
	}
	return tl
}

// fromSuite gives each row its kind and pools its trials' expect matches.
func (tl tally) fromSuite(byID map[string]eval.Row, matches map[string]metrics.ExpectMatch) {
	for i := range tl.rows {
		tl.rows[i].kind = byID[tl.rows[i].row].Kind
	}
	for row, x := range matches {
		for i := range tl.rows {
			if tl.rows[i].row == row {
				tl.rows[i].expect = x
			}
		}
	}
}

// interval is the row's Wilson 95 % interval over its graded trials, or
// n/a when it has none.
func (rt rowTally) interval() string {
	if rt.n == 0 {
		return "n/a"
	}
	return bracket(metrics.Wilson(rt.n, rt.c, 1.96))
}

// suiteInterval is the Wilson 95 % interval over the rows with a graded
// trial, each scored c/n (the eval-metrics plan § Interfaces), an indicator
// row left out of it, or n/a.
func (tl tally) suiteInterval() string {
	n, sum := 0, 0.0
	for _, rt := range tl.rows {
		if rt.n > 0 && rt.kind != eval.KindIndicator {
			n++
			sum += float64(rt.c) / float64(rt.n)
		}
	}
	if n == 0 {
		return "n/a"
	}
	return bracket(metrics.WilsonRate(n, sum/float64(n), 1.96))
}

func bracket(lo, hi float64) string { return fmt.Sprintf("[%.2f, %.2f]", lo, hi) }

// titleSuffix is the title's " (k=<N>, <e> errors)", shown past one run per
// row or when a trial was an ERROR.
func titleSuffix(runs, errors int) string {
	if runs <= 1 && errors == 0 {
		return ""
	}
	return fmt.Sprintf(" (k=%d, %s)", runs, errorsText(errors))
}

func errorsText(n int) string {
	if n == 1 {
		return "1 error"
	}
	return fmt.Sprintf("%d errors", n)
}

func trialPolicyModel(home, explicitModel string, r eval.Result) string {
	if explicitModel != "" {
		return explicitModel
	}
	if ms := trialModels(home, []eval.Result{r}); len(ms) > 0 {
		return ms[0]
	}
	return ""
}

// trialModels is the models the run's sessions name in home's store; a
// store that cannot be read names none, and the record says unknown.
func trialModels(home string, results []eval.Result) []string {
	runs := make([]string, 0, len(results))
	for _, r := range results {
		runs = append(runs, r.Run)
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return nil
	}
	defer s.Close()
	models, _ := s.RunModels(runs)
	return models
}

// trialExpects is each row's expect match over the run's trials, pooled; a
// store that cannot be read matches nothing, and the record prints -.
func trialExpects(home string, results []eval.Result) map[string]metrics.ExpectMatch {
	runs := make([]string, 0, len(results))
	for _, r := range results {
		runs = append(runs, r.Run)
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return nil
	}
	defer s.Close()
	matches, err := s.ExpectMatches(runs)
	if err != nil {
		return nil
	}
	out := map[string]metrics.ExpectMatch{}
	for _, r := range results {
		x := out[r.Row]
		x.M += matches[r.Run].M
		x.N += matches[r.Run].N
		out[r.Row] = x
	}
	return out
}

// modelName is the header's model: the one model the sessions name,
// "mixed (<each>)" for several, "unknown" for none.
func modelName(models []string) string {
	switch len(models) {
	case 0:
		return "unknown"
	case 1:
		return models[0]
	}
	return "mixed (" + strings.Join(models, ", ") + ")"
}

// registerClone registers a row's clone under the eval home before its
// sandbox is ingested, so the metrics join reads the clone's trail and its
// eval=<row> sessions get their handoff, testing, review and bug events
// (ledger item 76). The operator's run of fyr eval is the consent; the
// marker goes into the clone's own git directory, which dies with the
// sandbox, and the seal names the eval home's key, never the real home's.
//
// The agent ran with command(*) in the clone, so its git directory is
// checked first: a clone that is a link, or whose git directory resolves
// outside the clone (a .git file or commondir pointing at another
// repository), is not registered, because the marker would be written there.
// A --home that is the real home, or whose state directory is the real one
// through a link, is refused: the seal would take the real key, and the join
// would load the trail the agent wrote into the real brain store. Where eval
// cannot see the processes a row left (procsChecked false, systems other than
// Linux and macOS), one still running could swap the git directory between that
// check and the write, so nothing is registered and the sessions get no record
// events; that is a known limit there, not a failed ingest, which would make
// every run exit 1 (the phase's security review, rounds 1 and 2).
func registerClone(home, sandbox string, procsChecked bool) error {
	if real, shared := sharesRealState(home); shared {
		return fmt.Errorf("--home is the real home %s, or shares its state directory; eval registers a clone only under a separate home", real)
	}
	if !procsChecked {
		return nil
	}
	app := filepath.Join(sandbox, "app")
	fi, err := os.Lstat(app)
	if os.IsNotExist(err) {
		return nil // the row stopped before its clone: nothing to join
	}
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", app)
	}
	if err := verifyCloneRepoInside(app); err != nil {
		return err
	}
	_, err = trail.Register(filepath.Join(home, harness.StateDir), app, time.Now())
	return err
}

func verifyCloneRepoInside(app string) error {
	out, err := trail.Command(app, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("git rev-parse in %s: %w", app, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	realApp, err := filepath.EvalSymlinks(app)
	if err != nil || len(lines) != 2 {
		return fmt.Errorf("%s is not its own repository", app)
	}
	for _, p := range lines {
		r, err := filepath.EvalSymlinks(p)
		if err != nil || (r != realApp && !strings.HasPrefix(r, realApp+string(os.PathSeparator))) {
			return fmt.Errorf("%s's repository is not inside it", app)
		}
	}
	return nil
}

// trialTextMax is the longest harness or cli value metrics stores on a
// trial (its trialText).
const trialTextMax = 120

// harnessKey names the harness a run's trials install (the eval-metrics
// plan D15): the install source's short HEAD with " (dirty)" when what an
// install reads (harness.ManagedDirs) has changes, so the trail a run's own
// testing record writes leaves the key alone (PLAN.md item 53), " (dirty:
// unknown)" when that cannot be read, "unknown" for
// a source that is not a git checkout, or "embedded <version>" for the
// harness built into a release binary ("embedded" when it has none). Two
// dirty trees at one HEAD share a key, and so do two binaries built from
// one HEAD with different cmd/ or internal/ edits: stated limits
// (SPEC-fyr-eval § Known limits).
func harnessKey(src harness.Source) string {
	if src.Dir == "" {
		return trialText(strings.TrimSpace("embedded " + src.Version))
	}
	key := gitShort(src.Dir)
	if key == "unknown" {
		return key
	}
	if dirty, err := trail.DirtyUnder(src.Dir, harness.ManagedDirs); err != nil {
		key += " (dirty: unknown)"
	} else if dirty {
		key += " (dirty)"
	}
	return trialText(key)
}

// trialText is s as a trial stores it: printable, at most trialTextMax
// bytes, cut on a rune boundary.
func trialText(s string) string {
	s = search.Printable(s)
	if len(s) <= trialTextMax {
		return s
	}
	n := trialTextMax
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func trialStatusString(st string) string {
	switch st {
	case eval.StatusPass:
		return "pass"
	case eval.StatusFail:
		return "fail"
	case eval.StatusError:
		return "error"
	default:
		return ""
	}
}

// trialOf is the trials row of one result of suite row row, under the run's
// harness key and the CLI's version line: the row's version, kind and
// cluster as parsed, defaults applied, and its expect list (ADR-085).
func trialOf(r eval.Result, row eval.Row, key, cli string) metrics.Trial {
	t := metrics.Trial{
		Run: r.Run, Label: "eval=" + r.Row, Harness: key, CLI: trialText(cli),
		Status: trialStatusString(r.Status),
		Cause:  r.Cause, StartedAt: r.Started, Seconds: int64(r.Seconds),
		RowVersion: row.Version, Kind: row.Kind, Cluster: row.Cluster, Expect: row.Expect,
	}
	if r.SetupSeconds >= 0 {
		s := int64(r.SetupSeconds)
		t.SetupSeconds = &s
	}
	if r.SessionSeconds >= 0 {
		s := int64(r.SessionSeconds)
		t.SessionSeconds = &s
	}
	if r.CheckSeconds >= 0 {
		s := int64(r.CheckSeconds)
		t.CheckSeconds = &s
	}
	return t
}

// recordTrial stores t in home's metrics store, once (INSERT OR IGNORE).
// Asked first, as ingestRow asks: the trial's agent may have linked
// --home's state to the real one since the start check.
func recordTrial(home string, t metrics.Trial) error {
	if real, shared := sharesRealState(home); shared {
		return fmt.Errorf("not recorded: --home now shares the real state of %s", real)
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RecordTrial(t)
}

// evalRecord is what the testing record is written from: the run's
// options, install source and keys, and its trials with their tally.
type evalRecord struct {
	root            string
	o               eval.Options
	src             harness.Source
	cli, key, model string
	runs            int
	results         []eval.Result
	tally           tally
}

// body is the testing record's body (SPEC-fyr-eval § The testing record):
// what ran against what, how it is counted, one table row per trial and
// one per suite row. No output of the CLI or a check goes in it.
func (e evalRecord) body() string {
	var b strings.Builder
	fixture := e.o.Fixture
	if fixture == "" {
		fixture = e.root
	}
	source := "embedded " + e.src.Version
	if e.src.Dir != "" {
		source = e.src.Dir
		// A tree Dirty cannot read is labelled so, never shown as clean
		// (security review of the 2026-10-27 fix).
		if dirty, err := trail.Dirty(e.src.Dir); err != nil {
			source += " (dirty: unknown, " + search.Printable(err.Error()) + ")"
		} else if dirty {
			source += " (dirty)"
		}
	}
	fmt.Fprintf(&b, "# fyr eval — %s\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- fyr: %s\n", buildVersion())
	fmt.Fprintf(&b, "- project: %s at %s\n", e.root, gitShort(e.root))
	fmt.Fprintf(&b, "- fixture: %s at %s\n", fixture, gitShort(fixture))
	fmt.Fprintf(&b, "- install source: %s\n", source)
	fmt.Fprintf(&b, "- runs per row: %d; estimator: c/n over graded trials; pass^k = C(c,k)/C(n,k)\n", e.runs)
	fmt.Fprintf(&b, "- model: %s; agy: %s; harness: %s\n", search.Printable(e.model), e.cli, e.key)
	fmt.Fprintf(&b, "- interval: Wilson 95%%, per row below and over rows for the suite: %s\n", e.tally.suiteInterval())
	if !eval.ProcsChecked {
		b.WriteString("- the leftover-process check did not run: no /proc here (plan fyr-eval O1)\n")
	}
	b.WriteString("\n| row | trial | status | cause | reasons | agy exit | seconds | session s | sandbox |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range e.results {
		cause, reasons, session := orDash(r.Cause), orDash(strings.Join(r.Reasons, "; ")), "-"
		if r.SessionSeconds >= 0 {
			session = fmt.Sprint(r.SessionSeconds)
		}
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s | %s | %d | %d | %s | `%s` |\n", r.Row, r.Trial, r.Status, cause, strings.ReplaceAll(reasons, "|", `\|`), r.AgyExit, r.Seconds, session, r.Dir)
	}
	b.WriteString("\n| row | n | c | errors | wilson95 | expect |\n|---|---|---|---|---|---|\n")
	for _, rt := range e.tally.rows {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %s | %s |\n", rt.row, rt.n, rt.c, rt.errors, rt.interval(), rt.expect)
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
