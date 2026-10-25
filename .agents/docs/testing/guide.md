---
owner: verifier
date: 2026-10-24
---
# Verification guide — fyr

The standing procedure for this repo. Written under the rule in `agents/verifier/agent.md`
("What to run"): the first run under a new project writes this guide; later runs cite
it and record outcomes only. Update this file in the same run that changes the steps.

## 1. Go checks (every change, including one that touches only `context/`, `skills/`, `agents/`, `commands/` or `rules/`: the root package's embed tests compare those trees with the binary's embedded copy, so a new file or mode there fails here, BUG-137)

```bash
gofmt -l cmd internal     # must print nothing; NOT `gofmt -l .` — it walks .agents/scratch,
                          # where a stray Go file fails a gate that has nothing to do with it (BUG-048)
go vet ./...
go test -race -timeout 30m ./...   # -timeout 30m: on a one-CPU box (nproc = 1) cmd/fyr takes ~13 min under -race,
                                   # past Go's 10-minute default per-package timeout (a larger machine: ~62 s)
```

The suite does not depend on the caller's environment: every test package drops `GIT_*` variables in a `TestMain` (git exports them inside a hook, and they would aim a fixture's git at another repository). A check for that, when a test helper that runs git changes: `GIT_DIR=/nonexistent go test ./...`.

## 2. Harness self-check (every change to `context/`, `agents/`, or `skills/`)

```bash
go run ./cmd/fyr list           # sanity: every skill and agent parses; its section headers print the counts
go run ./cmd/fyr render | wc -l        # rendered context/AGENTS.md must stay under 100 lines
go test ./internal/harness/... -run 'TestRenderedContextFitsAntigravityBlock|TestRenderedContextUnderTarget' -v   # a change to context/*.md
  # or rules/*.md must pass this: it renders the real checkout plus every rules/*.md and pins the
  # result under 22,000 bytes, inside Antigravity's 24,000-byte global rules block, and the hand-written part (before the generated `## Installed` inventory) under 15,000 bytes,
  # the harness's own ceiling (goal 14,500; plan audit-fix D2b) (REQUIREMENTS §
  # Known limits, ADR-035); already covered by `go test ./...` in § 1, named here explicitly
```

The installer also refuses a command or rule whose first line opens a frontmatter block (`---`, also after a byte-order mark or with spaces after it) that cannot be read: such a file was once deployed as written with its keys unchecked (`TestLintRefusesUnreadableFrontmatterInDocs`).

## 3. Scratch-home install and drift check

Never install into the operator's real `$HOME` from a dev session.

```bash
rm -rf .agents/scratch/<slug>-home
mkdir -p .agents/scratch/<slug>-home
go run ./cmd/fyr install --home .agents/scratch/<slug>-home
go run ./cmd/fyr status --home .agents/scratch/<slug>-home        # must report "drift none"
```

Confirm every `agents/<name>/agent.md` is byte-equal to its installed copy under
`.gemini/config/agents/<name>/agent.md` in the scratch home (`cmp`) when an agent
definition changed: install copies it as written (ADR-062).

## 3a. Plain-copy install (change touching checkout-vs-mirror rebuild logic)

When a change affects `IsCheckout` / rebuild-decision logic in `cmd/fyr` or
`internal/harness/repo.go`, the scratch-home tier above only exercises the checkout
path. Also build the real binary and run it against a read-only, `.git`-less copy:

```bash
go build -o .agents/scratch/<slug>/fyr ./cmd/fyr
mkdir -p .agents/scratch/<slug>/mirror
for d in context skills agents commands rules; do
  mkdir -p ".agents/scratch/<slug>/mirror/$d"
  rsync -a --exclude .git "$d/" ".agents/scratch/<slug>/mirror/$d/"
done
chmod -R a-w .agents/scratch/<slug>/mirror
.agents/scratch/<slug>/fyr install --repo .agents/scratch/<slug>/mirror \
  --home .agents/scratch/<slug>/home2   # must say "plain copy" and not rebuild
ls -a .agents/scratch/<slug>/mirror     # must show no .agents/ created
.agents/scratch/<slug>/fyr status --repo .agents/scratch/<slug>/mirror \
  --home .agents/scratch/<slug>/home2   # first line "plain copy"; drift none
chmod -R u+w .agents/scratch/<slug>/mirror   # restore before cleanup
```

Then confirm the checkout path still rebuilds with
`<built binary> install --repo <this checkout> --home .agents/scratch/<slug>/home3 --dry-run`
→ `[dry-run] the binary would be rebuilt from <checkout>`.

## 3b. The redactor (change to `internal/redact`, or to what `internal/dream` passes through it)

`report` and the feedback repository are gone (ADR-062); the redactor stays, because the
dreamer's prompt is the one thing that leaves the machine. `go test -race ./internal/redact`
runs every shape, over-redaction and benign near-miss (`TestRedactShapes`: a new needle adds
its near-miss there, project `AGENTS.md`), and `go test ./internal/dream -run 'Redact|Secret'`
the dream's use of it on feedback lines and transcripts. A new value flag registered on a
verb's FlagSet is covered by `go test ./cmd/... -run TestReorder` (reorder derives the
value-taking set from it).

## 3c. Verb × place-kind matrix (change to `cmd/fyr` or `internal/harness` that touches a verb's behavior or a refusal message)

`.agents/docs/testing/fyr-matrix.md` is the reproducible spec: every `fyr`
verb run against a checkout clone, a `git archive` plain copy, and a release-stamped
local build, each from a fresh scratch home, driven by `.agents/scratch/matrix/run.sh`
(gitignored; `git clone` of the checkout for the checkout kind, `go build ./cmd/fyr`
for the development binary both the checkout and plain-copy kinds run,
`go build -trimpath -ldflags "-X main.version=vX.Y.Y"` for the release-shaped binary,
`git archive HEAD | tar -x` for the plain copy). `update` runs with a PATH holding no
`gh`, so nothing reaches the network.

Run it, then update the matrix file's table and its "How the cells were run" line (tree
SHA and date) in the same change. Every cell is `exercised` (ran, output logged),
`refused` (a message naming the cause and the alternative), `n/a` (the verb has no
meaning there), or `remainder` (no machine here can run it) — none blank. Pass means: no
silent cell; every `refused` cell's message names both the cause and the alternative;
and `status` reports drift none from a fresh home on each of the three place kinds.

## 3d. Search index (change to `internal/search`, or to the `search`/`index` wiring in `cmd/fyr`)

```bash
go test -race -timeout 30m ./internal/search ./cmd/fyr         # unit tests: reconcile, parsing, queries, refusals, the global search index
SH_AGENT_RECALL=1 go test ./internal/search -run 'TestRecall$' -v   # recall measurement over testdata/recall.tsv: prints per-question recall and the means; not a gate
go build -o .agents/scratch/fyr ./cmd/fyr
.agents/scratch/fyr index --rebuild --home .agents/scratch/s        # this checkout: under 10 s
.agents/scratch/fyr search --home .agents/scratch/s <words>         # warm, nothing changed: under 100 ms end to end
```

A new or changed verb also gets its rows in `fyr-matrix.md` (section 3c). A test written after its code is checked by the mutation protocol (`review-standards` § 2): the mutated code compiles and a named test fails.

## 3e. Memory, dream and the timer (change to `internal/memory`, `internal/dream`, or the `memory`/`dream` wiring in `cmd/fyr`)

```bash
go test -race -timeout 30m ./internal/memory ./internal/dream ./internal/search ./cmd/fyr
```

- The unit tests put fake `agy`, `systemctl` and `launchctl` on PATH; nothing calls a real CLI or manager.
- A real `fyr dream` run in development goes against a scratch `--home` (under `.agents/scratch/`) whose search.db indexes only fixture projects — never `--all` against the real home: a real run sends redacted project content and transcripts to a model provider.
- The timer is verified by `systemd-analyze verify` on units rendered through a fake manager. The real `--install-timer` is the operator's step (R6).
- Corpus measurement of the Antigravity reader (a change to `internal/search/dream.go`): `SH_AGENT_CORPUS=<dir of copied Antigravity conversation databases> go test ./internal/search -run TestAgyCorpusLeaves -count=1 -v` prints `corpus: N databases, L prose leaves kept, K with a tool-argument key`. K must be 0; that is the invariant. L is the box's own corpus and is recorded, never compared across boxes: 971 over 95 databases on 2026-09-30 (before and after the reader change), 197 over 23 on another box on 2026-10-09. The test is a measurement with no assertion: it is read, not passed. The copies live under `.agents/scratch/` and are deleted after.
- The kit is in `testing/2026-09-30-verify-parity-memory-dream-phase-4.md`.
- The Antigravity login differs per OS and the unit tests only fake it (`TestAgyHomeMacKeychain`): a change to how `agyHome` finds the login, or to the agy command line, owes one real `fyr dream --all` on a macOS place with Antigravity before the release that carries it, and its `dream.log` line read for every project (`ok N proposals` or `unchanged`, never `not logged in` or `invalid output`), and the interactive agy still answers afterwards (`agy -p "Reply with the word OK"`): on macOS the dream job shares its Keychain login. v2.6.1 shipped a dream job that could not log in on macOS because only Linux had run it (BUG-166).

## 3g. The metrics store (change to `internal/metrics`, `internal/trail`, or the `metrics` wiring in `cmd/fyr` and `internal/dream`)

```bash
go test -race -timeout 30m ./internal/metrics ./internal/trail ./internal/dream ./internal/search ./cmd/fyr
```

- **No content in the store.** The fixtures place `SECRETCANARY123` in every field that carries content. A change to the ingester or to `Verb` keeps the canary tests (`TestAgyIngestShape`, `TestVerbDropsContent`) and re-runs their must-fail control on a copy: `Verb` patched to keep positional words must turn them red.
- **Real data, from copies only.** Copy `~/.gemini/antigravity-cli` into `.agents/scratch/metrics-home/.gemini/`. Run `fyr metrics ingest --home .agents/scratch/metrics-home` twice; the second run must add 0. Then check:
  - `metrics show --json --all-labels` `B.tool_calls_by_tool` equals an independent count of type-132 steps over the same copy;
  - every program and flag in `step_verbs` is name-shaped (spec § Normalised verb).

  Delete the copy after: it may hold secrets. Never ingest the real home in development (R6); the operator's first real ingest is theirs.
- **The report.** `metrics report --home <scratch> --out <file>` is opened in a real browser, in light and dark themes and at 400 px, with a screenshot of each under `.agents/scratch/`.

## 3f. The Google skills catalog (change under `skills/google-catalog/`, to `internal/harness/catalog_test.go`, or to the listing budget)

- The catalog tests run inside § 1's `go test ./...`: `TestCatalogMatchesChecksums`, `TestCatalogIndexMatchesEntries`, `TestCatalogFilesAreCarryable` (each fails on an empty catalog), `TestCatalogInstallsClean`, `TestRouterDescription`, `TestSkillListingBudget` (names plus descriptions of the skills the model is shown at most 19,500 bytes; a skill with `disable-model-invocation: true` is not counted, the test skips it; a new or longer skill description can therefore fail `go test ./...`, with 160 bytes of room today: 19,340 of 19,500, the `review` description being shorter than it was in v2.2.0), and `TestCatalogNotesNeverAllowQuiet` (a note that names `--quiet` says to leave it out, with no exception in that clause), and `TestRefreshCatalogScript`, which builds local fixture repositories and never reaches the network (it needs `bash`, `git` and `go`; `-short` skips it) and covers the work directory (never `/`, the home directory or the checkout; never nested with `--dest`; a non-empty one needs the script's marker `.refresh-catalog-work` or an `origins.txt`), a leading `//` in `--dest`, and the two source overrides (an `https://` URL without user information or a query string, or an absolute path; no message ever shows such a value, git's own message is not shown either, hidden on all three git steps that carry the value (pointing an existing clone at the source, fetch, clone), and the script turns git's tracing and credential prompts off for its own calls (`GIT_TERMINAL_PROMPT=0` and an empty `GIT_ASKPASS`: no terminal prompt, no askpass program; the three `GIT_TRACE2*` targets set to 0, because they can come from git's config; every other `GIT_TRACE*` variable the caller exports set to 0, except `GIT_TRACE_REDACT`, which is set to 1; `GIT_CURL_VERBOSE` removed, because set to anything it turns curl's trace on); the test asserts it four ways: `GIT_TRACE` and `GIT_TRACE2_EVENT` pointing at files that must not hold the override's value, all three `trace2` targets (`eventTarget`, `normalTarget`, `perfTarget`) in the fixture home's git config, an https fixture server that answers 401 with an askpass stub that must not run (the case also fails if git never reached the server), and the script's text for `GIT_CURL_VERBOSE` (no run shows that one: the calls' output is discarded), each failure named by the source's role; a run that used an override says when it ends that git keeps the value in the work clones under the work directory, to be deleted if the value is sensitive, printed however the run ends after the first clone or fetch (success, a refusal of the script's own, a command that stops it, or a run ended by TERM: the test signals the script's process group while a stub `go` sleeps, and wants exit 143 and the note) and not printed by a refusal before any clone; the test asserts each of these with a pin that does not resolve after the clone, a fetch that fails, a clone whose git config is locked, a destination that cannot be created, and a refused override value; a pin must be a full commit id (40 hex digits) and is refused by shape before any clone ("a pin must be a full commit id (40 hex digits)"), because git checks a tag or branch named like a shorter id out before the abbreviated commit id; the script checks the pin out as `<pin>^{commit}` and compares the checked-out commit with it, because git takes a local branch of that exact name first, and the test has a source whose default branch is named like the pin and points elsewhere (its content must not be vendored); the test refuses a tag named in hex digits, the commit's 7-digit id and its 39-digit id; and the manifest records the upstream repositories' names, never an override); the catalog tests give every subprocess an environment without `GIT_*` variables. `TestScriptsHaveNoEarlyExitPipe` fails on any tracked script under `skills/*/scripts/` or `.agents/docs/testing/` that sets `pipefail` and pipes into an early-exit consumer without ending `|| true`: it cuts each logical line at its unquoted single pipes (a command substitution is code again inside double quotes) and matches each stage: `head`, `grep -q`/`-m`/`-l` and their long forms, a `sed` script with a `q` or `Q` command, an `awk` program with `exit`, `read` (BUG-155, BUG-157).
- A refresh — `bash skills/google-catalog/scripts/refresh-catalog.sh <agents-cli pin> <google-skills pin>` (each pin a full 40-digit commit id), which needs the network — is followed by `git add skills/google-catalog && go test ./...` (an unstaged catalog fails `TestEmbedHoldsOnlyTrackedFiles` with "embedded but not tracked", the same message an ignored path gives), a read of `git diff --cached --stat -- skills/google-catalog`, and a security review of the diff before the commit (`AGENTS.md` § Rules).
- Behaviour on the CLIs is three matrix rows (`skill-matrix.md`: `catalog-no-leak`, `catalog-route`, `catalog-precedence`), run from an Antigravity sandbox home; each is judged from the run's own record (Antigravity's conversation database), not from the model's reply.

## 4. Sandbox start of the CLI (change to `context/`, `agents/`, or `skills/`)

One start of Antigravity in the sandbox (paths and flags in
`.agents/docs/testing/2026-09-03-verify-sandbox.md`), each asked a context question
that quotes the new wording, log grepped for parse errors. The full skill-matrix
scenarios are `.agents/docs/testing/skill-matrix.md` — run the matrix in full only
before saying "complete" or "all skills"; a targeted change runs only the matrix row(s)
it affects.
On a styrman box every login shell's credential broker (`/etc/profile.d/zz-styrman-credentials.sh`) writes the SSH key, gcloud ADC and the Antigravity token into `$HOME`: a run with `HOME=<sb>` starts its CLI with `STYRMAN_CRED_BROKER` unset and `STYRMAN_SKIP_CRED_PULL=1`, best from `env -i` with an allowlist, which keeps operator tokens out of the agent's environment (BUG-160 addendum). That is not isolation: the broker still answers any process that asks, and ssh reads the real `~/.ssh` (security review 2026-10-07). The committed kit (`testing/agy-rel/run.sh`, with `kit-preamble.sh`) is the worked example: it LINKS the agy token `~/.gemini/antigravity-cli/antigravity-oauth-token` instead of copying it, so a token refresh lands in the real file, check the link and the real file after the run, and keeps its homes under `~/sandbox/<kit>/homes`, outside every checkout, so a `git` run from `~` reaches no repository. A home used only to test an install, with no agent running in it, stays under `.agents/scratch/` (project `AGENTS.md`); a home an agent RUNS in lives outside every checkout, as the committed kit's do. A sandbox home that must be logged in is built under `.agents/scratch/` only when no agent runs in it (gitignored, never
embedded: `go:embed` covers only the five source directories); any credential file copied into
it from the real home is `chmod 600`, lives only for the run, and is deleted by name right
after (security review 2026-09-21). The kit preamble's exit check proves it: a run counts only
when its output holds `kit: no credentials left in KIT_HOME` (one of the exit check's three lines, § Behavioural runs), which it prints after finding no
credential file by name and no token or private key inside any file in the sandbox home, binary files included (AWS's published example key `AKIAIOSFODNN7EXAMPLE` is set aside by exact match, since an agent that reads a test holding it leaves it in its transcripts; any other match in the same file still fails); it reads through a link to a file, fails on a link to a directory and on a path it cannot read — a CLI
can write one where the kit never copied it (BUG-160). Prefer the real-home smoke checks of § 5 when the change
needs no separate home.

There is one kind of machine (Antigravity only, ADR-062), so a change is checked in one
Antigravity sandbox home, built by `fyr install --home <sb>`, with `HOME=<sb>` for the whole
run so every `fyr` call the agent makes lands there (BUG-107). The run has the agy token
and a settings file written for it (§ Behavioural runs below).

### Behavioural runs (a change to `agents/<name>/agent.md` or to a `context/` rule that changes what the main agent does)

Moved here from `AGENTS.md` § Testing on 2026-10-01 (plan audit-fix, C17); the project file points at this section.

- A change to `agents/<name>/agent.md` gets a behavioural run on Antigravity through the committed kit `testing/agy-rel/run.sh` (sourcing `kit-preamble.sh`): its dispatch rows `dispatch-bugs`, `dispatch-changelog` and `dispatch-documentation`, and `agents-security`, each in a scratch clone of a small repository, with the agent's artifact carrying something only the new definition writes, so the run proves which definition ran. An agent without a row gets one in a scratch copy of the kit under `.agents/scratch/` (one row per agent; the worked example is `testing/2026-10-24-verify-fyr-agents.md`). The dispatch text does NOT repeat the changed rule (the definition alone is under test); the run's tool-call order is read from the conversation database (`~/.gemini/antigravity-cli/conversations/<id>.db`); the result is reported as n of N runs, never "works" (BUG-083). A change to `context/` that should change what the main agent does runs the same way, with the new text installed into the sandbox home's rules file.
- The real home is never touched, except through the committed kit's credential link (`testing/agy-rel/`), where a token refresh writes the real agy token `~/.gemini/antigravity-cli/antigravity-oauth-token` on purpose. A CLI started from a scratch `HOME` cannot authenticate (credentials live in the real home), so a scratch `HOME` run is the § 4 sandbox procedure — the agy token linked by the kit — or the real-home smoke checks of § 5, never a bare `HOME=<scratch> agy -p`. A verification log states what it did not exercise; "complete" or "all skills" is said only after the full matrix in `skill-matrix.md` has run.
- **Every kit sources `kit-preamble.sh` first** (`. .agents/docs/testing/kit-preamble.sh` at the top of the kit script built in scratch, with `KIT_CLONE`, `KIT_HOME` and, when the dispatch states fixture facts, `KIT_ASSERT_GREP` (newline-separated `file:pattern`) and `KIT_ASSERT_COMMIT` (one commit SHA of 7 to 40 hex digits, never a ref name: `HEAD` or a branch would be in every clone's history; checked with `git merge-base --is-ancestor`)). The preamble is the setup proof BUG-087, 090, 091, 106, 107, 115, 116 and 123 each asked for: it asserts every fixture fact before the CLI starts, refuses a clone whose parent is a git checkout (an ancestor `AGENTS.md` would load and an agent resolved the docs root upward into this repository's trail, BUG-106), lists the real `~/.config/fyr` and `~/.config/sh-agent` (both until every place has crossed to fyr, plan fyr D29) before the run and again on exit, and FAILS the kit (exit 1) when either changed (a kit's own `--home` does not reach the agent's calls: `search` registered test clones in the real global index, BUG-107, and a `/distill` probe appended to the real feedback and retro logs, BUG-123), and puts a wrapper first on `PATH` that pins `--home "$KIT_HOME"` on every `fyr` call the agent makes. The preamble has a behaviour test, `TestKitPreambleAssertsCommit` (run by § 1's `go test ./...`; `-short` skips it): twelve values of `KIT_ASSERT_COMMIT` (a ref named in hex digits is refused: the value is resolved first and the commit's id must start with it), a home without `~/.config/fyr` (BUG-158), a run that creates, appends to or adds a file in the real `~/.config/sh-agent`, and a run that changes a name or a file's contents in the real config (the kit fails; the test's cases are a new file, a new directory and a new looping symlink through a symlinked config directory, and an append to a symlinked file). The fingerprint is exact about links: the config directory itself may be a symlink; a link that points at a regular file is read through; any other link contributes the path it points at, so re-pointing it is seen, and a link to a device or a fifo is never opened; links are not walked. It does not see a rewrite with the same bytes, a `touch` or a `chmod`, a file made and removed again before the run ends, or anything inside a directory it cannot list or inside a subdirectory that is itself a symlink. The test's cases also include a link to `/dev/zero` and a link to a fifo (each must return within 30 seconds), and a link re-pointed at another directory. The preamble's exit check is its EXIT trap and nothing else: a kit never sets an EXIT trap of its own after sourcing it and never ends in `exec <cli>`. The trap prints `kit: no processes left by the run`, `kit: real config unchanged` and `kit: no credentials left in KIT_HOME` when it ran and found nothing, and a run counts only when its output holds all three lines or a FAIL line (BUG-107, BUG-160, BUG-163; `TestKitPreambleRefusesLeftoverCredentials`, `TestKitPreambleKillsLeftoverProcesses`). A process left by the run (its cwd or executable under the clone or `KIT_HOME`, or carrying `HOME=$KIT_HOME`) gets SIGTERM and FAILs it. On a machine without `/proc` the first line reads `kit: no /proc here: …`, and the log names that gap. Because the trap sends SIGTERM under the clone and `KIT_HOME`, the preamble refuses at load (exit 2) a relative `KIT_CLONE`, and any root that resolves to `/`, the real home or a parent of it, or the harness checkout or a parent of it. A `KIT_HOME` inside the home, such as `~/sandbox/<kit>/homes/<run>`, is fine. Nobody works inside a kit's clone or home while it runs.
- **A behavioural, sandbox or real-home run is recorded so the release gate can count it:** the main agent writes its own log `testing/<date>-verify-<slug>.md` with frontmatter `owner: main` and a `Range reviewed: <base>..<head>` line reaching the head the rows ran on. The log also carries a `## Status: PASS` or `## Status: FAIL` heading and gives each row as n of N against the row's own pass criterion: `/preflight` Step 2 counts a tier only when its rows ran and passed, and a log that shows a row failing blocks the gate whatever its heading. `/preflight` Step 2 counts such a log only for tiers no subagent can run (these rows), never for a verification command, which only a log with `owner: verifier` shows as run.
- A sandbox home for headless agy gets its own `~/.gemini/antigravity-cli/settings.json`, written for the run with the fewest grants the run needs — the grant store knows `command(<name>|*)`, `read_file(*)`, `write_file(*)` and `mcp(*)` only (any other entry is skipped with `ignoring invalid allow entry` in `cli.log`), and a row that dies with "auto-denied" is diagnosed from its conversation database (`strings ~/.gemini/antigravity-cli/conversations/<id>.db | grep -o '"CommandLine":"[^"]*'`: the last command line names what was denied) before any grant is widened (BUG-141) (a `command(<name>)` grant allows every argument of that command) and the scratch clone as its only trusted workspace, and deleted by name with the copied token (or, in `testing/agy-rel/run.sh`, the linked one, after each row) — never a copy of the real home's file, which approves every tool and trusts real checkouts; a run counts only when its stderr has no "auto-denied" line and its `cli.log` no "invalid allow entry" line.
- A sandbox home is a `HOME` override, nothing more: the CLI under test keeps the operator's uid, environment, Docker socket and reachable checkouts, and rows that need `command(*)` or `--dangerously-skip-permissions` run that way. Accepted as the design on the operator's word (2026-10-01, the v2.0.0 gate's trail-slice finding, "Go with your recommendations"): the kits' guards (fixture asserts, the clone outside every checkout, the fingerprinted real config directory, the `--home` wrapper) are what keeps a run honest, not isolation, and a row that must not reach the machine is not run headless here. That acceptance covers text the harness wrote. A row that loads vendored or third-party text (the `google-catalog` rows, a newly adopted skill) runs with read-only grants — `read_file(*)` and the named read commands — never `command(*)` (the v2.1.0 gate's trail-slice finding; the Phase 0 probes of the catalog plan predate this rule).
- The must-fail control for a change to `context/` runs in a project that does not contain the harness source, never in a clone of this repository, whose own `context/` carries the new text.
- A run kit separates the RUN from the CHECK (BUG-140): the run saves the CLI's output and leaves the clone as it is; the check re-reads the saved output and the clone from disk and prints the result line, so it can be run again when a process dies or a verdict script has a bug (three result lines were lost on 2026-10-01, cause unknown; two verdicts were re-judged from saved records the same day). A count inside a kit is `grep -c … || true`, never `grep … | wc -l || echo 0` under `pipefail`.
- A headless probe of a working-tree skill passes `--setting-sources project,local`, or first shows the real home has no installed skill of that name: a user-level skill shadows the project link (plan `2026-09-29-dreamer-memory-loop.md` D15).

## 5. Real-home smoke checks (after any install into a real home)

The three checks in `.agents/docs/testing/2026-09-03-verify-real-home.md` (context
questions — including the canary word GANNET that ends the rendered context, which a
truncated file fails (BUG-085) — one skill load, one agent dispatch) from that home, on
Antigravity, before the install is called done. The Mac procedure is
`.agents/docs/testing/2026-09-04-verify-mac-real-home.md`.
Run them from a fresh clone of the demo service, never from inside a harness checkout: the
canary word is written in this repo's README and AGENTS.md, so a session started here could
answer it from the project context without the global context having loaded. On Antigravity
the answer counts only with the store check beside it (`strings` the newest
`~/.gemini/antigravity-cli/conversations/*.db`: one `<RULE[...AGENTS.md]>` block with the canary
line inside it, plus one `<RULE[...]>` block per `rules/*.md` file the harness ships (today
`critical-thinking.md`), and no `<truncated` line). A headless Antigravity check that needs tools uses the
real settings' `permissions.allow` entries; `--dangerously-skip-permissions` only inside a
throwaway clone, and never as the standing way.

After any `fyr update`, also run the deploy guides' post-update check from that
home: `hash -r` (bash) or `rehash` (zsh), then `fyr version` unqualified and
confirm it matches the version just installed, then `fyr status` and confirm it
prints no `shadowed` line (BUG-079). When the version is wrong, `type fyr` then
`which -a fyr` name which file the shell is still reaching.

The by-hand install chains (README § Install without cloning, `remote.md` § First
install) are exercised only in `.agents/docs/testing/2026-09-17-release-binary-e2e.md`,
not by any tier in this guide — an accepted e2e-only check.

## Full matrix

`.agents/docs/testing/skill-matrix.md` is the reproducible spec of every skill/agent
sandbox scenario. Cite it rather than duplicating rows here; add a row there when a
skill or agent gains new behavior worth an end-to-end check.
