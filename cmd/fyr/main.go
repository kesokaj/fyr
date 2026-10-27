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
//	fyr dream --all|--project DIR [--dry-run]  propose rules and skills, as proposal records, from every idle project's memory, trail, feedback and transcripts
//	fyr dream --install-timer|--remove-timer [--dry-run]  install or remove the nightly timer
//	fyr metrics ingest|show|report|export  read every session's shape into the metrics store, summarise it for agent evaluation (text, JSON or an HTML report with charts), or back it up as JSONL
//	fyr memory add|show         write one memory record (body on stdin) or print this project's memory index (the brain store, D14)
//	fyr record add|get|edit|list|import  the brain store: this project's trail records (exported to .agents/trail.jsonl) and the global ones
//	fyr check                   read-only drift check of this checkout's docs against the binary and the tree
//	fyr eval [--fixture DIR] <row>...  run the project's GUIDE-eval rows against the Antigravity CLI, each in its own sandbox
//	fyr version
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
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
  dream --all | --project DIR [--dry-run]  the background dreamer: first ingest every session's shape into the metrics store; then for every project (the file index's, and every project scope of the brain store) that is idle — no commit, record update or Antigravity session within 24 hours; --all only — and changed since its last dream (or the one named), send its memory, new trail, feedback, a numbers-only summary of its metrics and its Antigravity conversations, redacted, to agy with no tool it may use, and store each proposal it returns as a global queued proposal record about the project, its draft skills under ~/.config/fyr/proposals/dream/drafts/ (R3's exception)
  metrics ingest [--source DIR --label KEY=VALUE [--outcome pass|fail]]  read the shape of every Antigravity session (tools, command verbs, tokens, timings; never arguments or prose) into ~/.config/fyr/metrics.db, the metrics store; --source reads a sandbox home and stamps its sessions with the label; conversations come from --source, else this home
  metrics show [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--json]  the agent-evaluation summary of the metrics store, an agy column (and a claude (historical) column only when the store holds rows an older ingest read from Claude Code): outcome (DoD closure, verifier status, review findings, escaped bugs, corrections, pass^k per label), efficiency (tool calls, turns, tokens, cost, wall and active time, peak context, the main sessions whose peak passed the context target — N tokens, default 300000 — and each main session's peak in --json, compactions), process adherence (fyr search vs grep, forbidden command shapes, first reads, skills) and reliability (tool error rate, denials, interrupts, retry loops, subagent tool calls); n/a is a metric that CLI cannot produce; sandbox (labelled) sessions are left out unless named
  metrics report [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--out FILE]  write show's summary as one self-contained HTML file with charts (per-day tool calls, tokens, cost, search:grep and errors, one line per column; tool calls by tool; subagent tool calls by type; pass^k per sandbox label) and the summary table, with the sessions over the context target as k of n, inline SVG, no script, light and dark; default ~/.config/fyr/reports/<date-time>-metrics.html, 0600, an existing file refused
  metrics export FILE     write every row of the metrics store but its cmd_hash salt to FILE as JSONL (0600; an existing FILE is refused): its only backup, since the store is the one copy once a transcript expires
  memory show             print this project's memory index, one line per memory record, capped at what a session loads (200 lines, 25 KB)
  record add <kind> --title T [--status S] [--id ID] [--global [--project ROOT]]  store one record, its body on stdin (empty or over 1 MiB refused), in this project's scope of the brain store (~/.config/fyr/brain.db; every worktree of a repository shares one) or with --global the global scope, and print its ID (BUG-NNN, ADR-NNN, else KIND-YYYY-MM-DD-HHMM-slug); a title or body the dream's redactor would change is stored with a warning on stderr, never its text; kinds: plan review testing change handoff bug adr retro learning spec deploy guide memory, and feedback and proposal, which are --global only; --project names the project a global record is about
  record get <id>... [--global]  print each record's line and its body, through the same printable filter as search
  record edit <id> [--status S] [--title T] [--body] [--global]  change a record; --body reads the new body on stdin; a new title or body gets add's redactor warning; without it only the metadata changes; a plan's status follows its body (open while it has an unticked "- [ ]")
  record list [--kind K] [--open] [--global [--project ROOT]] [--all]  ID  kind  status  date  title, newest first; --open: open plans and bugs, current specs, deploy docs and guides, queued proposals; --all: every scope, each line ending in its scope
  record import [--dry-run] [<docs-root>]  migrate this project's old trail (default .agents/docs/, else docs/) and its old .agents/memory/ store into records: open plans, the newest handoff, every ADR and bug (a closed bug trimmed to its title and prevention rule), specs, deploy docs and guides, and every trail file those, REQUIREMENTS.md, AGENTS.md, README.md, context/, skills/, agents/ or rules/ name; the rest is dropped (git history keeps it); refuses a dirty tree and a non-markdown file under the docs root; removes nothing, and prints every file to remove by name afterwards
  record import --global [--dry-run]  migrate ~/.config/fyr's feedback.jsonl, retros.jsonl and proposals/ into global records, and every indexed project's old .agents/memory/ store into its memory records; a bad line is reported, records already stored are skipped
      a project's records other than memory are exported on every write to .agents/trail.jsonl (commit it); when that file changes under the store (a pull, checkout or merge) it replaces the project's records before the next record or search call, and a bad line or a duplicate ID is refused with its line number; in a project whose trail is still files (docs/ or .agents/docs/ and no .agents/trail.jsonl) record refuses until it is migrated with fyr record import; exit 0 with or without output, 1 a refusal, 2 a usage error
  check                   read-only drift check of the working directory's git checkout, writing nothing (no index, no brain store): every fyr verb, subverb and flag named in code (inline spans and fences) by its tracked Markdown files and its current spec, guide and deploy records in .agents/trail.jsonl, and every path or path:line of the checkout named in an inline span, against the running binary and the tree; every current spec record whose code (the existing non-Markdown paths it names) was committed to after the commit carrying its current trail line, or that names none, skipped with a note on a shallow or partial clone; every Must and Should item of REQUIREMENTS.md whose enforced by: comment is missing, empty, the template's placeholder, not yet enforced, or names a path, Go test or record that does not exist, with a note when the file has no enforced by: field; ROADMAP.md, TODO.md, vendored skills and noindex directories are left out; prints "<where>:<line>  <rule>  <text>" per finding, a "note:" line per part not checked, then "check: N findings (docs D, specs S, requirements R); fyr <version>"; exit 0 clean, 1 with findings, 1 with no summary line and "check: <reason>" on stderr for a refusal (not a checkout, a trail.jsonl that does not parse)
  eval [--fixture DIR] <row>...  run the named rows of this checkout's suite, the GUIDE-eval record (its committed copy: a suite that differs from HEAD's is refused), against the Antigravity CLI, one at a time, each in its own sandbox under <home>/.config/fyr/eval/<stamp>/<row>/ (a clone of the fixture, default this checkout, and a throwaway HOME with the harness installed as install would and your Antigravity login linked in for the run); the agent's prompts and the files it reads go to the CLI's model, unredacted, as in any session (R8, ADR-065); a row PASSes when its check passes, the agent it names was invoked, nothing was auto-denied, and the run left no process, no new credential in the sandbox and no change to the real config it watches (~/.config/fyr, ~/.config/sh-agent, ~/.gemini/config and the CLI's settings.json); prints one line per row, then writes one testing record and ingests each sandbox into the metrics store with the label eval=<row> (fyr metrics show --label eval=<row> compares runs); the sandboxes stay, and uninstall leaves them; a --home inside a git checkout is refused (BUG-106); exit 0 every row passed, 1 any failed, 2 a refusal before any row ran
  eval --list             print the suite's rows: id, agent or -, named or wide
  version                 print the build version

Flags accepted by every command:
  --repo DIR        harness checkout, for a build made from one (default: --repo, $FYR_REPO, the working directory or its parents, then the last install). A release binary installs the harness built into it and refuses --repo (ADR-034)
  --home DIR        install under DIR instead of $HOME (for testing)
`

// stderr is where a read-only verb names the state directory from before the
// rename, and where `record add` and `record edit` print the redactor
// warning: their stdout is data (render, list, metrics show --json, a new
// record's ID); a test replaces it.
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
