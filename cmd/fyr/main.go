// Command fyr installs, resyncs, inspects, and removes the fyr
// harness — global context, skills, and subagents — for the Antigravity CLI.
//
//	fyr install [--dry-run]     deploy the repo into ~/.gemini/config
//	fyr status                  show drift between the repo and what is installed
//	fyr diff                    unified diff of every drifted file
//	fyr update [--check|--dry-run]  checkout: git pull and reinstall; release binary: download and verify the newest release
//	fyr update --from-zip FILE  zip place: install the harness in a repository zip with the running binary
//	fyr uninstall [--dry-run]   remove exactly what install wrote, and the dream timer
//	fyr list                    print the skill and agent inventory
//	fyr render                  print the assembled context file
//	fyr adopt <name|path>       copy a skill from elsewhere into the repo
//	fyr search <words>          ranked search of this checkout's files and trail, reconciled against git first
//	fyr index [--rebuild]       reconcile (or rebuild) the search index and print its counts
//	fyr dream --all|--project DIR [--dry-run]  propose rules and skills from every project's memory, trail, feedback and transcripts
//	fyr dream --install-timer|--remove-timer [--dry-run]  install or remove the nightly timer
//	fyr metrics ingest|show|report|export  read every session's shape into the metrics store, summarise it for agent evaluation (text, JSON or an HTML report with charts), or back it up as JSONL
//	fyr memory add|show         write one memory record (body on stdin) or print this project's memory index (the brain store, D14)
//	fyr record add|get|edit|list|import  the brain store: this project's trail records (exported to .agents/trail.jsonl) and the global ones
//	fyr version
package main

import (
	"debug/buildinfo"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

const usage = `fyr — lifecycle tool for the fyr harness

Commands:
  install [--dry-run]     deploy context, skills, and agents into ~/.gemini/config (alias: sync)
  status                  show what is installed and whether it drifted from the repo (exit 1 on drift)
  diff                    unified diff of every drifted file
  update [--check|--dry-run]  checkout: git fetch, pull --ff-only and reinstall when behind; release binary: download the newest release, check it against its SHA256SUMS, replace the binary and reinstall (--check only reports, exit 2 when behind; --dry-run reports what update would do and changes nothing in the tree or the home; at a checkout both still run git fetch, which moves the remote-tracking refs)
  update --from-zip FILE [--dry-run]  zip place: unpack a repository zip you downloaded, install its harness and the running binary (nothing in the zip replaces the binary), delete the unpacked copy; with the release's SHA256SUMS beside it the zip is checked against it, otherwise it is installed marked UNVERIFIED; the unpacked copy is a zip-* directory under ~/.config/fyr, or, on a --dry-run with no ~/.config/fyr, beside the zip, removed before the command returns
  uninstall [--dry-run]   remove exactly the files install wrote, and the dream timer when one is installed; nothing else
  list                    print the skill and agent inventory
  render                  print the assembled context file
  adopt <name|path>       copy a skill installed elsewhere into the repo's skills/ (--force to replace)
  search [--limit N] [--kind K] [--global] <words...>  ranked search of the brain store's records (this project's and the global ones; --global every scope), printed first as ID, kind, status, date, title and snippet, then of every file and trail record in this git checkout: path:line, kind, record status, snippet; the index (.agents/scratch/search.db) is reconciled against git first, so it never answers from another branch
  search --refs <BUG-NNN|ADR-NNN|path> [--kind K] [--global]  every record naming an ID, a file or a directory (trailing /), with the ID's own status: the brain store's records first, then the files
  search --open [--kind K] [--global]  open bugs and plans with unticked boxes: the brain store's open records first (also current specs, deploy docs and guides, queued proposals), then the files
      --global searches the global search index (~/.config/fyr/search.db): ~/.config/fyr's logs and proposals and the .agents/docs trail of every harness-scaffolded checkout that has run search; --kind is one of bug adr plan change handoff review testing retro spec deploy trail code doc feedback proposal learning guide memory; --limit 1-50, default 10, for the records and the files each
  index [--global] [--rebuild]  reconcile the search index, or delete and rebuild it, and print its counts
  memory add --type T --name SLUG --description TEXT  save one memory, its body on stdin, as the record MEMORY-SLUG in this project's scope of the brain store, replacing one of the same name; never exported (T: user, feedback, project or reference); every session writes memory this way (R10, D14)
  dream --install-timer | --remove-timer [--dry-run]  install (systemd user timer on Linux, launchd agent on macOS) or remove the nightly "dream --all", run with the PATH of the installing shell; uninstall removes it too
  dream --all | --project DIR [--dry-run]  the background dreamer: first ingest every session's shape into the metrics store; then for every indexed project (or one) that changed since its last dream, send its memory, new trail, feedback, a numbers-only summary of its metrics and its Antigravity conversations, redacted, to agy with no tool it may use, and write the proposals and draft skills it returns under ~/.config/fyr/proposals/dream/ (R3's exception)
  metrics ingest [--source DIR --label KEY=VALUE [--outcome pass|fail]]  read the shape of every Antigravity session (tools, command verbs, tokens, timings; never arguments or prose) into ~/.config/fyr/metrics.db, the metrics store; --source reads a sandbox home and stamps its sessions with the label; conversations come from --source, else this home
  metrics show [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--json]  the agent-evaluation summary of the metrics store, an agy column (and a claude (historical) column only when the store holds rows an older ingest read from Claude Code): outcome (DoD closure, verifier status, review findings, escaped bugs, corrections, pass^k per label), efficiency (tool calls, turns, tokens, cost, wall and active time, peak context, the main sessions whose peak passed the context target — N tokens, default 300000 — and each main session's peak in --json, compactions), process adherence (fyr search vs grep, forbidden command shapes, first reads, skills) and reliability (tool error rate, denials, interrupts, retry loops, subagent tool calls); n/a is a metric that CLI cannot produce; sandbox (labelled) sessions are left out unless named
  metrics report [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--out FILE]  write show's summary as one self-contained HTML file with charts (per-day tool calls, tokens, cost, search:grep and errors, one line per column; tool calls by tool; subagent tool calls by type; pass^k per sandbox label) and the summary table, with the sessions over the context target as k of n, inline SVG, no script, light and dark; default ~/.config/fyr/reports/<date-time>-metrics.html, 0600, an existing file refused
  metrics export FILE     write every row of the metrics store but its cmd_hash salt to FILE as JSONL (0600; an existing FILE is refused): its only backup, since the store is the one copy once a transcript expires
  memory show             print this project's memory index, one line per memory record, capped at what a session loads (200 lines, 25 KB)
  record add <kind> --title T [--status S] [--id ID] [--global [--project ROOT]]  store one record, its body on stdin (empty or over 1 MiB refused), in this project's scope of the brain store (~/.config/fyr/brain.db; every worktree of a repository shares one) or with --global the global scope, and print its ID (BUG-NNN, ADR-NNN, else KIND-YYYY-MM-DD-HHMM-slug); kinds: plan review testing change handoff bug adr retro learning spec deploy guide memory, and feedback and proposal, which are --global only; --project names the project a global record is about
  record get <id>... [--global]  print each record's line and its body, through the same printable filter as search
  record edit <id> [--status S] [--title T] [--body] [--global]  change a record; --body reads the new body on stdin, without it only the metadata changes; a plan's status follows its body (open while it has an unticked "- [ ]")
  record list [--kind K] [--open] [--global [--project ROOT]] [--all]  ID  kind  status  date  title, newest first; --open: open plans and bugs, current specs, deploy docs and guides, queued proposals; --all: every scope, each line ending in its scope
  record import [--dry-run] [<docs-root>]  migrate this project's old trail (default .agents/docs/, else docs/) and its old .agents/memory/ store into records: open plans, the newest handoff, every ADR and bug (a closed bug trimmed to its title and prevention rule), specs, deploy docs and guides, and every trail file those, REQUIREMENTS.md, AGENTS.md, README.md, context/, skills/, agents/ or rules/ name; the rest is dropped (git history keeps it); refuses a dirty tree and a non-markdown file under the docs root; removes nothing, and prints every file to remove by name afterwards
  record import --global [--dry-run]  migrate ~/.config/fyr's feedback.jsonl, retros.jsonl and proposals/ into global records, and every indexed project's old .agents/memory/ store into its memory records; a bad line is reported, records already stored are skipped
      a project's records other than memory are exported on every write to .agents/trail.jsonl (commit it); when that file changes under the store (a pull, checkout or merge) it replaces the project's records before the next record or search call, and a bad line or a duplicate ID is refused with its line number; in a project whose trail is still files (docs/ or .agents/docs/ and no .agents/trail.jsonl) record refuses until it is migrated with fyr record import; exit 0 with or without output, 1 a refusal, 2 a usage error
  version                 print the build version

Flags accepted by every command:
  --repo DIR        harness checkout, for a build made from one (default: --repo, $FYR_REPO, the working directory or its parents, then the last install). A release binary installs the harness built into it and refuses --repo (ADR-034)
  --home DIR        install under DIR instead of $HOME (for testing)
`

// stderr is where a read-only verb names the state directory from before the
// rename: its stdout is data (render, list, metrics show --json); a test
// replaces it.
var stderr io.Writer = os.Stderr

// version is set by a release build, -ldflags "-X main.version=vX.Y.Z"; empty
// otherwise, and buildVersion reads the build info instead.
var version string

func main() {
	if os.Getenv(harness.ChildEnv) != "1" {
		oldEnvNotices(os.Environ(), os.Stderr)
	}
	if err := run(os.Args[1:], os.Stdout); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "fyr:", err)
		os.Exit(1)
	}
}

// oldEnvNotices names each SH_AGENT_* variable still set: fyr reads FYR_*
// only, and an old SH_AGENT_REPO ignored in silence would change which tree
// an install takes (plan fyr D15). An empty one changes nothing and is not
// named; the rebuilt child of an install inherits the parent's environment
// and stays quiet, so each variable is named once.
func oldEnvNotices(environ []string, w io.Writer) {
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, "SH_AGENT_"); ok && v != "" {
			fmt.Fprintf(w, "fyr: $%s is set but no longer read; rename it $FYR_%s\n", k, name)
		}
	}
}

// exitCode carries a non-error exit status such as "drift found".
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// ctx is what one verb runs with: its FlagSet (the global flags plus the
// verb's own, and nothing else), the parsed values, the home, the writer, and
// for the verbs that read the harness, the resolved source.
type ctx struct {
	cmd  string
	fs   *flag.FlagSet
	out  io.Writer
	home string
	// global flags, accepted by every verb
	repoFlag, homeFlag string
	// per-verb flags; a verb's flags func registers only the ones it reads
	dryRun, check, force, openRecords, global, rebuild, dreamAll, installTimer, removeTimer bool
	fromZip, kind, refs, memType, memName, memDesc, dreamProject                            string
	metricsSource, metricsLabel, metricsOutcome, metricsSince, metricsProject, metricsOut   string
	metricsUntil                                                                            string
	metricsContextTarget                                                                    int64
	metricsAllLabels, metricsJSON                                                           bool
	recTitle, recStatus, recID, recProject                                                  string
	recBody, recAll                                                                         bool
	limit                                                                                   int
	// src is the harness, resolved before run for the verbs that mark source
	src harness.Source
}

// repo is the tree on disk, "" for the files embedded in a release binary;
// no git helper is ever called with "" (D17).
func (c *ctx) repo() string { return c.src.Dir }

func (c *ctx) dryRunFlag() {
	c.fs.BoolVar(&c.dryRun, "dry-run", false, "print what would change, change nothing")
}

// verb is one command: the flags it declares, whether it reads the harness
// tree, and what it does.
type verb struct {
	flags      func(c *ctx) // registers the verb's own flags; nil for none
	source     bool         // resolve the harness source before run
	positional bool         // takes positional arguments (search words, adopt's name, memory's subverb); every other verb refuses a stray one
	run        func(c *ctx) error
}

// verbs is the command table. A verb's FlagSet holds the two global flags
// and the ones its flags func registers, so `list --check` is a usage error
// instead of a flag parsed and silently ignored (IN5). Adding a verb here is
// a map change under the project's README map rule (D9: verbs unchanged).
var verbs = map[string]verb{
	"version":   {run: runVersion},
	"uninstall": {flags: (*ctx).dryRunFlag, run: runUninstall},
	"memory": {flags: func(c *ctx) {
		c.fs.StringVar(&c.memType, "type", "", "memory add: user, feedback, project or reference")
		c.fs.StringVar(&c.memName, "name", "", "memory add: the memory's slug")
		c.fs.StringVar(&c.memDesc, "description", "", "memory add: one line")
	}, positional: true, run: runMemory},
	"dream": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.dreamAll, "all", false, "dream: every project in the global search index")
		c.fs.StringVar(&c.dreamProject, "project", "", "dream: one checkout")
		c.fs.BoolVar(&c.installTimer, "install-timer", false, "dream: install the nightly user timer")
		c.fs.BoolVar(&c.removeTimer, "remove-timer", false, "dream: remove the nightly user timer")
		c.dryRunFlag()
	}, run: runDream},
	"metrics": {flags: func(c *ctx) {
		c.fs.StringVar(&c.metricsSource, "source", "", "metrics ingest: a sandbox home to read instead of this one")
		c.fs.StringVar(&c.metricsLabel, "label", "", "metrics ingest: KEY=VALUE stamped on every --source session; show: only that label")
		c.fs.StringVar(&c.metricsOutcome, "outcome", "", "metrics ingest: pass or fail, the --source run's acceptance result")
		c.fs.StringVar(&c.metricsSince, "since", "", "metrics show: start of the window, YYYY-MM-DD (default 30 days ago)")
		c.fs.StringVar(&c.metricsUntil, "until", "", "metrics show: end of the window, YYYY-MM-DD, exclusive (default now)")
		c.fs.StringVar(&c.metricsProject, "project", "", "metrics show: only sessions in this project root")
		c.fs.BoolVar(&c.metricsAllLabels, "all-labels", false, "metrics show: include labelled (sandbox) sessions")
		c.fs.BoolVar(&c.metricsJSON, "json", false, "metrics show: print the summary as JSON")
		c.fs.Int64Var(&c.metricsContextTarget, "context-target", 0, "metrics show, report: the peak context, in tokens, a main session should stay under (default 300000)")
		c.fs.StringVar(&c.metricsOut, "out", "", "metrics report: the HTML file to write (default ~/.config/fyr/reports/<date-time>-metrics.html)")
	}, positional: true, run: runMetrics},
	"record": {flags: func(c *ctx) {
		c.fs.StringVar(&c.recTitle, "title", "", "record add, edit: the record's one-line title")
		c.fs.StringVar(&c.recStatus, "status", "", "record add, edit: the status (per kind; see fyr help)")
		c.fs.StringVar(&c.recID, "id", "", "record add: the ID to use instead of the next one")
		c.fs.StringVar(&c.recProject, "project", "", "record add, list with --global: the project a global record is about")
		c.fs.BoolVar(&c.global, "global", false, "record: the global scope")
		c.fs.BoolVar(&c.recBody, "body", false, "record edit: read the new body on stdin")
		c.fs.StringVar(&c.kind, "kind", "", "record list: only records of this kind")
		c.fs.BoolVar(&c.openRecords, "open", false, "record list: only open records")
		c.fs.BoolVar(&c.recAll, "all", false, "record list: every scope")
		c.dryRunFlag()
	}, positional: true, run: runRecord},
	"search": {flags: func(c *ctx) {
		c.fs.IntVar(&c.limit, "limit", 0, "search: hits to print, 1-50 (default 10)")
		c.fs.StringVar(&c.kind, "kind", "", "search: only hits of this kind")
		c.fs.StringVar(&c.refs, "refs", "", "search: records naming this ID or path")
		c.fs.BoolVar(&c.openRecords, "open", false, "search: open bugs and plans with unticked boxes")
		c.fs.BoolVar(&c.global, "global", false, "search, index: the global search index")
	}, positional: true, run: runSearch},
	"index": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.global, "global", false, "search, index: the global search index")
		c.fs.BoolVar(&c.rebuild, "rebuild", false, "index: delete and rebuild the index")
	}, run: runSearch},
	"install": {flags: (*ctx).dryRunFlag, source: true, run: runInstall},
	"sync":    {flags: (*ctx).dryRunFlag, source: true, run: runInstall},
	"status":  {source: true, run: func(c *ctx) error { return status(c.src, c.home, c.out) }},
	"diff":    {source: true, run: runDiff},
	"update": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.check, "check", false, "update: only report whether the repo is behind")
		c.fs.StringVar(&c.fromZip, "from-zip", "", "update: install the harness in this repository zip with the running binary")
		c.dryRunFlag()
	}, source: true, run: runUpdate},
	"list":   {source: true, run: runList},
	"render": {source: true, run: runRender},
	"adopt": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.force, "force", false, "adopt: replace an existing skill")
	}, source: true, positional: true, run: runAdopt},
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	v, ok := verbs[cmd]
	if !ok {
		// A usage error like every other: exit 2, said on the writer (IN16).
		fmt.Fprintf(out, "fyr: unknown command %q\n\n%s", cmd, usage)
		return exitCode(2)
	}
	// A zip place (R8, ADR-038): the operator named the archive, so no tree is
	// resolved and no other verb runs — `uninstall --from-zip` must not
	// uninstall. Refused before the FlagSet sees it, so the refusal names the
	// right verb rather than an undefined flag.
	if cmd != "update" && hasFlag(rest, "from-zip") {
		fmt.Fprintln(out, "fyr "+cmd+": --from-zip belongs to update: fyr update --from-zip <file>")
		return exitCode(2)
	}

	c := &ctx{cmd: cmd, out: out, fs: flag.NewFlagSet(cmd, flag.ContinueOnError)}
	c.fs.SetOutput(out)
	c.fs.StringVar(&c.repoFlag, "repo", "", "harness checkout")
	c.fs.StringVar(&c.homeFlag, "home", "", "install under this directory instead of $HOME")
	if v.flags != nil {
		v.flags(c)
	}
	if err := c.fs.Parse(reorder(c.fs, rest)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// Help is a successful ask: the FlagSet has printed the verb's
			// flags on the writer (IN15).
			return nil
		}
		// The flag package has named the flag on the writer; a usage error
		// exits 2 like every other usage error here.
		return exitCode(2)
	}
	if !v.positional && c.fs.NArg() > 0 {
		// A stray argument is never ignored: `list - --check` and `update --
		// --from-zip x` would otherwise run the verb as if nothing had been said.
		fmt.Fprintf(out, "%s takes no arguments: %q\n", cmd, c.fs.Args())
		return exitCode(2)
	}

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

	fromZipSet := false
	c.fs.Visit(func(f *flag.Flag) { fromZipSet = fromZipSet || f.Name == "from-zip" })
	// The parsed FlagSet decides, never a scan of the raw arguments: `--home --`
	// makes `--` a value, and a scan that stopped there would skip this branch
	// and run a normal update (security review 2026-10-01, round 2).
	if cmd == "update" && fromZipSet {
		// Every refusal comes before any command runs, and an empty value is
		// refused rather than read as no flag — that would fall through to a
		// normal update, which on a release binary downloads and replaces the
		// binary.
		switch {
		case c.fromZip == "" || strings.HasPrefix(c.fromZip, "-"):
			// A value that looks like a flag was a flag: `--from-zip --dry-run`
			// must not read --dry-run as the file and run a real update.
			fmt.Fprintln(out, "fyr update: --from-zip needs a file: fyr update --from-zip <file>")
			return exitCode(2)
		case c.check || c.repoFlag != "":
			fmt.Fprintln(out, "fyr update: --from-zip installs the named zip; --check/--repo do not apply to it")
			return exitCode(2)
		}
		if err := c.migrateStateDir(); err != nil {
			return err
		}
		return updateFromZip(c.fromZip, c.home, c.dryRun, out)
	}

	// Before the source is resolved: it falls back to the last install's
	// manifest, which a migration moves.
	if err := c.migrateStateDir(); err != nil {
		return err
	}
	if v.source {
		src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
		if err != nil {
			return err
		}
		c.src = src
	}
	return v.run(c)
}

// writesState reports whether this invocation may write under the state
// directory, and so moves the old one first (plan fyr D13). A dry run, a
// --check and a verb or subcommand that only reads answer false. The table is
// by verb, so it errs early on purpose: adopt (which writes into the repo), a
// project-local search or index (which writes .agents/scratch/search.db) and
// dream --install-timer or --remove-timer write nothing there, yet migrate
// too. Moving the old directory early is harmless — one rename, refused for
// a symlink, and both directories present are left alone — and a verb that
// starts writing there later needs no change here (IN18).
func (c *ctx) writesState() bool {
	switch c.cmd {
	case "install", "sync", "uninstall", "adopt", "index", "search", "dream":
		return !c.dryRun
	case "record":
		// Every record verb may write the brain store: a read loads a
		// trail.jsonl that changed under it (ADR-063, D21).
		return true
	case "update":
		return !c.dryRun && !c.check
	case "memory":
		return c.fs.Arg(0) == "add"
	case "metrics":
		return c.fs.Arg(0) == "ingest"
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

func runVersion(c *ctx) error {
	fmt.Fprintln(c.out, "fyr", buildVersion())
	return nil
}

func runUninstall(c *ctx) error {
	// The dream timer is not in the manifest (the next install would delete
	// it as stale); uninstall removes it by R4's dream clause, also when no
	// manifest is left, and a dry run only says so.
	// The unit under the name before the rename goes too (TR9).
	old := dream.OldTimerHere(c.home, runtime.GOOS)
	if dream.TimerInstalledHere(c.home, runtime.GOOS) || old != "" {
		if c.dryRun {
			fmt.Fprintln(c.out, "[dry-run] would remove the dream timer"+oldTimerNote(old))
		} else if removed, err := dream.RemoveTimer(c.home, runtime.GOOS); err != nil {
			return err
		} else if removed {
			// Said only when a unit was removed; the guard above already
			// counts this system's units alone.
			fmt.Fprintln(c.out, "removed the dream timer"+oldTimerNote(old))
		}
	}
	err := harness.Uninstall(c.home, c.dryRun, c.out)
	// A unit from v2.0.0 or earlier under the shared name is not this home's
	// to remove; the operator is told where it is and what to do, dry run or not.
	if notice := dream.LegacyTimerNotice(c.home, runtime.GOOS); notice != "" {
		fmt.Fprintln(c.out, notice)
	}
	otherSystemTimerNote(c)
	return err
}

// oldTimerNote names this home's unit under the name before the rename in a
// removal line, or says nothing when there is none.
func oldTimerNote(old string) string {
	if old == "" {
		return ""
	}
	return " (with " + old + ", its unit from before the rename to fyr)"
}

// otherSystemTimerNote names unit files of the operating system this binary
// is not running on: they are never removed here, and status counts them.
func otherSystemTimerNote(c *ctx) {
	if files := dream.OtherSystemTimerFiles(c.home, runtime.GOOS); len(files) > 0 {
		fmt.Fprintln(c.out, "a dream timer unit for another system is in this home and stays: "+strings.Join(files, ", "))
	}
}

// runMemory needs no harness tree (R10).
func runMemory(c *ctx) error {
	return memoryCmd(c.fs.Args(), brain.MemoryEntry{Type: c.memType, Name: c.memName, Description: c.memDesc}, c.home, c.out)
}

// runDream needs no harness tree (R3's exception).
func runDream(c *ctx) error {
	var err error
	switch {
	case c.installTimer && c.removeTimer:
		// A usage error exits 2 like every other dream usage error, and says
		// why on the caller's writer (IN12, IN13).
		fmt.Fprintln(c.out, "fyr dream: --install-timer or --remove-timer, not both")
		return exitCode(2)
	case (c.installTimer || c.removeTimer) && c.dryRun:
		// A new side effect goes below the read-only mode (context § Hard
		// rules): the dry run names what would change and calls nothing.
		what := "install the nightly dream timer (fyr dream --all at 03:00)"
		if c.removeTimer {
			what = "remove the nightly dream timer" + oldTimerNote(dream.OldTimerHere(c.home, runtime.GOOS))
		}
		fmt.Fprintln(c.out, "[dry-run] would "+what+"; installed now: "+strconv.FormatBool(dream.TimerInstalledHere(c.home, runtime.GOOS)))
		if notice := dream.LegacyTimerNotice(c.home, runtime.GOOS); c.removeTimer && notice != "" {
			fmt.Fprintln(c.out, notice)
		}
		if c.removeTimer {
			otherSystemTimerNote(c)
		}
	case c.installTimer:
		err = dream.InstallTimer(c.home, os.Getenv("PATH"), runtime.GOOS)
		if err == nil {
			fmt.Fprintln(c.out, "dream timer installed: fyr dream --all runs nightly at 03:00")
		}
	case c.removeTimer:
		var removed bool
		old := dream.OldTimerHere(c.home, runtime.GOOS)
		removed, err = dream.RemoveTimer(c.home, runtime.GOOS)
		if err == nil {
			if removed {
				fmt.Fprintln(c.out, "dream timer removed"+oldTimerNote(old))
			} else {
				fmt.Fprintln(c.out, "no dream timer found for "+c.home+" on this system")
			}
			if notice := dream.LegacyTimerNotice(c.home, runtime.GOOS); notice != "" {
				fmt.Fprintln(c.out, notice)
			}
			otherSystemTimerNote(c)
		}
	default:
		err = dream.Run(dream.Options{Home: c.home, All: c.dreamAll, Project: c.dreamProject, DryRun: c.dryRun, Out: c.out})
	}
	var ee *dream.ExitError
	if errors.As(err, &ee) {
		fmt.Fprintln(c.out, ee.Msg)
		return exitCode(ee.Code)
	}
	return err
}

// metricsSummary is what show and report share: the show options (a usage
// error exits 2 with its reason on the writer), the existing store, opened
// and closed here, and its summary.
func (c *ctx) metricsSummary(sub string) (*metrics.Summary, error) {
	o, err := c.metricsShowOptions()
	if err != nil {
		fmt.Fprintln(c.out, "fyr metrics "+sub+": "+err.Error())
		return nil, exitCode(2)
	}
	s, err := c.existingMetricsStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.Show(o)
}

// runMetrics serves `metrics ingest`, `show`, `report` and `export` (spec
// .agents/docs/specs/fyr/metrics.md).
func runMetrics(c *ctx) error {
	args := c.fs.Args()
	if len(args) == 0 {
		fmt.Fprintln(c.out, "fyr metrics: ingest | show | report | export FILE")
		return exitCode(2)
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "ingest":
		if bad := c.metricsFlagsOutside("source", "label", "outcome"); bad != "" {
			fmt.Fprintf(c.out, "fyr metrics ingest: --%s belongs to show or report\n", bad)
			return exitCode(2)
		}
		if len(args) > 0 {
			fmt.Fprintf(c.out, "metrics ingest takes no arguments: %q\n", args)
			return exitCode(2)
		}
		o := metrics.Options{Home: c.home, Source: c.metricsSource, Label: c.metricsLabel, Outcome: c.metricsOutcome}
		if err := o.Validate(); err != nil {
			fmt.Fprintln(c.out, "fyr metrics ingest: "+err.Error())
			return exitCode(2)
		}
		s, err := metrics.Open(metrics.Path(c.home))
		if err != nil {
			return err
		}
		defer s.Close()
		r, err := metrics.Ingest(s, o, c.out)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, r.Line())
		if len(r.Failed) > 0 {
			return exitCode(1)
		}
		return nil
	case "show":
		if len(args) > 0 {
			fmt.Fprintf(c.out, "metrics show takes no arguments: %q\n", args)
			return exitCode(2)
		}
		sum, err := c.metricsSummary("show")
		if err != nil {
			return err
		}
		if c.metricsJSON {
			return sum.WriteJSON(c.out)
		}
		sum.WriteText(c.out)
		return nil
	case "report":
		if len(args) > 0 || c.metricsJSON {
			fmt.Fprintln(c.out, "fyr metrics report [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--out FILE]")
			return exitCode(2)
		}
		sum, err := c.metricsSummary("report")
		if err != nil {
			return err
		}
		now := time.Now()
		file := c.metricsOut
		if file == "" {
			file = metrics.ReportPath(c.home, now)
		}
		if err := metrics.WriteReport(sum, file, now); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "metrics: report written to "+file)
		return nil
	case "export":
		if bad := c.metricsFlagsOutside(); bad != "" {
			fmt.Fprintf(c.out, "fyr metrics export: --%s does not apply to export\n", bad)
			return exitCode(2)
		}
		if len(args) != 1 {
			fmt.Fprintln(c.out, "fyr metrics export FILE")
			return exitCode(2)
		}
		s, err := c.existingMetricsStore()
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.Export(args[0]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "metrics: exported to "+args[0])
		return nil
	}
	fmt.Fprintf(c.out, "fyr metrics: unknown subcommand %q (ingest, show, report, export)\n", sub)
	return exitCode(2)
}

// metricsFlagsOutside names the first metrics flag set on the command line
// that is not in allowed, or "": a flag a subcommand does not read is a
// usage error, never ignored.
func (c *ctx) metricsFlagsOutside(allowed ...string) string {
	own := map[string]bool{"source": true, "label": true, "outcome": true, "since": true, "until": true, "project": true, "all-labels": true, "json": true, "out": true, "context-target": true}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	bad := ""
	c.fs.Visit(func(f *flag.Flag) {
		if bad == "" && own[f.Name] && !ok[f.Name] {
			bad = f.Name
		}
	})
	return bad
}

// existingMetricsStore opens the store for show, report and export, which
// read it: a home that never ingested has none, and creating an empty one
// would print zeros that read as "no sessions" (exit 1 instead).
func (c *ctx) existingMetricsStore() (*metrics.Store, error) {
	path := metrics.Path(c.home)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(c.out, "fyr metrics: no metrics store at %s; run `fyr metrics ingest` first\n", path)
		return nil, exitCode(1)
	}
	return metrics.Open(path)
}

// metricsShowOptions reads show's filters; --label and --all-labels exclude
// each other, and --project is made absolute as the store records roots.
func (c *ctx) metricsShowOptions() (metrics.ShowOptions, error) {
	var o metrics.ShowOptions
	if c.metricsSince != "" {
		t, err := time.Parse("2006-01-02", c.metricsSince)
		if err != nil {
			return o, fmt.Errorf("--since %q: want YYYY-MM-DD", c.metricsSince)
		}
		o.Since = t
	}
	if c.metricsUntil != "" {
		t, err := time.Parse("2006-01-02", c.metricsUntil)
		if err != nil {
			return o, fmt.Errorf("--until %q: want YYYY-MM-DD", c.metricsUntil)
		}
		if !o.Since.IsZero() && !t.After(o.Since) {
			return o, errors.New("--until must be after --since")
		}
		o.Now = t // exclusive: --since A --until B and --since B never overlap
	}
	if c.metricsLabel != "" && c.metricsAllLabels {
		return o, errors.New("--label and --all-labels exclude each other")
	}
	if c.metricsSource != "" || c.metricsOutcome != "" {
		return o, errors.New("--source and --outcome belong to ingest")
	}
	if c.metricsOut != "" && c.fs.Arg(0) != "report" {
		return o, errors.New("--out belongs to report")
	}
	// Given means set: 0 left to the package is the default target, 0 typed
	// is a mistake.
	given := false
	c.fs.Visit(func(f *flag.Flag) { given = given || f.Name == "context-target" })
	if given && c.metricsContextTarget <= 0 {
		return o, errors.New("--context-target takes a token count above 0")
	}
	o.ContextTarget = c.metricsContextTarget
	o.Label, o.AllLabels = c.metricsLabel, c.metricsAllLabels
	if c.metricsProject != "" {
		abs, err := filepath.Abs(c.metricsProject)
		if err != nil {
			return o, err
		}
		o.Project = abs
	}
	return o, nil
}

// runSearch serves `search` and `index`; neither needs a harness tree, and a
// release-binary or zip place has none (plan D10).
func runSearch(c *ctx) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := search.Options{Dir: wd, Home: c.home, Words: c.fs.Args(), Refs: c.refs, Open: c.openRecords,
		Kind: c.kind, Limit: c.limit, Global: c.global, Rebuild: c.rebuild}
	if c.cmd == "index" {
		err = search.Index(o, c.out)
	} else if err = search.Validate(o); err == nil {
		recordHits(c, o)
		err = search.Search(o, c.out)
	}
	if search.IsUsage(err) {
		fmt.Fprintln(c.out, "fyr", err)
		return exitCode(2)
	}
	return err
}

func runInstall(c *ctx) error {
	repo := c.repo()
	if repo == "" {
		// A release binary installs the harness built into it and itself:
		// there is no source to build from (D13).
		if c.dryRun {
			fmt.Fprintf(c.out, "[dry-run] release binary %s: the harness built into it and the running binary would be installed\n", c.src.Version)
		} else {
			fmt.Fprintf(c.out, "release binary %s: installing the harness built into it and the running binary\n", c.src.Version)
		}
		return installFiles(c.src, c.home, c.dryRun, c.out)
	}
	if err := knownCheckout(c.repoFlag, c.home, repo); err != nil {
		return err
	}
	// The deployed binary must be the repo's own build: rebuild and let that
	// build install, unless this process is that build (ChildEnv), there is
	// no toolchain, the tree is a plain copy, or this is a dry run — a dry run
	// never builds or runs a fresh binary, though like every install it reads
	// the checkout's git state, which is why the checkout must be one the
	// operator named or installed before. A plain copy (no .git) never
	// rebuilds: whoever unpacked it built the running binary from that same
	// tree, the tree may be read-only, and git cannot update it (BUG-015).
	_, goErr := exec.LookPath("go")
	own := harness.IsCheckout(repo)
	rebuild := shouldRebuild(goErr == nil, os.Getenv(harness.ChildEnv) == "1", own)
	if c.dryRun && rebuild {
		fmt.Fprintf(c.out, "[dry-run] the binary would be rebuilt from %s\n", repo)
	} else if rebuild {
		return harness.Reinstall(repo, installPassArgs(c.homeFlag, c.home), c.out)
	}
	if !own && c.dryRun {
		fmt.Fprintln(c.out, "[dry-run] plain copy, not its own git checkout: the running binary would be installed as is")
	} else if !own {
		fmt.Fprintln(c.out, "plain copy, not its own git checkout: the running binary is installed as is")
	} else if goErr != nil {
		fmt.Fprintln(c.out, "warning: go is not on PATH; the running binary was copied, not rebuilt — install Go and run `fyr update`")
	}
	return installFiles(c.src, c.home, c.dryRun, c.out)
}

func runDiff(c *ctx) error {
	// A zip install is not compared: the zip is not kept, and the source this
	// binary resolves (its embedded files, a checkout) is not what was
	// installed (ADR-038).
	if st, found, err := harness.LoadState(c.home); err == nil && found && st.Origin().Kind == harness.OriginZip {
		fmt.Fprintln(c.out, "diff       not compared — this home was installed from a zip that is not kept; run update --from-zip <file> again to change it")
		return nil
	}
	files, err := harness.Plan(c.src, nil)
	if err != nil {
		return err
	}
	return harness.Diff(c.home, files, c.out)
}

func runUpdate(c *ctx) error {
	// A dry run of update is its --check: it reports, and changes nothing —
	// no download, no binary replaced, no pull. It says
	// what a real update would do instead.
	dry := c.dryRun
	check := c.check || dry
	repo := c.repo()
	if repo == "" {
		// A release binary has no source to build from: the newest release is
		// downloaded and verified instead.
		args := installPassArgs(c.homeFlag, c.home)
		// The BUG-079 note, when this run wrote the installed binary, is
		// printed by UpdateFromRelease itself: only there is "replaced" known.
		err := harness.UpdateFromRelease(c.home, c.src.Version, check, args, c.out)
		if errors.Is(err, harness.ErrNewerRelease) {
			if dry {
				fmt.Fprintln(c.out, "[dry-run] update would download that release, check it against its SHA256SUMS, replace the binary and reinstall")
				return nil
			}
			return exitCode(2)
		}
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}
		return nil
	}
	if err := knownCheckout(c.repoFlag, c.home, repo); err != nil {
		return err
	}
	err := update(repo, c.home, check, c.homeFlag, c.out)
	var code exitCode
	if dry && errors.As(err, &code) && code == 2 {
		fmt.Fprintln(c.out, "[dry-run] update would pull those commits, rebuild and reinstall")
		return nil
	}
	return err
}

func runList(c *ctx) error {
	inv, err := harness.LoadInventory(c.src)
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, harness.RenderInventory(inv))
	return nil
}

func runRender(c *ctx) error {
	inv, err := harness.LoadInventory(c.src)
	if err != nil {
		return err
	}
	data, err := harness.RenderContext(c.src, inv)
	if err != nil {
		return err
	}
	_, err = c.out.Write(data)
	return err
}

func runAdopt(c *ctx) error {
	if c.fs.NArg() != 1 {
		fmt.Fprintln(c.out, "usage: fyr adopt <skill-name-or-path> [--force]")
		return exitCode(2)
	}
	repo := c.repo()
	if repo == "" {
		return errors.New("adopt needs a harness checkout (`git clone git@github.com:kesokaj/fyr.git`, then run adopt from a build of it); this install is a release binary and carries no tree to adopt into")
	}
	dest, err := harness.Adopt(repo, c.home, c.fs.Arg(0), c.force)
	if err != nil {
		return err
	}
	next := "commit and push the repo"
	if !harness.IsCheckout(repo) {
		// A plain copy has no repository: the adopted skill lives only here
		// until it is copied into a checkout.
		next = "copy it into your harness checkout and commit there — this tree is a plain copy with no repository"
	}
	fmt.Fprintf(c.out, "adopted into %s\nnext: review it, run `fyr install`, then %s\n", dest, next)
	return nil
}

// installFiles plans the source with the running binary and installs it,
// then the PATH note. No vendor setting is read or written (ADR-062): the
// harness is files in the customization directory, nothing else.
func installFiles(src harness.Source, home string, dryRun bool, out io.Writer) error {
	self, err := selfExecutable()
	if err != nil {
		return err
	}
	files, err := harness.Plan(src, self)
	if err != nil {
		return err
	}
	if err := harness.Install(src, home, files, dryRun, out); err != nil {
		return err
	}
	// After the binary is written: the fyr-dream unit runs it. The PATH is the
	// installing shell's, as --install-timer's is.
	old, replaced, err := dream.ReplaceOldTimer(home, os.Getenv("PATH"), runtime.GOOS, dryRun)
	switch {
	case err != nil:
		return err
	case old != "" && !replaced && dryRun:
		fmt.Fprintf(out, "[dry-run] the dream timer %s is turned off: its files would be removed and no fyr timer installed\n", old)
	case old != "" && !replaced:
		fmt.Fprintf(out, "removed the dream timer %s, which was turned off; no fyr timer installed (fyr dream --install-timer turns one on)\n", old)
	case old != "" && dryRun:
		fmt.Fprintf(out, "[dry-run] the dream timer %s would be replaced by its fyr name\n", old)
	case old != "":
		unit, plist := dream.UnitFor(home)
		if runtime.GOOS == "darwin" {
			unit = plist
		}
		fmt.Fprintf(out, "replaced the dream timer %s with %s\n", old, unit)
	}
	// A dry run wrote no binary, so no statement about how PATH reaches it is
	// true.
	if !dryRun {
		pathHint(home, out)
	}
	return nil
}

// stdin is where `memory add` reads the body; a test replaces it.
var stdin io.Reader = os.Stdin

// memoryCmd runs `memory add` and `memory show` in the checkout holding the
// working directory: an alias over the brain store's memory records (D14).
// A refusal prints its reason and exits 2.
func memoryCmd(args []string, e brain.MemoryEntry, home string, out io.Writer) error {
	if len(args) != 1 || (args[0] != "add" && args[0] != "show") {
		// A usage error exits 2 like every other usage error here (dream's, a verb's unknown flag).
		fmt.Fprintln(out, "usage: fyr memory add --type T --name SLUG --description TEXT (body on stdin) | fyr memory show")
		return exitCode(2)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := brain.Options{Home: home, Dir: wd}
	var text string
	if args[0] == "add" {
		text, err = brain.MemoryAdd(o, e, stdin)
		if err == nil {
			text = "saved " + text + "\n"
		}
	} else {
		text, err = brain.MemoryShow(o)
	}
	if brain.IsRefusal(err) || brain.IsUsage(err) {
		fmt.Fprintln(out, search.Printable("fyr memory: "+err.Error())) // on the caller's writer, where a test reads it (IN13)
		return exitCode(2)
	}
	if err != nil {
		return errors.New(search.Printable(err.Error()))
	}
	fmt.Fprint(out, text)
	return nil
}

func status(src harness.Source, home string, out io.Writer) error {
	repo := src.Dir
	// Read first: the source line says when this home came from a zip, which
	// the embedded files or a checkout beside it do not describe (ADR-038).
	st, found, err := harness.LoadState(home)
	if err != nil {
		return err
	}
	origin := st.Origin()
	fromZip := found && origin.Kind == harness.OriginZip
	zipNote := ""
	if fromZip {
		zipNote = "; this home was installed from a zip — run update --from-zip <file> again to change it"
	}
	var info harness.RepoInfo
	switch {
	case repo == "":
		// The embedded files: no git state to show, and none is asked (D17).
		fmt.Fprintf(out, "source     embedded %s (release binary; update downloads the newest release%s)\n", src.Version, zipNote)
	default:
		info = harness.Inspect(repo)
		dirty := "clean"
		if info.StatusUnknown {
			// Never render an unreadable tree as clean: git failing to report is
			// not evidence of anything.
			dirty = "state unknown — git could not report it"
		} else if info.Dirty {
			dirty = "uncommitted changes"
		}
		if info.Commit == "" {
			fmt.Fprintf(out, "repo       %s (plain copy, not its own git checkout; update is refused%s)\n", repo, zipNote)
		} else {
			fmt.Fprintf(out, "repo       %s (%s @ %s, %s%s)\n", repo, info.Branch, info.Commit, dirty, zipNote)
		}
	}

	if !found {
		fmt.Fprintln(out, "installed  nothing — run `fyr install`")
		return exitCode(1)
	}
	from := "commit " + st.Commit
	switch origin.Kind {
	case harness.OriginRelease:
		from = "release " + origin.Release
	case harness.OriginZip:
		from = "zip sha256:" + origin.ShortSum()
		if origin.Trust != "" { // a manifest from before the verdict was recorded has none
			from += " (" + origin.Trust + ")"
		}
	}
	fmt.Fprintf(out, "installed  %s from %s (%d files, manifest ~/.config/fyr/state.json)\n", st.InstalledAt, from, len(st.Files))
	// The line describes the installed file, not this process: status may
	// run from `go run` or from a build outside ~/.local/bin.
	binaryLine := "binary     unknown (installed file has no build info)"
	if bi, err := buildinfo.ReadFile(filepath.Join(home, harness.BinPath)); err == nil {
		rev := versionFromInfo(bi)
		if repo == "" {
			rev = releaseFromInfo(bi)
		}
		binaryLine = "binary     " + rev
		switch {
		case repo == "":
			// No checkout commit to compare against.
		case !binaryOlder(rev, info.Commit):
		case rev == "(devel)" || rev == "unknown":
			binaryLine += " — no build stamp; run `fyr update`"
		default:
			binaryLine += fmt.Sprintf(" — older than the repo (%s); run `fyr update`", info.Commit)
		}
	}
	fmt.Fprintln(out, binaryLine)
	// The line above describes the installed file; these say when that file is
	// not the one an unqualified `fyr` runs (BUG-078). Only when PATH
	// reaches the installed file at all: for a scratch home it never does, and
	// the operator's own home is the one where the distinction bites.
	if shadows, onPath := harness.PathShadows(os.Getenv("PATH"), filepath.Join(home, harness.BinPath)); onPath {
		for _, s := range shadows {
			fmt.Fprintf(out, "shadowed   %s comes first on PATH, so `fyr` runs that, not the binary above\n", s)
		}
	}
	// A crossing whose removal of the old binary was refused, or whose old
	// manifest never listed it, leaves it on PATH (plan fyr F2, F4; TR9).
	for _, old := range harness.OldBinariesOnPath(os.Getenv("PATH")) {
		fmt.Fprintf(out, "leftover   %s is the binary from before the rename to fyr; remove it by hand, and any unit or profile line that runs it\n", old)
	}
	if last, ok := harness.LastRetro(home); ok {
		days := int(time.Since(last).Hours() / 24)
		hint := ""
		if days > 14 {
			hint = " — past 14 days, run /retro at the end of the next session"
		}
		fmt.Fprintf(out, "retro      last proposal %s (%d days ago)%s\n", last.Format("2006-01-02"), days, hint)
	} else {
		fmt.Fprintln(out, "retro      none yet — run /retro at the end of a session with corrections")
	}
	fmt.Fprintln(out, "dream      "+dream.StatusLine(home))

	inv, err := harness.LoadInventory(src)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "repo has   %d skills, %d agents, %d commands, %d rules\n", len(inv.Skills), len(inv.Agents), len(inv.Commands), len(inv.Rules))
	fmt.Fprintf(out, "agy        ~/%s  (rules/AGENTS.md, skills/, agents/, workflows/, rules/)\n", harness.AgyRoot)

	if fromZip {
		// The zip is not kept, so its content cannot be compared; say so and
		// check what can be checked — every file the install wrote is there.
		var missing []string
		for _, rel := range st.Files {
			if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
				missing = append(missing, rel)
			}
		}
		fmt.Fprintf(out, "drift      not compared — installed from a zip that is not kept; %d of %d manifest files present\n", len(st.Files)-len(missing), len(st.Files))
		for _, rel := range missing {
			fmt.Fprintf(out, "  missing  ~/%s\n", rel)
		}
		if len(missing) > 0 {
			return exitCode(1)
		}
		return nil
	}
	files, err := harness.Plan(src, nil)
	if err != nil {
		return err
	}
	report, err := harness.Status(home, files)
	if err != nil {
		return err
	}
	for _, e := range report.Entries {
		if e.State != "ok" && e.State != "kept" {
			fmt.Fprintf(out, "  %-8s ~/%s\n", e.State, e.Rel)
		}
	}
	if n := report.Counts["kept"]; n > 0 {
		// Not drift: no install changes them (plan fyr D32, IN32).
		fmt.Fprintf(out, "kept       %d files under ~/.claude that an install before fyr wrote for Claude Code; fyr never removes them, and the next install drops them from its manifest\n", n)
	}
	if !report.Drift() {
		fmt.Fprintf(out, "drift      none — %d files match the repo\n", report.Counts["ok"])
		return nil
	}
	fmt.Fprintf(out, "drift      %d missing, %d differ, %d stale — run `fyr diff`, then `fyr install`\n",
		report.Counts["missing"], report.Counts["differs"], report.Counts["stale"])
	return exitCode(1)
}

func update(repo, home string, checkOnly bool, homeFlag string, out io.Writer) error {
	behind, err := harness.Behind(repo)
	if err != nil {
		return err
	}
	info := harness.Inspect(repo)
	if behind == 0 {
		fmt.Fprintf(out, "up to date with upstream (%s)\n", info.Commit)
	} else {
		fmt.Fprintf(out, "behind upstream by %d commit(s)\n", behind)
	}
	// A dirty checkout is never installed, on either path: `update` deploys
	// the WORKING TREE, not the fetched commit, so uncommitted work would reach
	// every vendor directory (BUG-046); `install` stays the deliberate way to
	// deploy a dirty tree. The check sits above the --check and --dry-run
	// returns, so the read-only mode refuses exactly what the real run refuses
	// (IN4).
	if info.Dirty || info.StatusUnknown {
		what := "has uncommitted changes"
		if info.StatusUnknown {
			what = "could not be checked for uncommitted changes (git did not report its status)"
		}
		return fmt.Errorf("%s %s and `update` installs the working tree, not the fetched commit; commit or stash them, or run `fyr install` to deploy them deliberately", repo, what)
	}
	if checkOnly {
		if behind == 0 {
			return nil
		}
		return exitCode(2)
	}
	if behind > 0 {
		if err := harness.Pull(repo, out); err != nil {
			return err
		}
		fmt.Fprintf(out, "pulled to %s\n", harness.Inspect(repo).Commit) // the tree moved: inspected again
	}
	if err := harness.Reinstall(repo, installPassArgs(homeFlag, home), out); err != nil {
		return err
	}
	// Reinstall wrote ~/.local/bin/fyr; the process running this update
	// may be another file (BUG-079).
	harness.NoteRunningElsewhere(home, out)
	return nil
}

// installPassArgs are the flags an update or a rebuilding install hands to
// the install it runs: the home it was told to use.
func installPassArgs(homeFlag, home string) []string {
	if homeFlag != "" {
		return []string{"--home", home}
	}
	return nil
}

// knownCheckout refuses to build and install from a checkout that was only
// discovered by walking up from the working directory when a different
// checkout is already installed: building runs that tree's code and deploys
// its context everywhere, so standing in a harness-shaped directory must not
// be enough. An explicit --repo or $FYR_REPO, or a first install, is.
func knownCheckout(repoFlag, home, repo string) error {
	if repoFlag != "" || os.Getenv("FYR_REPO") != "" {
		return nil
	}
	st, found, err := harness.LoadState(home)
	if err != nil {
		return fmt.Errorf("reading the manifest before building: %w", err)
	}
	if !found || st.Repo == "" {
		return nil
	}
	// A manifest written by an embedded or zip install records a release or
	// an archive, never a path: neither is handed to samePath, which would
	// resolve it against the working directory.
	switch o := st.Origin(); o.Kind {
	case harness.OriginRelease:
		return fmt.Errorf("this home was installed from the release binary %s, not from a checkout; pass --repo %s to build and install from it", o.Release, repo)
	case harness.OriginZip:
		return fmt.Errorf("this home was installed from a zip (sha256 %s), not from a checkout; pass --repo %s to build and install from it", o.ShortSum(), repo)
	}
	if samePath(st.Repo, repo) {
		return nil
	}
	return fmt.Errorf("checkout %s is not the installed one (%s); pass --repo %s to build and install from it", repo, st.Repo, repo)
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}

// shouldRebuild decides whether install re-executes through a fresh build
// of the repo: yes whenever a toolchain is present and the repo is its own
// git checkout, unless this process is already the fresh build that
// Reinstall spawned. A plain copy is installed from the running binary.
func shouldRebuild(goOnPath, child, ownCheckout bool) bool {
	return goOnPath && !child && ownCheckout
}

// binaryOlder reports whether the running binary is not the repo's current
// build: unstamped (`go run`, or a build outside git) or stamped with a
// different revision. Prefixes are compared both ways because git's short
// hash length is not fixed.
func binaryOlder(rev, commit string) bool {
	rev = strings.TrimSuffix(rev, "+dirty")
	if rev == "" || commit == "" {
		return false
	}
	if rev == "(devel)" || rev == "unknown" {
		return true
	}
	return !strings.HasPrefix(rev, commit) && !strings.HasPrefix(commit, rev)
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

// selfExecutable returns the running binary so install can copy it to
// ~/.local/bin. On the normal path this is the build Reinstall just made;
// without a toolchain it is whatever binary is running, and status says so.
func selfExecutable() ([]byte, error) {
	p, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// pathHint says, after an install, whether the next unqualified `fyr`
// runs the binary just written — one message per situation. PATH reaching the
// file but only after an earlier entry is the BUG-078 case: install reports
// success and `status` reports drift none, both about a binary the next
// command will not run. PATH not reaching it is the old note, naming what
// answers instead when something does. Every fact comes from one PathShadows
// call, so the two halves cannot disagree on what "on PATH" means.
func pathHint(home string, out io.Writer) {
	bin := filepath.Join(home, harness.BinPath)
	binDir := filepath.Dir(bin)
	shadows, onPath := harness.PathShadows(os.Getenv("PATH"), bin)
	switch {
	case onPath:
		for _, s := range shadows {
			fmt.Fprintf(out, "warning: %s comes before %s on PATH, so `fyr` runs that one\n", s, bin)
		}
	case len(shadows) > 0:
		fmt.Fprintf(out, "note: %s is not on PATH, so `fyr` runs %s instead; put %s ahead of it\n", binDir, shadows[0], binDir)
	default:
		fmt.Fprintf(out, "note: %s is not on PATH; add it so `fyr` is available everywhere\n", binDir)
	}
}

// buildVersion describes the running process, for `fyr version`.
// `fyr status` reads the installed file instead (see status).
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return versionFromInfo(info)
}

// releaseFromInfo renders an installed release binary's version: the module
// version when it is a release tag, which Go stamps for a build at a clean
// tag and `go install …@vX.Y.Z` records — a release build's -ldflags are not
// in the build info, since -trimpath leaves them out — and the VCS stamp
// otherwise.
func releaseFromInfo(info *debug.BuildInfo) string {
	if harness.IsReleaseVersion(info.Main.Version) {
		return info.Main.Version
	}
	return versionFromInfo(info)
}

// versionFromInfo renders a binary's VCS stamp as the short revision with a
// "+dirty" suffix for a modified tree, or the module version when the build
// carried no stamp (`go run`, `go test`, a build outside git).
func versionFromInfo(info *debug.BuildInfo) string {
	rev, modified := "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "+dirty"
			}
		}
	}
	if rev == "" {
		return info.Main.Version
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + modified
}

// updateFromZip installs the harness in the repository zip the operator
// downloaded, with the running binary: unpack, install, delete the unpacked
// copy (R8, R9's zip place; ADR-038). Nothing is fetched, and nothing in the
// zip replaces the binary: the running one is installed as in every install.
func updateFromZip(zipPath, home string, dryRun bool, out io.Writer) (err error) {
	// A dry run installs nothing and never creates ~/.config/fyr: when that
	// directory is absent the unpack goes into a zip-* directory beside the
	// zip instead, never the system temp directory (IN10), and that directory
	// is removed before this returns, as on a real run; the usage says so
	// (IN21).
	parent := filepath.Join(home, harness.StateDir)
	if dryRun {
		if _, err := os.Stat(parent); err != nil {
			parent = ""
		}
	} else {
		// Created here, and removed again if anything below fails — the unpack
		// or the install that can still refuse the zip's content — so a
		// refused zip leaves the home as it was (BUG-093).
		// `=`, not `:=`: the deferred check must see the function's result, not
		// a block-scoped copy of it.
		var created []string
		if created, err = harness.MkdirAllTracked(parent); err != nil {
			return err
		}
		defer func() {
			if err != nil {
				harness.RemoveCreated(created)
			}
		}()
	}
	// The zip place is the fallback for machines that cannot take the normal
	// way in, so it accepts the zip it is given (ADR-041). A release zip with
	// its SHA256SUMS beside it is checked before a byte is written, and refused
	// if that check fails; without it, it is installed marked UNVERIFIED (R9,
	// ADR-053: the checksums are not signed, so the check is of integrity).
	verifiedTag := ""
	check := func(sum string) error {
		tag, err := harness.VerifyZipSums(zipPath, sum)
		if errors.Is(err, harness.ErrZipNoSums) {
			return nil
		}
		if err != nil {
			return err
		}
		verifiedTag = tag
		return nil
	}
	dir, sum, err := harness.UnpackZip(zipPath, parent, check)
	if err != nil {
		return err
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			fmt.Fprintf(out, "warning: could not remove the unpacked copy %s: %v\n", dir, rerr)
		}
	}()
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	trust := "UNVERIFIED — no SHA256SUMS beside it; trust rests on your own download"
	if verifiedTag != "" {
		trust = "CHECKED against the SHA256SUMS of release " + verifiedTag + " (integrity only)"
	}
	fmt.Fprintf(out, "%szip %s (sha256 %s, %s): installing its harness and the running binary %s; nothing is downloaded and nothing in the zip replaces the binary\n", prefix, zipPath, sum, trust, buildVersion())
	// The manifest keeps the verdict too, so status can tell a verified install
	// from an unverified one after the output has scrolled away.
	src := harness.Source{FS: os.DirFS(dir), Dir: dir, Origin: harness.ZipOrigin(sum, verifiedTag)}
	if err := installFiles(src, home, dryRun, out); err != nil {
		return err
	}
	// Only after a successful install: a refused or dry run changes nothing,
	// not even a stale leftover from an interrupted run.
	if !dryRun {
		harness.SweepStaleDirs(parent, "zip-", time.Hour, out)
	}
	return nil
}
