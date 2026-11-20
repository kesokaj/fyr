package main

// The text of fyr's help, as data (plan ledger D19, § Interfaces "Phase 4b"):
// the short page, and per verb its synopsis and what it does. help.go builds
// each page's Flags: list from the verb's FlagSet, so a flag is described
// once, in its usage string. Every line is at most 79 runes, so an
// 80-column terminal never wraps it; help_test.go pins that, the ten-line
// cap on a description, and the verbs against the command table.

// helpLast ends the short page and every verb page. Its third line is the
// real-home guard (BUG-507, ADR-505, realhome_guard.go).
const helpLast = "Every command takes --home DIR, --repo DIR and -C/--dir DIR.\n" +
	"Without --home, a build not at ~/.local/bin/fyr\n" +
	"runs only install, sync, update, version, list, render, diff."

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
  resume       read-only seven-step session resume
  search       ranked search of records and files
  index        reconcile the search index; --register opts in
  graph        interactive AST and telemetry constellation

Background and measurement
  dream        consolidate project memory and trail into proposals
  auto         idle-time runs: on, off, status, run, log
  metrics      session shapes: ingest, show, report, evals, trajectory, export
  report       project or global usage report (HTML, --md, --json, --web)
  models       the live agy model catalog and role picks
  eval         run behavioural rows against the CLI
  verify       run gofmt, go vet, go test -race, check and write stamp
  gate         run lease, numstat, secret and trailer checks on a diff
  land         gate, squash-verify in worktree, ff main and delete branch
  close        write one handoff record embedding the fresh verify stamp
  check        read-only drift check of this checkout's docs
  git-check    read-only post-dispatch git status, log and tag check
  watch        inspect sessions, artifacts and processes for hangs
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
var pages = mergePages(coreVerbPages, opsVerbPages)

func mergePages(groups ...map[string]verbPage) map[string]verbPage {
	out := make(map[string]verbPage)
	for _, g := range groups {
		for k, v := range g {
			out[k] = v
		}
	}
	return out
}

var coreVerbPages = map[string]verbPage{
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
		synopsis: []string{"usage: fyr status [--json]"},
		about: []string{
			"Prints what is installed, from which commit, the binary's build, the",
			"drift against the harness, dream status, auto, and a note when another",
			"fyr comes first on PATH; --json prints it as JSON. Exit 1 on drift.",
		},
	},
	"diff": {
		synopsis: []string{"usage: fyr diff"},
		about:    []string{"Prints a unified diff of every installed file that drifted."},
	},
	"uninstall": {
		synopsis: []string{"usage: fyr uninstall [--dry-run]"},
		about: []string{
			"Removes exactly the files install wrote, and the auto timer or any",
			"legacy dream timer when installed; nothing else. The metrics store,",
			"eval's sandboxes, auto.json and auto/ stay. --dry-run names what",
			"would go.",
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
			"usage: fyr record add <kind> [--title T | --title-file FILE] [--status S]",
			"                      [--id ID] [--body-file FILE | --json-in]",
			"                      [--global [--project ROOT]]",
			"       fyr record get <id>... [--out FILE | --json] [--global]",
			"       fyr record edit <id> [--status S] [--title T]",
			"                       [--body | --body-file FILE | --json-in] [--global]",
			"       fyr record list [--kind K] [--open] [--limit N] [--json]",
			"                       [--global [--project ROOT]] [--all]",
			"       fyr record export",
			"       fyr record sync",
			"       fyr record import [--dry-run] [<docs-root>]",
			"       fyr record import --global [--dry-run]",
		},
		about: []string{
			"The brain store, ~/.config/fyr/brain.db: this project's trail records",
			"(every worktree shares them) and, with --global, the global ones. add",
			"reads the body on stdin, --body-file or --json-in (empty or over 1 MiB",
			"refused) and prints the new ID; an --id, title or body the dream's redactor",
			"would change is stored with a warning on stderr. get prints each record (with",
			"(provenance P) on memory and feedback), writes one body under .agents/scratch/",
			"with --out, or prints JSON with --json. export writes .agents/trail.jsonl on",
			"demand; sync loads .agents/trail.jsonl into the store and its FTS index;",
			"import migrates a file-era trail. Exit 0, 1 a refusal, 2 a usage error.",
		},
	},
	"memory": {
		synopsis: []string{
			"usage: fyr memory add [--type T --name SLUG --description TEXT]",
			"                      [--body-file FILE | --json-in]",
			"                      [--provenance operator|derived] [--global]",
			"       fyr memory show [--global] [--include-low] [--json]",
		},
		about: []string{
			"Project or shared (--global) memory cards in the brain store, never exported.",
			"add saves one, its body on stdin (with optional Type:/Name:/Description:/",
			"Provenance: headers), --body-file or --json-in, as MEMORY-<SLUG> in the",
			"project's scope or shared global scope, replacing one of the same name (R10).",
			"--type takes semantic, procedural or episodic. --provenance defaults to",
			"operator; pass derived when saved from a file or tool output. show prints",
			"active cards (>= 0.40 validity; --include-low for all, --json for JSON),",
			"capped at 200 lines and 25 KB in text mode.",
		},
	},
	"search": {
		synopsis: []string{
			"usage: fyr search [--limit N] [--kind K] [--global] [--json] <words>...",
			"       fyr search --refs <BUG-NNN|ADR-NNN|path> [--kind K] [--global]",
			"       fyr search --open [--kind K] [--global]",
			"       fyr search --slice QUERY [--hops N] [--budget N] [--session ID]",
			"                  [--include-docs]",
			"       fyr search --skeleton FILE",
			"       fyr search --path SYM [--to SYM]",
			"       fyr search --affected TARGET",
			"       fyr search --map [--include-docs]",
			"       fyr search --vet PROMPT",
			"       fyr search --hook <session-start|pre-invocation|pre-tool-use|",
			"                          post-tool-use|stop>",
		},
		about: []string{
			"Prints the brain store's matching records first (this project's and the",
			"global ones), then a ranked search of every file in this",
			"checkout. The index, .agents/scratch/search.db, is reconciled against git",
			"first, so it never answers from another branch. --refs finds every record",
			"naming an ID, a file or a directory (trailing /); --open lists open bugs,",
			"plans with unticked boxes, current specs, guides and deploy docs, and",
			"queued proposals. --slice, --skeleton, --path, --affected and --map query",
			"the AST symbol and reference graph; --vet runs the 9-mode intent, 6-anchor",
			"and skill/record vetter; --hook serves Antigravity CLI hooks.",
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
			"this checkout's path and this home: the operator's consent that fyr",
			"search --global, fyr record import --global, and the metrics join read",
			"this checkout once it has records in brain.db or .agents/trail.jsonl",
			"(ADR-068, ADR-091). A copy, a moved checkout or another home",
			"registers again; delete the file to opt out. --register exits 2 with",
			"--global or outside a git checkout.",
		},
	},
}
