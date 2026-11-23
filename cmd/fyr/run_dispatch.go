package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
)

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, shortPage)
		return nil
	}
	cmd, rest, errMsg := splitLeadingVerb(args)
	if errMsg != "" {
		return usageErr(out, "", errMsg)
	}
	if cmd == "help" || cmd == "-h" || cmd == "-help" || cmd == "--help" {
		return runHelp(rest, out)
	}
	v, ok := verbs[cmd]
	if !ok {
		// A usage error like every other: exit 2, said on the writer (IN16),
		// pointing at the short page rather than printing it (D19 4).
		return usageErr(out, "", fmt.Sprintf("fyr: unknown command %q", cmd))
	}
	// A zip place (R8, ADR-038): the operator named the archive, so no tree is
	// resolved and no other verb runs — `uninstall --from-zip` must not
	// uninstall. Refused before the FlagSet sees it, so the refusal names the
	// right verb rather than an undefined flag.
	if cmd != "update" && hasFlag(rest, "from-zip") {
		fmt.Fprintln(out, "fyr "+cmd+": --from-zip belongs to update: fyr update --from-zip <file>")
		return exitCode(2)
	}

	c := newCtx(cmd, v, out)
	return c.execute(v, rest)
}

func (c *ctx) execute(v verb, rest []string) error {
	if stop, err := c.parse(v, rest); stop {
		return err
	}
	c.warnIgnoredRepo(v)
	if err := c.resolveGuardedHome(); err != nil {
		return err
	}
	if zip, err := c.updateFromZipFlag(); zip {
		return err
	}

	// Before the source is resolved: it falls back to the last install's
	// manifest, which a migration moves.
	if err := c.migrateStateDir(); err != nil {
		return err
	}
	if v.source {
		if done, err := c.resolveSource(); done {
			return err
		}
	}
	return v.run(c)
}

// resolveSource sets c.src for a verb that reads the harness; done says the
// verb ends here, with err as its result: the source did not resolve, or a
// zip place answered without one (BUG-203).
func (c *ctx) resolveSource() (done bool, err error) {
	src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
	if err != nil {
		if handled, zerr := c.zipPlaceWithoutSource(); handled {
			return true, zerr
		}
		return true, err
	}
	c.src = src
	return false, nil
}

// parse parses the verb's flags and arguments; stop says run ends here, with
// err as its result.
func (c *ctx) parse(v verb, rest []string) (stop bool, err error) {
	if c.cmd == "auto" && hasAutoSIMSubcommand(rest) {
		return c.parseAutoSIMArgs(rest)
	}
	if err := c.fs.Parse(reorder(c.fs, rest)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// Help is a successful ask: the verb's page, on the writer (IN15).
			fmt.Fprint(c.out, verbHelp(c.cmd))
			return true, nil
		}
		// The flag package has named the flag on the writer; a usage error
		// exits 2 like every other usage error here.
		return true, usageErr(c.out, c.cmd, "")
	}
	if !v.positional && c.fs.NArg() > 0 {
		// A stray argument is never ignored: `list - --check` and `update --
		// --from-zip x` would otherwise run the verb as if nothing had been said.
		return true, usageErr(c.out, c.cmd, fmt.Sprintf("%s takes no arguments: %q", c.cmd, c.fs.Args()))
	}
	if c.cmd == "metrics" || c.cmd == "report" {
		projectVisited := false
		c.fs.Visit(func(f *flag.Flag) { projectVisited = projectVisited || f.Name == "project" })
		if projectVisited && (c.metricsProject == "" || c.metricsProject == missingProjectSentinel || strings.HasPrefix(c.metricsProject, "-")) {
			fmt.Fprintf(c.out, "fyr %s: --project needs a directory: --project <dir>\n", c.cmd)
			return true, exitCode(2)
		}
	}
	return false, nil
}

// warnIgnoredRepo says on stderr that --repo was ignored: every FlagSet
// accepts it, and only the verbs that resolve the harness tree read it. Said,
// never silently dropped, and the verb still runs (IN36 of the 2026-10-27
// audit).
func (c *ctx) warnIgnoredRepo(v verb) {
	if c.repoFlag != "" && !v.source && !v.readsRepo {
		fmt.Fprintf(stderr, "fyr: warning: --repo is ignored by %s\n", c.cmd)
	}
}

// resolveHome sets c.home: --home, else the user's home, made absolute.
func (c *ctx) resolveHome() error {
	c.home = c.homeFlag
	if c.home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		c.home = h
	}
	// Absolute before anything runs: the checkout path rebuilds and runs the
	// build's install with cmd.Dir = repo, so a relative --home would land
	// inside the repository on a real install and under the invoking
	// directory on --dry-run (BUG-080).
	if abs, err := filepath.Abs(c.home); err == nil {
		c.home = abs
	}
	return nil
}

// updateFromZipFlag runs `update --from-zip`; zip is false when this is not
// that command line.
func (c *ctx) updateFromZipFlag() (zip bool, err error) {
	fromZipSet := false
	c.fs.Visit(func(f *flag.Flag) { fromZipSet = fromZipSet || f.Name == "from-zip" })
	// The parsed FlagSet decides, never a scan of the raw arguments: `--home --`
	// makes `--` a value, and a scan that stopped there would skip this branch
	// and run a normal update (security review 2026-10-01, round 2).
	if c.cmd != "update" || !fromZipSet {
		return false, nil
	}
	// Every refusal comes before any command runs, and an empty value is
	// refused rather than read as no flag — that would fall through to a
	// normal update, which on a release binary downloads and replaces the
	// binary.
	switch {
	case c.fromZip == "" || strings.HasPrefix(c.fromZip, "-"):
		// A value that looks like a flag was a flag: `--from-zip --dry-run`
		// must not read --dry-run as the file and run a real update.
		fmt.Fprintln(c.out, "fyr update: --from-zip needs a file: fyr update --from-zip <file>")
		return true, exitCode(2)
	case c.check || c.repoFlag != "":
		fmt.Fprintln(c.out, "fyr update: --from-zip installs the named zip; --check/--repo do not apply to it")
		return true, exitCode(2)
	}
	// After the migration: a real run has moved ~/.config/sh-agent by now, so
	// the guard reads the manifest the install would.
	if err := c.migrateStateDir(); err != nil {
		return true, err
	}
	if err := refuseDevelZipOverPath(c.home); err != nil {
		return true, err
	}
	return true, c.runUpdateFromZip()
}

// splitLeadingVerb accepts leading global flags (-C, --dir, --home, --repo in
// space-separated or = forms, single or double dash) before the verb or help,
// splicing them right after the verb so FlagSet parses them identically.
func splitLeadingVerb(args []string) (cmd string, rest []string, errMsg string) {
	var leading []string
	i := 0
	for i < len(args) {
		a := args[i]
		name, ok := flagTokenName(a)
		if !ok || !isGlobalFlag(name) {
			break
		}
		if _, val, hasEq := strings.Cut(a, "="); hasEq {
			if val == "" {
				return "", nil, fmt.Sprintf("fyr: flag needs an argument: %s", a)
			}
			leading = append(leading, a)
			i++
			continue
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
			return "", nil, fmt.Sprintf("fyr: flag needs an argument: %s", a)
		}
		leading = append(leading, a, args[i+1])
		i += 2
	}
	if i >= len(args) {
		return "", nil, "fyr: missing command"
	}
	cmd, rest = args[i], args[i+1:]
	if len(leading) > 0 {
		rest = append(append([]string(nil), leading...), rest...)
	}
	return cmd, rest, ""
}

// writesState reports whether this invocation may write under the state
// directory, and so moves the old one first (plan fyr D13). A dry run, a
// --check and a verb or subcommand that only reads answer false, except
// record: every record verb but a dry-run import answers true, get and list
// included, because opening brain.db may create or migrate it (record reads
// do not auto-load trail.jsonl; record sync loads it on demand, ADR-091).
// The table is by verb, so it errs early on purpose: adopt (which writes into
// the repo), a project-local search or index (which writes .agents/scratch/search.db)
// writes nothing there, yet migrates too. Moving the old directory early is harmless — one rename, refused for
// a symlink, and both directories present are left alone — and a verb that
// starts writing there later needs no change here (IN18).
func (c *ctx) writesState() bool {
	switch c.cmd {
	case "install", "sync", "uninstall", "adopt", "index", "search", "dream":
		return !c.dryRun
	case "record":
		// Every record verb except import --dry-run opens brain.db (creating
		// or migrating it; reads do not auto-load trail.jsonl, ADR-091).
		return !c.dryRun
	case "update":
		return !c.dryRun && !c.check
	case "memory":
		return c.fs.Arg(0) == "add"
	case "report":
		return !c.noIngest
	case "metrics":
		sub := c.fs.Arg(0)
		return sub == "ingest" || sub == "record-run" || (sub == "report" && !c.noIngest)
	case "eval":
		// Sandboxes, the metrics store and the testing record; --list reads,
		// and a refused --home is refused before anything moves (D37).
		return !c.evalList && evalHomeRefused(c.homeFlag, c.home) == ""
	case "auto":
		if len(c.args) >= 1 && c.args[0] == "sim" {
			return len(c.args) >= 2 && (c.args[1] == "record" || c.args[1] == "merge")
		}
		// status and any --dry-run read; on, off and real run write auto.json or auto/.
		sub := c.fs.Arg(0)
		return !c.dryRun && (sub == "on" || sub == "off" || sub == "run")
	case "relay":
		return !c.dryRun
	case "models":
		if (c.modelsTier != "" && c.modelsPick == "") || (c.modelsExplain != "" && c.modelsPick != "") {
			return false
		}
		if c.modelsRefresh || !models.CatalogExists(c.home) {
			return true
		}
		if c.modelsPick != "" {
			if normRole, _, err := models.NormalizeRoleTier(c.modelsPick, c.modelsTier); err == nil {
				return isCrossFamilyRole(normRole)
			}
		}
		return false
	}
	return false
}

// migrateStateDir moves ~/.config/sh-agent to ~/.config/fyr for a verb that
// writes there, and names it on stderr for one that only reads, which reads
// nothing from it.
func (c *ctx) migrateStateDir() error {
	if c.writesState() {
		return harness.MigrateStateDir(c.home, c.out)
	}
	if notice := harness.StateDirNotice(c.home); notice != "" {
		fmt.Fprintln(stderr, "fyr: "+notice)
	}
	return nil
}

// hasFlag reports whether args carry the named flag, as `-name`, `--name`,
// `-name=…` or `--name=…`, before `--`.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		t := strings.TrimLeft(a, "-")
		if len(a)-len(t) > 2 || len(a) == len(t) {
			continue
		}
		if t == name || strings.HasPrefix(t, name+"=") {
			return true
		}
	}
	return false
}

// reorder moves flags in front of positional arguments so that
// `fyr adopt my-skill --home /tmp/x` parses; the flag package stops at
// the first positional argument otherwise. Which flags take a value is read
// from the FlagSet itself, so a value flag can never be missing from a
// hand-kept list and swallow the next argument (BUG-002: the removed `report
// --feedback-repo /x --dry-run` once parsed --dry-run as the path).
func reorder(fs *flag.FlagSet, args []string) []string {
	valueFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			return
		}
		valueFlags["-"+f.Name], valueFlags["--"+f.Name] = true, true
	})
	var flags, positional []string
	marker := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			// Everything after the marker is positional, flag-shaped or not; the
			// marker itself is re-emitted once, between the flags and every
			// positional, so the flag package stops exactly there.
			marker = true
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			if rewritten, ok := rewriteBareValueFlag(fs, args, i, valueFlags); ok {
				flags = append(flags, rewritten)
				continue
			}
			flags = append(flags, a)
			if valueFlags[a] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		default:
			positional = append(positional, a)
		}
	}
	if marker {
		flags = append(flags, "--")
	}
	return append(flags, positional...)
}

func rewriteBareValueFlag(fs *flag.FlagSet, args []string, i int, valueFlags map[string]bool) (string, bool) {
	a := args[i]
	if !valueFlags[a] || (i+1 < len(args) && !strings.HasPrefix(args[i+1], "-")) {
		return "", false
	}
	if a == "--project" || a == "-project" {
		if isReportBareProject(fs, args, i, valueFlags) {
			return "--project=" + bareProjectSentinel, true
		}
		if fs.Name() == "metrics" {
			return "--project=" + missingProjectSentinel, true
		}
	}
	if fs.Name() == "models" && (a == "--explain" || a == "-explain") {
		return "--explain=" + bareExplainSentinel, true
	}
	return "", false
}

func isReportBareProject(fs *flag.FlagSet, args []string, i int, valueFlags map[string]bool) bool {
	a := args[i]
	if (a != "--project" && a != "-project") || !valueFlags[a] {
		return false
	}
	if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
		return false
	}
	if fs.Name() == "report" {
		return true
	}
	if fs.Name() != "metrics" {
		return false
	}
	for j := 0; j < len(args); j++ {
		if args[j] == "--" {
			return j+1 < len(args) && args[j+1] == "report"
		}
		if strings.HasPrefix(args[j], "-") {
			if j != i && valueFlags[args[j]] && j+1 < len(args) {
				j++
			}
			continue
		}
		return args[j] == "report"
	}
	return false
}
