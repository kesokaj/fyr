package main

// The text of fyr's help, as data (plan ledger D19, § Interfaces "Phase 4b"):
// the short page, and per verb its synopsis and what it does. help.go builds
// each page's Flags: list from the verb's FlagSet, so a flag is described
// once, in its usage string. Every line is at most 79 runes, so an
// 80-column terminal never wraps it; help_test.go pins that, the ten-line
// cap on a description, and the verbs against the command table.

// helpLast ends the short page and every verb page.
const helpLast = "Every command takes --home DIR and --repo DIR."

// helpSynopsis is help's own, for its usage errors; `fyr help help` prints
// the short page, so help has no verb page.
const helpSynopsis = "usage: fyr help [<command>]"

// shortPage is `fyr`, `fyr help`, `-h` and `--help`. Each verb line keeps the
// shape "\n  <verb> ", which TestHarnessMap and TestUsageMatchesDocComment
// read; sync is install's alias and is named on install's page only.
const shortPage = `fyr — installs the agent harness and keeps its records

Usage: fyr <command> [flags]       fyr help <command> for its flags

Harness
  install      deploy the harness into ~/.gemini/config
  update       move this place to the newest harness
  status       what is installed, and whether it drifted
  diff         the diff of every drifted file
  uninstall    remove exactly what install wrote
  list         the skills and agents
  render       print the assembled context
  adopt        copy a skill from elsewhere into skills/

Records and search
  record       add, get, edit, list or import trail records
  memory       save or show this project's memory
  search       ranked search of records and files
  index        reconcile the search index; --register opts in

Background and measurement
  dream        the hourly dreamer and its timer
  auto         idle-time runs: on, off, status, run
  metrics      session shapes: ingest, show, report, evals, export
  eval         run behavioural rows against the CLI
  work         run Kickoffs as agy workers (isolated only with --runner docker)
  check        read-only drift check of this checkout's docs
  serve        a local page of status, records, memory and metrics
  version      print the build version

` + helpLast + "\n"

// verbPage is one verb's page before its flags: the synopsis, one line per
// subverb or mode (the first beginning "usage: fyr <verb>", a line too wide
// continued under its first argument), and at most ten lines of what the
// verb does and never does. A usage error prints the synopsis too.
type verbPage struct {
	synopsis []string
	about    []string
}

// pages holds a page per verb but sync, which is install's (pageOf).
var pages = map[string]verbPage{
	"install": {
		synopsis: []string{"usage: fyr install [--dry-run]"},
		about: []string{
			"Deploys the harness (the global context, skills, agents, commands and",
			"rules) into ~/.gemini/config and the binary into ~/.local/bin, from the",
			"harness checkout or from the harness built into a release binary, and",
			"removes the files a previous install wrote that the harness no longer",
			"has, except the Claude Code files an install before fyr wrote under",
			"~/.claude, which it keeps and stops managing (DEPLOY-remote § Crossing",
			"to fyr). It edits no vendor setting. Running it again changes only",
			"what drifted. sync is an alias of install.",
		},
	},
	"update": {
		synopsis: []string{
			"usage: fyr update [--check | --dry-run]",
			"       fyr update --from-zip FILE [--dry-run]",
		},
		about: []string{
			"At a checkout: fetch, refuse a dirty tree, fast-forward, rebuild and",
			"reinstall. At a release-binary install: download the newest release, check",
			"it against its SHA256SUMS, replace the binary and reinstall. --from-zip",
			"installs the harness in a repository zip you downloaded, with the running",
			"binary; a development build refuses it where a tree was installed, and at",
			"a zip place refuses update and names fyr install --repo <checkout> (ADR-082).",
			"Which place gets what from a tag: SPEC-fyr-delivery § The model.",
			"--check only reports (exit 2 when behind); --dry-run names what would",
			"change and writes nothing. At a checkout, --check and --dry-run still run",
			"git fetch, which moves the remote-tracking refs.",
		},
	},
	"status": {
		synopsis: []string{"usage: fyr status"},
		about: []string{
			"Prints what is installed, from which commit, the binary's build, the",
			"drift against the harness, the dream timer, auto, and a note when another fyr",
			"comes first on PATH. Exit 1 on drift.",
		},
	},
	"diff": {
		synopsis: []string{"usage: fyr diff"},
		about:    []string{"Prints a unified diff of every installed file that drifted."},
	},
	"uninstall": {
		synopsis: []string{"usage: fyr uninstall [--dry-run]"},
		about: []string{
			"Removes exactly the files install wrote, and the dream timer and the",
			"auto timer when installed; nothing else. The metrics store, eval's",
			"sandboxes, auto.json and auto/ stay. --dry-run names what would go.",
		},
	},
	"list": {
		synopsis: []string{"usage: fyr list"},
		about:    []string{"Prints the harness's skill and agent inventory."},
	},
	"render": {
		synopsis: []string{"usage: fyr render"},
		about:    []string{"Prints the assembled context file, as install deploys it."},
	},
	"adopt": {
		synopsis: []string{"usage: fyr adopt <name|path> [--force]"},
		about: []string{
			"Copies a skill installed elsewhere (for example by npx skills add -g) into",
			"the harness checkout's skills/; commit and push it there. A release binary",
			"carries no tree to adopt into, and says so.",
		},
	},
	"record": {
		synopsis: []string{
			"usage: fyr record add <kind> --title T [--status S] [--id ID]",
			"                      [--global [--project ROOT]]",
			"       fyr record get <id>... [--global]",
			"       fyr record edit <id> [--status S] [--title T] [--body] [--global]",
			"       fyr record list [--kind K] [--open] [--global [--project ROOT]] [--all]",
			"       fyr record export",
			"       fyr record sync",
			"       fyr record import [--dry-run] [<docs-root>]",
			"       fyr record import --global [--dry-run]",
		},
		about: []string{
			"The brain store, ~/.config/fyr/brain.db: this project's trail records",
			"(every worktree shares them) and, with --global, the global ones. add",
			"reads the body on stdin (empty or over 1 MiB refused) and prints the new",
			"ID; an --id, title or body the dream's redactor would change is stored",
			"with a warning on stderr. get prints each record (with (provenance P) on",
			"memory and feedback). export writes the project's trail records to",
			".agents/trail.jsonl on demand; sync loads .agents/trail.jsonl into the",
			"store and its FTS index. import migrates a trail that is still files, with",
			"that warning for each record it stores; until then record refuses.",
			"Exit 0 with or without output, 1 a refusal, 2 a usage error.",
		},
	},
	"memory": {
		synopsis: []string{
			"usage: fyr memory add --type T --name SLUG --description TEXT",
			"                      [--provenance operator|derived]",
			"       fyr memory show",
		},
		about: []string{
			"This project's memory: memory records in the brain store, never exported.",
			"add saves one, its body on stdin, as MEMORY-<SLUG> in this project's",
			"scope, replacing one of the same name; every session writes memory this",
			"way (R10). --provenance defaults to operator; pass derived when what is",
			"saved came from a file or tool output. show prints the memory index, one",
			"line per memory, capped at what a session loads at start (200 lines, 25 KB).",
		},
	},
	"search": {
		synopsis: []string{
			"usage: fyr search [--limit N] [--kind K] [--global] <words>...",
			"       fyr search --refs <BUG-NNN|ADR-NNN|path> [--kind K] [--global]",
			"       fyr search --open [--kind K] [--global]",
		},
		about: []string{
			"Prints the brain store's matching records first (this project's and the",
			"global ones), then a ranked search of every file in this",
			"checkout. The index, .agents/scratch/search.db, is reconciled against git",
			"first, so it never answers from another branch. --refs finds every record",
			"naming an ID, a file or a directory (trailing /); --open lists open bugs,",
			"plans with unticked boxes, current specs, guides and deploy docs, and",
			"queued proposals.",
		},
	},
	"index": {
		synopsis: []string{
			"usage: fyr index [--global] [--rebuild]",
			"       fyr index --register [--rebuild]",
		},
		about: []string{
			"Reconciles the search index against git, or deletes and rebuilds it, and",
			"prints its counts. --register first writes .git/fyr-registered, sealed to",
			"this checkout's path and this home: the operator's consent that search",
			"--global, dream --all, record import --global and the metrics join read",
			"this checkout once it has records in brain.db or .agents/trail.jsonl",
			"(ADR-068, ADR-091). A copy, a moved checkout or another home",
			"registers again; delete the file to opt out. --register exits 2 with",
			"--global or outside a git checkout.",
		},
	},
	"auto": {
		synopsis: []string{
			"usage: fyr auto on [--level L] [--project-idle D] [--user-idle D]",
			"       fyr auto off",
			"       fyr auto status",
			"       fyr auto run [--dry-run]",
		},
		about: []string{
			"An hourly run the operator switches: it does nothing unless on writes",
			"~/.config/fyr/auto.json. on installs its timer, and a flag not given",
			"keeps its value only while auto is on. off needs no lock, replaces a",
			"corrupt config, removes the timer, and exits 1 only when either failed.",
			"status prints the switch, the level, the last run and the day's model",
			"runs. run reads its gates, runs the free checks on each idle, clean",
			"project, logs to ~/.config/fyr/auto/auto.log and files a global review",
			"record when the findings changed; it spends no model and changes no",
			"project. --dry-run prints each gate's verdict and writes nothing.",
			"uninstall removes the timer and leaves auto.json and auto/.",
		},
	},
	"dream": {
		synopsis: []string{
			"usage: fyr dream --all [--dry-run]",
			"       fyr dream --project DIR [--dry-run]",
			"       fyr dream --install-timer [--dry-run]",
			"       fyr dream --remove-timer [--dry-run]",
		},
		about: []string{
			"The background dreamer. It ingests every session into the metrics store,",
			"then for each registered project (fyr index --register) that is idle (no",
			"commit, record or session for 24 hours; --all only) and changed since its",
			"last dream, sends its memory, new trail, feedback, a numbers-only metrics",
			"summary and its conversations, redacted, to agy granting no tool, and stores",
			"each proposal as a global queued proposal record. --all needs an agy",
			"session of this user, logs every other project as skipped and tries one",
			"once in 24 hours; --project needs no registration. --install-timer runs",
			"dream --all hourly with this shell's PATH (uninstall removes it). --dry-run",
			"writes nothing of fyr's but asks agy's permission reports, as a run does.",
		},
	},
	"metrics": {
		synopsis: []string{
			"usage: fyr metrics ingest [--source DIR --label KEY=VALUE",
			"                          [--outcome pass|fail|error]]",
			"       fyr metrics show [--since YYYY-MM-DD] [--until YYYY-MM-DD]",
			"                        [--project DIR] [--label KEY=VALUE | --all-labels]",
			"                        [--context-target N] [--json]",
			"       fyr metrics report [--since YYYY-MM-DD] [--until YYYY-MM-DD]",
			"                          [--project DIR] [--label KEY=VALUE | --all-labels]",
			"                          [--context-target N] [--out FILE]",
			"       fyr metrics evals [--since YYYY-MM-DD] [--until YYYY-MM-DD]",
			"                         [--harness KEY] [--json]",
			"       fyr metrics export FILE",
		},
		about: []string{
			"The metrics store, ~/.config/fyr/metrics.db. ingest reads the shape of",
			"every Antigravity session (tools, command verbs, tokens, timings; never",
			"arguments or prose). show summarises it: outcome, efficiency, adherence,",
			"reliability; labelled (sandbox) sessions only when named. report writes",
			"that as one HTML file with charts, 0600, never over a file. evals prints",
			"a line per fyr eval row (kind, pass rates, Wilson interval, efficiency",
			"means, expect's m of n, a flag from its whole history: saturated, broken,",
			"unstable or history) and the suite with each kind's rate; --harness keeps",
			"one key's trials. export writes every row but the cmd_hash salt to FILE",
			"as JSONL, 0600, an existing FILE refused: it is the store's only backup.",
		},
	},
	"eval": {
		synopsis: []string{
			"usage: fyr eval --home DIR [--fixture DIR] [--suite-file PATH] [--runs N]",
			"                <row>...",
			"       fyr eval [--suite-file PATH] --list",
		},
		about: []string{
			"Runs the named rows of HEAD's .agents/eval/GUIDE-eval.md (else the GUIDE-eval",
			"record; or --suite-file PATH outside every checkout) against the Antigravity",
			"CLI, each row --runs N times (1 to 25, default 1), one trial at a time in its",
			"own sandbox under --home's .config/fyr/eval/<stamp>/<row>/<i>/: a clone of the",
			"fixture, a throwaway HOME with the harness installed, your login linked. What",
			"the agent reads goes to the CLI's model (R8). Prints PASS, FAIL or ERROR per",
			"trial, writes one testing record, stores each trial and ingests it as",
			"eval=<row> (or eval=holdout/<row>); sandboxes stay. --home is required,",
			"refused when it is the real home, links its state, or is in a checkout.",
			"Exit 0 all passed, 1 any did not or did not run, 2 a refusal.",
		},
	},
	"work": {
		synopsis: []string{"usage: fyr work [--timeout D] [--model M] [--max-workers N] [--keep]",
			"                [--runner host|docker] [--image REF] [--agy-linux PATH]",
			"                <kickoff-file>...",
			"       fyr work --kill <stamp>"},
		about: []string{
			"Runs each Kickoff file as a worker: an unattended agy session in its own",
			"sandbox under --home's .config/fyr/work/, on a clone of HEAD with no",
			"remote, on the branch work/<file name>, at most N at once machine-wide",
			"(default 4, at most 8); files sharing a path are refused. What the agent",
			"reads goes to the CLI's model (R8). A Kickoff's Model: line, else --model,",
			"is its agy model, refused unless agy models lists it. Each branch is",
			"fetched back, checked against its Files: line, reported in at most 39",
			"lines; never pushed or merged. --runner docker: each session in a hardened",
			"container of --image with a linux agy. Refused on a dirty tree. --kill",
			"<stamp> ends that run, same --home. Exit 0 all PASS, 1 any FAIL, 2 refused.",
		},
	},
	"check": {
		synopsis: []string{"usage: fyr check"},
		about: []string{
			"A read-only drift check of the working directory's git checkout; it",
			"writes nothing. It checks every fyr verb, subverb and flag the tracked",
			"Markdown and the current spec, guide and deploy records name in code, and",
			"every path they name, against this binary and the tree; every current",
			"spec whose code changed after it, or that names none; every Must and",
			"Should item of PRD.md (REQUIREMENTS.md without one) whose enforced by:",
			"does not resolve, or, when SDD.md exists, that SDD.md's trace lacks; and",
			"every SDD.md component TDD.md's trace lacks. One line per finding, then a",
			"summary. Exit 0 clean, 1 with findings, or 1 for a refusal (on stderr).",
		},
	},
	"serve": {
		synopsis: []string{"usage: fyr serve [--port N]"},
		about: []string{
			"Serves pages of this checkout and this home on http://127.0.0.1:N/",
			"until Ctrl-C: status, the context, records, search, memory, metrics,",
			"eval results, the dream log and fyr check, with two buttons that run",
			"fyr index and fyr metrics ingest, one at a time, no flags. Prints the",
			"URL with this run's path and token once; open it by pasting it into the",
			"address bar. Listens on 127.0.0.1 only and opens no browser. A request without",
			"the token, from another host name or from another site is refused. A",
			"second Ctrl-C while it stops exits 1 at once. A busy port exits 1.",
		},
	},
	"version": {
		synopsis: []string{"usage: fyr version"},
		about:    []string{"Prints the build version."},
	},
}
