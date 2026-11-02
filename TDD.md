# TDD — fyr

How each part of the harness is built and tested: one section per component of SDD.md, each pointing at the record that holds its detail.
Status: current · Last updated: 2026-11-02 · Requirements: [PRD.md](PRD.md) · Design: [SDD.md](SDD.md)

<!-- One section T1… per component M… of SDD.md — its interfaces, data, errors, configuration and dependencies —
     each a summary that points at the SPEC record holding the detail (`fyr record get SPEC-…`), never a copy of it.
     TODO.md tasks name the section they build (→ T1). Kept current by the session that changes the code, in the same
     commit. -->

## T1 — Global context and rules

Implements: M1 · Spec: none (the files are their own spec; the size limits below and ADR-035)

- **Interfaces:** `context/AGENTS.md`, then `context/dev-setup.md`, rendered by `fyr render` into one file (`~/.gemini/config/rules/AGENTS.md`), plus each `rules/*.md` file — `critical-thinking.md` (ADR-050), `session-sizing.md` (ADR-058, ADR-077), `agy-dispatch.md` (`PLAN.md` D15), `code-modularity.md` (ADR-066) — all in Antigravity's rules directory.
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

One line each; `fyr help <verb>` and the record named are the verb's full behaviour.

| Command | What it does | Spec |
|---|---|---|
| `fyr status` | What is installed and its drift; exit 1 on drift | `SPEC-fyr-delivery` |
| `fyr update` | Moves this place forward by what it is: a checkout, a release binary or a zip | `SPEC-fyr-delivery` |
| `fyr install` (or `sync`) | Deploys the harness and removes what it no longer has | `SPEC-fyr-delivery` |
| `fyr diff` | A unified diff of every drifted file | `SPEC-fyr-delivery` |
| `fyr uninstall` | Removes exactly what install wrote | `SPEC-fyr-delivery` |
| `fyr list` / `render` | The inventory; the assembled context | `SPEC-fyr-delivery` |
| `fyr record add` / `get` / `edit` / `list` / `export` / `sync` / `import` | The brain store and its on-demand `.agents/trail.jsonl` export and sync | `SPEC-fyr-record` |
| `fyr search <words>`, `fyr index` | Records first, then ranked files; consent to be read across projects | `SPEC-fyr-search` |
| `fyr memory add` / `show` | The project's memory records and their index | `SPEC-fyr-memory-loop` |
| `fyr metrics ingest` / `show` / `report` / `evals` / `export` | Session shapes and eval trials: stored, summarised, rendered | `SPEC-fyr-metrics` |
| `fyr dream --install-timer` | The hourly job that proposes from idle projects; `--remove-timer` undoes it | `SPEC-fyr-dream` |
| `fyr auto on` / `off` / `status` / `run` | The switch of the hourly idle-time run, its timer, and the run (§ T19) | `SPEC-fyr-auto` |
| `fyr check` | A read-only drift check of this checkout; exit 1 on findings | `SPEC-fyr-check` |
| `fyr serve` | A local page of this checkout and the home's stores | `SPEC-fyr-serve` |
| `fyr eval --home DIR [--runs N] [--suite-file PATH] <row>...` | Behavioural rows against the Antigravity CLI, each in a sandbox | `SPEC-fyr-eval` |
| `fyr work <kickoff-file>...`, `fyr work --kill <stamp>` | Each Kickoff as an unattended worker in a sandbox (§ T18) | `SPEC-fyr-work` |
| `fyr adopt <name>` | Copies a skill installed elsewhere into `skills/` | `fyr help adopt` |
| `fyr version` | The build version | `fyr help version` |

`fyr` and `fyr help` print the short page, one line per command; `fyr help <verb>` or `fyr <verb> --help` prints that verb's page: its synopsis, what it does, and its flags. A flag a verb does not know is a usage error, exit 2, which prints the verb's synopsis. The global flags are `--repo DIR`, the harness checkout to build and install from (by default `$FYR_REPO`, then the working directory or its parents, then the tree the last install recorded; a release binary installs the harness built into it and refuses `--repo` and `$FYR_REPO`, ADR-034; a verb that does not read it, every verb but `install`, `sync`, `status`, `diff`, `update`, `list`, `render`, `adopt`, `serve`, `eval` and `auto`, says on stderr that it ignored it and runs), and `--home DIR` (install somewhere other than `$HOME`, which the tests use); `--vendor` no longer exists and is a usage error. The full behaviour of each verb is in the `SPEC-fyr-*` records (`fyr record list --kind spec`, then `fyr record get`).

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

- **Interfaces:** `fyr record add`, `get`, `edit`, `list`, `export`, `sync` and `import`; `brain.MemoryDraft` for `fyr auto`'s validated memory drafts (`add`, `retire`, `merge`); `brain.ReadScope`, `brain.HasRecords` and `brain.LastUpdated` for read-only consumers; a project's records exported to `.agents/trail.jsonl` on demand with `fyr record export` and loaded into `brain.db` and `records_fts` on demand with `fyr record sync` (ADR-091).
- **Data:** `~/.config/fyr/brain.db`, the store of record for every project's trail records, memory and the global records (R4, R7). Migration 2 adds `provenance` (`operator`, `derived` or `unknown` on `memory` and `feedback` records; every record stored before the migration or imported from files reads as `unknown`, never `operator`).
- **Errors:** a trail line that does not parse refuses the whole file with its line number.

## T8 — Search

Implements: M8 · Spec: `SPEC-fyr-search`

- **Interfaces:** `fyr search` (records first, then files), `--refs`, `--open`, `--global`; `fyr index` and `fyr index --register`.
- **Data:** the indexes are caches, rebuilt when deleted (R4). The dream job's two read-only SQLite reads live here too: the global index's project list and Antigravity's conversation databases, both from copies.

## T9 — Memory

Implements: M9 · Spec: `SPEC-fyr-memory-loop`

- **Interfaces:** `fyr memory add [--provenance operator|derived]` (default `operator`) and `fyr memory show`, the index a session loads at start.
- **Data:** `memory` records in the brain store with `provenance`, never exported (R7).

## T10 — Metrics

Implements: M10 · Spec: `SPEC-fyr-metrics`

- **Interfaces:** `fyr metrics ingest`, `show`, `report`, `evals`, `export`; `(*Store).ProjectRoots` for M16's cross-project views.
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

Implements: M13 · Spec: `SPEC-fyr-eval`; the rows are the tracked `.agents/eval/GUIDE-eval.md`, its setups run `.agents/eval/fixture.sh` (ADR-500); the `GUIDE-eval` record only where that file is absent; or `--suite-file PATH`

- **Interfaces:** `fyr eval --home DIR [--runs N] [--suite-file PATH] <row>...` and `fyr eval --list [--suite-file PATH]`; `--runs N` (1 to 25, default 1) runs each row N times, one trial per sandbox `<stamp>/<row>/<i>`, and each trial is `PASS`, `FAIL` or `ERROR` with a cause (`internal/eval/cause.go`; a replaced sandbox is a FAIL). The suite is `GUIDE-eval`'s one table (read by `eval.ReadTrackedSuite`, at HEAD byte for byte for a run; `TestSuiteHarness` pins its rows and `TestSuiteChecks` runs each check against a good and a bad outcome, `internal/eval/suite_outcomes_test.go`) of seven columns, or eleven with `version`, `kind`, `cluster` and `expect` (`internal/eval/suite.go`, `suite_columns.go`; ADR-085), or `--suite-file PATH`'s regular file outside every git checkout (`eval.ReadSuiteFile`), and each trial stores those four.
- **Errors:** a missing `--home`, the real home, or a home inside a git checkout is refused with exit 2 before anything is written, as is `--runs` outside 1 to 25 or with `--list`, or a `--suite-file` that is empty, missing, not a regular file, over 1 MiB or inside a git checkout.
- **Data:** one `testing` record per run, titled with the graded trials' p of n, its header naming k, the model, agy, the harness and the suite's Wilson interval above a per-trial and a per-row table (`cmd/fyr/eval_record.go`, `eval_body.go`); one `trials` row per trial (`metrics.RecordTrial`, before the ingest, whether or not a session ran), and metrics under the label `eval=<row>` (or `eval=holdout/<row>` when `--suite-file` is set) with the trial's `run` on its sessions; the package imports no SQLite driver: `cmd/fyr` writes the record, the trials and the metrics rows.
- **Dependencies:** M17 runs steps 1, 2, 4, 6–12 and 14 of `SPEC-fyr-eval` § One row; eval keeps steps 3, 5, 13 and 15 and calls M17 in the spec's order, its notes still prefixed `eval: <row>`.

## T14 — Drift check

Implements: M14 · Spec: `SPEC-fyr-check`

- **Interfaces:** `fyr check`, read-only: rule (a) docs, (b) specs, (c) requirements, (d) traces (`PRD.md` → `SDD.md` → `TDD.md`).
- **Errors:** exit 1 with findings; a trail that does not parse is a refusal, never a partial run.
- **Dependencies:** the trail through `brain.ReadScope` (with `brain.ReadTrail` fallback); it imports no SQLite driver of its own.

## T15 — Trail git reads

Implements: M15 · Spec: none (internal; shared by M7–M11 and M14 so that none imports another)

- **Interfaces:** `trail.Command`, which runs git with the repository variables dropped, with `Dates`, `Log` and `DocsRoot`; `NoIndexMarkers`; the registration marker that `fyr index --register` writes. No SQLite.

## T16 — The local page

Implements: M16 · Spec: `SPEC-fyr-serve` · Decision: ADR-075, ADR-075a · Code: `internal/serve`, `cmd/fyr/verb_serve.go` (read pages built in row 8, the two actions in row 9)

- **Interfaces:** the serve verb with `--port` (default 3000) and `--home`; a printed URL holding the run's token; read routes for status, context, records, search, memory, metrics, evals, dream and the drift check (scoped to the checkout when started inside one, or serving the all-projects overview, per-project metrics, records, global search, memory and evals when started outside one); two POST actions, the index and the metrics ingest, each the verb's own function.
- **Security:** bound to `127.0.0.1`; the `Host` header checked; a cross-site or same-site `Sec-Fetch-Site` refused; a per-run session token (URL once, then a cookie scoped to a per-run path prefix, `/s/<P>`, so no other port of `127.0.0.1` receives it, ADR-075a) and a separate CSRF token in every form's body; no script, a `default-src 'none'` CSP; a project `?scope=` query parameter outside a checkout is matched against `brain.Scopes` in memory and never touches the filesystem.
- **Errors:** a refusal is 403 with no detail; a store that will not open fails only its page (or renders its error line on `/` and `/metrics` beside the status and action forms); a busy port exits 1; a POST while an action runs is 409, and a failed action is 500 with its output and `failed: <error>`.
- **Dependencies:** M7, M8, M9, M10, M11 and M14 through their functions; the status, render and check printers and the two actions passed in from M5, which registers the verb in `init()`. Stdlib `net/http`, `html/template`, `go:embed`; no SQLite import of its own.

## T17 — The sandbox

Implements: M17 · Spec: `SPEC-fyr-eval` § One row (the steps) and § Safety checks · Plan: `PLAN-2026-10-29-workers` § Interfaces

- **Interfaces:** `sandbox.New(Config)` and a `Box` whose methods are the steps (`Make`, `Clone`, `Fingerprint`, `Install`, `Settings`, `Baseline`, `Session`, `After`, `ExitChecks`), with `Intact`, `TimedOut`, the reasons and `Note`; `CheckRoot`, `OutsideCheckout` and `CheckLogin` for a verb's refusals (`CheckLogin` accepts `$HOME/Library/Keychains` on `darwin` in host mode when `TokenRel` is absent, BUG-166, while requiring a regular `TokenRel` on Linux and under `--runner docker`, and `Session`/`After`/`ExitChecks` link and unlink `KeychainsRel` through the held `os.Root` before `scanCreds`); `Root(home, verb)` is `<home>/.config/fyr/<verb>`.
- **Errors:** a failed step adds a fixed reason string and returns false; the steps after it are the caller's to skip. A timeout is `TimedOut`, never a reason.
- **Data:** writes only under its `Dir`; check c's fingerprint skips the state directory's `eval/` and `work/` (D10).
- **Integrity (P12b):** `Intact` also reads `/proc/self/mountinfo` (`mounts_linux.go`): a mount at or under the sandbox, or a change in the mounts above its paths, is `sandbox replaced`. `GitIntact` (`gitdir.go`) walks the clone's `.git` for `fyr work`. `IntegrityReasons` is the list eval's ingest and work share.
- **Dependencies:** `internal/harness` and `internal/trail`; no SQLite driver (`TestNoSQLiteDriver`), so `internal/eval` keeps none.
- **Work's additions:** `Config.Model` (agy `--model`, none when empty) and `Config.SkipStores` (check c leaves out, at the state directory's top level only, the stores and their SQLite companions (`brain.db`, `search.db` and `metrics.db`, each with `-wal`, `-shm` or `-journal`), the dream's `dream.log`, `dream.lock`, `dream-state.json`, `dream-failed`, `dream-work` and `proposals`, the temporary directories `dream-agy-home-`, `dream-agy-`, `dream-projects-`, `dream-metrics-`, `dream-brain-` and `metrics-agy-` followed by digits, and WriteAtomic's temporary files `.fyr-` followed by digits (files only). Each is a writer's exact name, never a bare prefix, so a file added later under a similar name is still watched; D45 as D64 and D70 amend it, D91; `state.json` is fingerprinted, because only an install writes it); `Box.Started` reports that a session started agy; `Box.Remove` removes an intact sandbox through the path guard a approved, its read-only directories made writable first. Eval sets neither field.
- **Docker mode (Phase 7b-1, `docker.go`):** `Config.Docker` is nil on every eval path. When set (`Image`, `Agy`, `Stamp`, `Name`, `Network`), the session alone runs in a container, and every other step stays on the host.
  - The argv is the pure `dockerArgs`: the hardening flags, the limits `--memory 8g --memory-swap 8g --cpus 2 --pids-limit 1024` and `--network <the run's>` (ADR-083), four `--mount`s (the clone and the sandbox home at their own paths, never the worker directory whose `out.txt` and `err.txt` are the host's evidence; the real token at its path, a regular file only; the linux agy read-only), and the container's environment as `-e` pairs, with `PATH=<sb>/bin:`+`ImagePath`, `GOPATH` in the home and `GOTOOLCHAIN=local`. A mount path holding `,`, `"` or a control character is refused, and so are an image ref that is empty, starts with `-` or holds a space, a network that is not `RunNetwork`'s shape (`fyr-work-<stamp>`; so never `bridge`, `host`, `none` or `container:<id>`), and an environment entry without `=`.
  - The client and every helper call run with `FyrEnv()`, the real `HOME`.
  - The fyr wrapper execs `ContainerFyr`.
  - At the timeout: `docker kill`. At the context's end: `docker stop -t <StopGrace rounded up>`. Either way the client's group is SIGKILLed StopGrace later, so a failed docker call cannot hang the wait. At the context's end the group also gets SIGTERM once `docker stop` returns, unless the client has already exited. A slow daemon can therefore stretch the wait to the helper's 30s deadline plus StopGrace.
  - Exit 125–127, or no cid file (`<name>.cid` beside the worker directory, outside every mount, and removed once read), is agy not started. The client's process group is not in step 14's sweep, because `Wait` has reaped it.
  - Check e's container half (`containersLeft`) lists the worker's labelled containers. It fails `processes left` on one that is not exited or dead, on one still listed after `rm -f`, and on any docker error (fail closed). It runs at the session's end, before `After` unlinks the token, and only when a client was started.
  - Pinned by `TestDockerArgs`, `TestHostCmdUnchanged`, `TestWrapperByMode`, `TestDockerSession`, `TestDockerNotStarted`, `TestDockerTimeout`, `TestDockerCancel`, `TestContainersLeft`, `TestSessionListsContainers` and `TestDockerTokenNotRegular`, with a fake `docker` on PATH. `ImagePath` is pinned to `docker/work.Dockerfile` by `TestImagePathPinned` and `TestImageContextAllowList`, and its tzdata guard by `TestImageZoneGuard`. `Config.Zone`, agy's `TZ` on either runner and none for eval, is pinned by `TestAgyEnvZone` (workers D144).

## T18 — The worker

Implements: M18 · Spec: `SPEC-fyr-work` · Plan: `PLAN-2026-10-29-workers` § Interfaces § `fyr work`

- **Interfaces:** `work.Run(ctx, Options, files ...string)` runs the Kickoff files, at most `Options.MaxWorkers` at once across the machine (0 is `DefaultMaxWorkers`, 4; `MaxMaxWorkers` is 8, D30), and returns their `Result`s in file order, each `Block()` being the result contract; `ParseKickoff` is a file's parse, including its one optional `Model:` line (`parseKickoff(path, auto)` also accepts a bare `Files:` line as a drafts-only scope when `Options.Auto` is true, while `ParseKickoff` and the CLI stay unchanged); a worker's model is that line's, else `Options.Model`'s, else agy's default, and `Result.Model` carries it into the result line (ADR-080); `work.DockerPreflight(Options)` checks whether docker mode can run without making a network or starting a worker; `work.Kill(home, stamp)` signals a run (D33). `fyr work` (`cmd/fyr/verb_work.go`, § T5) maps the outcomes to its exit status; the result contract is at most 39 lines per worker (D85), printed whole in file order as each block and every block before it is done (D19).
- **Slots:** each run holds a pid file `<home>/.config/fyr/work/<stamp>/pid` under an exclusive `flock` for its whole run; its one line, `<pid> <running>`, is rewritten in place. A slot is taken under `slots.lock`, never waited for, counting every other live run's `running`, so two runs never take the last slot, and given back without it; a pid file whose lock can be taken is stale and counts nothing, and one that cannot be read counts as the whole limit. The fetch, scope check, records and drafts run for one worker at a time (a mutex), so two fetches never race in the lead's `.git`.
- **The fetch (`fetch.go`, `hostlimit.go`):** step 6 runs `git upload-pack --strict` on `<clone>/.git` after `GitIntact`, through `runBounded` (`fetchLimit`, its group killed at the deadline, `maxFetchOutput` kept); on Linux under `--runner docker`, `capMemory` wraps the fetch and each branch read in `sh -c 'ulimit -v 8388608 && exec "$@"'` (skipped on `darwin`, where XNU's `sh` refuses `ulimit -v`).
- **The lead's guard (`fetch.go`, `reflog.go`):** the base and the newest reflog entry of HEAD and of the branch it points to are recorded before the stamp line; a checkout with either reflog empty is refusal 23. Before the fetch, `leadUnchanged` fails `lead checkout changed` when HEAD is not the base, when either newest entry is not the recorded one (a commit and a reset back during the run, BUG-198, or the branch moved from another worktree; the note names the ref, the count and the first new entry), or when the tree is dirty. Every git call in the lead from then on is `leadGit`: no replace refs, grafts or commit-graph, and no hooks. Pinned by `TestRunLeadReflogMoved`, `TestRunLeadNoReflog`, `TestReflogMoved`, `TestReflogBranchMovedFromWorktree`, `TestLeadGitHistoryOptions` and `TestRunSafetyKeepsSandbox`.
- **Errors:** a refusal is `Run`'s error and makes nothing; the last, refusal 21 (`models.go`), runs the host's `agy models` once, only when a Kickoff or `--model` names a model, and fails closed (an error, a timeout, an over-cap output or an empty list refuses too). A worker that never got a slot because the run ended fails `killed` without a sandbox; one whose slot count errored fails `setup failed`. A worker's failure is a FAIL reason on the `Result`: the sandbox's own reasons, plus `timed out`, `killed`, `no commits`, `no handoff`, `more than one handoff`, `changed outside its Files`, `rewrote history`, `lead checkout changed` and `branch`.
- **Data:** writes the sandbox under `<home>/.config/fyr/work/`. In the lead's checkout it writes only the branch ref and `.agents/scratch/work/<stamp>/<name>/` (when `Options.Auto` is true, `copyDrafts` also accepts `auto-drafts.jsonl`). It opens no store directly; the worker's sandbox `brain.db` is seeded before `Baseline` and exported after `ExitChecks` to read the worker's records with `encoding/json` (or decoded from the branch's `trail.jsonl` when changed on the branch; ADR-063, ADR-091).
- **Runner (Phase 7b-2, `docker.go`, `Options.Runner`, `Image`, `AgyLinux`, `Version`):** `host` (or empty) is unchanged; `docker` sets `Config.Docker` for each worker (§ T17), so only the session runs in a container. After the refusals and before the stamp line, `makeNetwork` makes the run's network, a bridge labelled `fyr.work=<stamp>` with `enable_icc=false`; when it cannot be made the run is refused, never put on the default bridge (ADR-083).
  - `preflight` refuses an unknown runner, then `gitFloor` (refusal 25) and `prepared.dockerRefusals` run after the host checks (refusals 13-20 of `SPEC-fyr-work`, before anything is made; the unknown runner is 12): a `(devel)`/`unknown` build first, before any docker call; no `docker` on PATH, a daemon that is not local (`DOCKER_HOST` or the docker context's endpoint not `unix://`), a daemon that cannot apply the limits (refusal 24, `docker info`), an `--image` ref that starts with `-` or holds a space or control character, the image missing (`docker image inspect`, never pulled) or its ID not `sha256:` and 64 hex, or holding another fyr version (`docker run --rm --pull never … fyr version`, locked down, labelled and with no network; its container removed by the label when the check fails), the linux agy (`--agy-linux`, or the host's own on linux: a regular executable ELF file of the image's architecture, amd64 or arm64), and a mount path (`sandbox.Mountable`) the `--mount` syntax cannot carry. The agy-on-PATH refusal is not made. The image is then run by its ID, so a tag moved afterwards is not what runs.
  - **The sweep (`sweepStamp`):** at a run's end `Run` removes every container labelled `fyr.work=<stamp>`, then every network with that label, and prints `work: the run's container sweep (label fyr.work=<stamp>): <n> removed, error <err>` after the result blocks, only when it removed a container or failed (the run's own network is removed every time); `Kill` of a stale stamp, or of a stamp with no pid file, does the same, when docker is on PATH and its daemon is local, and says so in its refusal; with a daemon that is not local it sweeps nothing and says that. The sweep tries every container and network whatever fails. Each worker already removed its own container at its session's end (check e, § T17), so the sweep catches one created after that check.
  - Pinned by `TestRunDockerPass`, `TestRunDockerRefusals`, `TestVersionCheckCleansUp`, `TestRunSweepQuiet`, `TestSweepContinues`, `TestBuildLinePinned`, `TestELFMachines`, `TestKillStaleSweeps`, `TestKillStaleRemoteDaemon`, `TestRunNetworkLeftIsNamed` and `TestRunPidFileFailSweepsNetwork` in `internal/work/docker_test.go` (a fake `docker` on PATH; refusal 22's no-name row among the refusals), `TestLeadZone`, `TestLeadZoneNameChecks`, `TestRunZone` and `TestRunWindowZone` (the lead's zone, workers D144), and `TestWorkVerbRefusals` in `cmd/fyr` (an unknown runner, a development build, `--image` or `--agy-linux` without `--runner docker`) and `TestWorkVerbDockerFlags` (both flags reach the run). A real daemon is Phase 7c's smoke.
- **Dependencies:** `internal/sandbox` and `internal/trail`; no SQLite driver.

## T19 — Auto

Implements: M19 · Spec: `SPEC-fyr-auto` · Plan: `PLAN-2026-10-31-1636-plan-fyr-auto-mode-idle-time-self-improvement-of` § Interfaces

Built through Phase 7: the switch, the files, the timer, the gates, the free checks, the risk rubric, the worker runner, the apply step, the learning tasks' triggers and redacted inputs, the drafts validator and writer, the eval re-runs, and the harness self-edit and eval gate.

- **Interfaces:** `auto.On(Options, Request)`, `auto.Off(Options)`, `auto.Status(Options)`, `auto.Run(Options, dryRun)` and `auto.StatusLine(Options)`; `Options` carries the home, the writer and the `Now`, `GOOS`, `Path`, `RealHome`, `Self`, `Repo`, `Version`, `Work`, `DockerPreflight`, `Apply`, `TestRunner`, `Eval`, `EvalHome`, `EvalFixture`, `HoldoutFile` and `AgyVersion` seams, and `Request.CheckHarness` is how `cmd/fyr/verb_auto.go` answers whether level `harness` has a checkout, so the package reads no harness source. A verb's failure is an `*auto.ExitError`, its text on stdout.
- **Config (`config.go`):** `Load` fails closed: one object read key by key, so a repeated key, a key in another case and anything after the value are refused, every key required, unknown keys refused, the level and thresholds validated as the flags are, read through no link, 4 KiB at most, regular files only. `save` is `harness.WriteAtomic` at 0600, whose rename replaces a link instead of following it; that is how `off` takes one out, and why `On` refuses one first. `On` writes `enabled: true` last.
- **State (`files.go`):** `auto/` is 0700 and refused as a link, and `auto.lock`, `auto.log` and `runs.json` are opened with `O_NOFOLLOW` and `O_NONBLOCK` and must be regular files. `auto.lock` is a `flock` taken without waiting. A corrupt `runs.json`, which includes a null, an unknown key and a date that is no date or is over a day ahead of the clock, stops the run and is kept, because its model count is the budget. `pruneLog` drops dated lines over 30 days old, under the lock.
- **Timer (`timer.go`):** a `dream.Timer` value; `internal/dream` renders it, installs it and refuses as for the dream, naming `fyr auto on` and `fyr auto off`; a unit path that cannot be read as a regular file is kept and refused, for the dream too. `StrictStop` makes a failed manager stop an error, after the files go; every manager call in a removal has a 30 s limit. `render*`, `UnitFor` and the dream's package functions stay, as wrappers over `dreamTimer`, so its tests are unchanged and pin its text (the plan's D20).
- **Gates (`env.go`, `gates.go`, `usage.go`, `project.go`):** every machine reading goes through the `Env` interface, whose real one is `machine`, so each gate is tested in both states. G3 and G5 fail closed; G4's failed reading is no signal, and no signal is logged and passes (R3). `parseUsage` refuses anything but `/usage`'s four tab-separated fields, every pool with both windows; `disabled` reads as under the floors. `agy` runs in `auto/`, with `HOME` the run's home.
- **Run, checks, tasks, drafts, worker, apply, eval re-runs and eval gate (`run.go`, `checks.go`, `report.go`, `rubric.go`, `kickoff.go`, `tasks.go`, `learning_input.go`, `drafts.go`, `worker.go`, `apply.go`, `eval_rerun.go`, `eval_exec.go`, `eval_gate.go`):** `sweep` reads G3–G5, then per project G6–G8 and health, with G1 and G3–G5 read again before every project after the first. Catalog 1, 4 and 5 read the tree and its trail file; catalog 2, 3 and 7 and the queued proposals run once a run, only for a real run in which a project passed G8. Past `observe`, `runModelTask` picks at most one due class per run (`pickProjectTask` and `rotatedClasses` rotate `p.LastClass` to the end across the drift, onboarding, learning and level-`harness` classes), filters its typed `Files:` entries through `AllowedFiles` (filing a `queued` `proposal` record and incrementing `OpenAsks` for any denied entry), increments `ModelRuns` and hashes `runs.json` before `work.Run`, writes the fixed-template Kickoff to `auto/kickoffs/<name>.md` (0600) and the redacted learning input (`writeLearningInput`, passing conversations, memory, proposals and metric numbers through `internal/redact`), chooses `RunnerDocker` when `work.DockerPreflight` passes and `RunnerHost (<why>)` otherwise, runs `work.Run` with `Timeout: 30m` and `Auto: true`, and after the worker verifies `auto/` subdirectories (`verifyAutoDirs`), checks `ReasonRealConfig` and `auto.json` bytes (turning `auto` off on tamper), checks `runs.json`'s SHA-256 (restoring `ModelRuns` on tamper), collects `auto-drafts.jsonl` to `auto/drafts/<name>.jsonl` while validating and writing `memory`, `feedback`, `proposal` and `finding` draft lines (`processDraftLines`, each under 4 KiB, key-by-key JSON object, checked against `internal/redact`, `retire`/`merge` scoped to the project, `provenance: derived`) and cleaning `.agents/scratch/` in the project. For `ClassEvalRerun` at level `harness`, `execEvalRerun` re-runs due `GUIDE-eval` rows (skill/agent changed since last trial at `k=3`, unstable row with `< 5` graded trials on `HEAD` at `k=5`, regression rows on CLI version change at `k=1`) in a scratch clone with eval home `~/sandbox/fyr-auto/eval-home`, re-reading G1, G3–G5 and `/usage` (G9) between rows and storing a `testing` record. For an `OutcomeApplied` class it runs `applyBranch` (`apply.go`): re-reads G1 and G3–G5, verifies the checkout snapshot (`HEAD`, branch, reflogs, clean tree), parses `# auto: test` lines from `<base>:AGENTS.md` under `## Setup commands` (filing a `queued` `proposal` record and keeping `work/<name>` when none exist), verifies `work/<name>` against the rubric and trail rules, squash-commits in a detached worktree `auto/wt-<name>` with hooks, replace refs, commit-graph, grafts and GPG signing off and no trailer, runs the marked tests and `fyr check` (when `PRD.md` is present), re-reads G1 and G3–G5 and the checkout snapshot, fast-forwards the checkout (`merge --ff-only`), deletes `work/<name>` and removes the worktree, never pushing. For `ClassHarnessEdit` (`OutcomeEvalGate`) at level `harness`, `evalGateBranch` (`eval_gate.go`) verifies the candidate branch, collects every `GUIDE-eval` row naming the changed `skills/<s>/` or `agents/<a>/`, every `kind: regression` row and every held-out row from `~/sandbox/fyr-auto/holdout.md`, runs each at `--runs 3` on the parent and candidate commits in a scratch clone (stopping at 24 eval sessions or when G1, G3–G5 or G9 fails between rows), and keeps the candidate via `applyBranch` only when no row's pass count falls, no regression row fails and every held-out row holds (marking `install pending` and never installing into a real home, D19 item 3); otherwise it keeps `work/<name>` and leaves a `queued` `proposal` record holding both scores (exit 0). `Mark` holds the hash of the last reported findings, so an unchanged set writes no record. `cmd/fyr/verb_auto_checks.go` answers what needs the binary.
- **Check c:** `auto` is in `sandboxDirs`, which match a directory by its whole top-level name, so `auto.json` stays watched (`TestFingerprintWatchesAutoConfig`).
- **Dependencies:** `internal/dream`, `internal/harness`, `internal/brain`, `internal/check`, `internal/metrics`, `internal/search`, `internal/trail`, `internal/work`, `internal/sandbox` and `internal/redact`; no SQLite driver of its own, though those import one.

## Data model

| Store | Kind | Holds | Spec |
|---|---|---|---|
| `~/.config/fyr/brain.db` | store of record | every project's trail records and the global ones (feedback, proposal, retro), memory | `SPEC-fyr-record` |
| `.agents/trail.jsonl` | on-demand export | one project's exported records, one JSON object per line (`fyr record export` / `sync`) | `SPEC-fyr-record` |
| `~/.config/fyr/metrics.db` | store of record | session shape | `SPEC-fyr-metrics` |
| `.agents/scratch/search.db`, `~/.config/fyr/search.db` | cache | the full-text indexes | `SPEC-fyr-search` |
| `~/.config/fyr/state.json` | state | the install's manifest | `SPEC-fyr-delivery` |
| `~/.config/fyr/auto.json` | state | `fyr auto`'s switch, level and thresholds; `on` and `off` write it | `SPEC-fyr-auto` |
| `~/.config/fyr/auto/` | state | `runs.json`, whose model-run count is the budget, `auto.lock`, `auto.log`, `kickoffs/`, `drafts/`, `inputs/` | `SPEC-fyr-auto` |

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
| M7 | T7 | `TestAddGetEditList`, `TestExportByteStable`, `TestLoadReplacesRowsFromAChangedFile`, `TestRecordSyncWarnsOnRedactable`, `TestTwoProcessesAllocateDistinctIDs`, `TestProvenanceMigrationSetsUnknown`, `TestProvenanceMemoryAdd`, `TestProvenanceMemoryDraftActions`, `TestProvenanceFeedbackAndRecordAdd` |
| M8 | T8 | `TestSearchRanksARecordAboveAFile`, `TestAgyCorpusLeaves` |
| M9 | T9 | `TestMemoryVerb`, `TestMemoryProvenanceFlag`, `TestStoreRunsNoProjectHook` |
| M10 | T10 | `TestAgyIngestShape`, `TestShowAgyOnly`, `TestProjectRoots` |
| M11 | T11 | `TestRunIngestsMetricsFirst`, `TestPromptCarriesMetricsNumbersOnly`, `TestInstallTimerLinux` |
| M12 | T12 | `TestRedactShapes`, `TestEveryNeedleHasAShapeAndANearMiss` |
| M13 | T13 | `TestSuiteHarness`, `TestEvalRefusesItsHome`, `TestEvalSuiteFile`, `TestRunDarwinKeychainLogin` |
| M14 | T14 | `TestDocsRuleVerbs`, `TestSpecRuleStaleness`, `TestRequirementsRuleReadsPRD`, `TestTracesRule` |
| M15 | T15 | `TestCommandIgnoresRepositoryVariables`, `TestRegisteredCloneIsNot` |
| M16 | T16 | `TestRefusals`, `TestCookieScopedToPrefix`, `TestTokenSetsCookieAndRedirects`, `TestPagesWriteNothing`, `TestServeSecondSignal`, `TestActionCSRF`, `TestActionBusy`, `TestServeActions`, `TestNoProject`, `TestOverviewAndScopedMetrics` |
| M17 | T17 | `TestBoxSession`, `TestCheckLoginMacKeychain`, `TestBoxSessionMacKeychain`, `TestGuardFingerprintSkipsSandboxRoots`, `TestGuardFingerprintSkipStores`, `TestBoxRemove`, `TestNoSQLiteDriver`, `TestDockerArgs`, `TestDockerSession`, `TestContainersLeft`, `TestAgyEnvZone`, `TestImageZoneGuard` |
| M18 | T18 | `TestWorkVerbPass`, `TestWorkVerbFail`, `TestWorkVerbRefusals`, `TestWorkVerbSignals`, `TestWorkVerbSeveral`, `TestWorkVerbKill`, `TestRunSeveralAtOnce`, `TestRunSeveralWaits`, `TestRunCountsOtherRuns`, `TestRunSeveralRefusals`, `TestRunSeveralKilled`, `TestRunDeadContextStartsNothing`, `TestRunSlotsLockBusy`, `TestLiveRunsFailClosed`, `TestHeldErrorFailsClosed`, `TestLiveRunsGlobChars`, `TestRunRootBeforeLimit`, `TestPidBounds`, `TestKill`, `TestKillRefusals`, `TestRunPass`, `TestRunWorkerWithoutTrailExport`, `TestRunRefusals`, `TestRunDarwinKeychainLogin`, `TestCapMemoryForm`, `TestRunFailReasons`, `TestRunSafetyKeepsSandbox`, `TestBlockCutOrder`, `TestNoSQLiteDriver`, `TestLeadZone`, `TestLeadZoneNameChecks`, `TestRunZone`, `TestRunWindowZone`, `TestWorkArgsUnchangedByAuto`, `TestAutoFilesLine`, `TestAutoDraftsCopy`, `TestDockerPreflight` |
| M19 | T19 | `TestAutoOnWritesTheConfigAndTheTimer`, `TestAutoOnFromOffStartsAtTheDefaults`, `TestAutoOffNeedsNoLock`, `TestAutoOffReplacesCorruptAndLink`, `TestAutoConfigCorrupt`, `TestAutoRunCorruptFailsClosed`, `TestAutoDryRunWritesNothing`, `TestAutoTimerSystemdText`, `TestAutoTimerPlistText`, `TestAutoVerbUsage`, `TestUninstallRemovesAutoTimerKeepsItsFiles`, `TestFingerprintWatchesAutoConfig`, `TestKickoffBuilder`, `TestKickoffRubricAndAsks`, `TestRunnerChoice`, `TestWorkerModelRunCountAndCap`, `TestWorkerBranchCleanup`, `TestWorkerConfigTamperDisablesAuto`, `TestWorkerStateTamperRestoredAndDraftsCollected`, `TestClassRotation`, `TestApplyHappyPath`, `TestApplyMovedCheckoutKeepsBranch`, `TestApplyRedMarkedTestKeepsBranch`, `TestApplyNoMarkedTestBecomesProposal`, `TestApplyHooksAndGPGSignOff`, `TestApplyNoPushFetchOrLsRemoteInGitLog`, `TestApplyRubricDeniedPathOnBranchKeepsBranch`, `TestApplyStep1RereadsGatesAndDraftFindings`, `TestDraftsMemoryAddRetireMerge`, `TestDraftsRetireOrMergeOtherProjectRefused`, `TestDraftsTokenShapeRefused`, `TestDraftsFeedbackProposalFindingAndLimits`, `TestTasksKickoffTemplates`, `TestTasksTriggers`, `TestTasksRedactedLearningInput`, `TestEvalRerunTriggers`, `TestEvalRerunExecAndBetweenRowsG9`, `TestEvalRerunSubprocessRunner`, `TestEvalGate` |
