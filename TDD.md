# TDD — fyr

How each part of the harness is built and tested: one section per component of SDD.md, each pointing at the record that holds its detail.
Status: current · Last updated: 2026-10-29 · Requirements: [PRD.md](PRD.md) · Design: [SDD.md](SDD.md)

<!-- One section T1… per component M… of SDD.md — its interfaces, data, errors, configuration and dependencies —
     each a summary that points at the SPEC record holding the detail (`fyr record get SPEC-…`), never a copy of it.
     TODO.md tasks name the section they build (→ T1). Kept current by the session that changes the code, in the same
     commit. -->

## T1 — Global context and rules

Implements: M1 · Spec: none (the files are their own spec; the size limits below and ADR-035)

- **Interfaces:** `context/AGENTS.md`, then `context/dev-setup.md`, rendered by `fyr render` into one file (`~/.gemini/config/rules/AGENTS.md`), plus each `rules/*.md` file — `critical-thinking.md` (ADR-050), `session-sizing.md` (ADR-058, ADR-077), `code-modularity.md` (ADR-066) — all in Antigravity's rules directory.
- **Errors:** a context over its size pin fails `go test ./internal/harness`; a rule with a frontmatter key outside its allow list fails the install's lint.
- **Configuration:** none; the canary word ends the rendered context.
- **Size pins** (`internal/harness/size_test.go`): `TestRenderedContextUnderTarget`, the hand-written context (the render before its generated `## Installed` inventory) under 15,000 bytes, the harness's own ceiling and the only one; `TestRenderedContextFitsAntigravityBlock`, the whole render under 22,000 bytes inside Antigravity's 24,000-byte block (ADR-035), which the inventory, growing with every skill, counts toward. A limit is never raised.

## T2 — Skills and commands

Implements: M2 · Spec: none (the Agent Skills `SKILL.md` standard; vendored provenance in `skills/VENDORED.md`, the catalog's in `skills/google-catalog/catalog/MANIFEST.md`)

- **Interfaces:** `skills/<name>/SKILL.md` with a `description`, loaded when it matches and typed as `/name`; `commands/<name>.md`, deployed to `~/.gemini/config/workflows/`, empty by design (the operator, 2026-09-30).
- **Errors:** the lint refuses a skill without `SKILL.md` or a description, a description over 1,024 characters, a key outside the allow list, and a value a YAML reader would read as a comment.
- **Configuration:** the listing budget, `TestSkillListingBudget`: 19,500 bytes over the name and description of every skill without `disable-model-invocation: true`; past Claude Code's budget a skill is a bare name nothing triggers, so catalog entries never become global skills.

## T3 — Subagents

Implements: M3 · Spec: none (Antigravity's `agent.md` format; the allow-list is ADR-056)

- **Interfaces:** `agents/<name>/agent.md` with `subagent: true`, `mainAgent: false` and a `tools:` list, installed as written (ADR-062).
- **Errors:** a tool name outside `knownTools` (`internal/harness/inventory.go`) fails the install, because Antigravity hangs such a subagent.

## T4 — The embedded harness

Implements: M4 · Spec: `SPEC-fyr-delivery`

- **Interfaces:** `embed.go`, package `fyr` at the root (a `go:embed` cannot reach a parent directory), embeds `context/`, `skills/`, `agents/`, `commands/` and `rules/`; a release binary installs from it and refuses `--repo` (ADR-034).
- **Errors:** an embedded file git does not track, or a tracked one the embed misses, fails `go test .`.

## T5 — The CLI

Implements: M5 · Spec: `SPEC-fyr-delivery` (install and update at each kind of place), `GUIDE-fyr-matrix` (every verb on every kind of place), and the verb's own `SPEC-fyr-*` record

- **Interfaces:** the verbs below; `fyr help <verb>` prints each one's page from `cmd/fyr/help_text.go`.
- **Errors:** a usage error exits 2 and prints the verb's synopsis; a refusal exits 1 with its reason on stderr.
- **Dependencies:** M6 for install, update and status; M7–M14 for their verbs.

### Verbs

The verb table below moved here whole from README § Daily commands on 2026-10-29 (plan D16, D36): several of its claims have no other home yet. Each row is thinned to one line and its spec link once its claims live in that spec or the verb's help page (`TODO.md` Backlog).

| Command | What it does |
|---|---|
| `fyr status` | What is installed, from which commit, the binary's build, drift against the repository, the dream timer (and when its unit is an older fyr's, the two commands that replace it), and a note for a `PATH` shadow. Exit 1 on drift. |
| `fyr update` | At a checkout: fetch, refuse a dirty tree, fast-forward, rebuild, reinstall. At a release-binary install: download the newest release, check it against its `SHA256SUMS`, replace the binary, reinstall. `--check` only reports (exit 2 when behind). `--from-zip FILE` installs a downloaded repository zip. `--dry-run` names what would change and writes nothing. |
| `fyr install` (or `sync`) | Deploys the repository (or the harness built into a release binary) and removes files a previous install wrote that the repository no longer has, except the Claude Code files an install before fyr wrote under `~/.claude`, which it keeps and stops managing (`DEPLOY-remote` § Crossing to fyr). Idempotent; `--dry-run` prints without changing. |
| `fyr diff` | A unified diff of every drifted file. |
| `fyr uninstall` | Removes exactly what it installed, the dream timer included; the metrics store stays. `--dry-run` names what would go. |
| `fyr list` / `render` | The skill and agent inventory; the assembled context as deployed. |
| `fyr record add` / `get` / `edit` / `list` / `import` | The brain store, `~/.config/fyr/brain.db`: this project's trail records (plans, bugs, ADRs, reviews, handoffs, specs, …) and the global ones (`--global`: feedback, proposals, retros), each with an ID and a status; the body comes on stdin, and a title or body the dream's redactor would change is stored with a warning on stderr. Every write in a project also rewrites `.agents/trail.jsonl`, the committed export, and a changed export (a pull) is loaded back before the next call. `list --open` is what is still open. `import` migrates a project whose trail is still Markdown files into records, once; `import --global` takes the old logs under `~/.config/fyr`. |
| `fyr search <words>` | Prints the matching brain-store records first, then a ranked full-text search over the current project's files and trail records (bugs, decisions, plans, changes), with each record's id and status; `--refs BUG-NNN` finds everything that cites one; `--open` lists what is still open. `fyr index` maintains the index; `fyr index --register` is the operator's consent that the project is read across projects — the global search index and record hits (`--global`), `fyr dream --all`, `record import --global` and the metrics join — a marker under `.git/` sealed to this checkout's path and this home, which no clone or copy carries (ADR-068, ADR-068a). |
| `fyr memory add` / `show` | The project's memory, `memory` records in the brain store (never exported); `show` prints the index a session loads at start. |
| `fyr metrics ingest` / `show` / `report` / `export` | Reads every session's shape into `~/.config/fyr/metrics.db`, summarises it (outcome, efficiency, process adherence, reliability; `--json`), renders it as one HTML page with charts, backs it up as JSONL. Ingest runs at every checkpoint (the `handoff` skill's last call, `/retro` before its numbers, a phase end) and in each dream run, and reads Antigravity only; rows an older version stored for another CLI are kept and shown as a historical column. Never arguments, prose or tool output. |
| `fyr dream --install-timer` | An hourly job (systemd or launchd user timer) that does nothing unless an interactive `agy` session of yours runs; then it ingests metrics and, for every project registered with `fyr index --register` that has been idle for a day and changed (each tried at most once a day), sends its memory, new trail and transcripts, redacted, to `agy` headless in plan mode with no tools: in your own Antigravity home when agy's own reports show it grants no tool without review and loads no hook, and nothing but your own setup sits above the dream's empty working directory (no git checkout or other `.agents`), so each project's dream is a conversation `/resume` lists, otherwise in a private home as before; the proposals are stored as global `proposal` records for `/retro` to apply. `--remove-timer` undoes it. A timer an older fyr installed keeps its nightly schedule, at which, with the session check, it almost never dreams, until you run `fyr dream --remove-timer`, then `fyr dream --install-timer`; `fyr status` names it. |
| `fyr check` | A read-only drift check of the current checkout: every `fyr` verb, subverb and flag its Markdown and current spec, guide and deploy records name in code, and every path or `path:line` they name in an inline span, against the running binary and the tree; every current spec whose code was committed to since the spec's own last update, or that names no code; and every Must and Should item of `PRD.md` (`REQUIREMENTS.md` in a project without one) whose `enforced by:` is missing or does not resolve (a file with no `enforced by:` field at all is noted, not checked). Prints one line per finding and a summary; exit 1 on findings. Writes nothing (`SPEC-fyr-check`). `/review --docs` and the `requirements` skill run it. |
| `fyr serve` | Serves read-only pages of the working directory's checkout and the home's stores on `127.0.0.1:N` (`--port`, default 3000) in the foreground until Ctrl-C: status, the context, records (project and global) and search, memory, the metrics report, eval results, the dream log and `fyr check`. Prints one URL with the run's path prefix and token; the token becomes a cookie scoped to that prefix, and a request without it, with another `Host` or from another site is refused 403. Opens no browser; a second Ctrl-C while it stops exits 1; a busy port exits 1 `/check` runs one at a time (`SPEC-fyr-serve`, ADR-075, ADR-075a). |
| `fyr eval --home DIR <row>...` | Runs the named rows of this checkout's suite, the `GUIDE-eval` record, against the Antigravity CLI, one at a time, each in its own sandbox under `<home>/.config/fyr/eval/` (a clone of the fixture, `--fixture DIR` or this checkout, and a throwaway HOME with the harness installed and your login linked in for the run), with its safety checks around it. Prints one line per row, writes one `testing` record, and ingests each sandbox into the metrics store under the label `eval=<row>`, so `fyr metrics show --label eval=<row>` compares runs over time. What the agent reads goes to the CLI's model, as in any session (R8). Sandboxes stay for inspection, and `fyr uninstall` leaves them. `--list` prints the rows. `--home` is required: a missing one, the real home, a home that reaches the real state through a link, and a `--home` inside a git checkout are refused with exit 2 before anything is written. Exit 1 when a row fails (`SPEC-fyr-eval`). |
| `fyr work [--timeout D] [--model M] [--max-workers N] [--keep] <kickoff-file>...`, `fyr work --kill <stamp>` | Runs each Kickoff file as a worker: an unattended `agy` session in its own sandbox under `<home>/.config/fyr/work/`, on a clone of this checkout's HEAD with no remote, on the branch `work/<file name>`. Afterwards it fetches the branch back into this checkout by path, checks it stayed inside the file's `Files:` line, copies its drafts under `.agents/scratch/work/`, removes the sandbox of a fetched worker unless `--keep`, and prints the result contract (the result line, the handoff's sections, the change records; at most 39 lines per worker). It never pushes or merges. Several files run at once up to `--max-workers` (default 4, 1 to 8), counted across the machine from each run's pid file; files past the limit wait and start in file order, and two files whose `Files:` entries intersect are refused. Each worker's block prints whole, in file order. `--kill <stamp>` sends SIGTERM to the run the stamp line named, which prints its own contract; it takes no file. `--timeout` defaults to 120m, whole minutes, at least 10m. SIGINT, SIGTERM, SIGHUP and SIGQUIT fail each running worker `killed` (a file still waiting never starts and fails `killed` too) and still print the contract. The default `--home` is the real home: the verb writes no store. What the agent reads goes to the CLI's model, as in any session (R8, ADR-076). Exit 0 PASS, 1 FAIL, 2 a refusal or usage error before anything ran, printed `work: <reason>` (`SPEC-fyr-work`). |
| `fyr adopt <name>` | Copies a skill installed elsewhere (for example by `npx skills add -g`) into `skills/`. |
| `fyr version` | The build version. |

`fyr` and `fyr help` print the short page, one line per command; `fyr help <verb>` or `fyr <verb> --help` prints that verb's page: its synopsis, what it does, and its flags. A flag a verb does not know is a usage error, exit 2, which prints the verb's synopsis. The global flags are `--repo DIR`, the harness checkout to build and install from (by default `$FYR_REPO`, then the working directory or its parents, then the tree the last install recorded; a release binary installs the harness built into it and refuses `--repo` and `$FYR_REPO`, ADR-034; a verb that does not read it, every verb but `install`, `sync`, `status`, `diff`, `update`, `list`, `render`, `adopt`, `serve` and `eval`, says on stderr that it ignored it and runs), and `--home DIR` (install somewhere other than `$HOME`, which the tests use); `--vendor` no longer exists and is a usage error. The full behaviour of each verb is in the `SPEC-fyr-*` records (`fyr record list --kind spec`, then `fyr record get`).

## T6 — The installer

Implements: M6 · Spec: `SPEC-fyr-delivery`; the working rules are `AGENTS.md` § Rules for working here

- **Interfaces:** `internal/harness/plan.go` says what is installed where; `internal/harness/inventory.go` holds the lint; the manifest is `~/.config/fyr/state.json`.
- **Rebuild or copy:** `fyr install` rebuilds the binary from its own git checkout with `go build` and lets that build install (BUG-006); it copies the running binary only when `go` is missing, when the tree is a plain copy with no `.git` (its binary was built from that tree, which may be read-only to the installing user, BUG-015), or when the source is the harness embedded in a release binary (ADR-033, D13).
- **Lint refusals** (`inventory.go`, each with its bug in `TestLint*` and `TestAgentDescriptionRules`): the flat `agents/<name>.md` format from before ADR-062; a skill without `SKILL.md` and its `description`; a description over 1,024 characters, or in an agent one that is not one line or names "dispatch prompt" or "harness binary"; a frontmatter value a strict YAML parser reads as a comment (BUG-004: quote it); in every kind a key outside its allow list (`agentKeys`, `skillKeys`, `commandKeys`, `ruleKeys`; IN23), a permission key (`allowed-tools`, `hooks`, `permissionMode`, `mcpServers`) above all; a command or rule whose file name fails `validName`; a frontmatter block a YAML reader could read differently from the lint's line grammar (BUG-143, BUG-153).
- **Errors:** every lint refusal names the file and the rule; a failed write keeps the manifest complete; `update` refuses a dirty checkout and an unknown status.
- **Configuration:** `--home DIR` for every development run (R6); `--repo DIR` or `$FYR_REPO` for the checkout.
- **Dependencies:** git and `go` for a checkout's rebuild; `gh` for a release binary's update.

## T7 — The brain store

Implements: M7 · Spec: `SPEC-fyr-record`

- **Interfaces:** `fyr record add`, `get`, `edit`, `list` and `import`; a project's records exported to `.agents/trail.jsonl`.
- **Data:** `~/.config/fyr/brain.db`, a store of record; project records are restored from the export, global ones exist only in the store (R4).
- **Errors:** a trail line that does not parse refuses the whole file with its line number.

## T8 — Search

Implements: M8 · Spec: `SPEC-fyr-search`

- **Interfaces:** `fyr search` (records first, then files), `--refs`, `--open`, `--global`; `fyr index` and `fyr index --register`.
- **Data:** the indexes are caches, rebuilt when deleted (R4). The dream job's two read-only SQLite reads live here too: the global index's project list and Antigravity's conversation databases, both from copies.

## T9 — Memory

Implements: M9 · Spec: `SPEC-fyr-memory-loop`

- **Interfaces:** `fyr memory add` and `fyr memory show`, the index a session loads at start.
- **Data:** `memory` records in the brain store, never exported (R7).

## T10 — Metrics

Implements: M10 · Spec: `SPEC-fyr-metrics`

- **Interfaces:** `fyr metrics ingest`, `show`, `report`, `export`.
- **Data:** `~/.config/fyr/metrics.db`, a store of record of session shape: never arguments, prose or tool output; `uninstall` leaves it.

## T11 — The dreamer

Implements: M11 · Spec: `SPEC-fyr-dream`

- **Interfaces:** `fyr dream`, `--all`, `--dry-run`, `--install-timer`, `--remove-timer`.
- **Errors:** an ingest failure does not fail the dream; a tool call by the model ends the run with no answer.
- **Dependencies:** M10 first, then M12 on every input, then `agy` headless in plan mode.

## T12 — The redactor

Implements: M12 · Spec: none (internal; `SPEC-fyr-dream` names where it runs, and its tests pin every needle)

- **Interfaces:** one function over text, one over JSON lines; stdlib only.
- **Needles:** a needle matched against raw text has no `\b` in front: a literal `\n` or an underscore is a word character and hides the token.
- **Errors:** over-redaction is the safe direction and is pinned, never argued (`AGENTS.md` § Rules for working here).

## T13 — Eval

Implements: M13 · Spec: `SPEC-fyr-eval`; the rows are the `GUIDE-eval` record

- **Interfaces:** `fyr eval --home DIR <row>...` and `fyr eval --list`.
- **Errors:** a missing `--home`, the real home, or a home inside a git checkout is refused with exit 2 before anything is written.
- **Data:** one `testing` record per run, and metrics under the label `eval=<row>`; the package imports no SQLite driver: `cmd/fyr` writes the record and the metrics rows.
- **Dependencies:** M17 runs steps 1, 2, 4, 6–12 and 14 of `SPEC-fyr-eval` § One row; eval keeps steps 3, 5, 13 and 15 and calls M17 in the spec's order, its notes still prefixed `eval: <row>`.

## T14 — Drift check

Implements: M14 · Spec: `SPEC-fyr-check`

- **Interfaces:** `fyr check`, read-only: rule (a) docs, (b) specs, (c) requirements.
- **Errors:** exit 1 with findings; a trail that does not parse is a refusal, never a partial run.
- **Dependencies:** the trail through `brain.ReadTrail`; it opens no store or SQLite database and imports no driver.

## T15 — Trail git reads

Implements: M15 · Spec: none (internal; shared by M7–M11 and M14 so that none imports another)

- **Interfaces:** `trail.Command`, which runs git with the repository variables dropped, with `Dates`, `Log` and `DocsRoot`; `NoIndexMarkers`; the registration marker that `fyr index --register` writes. No SQLite.

## T16 — The local page

Implements: M16 · Spec: `SPEC-fyr-serve` · Decision: ADR-075, ADR-075a · Code: `internal/serve`, `cmd/fyr/verb_serve.go` (read pages built in row 8; the two actions are row 9's)

- **Interfaces:** the serve verb with `--port` (default 3000) and `--home`; a printed URL holding the run's token; read routes for status, context, records, search, memory, metrics, evals, dream and the drift check; two POST actions, the index and the metrics ingest, each the verb's own function.
- **Security:** bound to `127.0.0.1`; the `Host` header checked; a cross-site or same-site `Sec-Fetch-Site` refused; a per-run session token (URL once, then a cookie scoped to a per-run path prefix, `/s/<P>`, so no other port of `127.0.0.1` receives it, ADR-075a) and a separate CSRF token in every form's body; no script, a `default-src 'none'` CSP.
- **Errors:** a refusal is 403 with no detail; a store that will not open fails only its page; a busy port exits 1.
- **Dependencies:** M7, M8, M9, M10, M11 and M14 through their functions; the status, render and check printers and the two actions passed in from M5, which registers the verb in `init()`. Stdlib `net/http`, `html/template`, `go:embed`; no SQLite import of its own.

## T17 — The sandbox

Implements: M17 · Spec: `SPEC-fyr-eval` § One row (the steps) and § Safety checks · Plan: `PLAN-2026-10-29-workers` § Interfaces

- **Interfaces:** `sandbox.New(Config)` and a `Box` whose methods are the steps (`Make`, `Clone`, `Fingerprint`, `Install`, `Settings`, `Baseline`, `Session`, `After`, `ExitChecks`), with `Intact`, `TimedOut`, the reasons and `Note`; `CheckRoot` and `OutsideCheckout` for a verb's refusals; `Root(home, verb)` is `<home>/.config/fyr/<verb>`.
- **Errors:** a failed step adds a fixed reason string and returns false; the steps after it are the caller's to skip. A timeout is `TimedOut`, never a reason.
- **Data:** writes only under its `Dir`; check c's fingerprint skips the state directory's `eval/` and `work/` (D10).
- **Dependencies:** `internal/harness` and `internal/trail`; no SQLite driver (`TestNoSQLiteDriver`), so `internal/eval` keeps none.
- **Work's additions:** `Config.Model` (agy `--model`, none when empty) and `Config.SkipStores` (check c leaves out, at the state directory's top level only, the stores and their SQLite companions (`brain.db`, `search.db` and `metrics.db`, each with `-wal`, `-shm` or `-journal`), the dream's `dream.log`, `dream.lock`, `dream-state.json`, `dream-failed`, `dream-work` and `proposals`, the temporary directories `dream-agy-home-`, `dream-agy-`, `dream-projects-`, `dream-metrics-`, `dream-brain-` and `metrics-agy-` followed by digits, and WriteAtomic's temporary files `.fyr-` followed by digits (files only). Each is a writer's exact name, never a bare prefix, so a file added later under a similar name is still watched; D45 as D64 and D70 amend it, D91; `state.json` is fingerprinted, because only an install writes it); `Box.Started` reports that a session started agy; `Box.Remove` removes an intact sandbox through the path guard a approved, its read-only directories made writable first. Eval sets neither field.

## T18 — The worker

Implements: M18 · Spec: `SPEC-fyr-work` · Plan: `PLAN-2026-10-29-workers` § Interfaces § `fyr work`

- **Interfaces:** `work.Run(ctx, Options, files ...string)` runs the Kickoff files, at most `Options.MaxWorkers` at once across the machine (0 is `DefaultMaxWorkers`, 4; `MaxMaxWorkers` is 8, D30), and returns their `Result`s in file order, each `Block()` being the result contract; `ParseKickoff` is a file's parse; `work.Kill(home, stamp)` signals a run (D33). `fyr work` (`cmd/fyr/verb_work.go`, § T5) maps the outcomes to its exit status; the result contract is at most 39 lines per worker (D85), printed whole in file order as each block and every block before it is done (D19).
- **Slots:** each run holds a pid file `<home>/.config/fyr/work/<stamp>/pid` under an exclusive `flock` for its whole run; its one line, `<pid> <running>`, is rewritten in place. A slot is taken under `slots.lock`, never waited for, counting every other live run's `running`, so two runs never take the last slot, and given back without it; a pid file whose lock can be taken is stale and counts nothing, and one that cannot be read counts as the whole limit. The fetch, scope check, records and drafts run for one worker at a time (a mutex), so two fetches never race in the lead's `.git`.
- **Errors:** a refusal is `Run`'s error and makes nothing. A worker that never got a slot because the run ended fails `killed` without a sandbox; one whose slot count errored fails `setup failed`. A worker's failure is a FAIL reason on the `Result`: the sandbox's own reasons, plus `timed out`, `killed`, `no commits`, `no handoff`, `more than one handoff`, `changed outside its Files`, `rewrote history`, `lead checkout changed` and `branch`.
- **Data:** writes the sandbox under `<home>/.config/fyr/work/`. In the lead's checkout it writes only the branch ref and `.agents/scratch/work/<stamp>/<name>/`. It opens no store; the branch's records are decoded from its `trail.jsonl` with `encoding/json` (ADR-063).
- **Dependencies:** `internal/sandbox` and `internal/trail`; no SQLite driver.

## Data model

| Store | Kind | Holds | Spec |
|---|---|---|---|
| `~/.config/fyr/brain.db` | store of record | every project's trail records and the global ones (feedback, proposal, retro), memory | `SPEC-fyr-record` |
| `.agents/trail.jsonl` | committed export | one project's exported records, one JSON object per line | `SPEC-fyr-record` |
| `~/.config/fyr/metrics.db` | store of record | session shape | `SPEC-fyr-metrics` |
| `.agents/scratch/search.db`, `~/.config/fyr/search.db` | cache | the full-text indexes | `SPEC-fyr-search` |
| `~/.config/fyr/state.json` | state | the install's manifest | `SPEC-fyr-delivery` |

## Testing strategy

`go test` only: unit tests with `-race`, the size pins, the lint tests, and three pins in the root package: `TestFunctionLength` (a non-test function over 50 lines, `rules/code-modularity.md` item 1, unless `longFuncs` names it), `TestGitTestPackagesDropGitVariables` (a test package that names the git binary needs a `TestMain` dropping `GIT_*`) and `TestAreasRunPatternsExist` (every `-run` pattern in `skills/harness-audit/references/areas.md` names a test, WS53). Behavioural runs against the Antigravity CLI are `fyr eval` rows. The procedure is the `GUIDE-testing` record (`fyr record get GUIDE-testing`); the commands are `AGENTS.md` § Setup commands.

## Build, run and deploy

The commands are `AGENTS.md` § Setup commands; the procedures are the `DEPLOY-local` and `DEPLOY-remote` records, and a release is `AGENTS.md` § Release.

## Trace

Every component of SDD.md, its section here, and tests that verify it.

| Component | Section | Verified by |
|---|---|---|
| M1 | T1 | `TestRenderedContextUnderTarget`, `TestRenderedContextFitsAntigravityBlock` |
| M2 | T2 | `TestSkillListingBudget`, `TestLintAllowListPerKind`, `TestVendoredRowsCarryAPin` |
| M3 | T3 | `TestAgentsInstallAsWritten`, `TestKnownToolsAreTheDerivedNames` |
| M4 | T4 | `TestEmbedHoldsOnlyTrackedFiles`, `TestFilesMatchTree`, `TestEmbeddedPlanEqualsCheckout` |
| M5 | T5 | `TestHarnessMap`, `TestVocabularyVerbsAndFlags`, `TestUpdateNeverInstallsDirtyTree`, `TestUpdateFromReleaseReplacesAndInstalls`, `TestUpdateFromZipVerifiedAndTampered` |
| M6 | T6 | `TestInstallStatusResyncUninstall`, `TestLintPermissionKeysByKind`, `TestPlainCopyNeverRebuilds`, `TestUninstallKeepsMetricsStore` |
| M7 | T7 | `TestAddGetEditList`, `TestExportByteStable`, `TestLoadReplacesRowsFromAChangedFile`, `TestTwoProcessesAllocateDistinctIDs` |
| M8 | T8 | `TestSearchRanksARecordAboveAFile`, `TestAgyCorpusLeaves` |
| M9 | T9 | `TestMemoryVerb`, `TestStoreRunsNoProjectHook` |
| M10 | T10 | `TestAgyIngestShape`, `TestShowAgyOnly` |
| M11 | T11 | `TestRunIngestsMetricsFirst`, `TestPromptCarriesMetricsNumbersOnly`, `TestInstallTimerLinux` |
| M12 | T12 | `TestRedactShapes`, `TestEveryNeedleHasAShapeAndANearMiss` |
| M13 | T13 | `TestSuiteHarness`, `TestEvalRefusesItsHome` |
| M14 | T14 | `TestDocsRuleVerbs`, `TestSpecRuleStaleness`, `TestRequirementsRuleReadsPRD` |
| M15 | T15 | `TestCommandIgnoresRepositoryVariables`, `TestRegisteredCloneIsNot` |
| M16 | T16 | `TestRefusals`, `TestCookieScopedToPrefix`, `TestTokenSetsCookieAndRedirects`, `TestPagesWriteNothing`, `TestServeSecondSignal` |
| M17 | T17 | `TestBoxSession`, `TestGuardFingerprintSkipsSandboxRoots`, `TestGuardFingerprintSkipStores`, `TestBoxRemove`, `TestNoSQLiteDriver` |
| M18 | T18 | `TestWorkVerbPass`, `TestWorkVerbFail`, `TestWorkVerbRefusals`, `TestWorkVerbSignals`, `TestWorkVerbSeveral`, `TestWorkVerbKill`, `TestRunSeveralAtOnce`, `TestRunSeveralWaits`, `TestRunCountsOtherRuns`, `TestRunSeveralRefusals`, `TestRunSeveralKilled`, `TestRunDeadContextStartsNothing`, `TestRunSlotsLockBusy`, `TestLiveRunsFailClosed`, `TestHeldErrorFailsClosed`, `TestLiveRunsGlobChars`, `TestRunRootBeforeLimit`, `TestPidBounds`, `TestKill`, `TestKillRefusals`, `TestRunPass`, `TestRunRefusals`, `TestRunFailReasons`, `TestRunSafetyKeepsSandbox`, `TestBlockCutOrder`, `TestNoSQLiteDriver` |
