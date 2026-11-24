# fyr

**`fyr`** is an open-source agentic coding harness, build controller, and zero-daemon SQLite/AST second brain for the **Antigravity CLI** (`agy`) on Linux and macOS (`Apple Silicon`).

It packages one global context (`context/AGENTS.md`, [agents.md](https://agents.md) standard), 35 skills ([Agent Skills `SKILL.md`](https://agentskills.io) standard), 7 specialized subagents (`agents/<name>/agent.md`), 5 rules (`rules/*.md`), 3 native parallel workflows (`workflows/*.py`), and a single static Go binary (`fyr`, built with `CGO_ENABLED=0` and `modernc.org/sqlite`). No background daemons, no proxies, no commit gates, no CI, and no hosted service (`PRD.md` R0–R10, MIT License in [`LICENSE`](LICENSE), security policy in [`SECURITY.md`](SECURITY.md)).

Quick links: [Workflow Guide](Workflow.md) (plan to finish, feature to finish & DoD) · [Feature Guides](#feature-guides--architecture) · [How It Works](#how-it-works) · [Quickstart](#quickstart-three-ways-in) · [Daily CLI Reference](#daily-cli-reference)

## Why it exists

Antigravity sessions start stateless and truncate global rules beyond `24,000` bytes. `fyr` solves three operational gaps without adding persistent background services:

- **Context drifts across machines.** Rules and skills tweaked on a laptop are missing on a workstation a week later. `fyr install` deploys the exact same context, skills, subagents, rules, and `< 250 ms` fail-open CLI hooks (`~/.gemini/config/hooks.json`) on every machine, and `fyr status` / `fyr diff` catch drift immediately.
- **Sessions forget.** When an `agy` conversation ends or compacts, architectural decisions, bug root causes, and multi-phase plans disappear unless recorded. `fyr` keeps a searchable SQLite + FTS5 trail (`~/.config/fyr/brain.db`) and validity-scored `semantic`/`procedural`/`episodic` memory (`project` and `--global`), backed by a pure-Go AST symbol/call graph (`.agents/scratch/search.db`) that injects Turn-0 context capsules and re-primes state after compaction.
- **A CLI cannot measure or verify itself.** `fyr metrics` and `fyr report` track session shapes, token trajectories, conversation ratings, and trigger coverage without storing prose; `fyr eval` runs behavioral suites inside OS-isolated sandboxes; and `fyr dream` / `fyr auto` consolidate memory, rate stale conversations, and propose eval-gated skill improvements across all workspaces.

## Feature Guides & Architecture

`fyr` divides its capabilities into seven focused subsystems that work together around every Antigravity CLI session. Start with the [Workflow Guide](Workflow.md) for the end-to-end engineering lifecycle (goal, turn classification, plan-to-finish, feature-to-finish, and step-by-step Definition of Done), or explore each subsystem deep-dive below for Mermaid lifecycle diagrams, internal thresholds, and collapsible flag references.

```mermaid
flowchart TB
    devEnv["You + Antigravity CLI (agy)"] --> delivery["1. Delivery & Customization\n(fyr install / update / adopt)"]
    delivery --> hooks["2. CLI Hooks & AST Graph\n(< 250 ms fail-open + search.db)"]
    hooks --> brain["3. Brain Store & Memory\n(brain.db trail + validity >= 0.40)"]
    devEnv --> workflows["4. Workflows & Build Control\n(35 skills, 7 agents, gate / land / relay)"]
    workflows --> brain
    workflows --> metrics["5. Telemetry & Reports\n(metrics.db shapes + fyr report)"]
    devEnv --> evalIso["6. Behavioural Eval & Isolation\n(Landlock / Seatbelt + fyr eval)"]
    metrics --> dreamAuto["7. Dream & Auto Mode\n(hourly timer + k=3 eval gate)"]
    brain --> dreamAuto
    evalIso --> dreamAuto
```

| Feature Guide | Components | Key CLI Verbs | What It Covers |
|---|---|---|---|
| [Delivery, Installer & Customization](features/delivery-and-customization.md) | M1–M6, M26 | `fyr install`, `fyr update`, `fyr status`, `fyr adopt`, `fyr state` | Atomic `~/.gemini/config/` deployment, three delivery paths, offline zip state bundles, stack overrides, size budgets, and allowlist lint |
| [CLI Hooks, Prompt Vetting & AST Graph](features/hooks-and-ast-graph.md) | M8, M25 | `fyr search`, `fyr index`, `fyr graph` | `< 250 ms` lifecycle hooks (`< 80 ms` warm `PreInvocation` / `< 15 ms` mid-turn dedup with parallel `<= 120 ms` slice deadline, lightweight subagent fast path & timeout fallback stamp), 9-mode intent vetter, explicit-target subtask decomposition, worker JIT capsules, `[COORDINATOR_GUARD]`, `[CROSS_FAMILY_NOTICE]`, `[SEARCH_NUDGE]`, `[KICKOFF_GUARD]`, out-of-root `read_file` bypass, SHA-256 read ledger + multi-line/pipe Bash read-chain enforcement, `[CLAMPED]` skeletons, `--include-docs`, `--include-vendored` (`fyr graph`), split `co_edit`/`co_read` coupling, and PageRank / Tarjan / Louvain AST graph slicing & export |
| [Brain Store, Trail & Memory](features/brain-and-memory.md) | M7, M15 | `fyr record`, `fyr memory`, `fyr resume` | 15 record kinds in `brain.db`, seven-step resume + fail-open `Daily Health & Auto Actions` brief, `.agents/trail.jsonl` export/sync, and validity-scored memory |
| [Workflows, Swarm & Build Control](features/workflows-and-build-control.md) | M2, M3, M14, M20, M21, M23, M27 | `fyr verify`, `fyr gate`, `fyr land`, `fyr close`, `fyr relay`, `fyr watch`, `fyr check`, `fyr models` | 35 skills, 7 subagents (incl. `builder`), 3 `run_workflow` scripts (`phase_loop.py`, `review_parallel.py`, `preflight_gate.py`), opt-in hands-off `fyr relay`, `< 50 ms` warm `fyr models --pick` (`<= 10m` cached pool quotas & `--bench`), out-of-band hang checks, and deterministic verification |
| [Telemetry, Trajectories & Reports](features/metrics-and-reports.md) | M10, M24 | `fyr metrics`, `fyr report` | Shape-only `metrics.db` (`v14` `orchestration_runs`, `background_runs.halt_reason`, `turn_vets.suggested_skills`, `trials.{policy_model,skill,arm,tokens,defects}`, `daily_digests`, informational `model_shift_audit`) ingestion, separated `ToolErrors`/`HookDenials`, dynamic subagent budget caps, calibrated `[40%..60%]` per-model context-knee trajectory anomalies, `DailyDigest` (`--daily-digest`, `"Daily Direction & Actions"` banner), `DiagnosticReport`, `--since-tag` cohorts, `Agent Roles`, Section `0. Scorecard`, `Background Autonomy`, live `Trigger coverage`, per-skill/SIM/orchestration views, and multi-section HTML/Markdown/`--json`/`--web` preview reports |
| [Behavioural Eval & OS Isolation](features/eval-and-isolation.md) | M13, M22 | `fyr eval` | Sandboxed multi-turn/ACP behavioral testing, Wilson 95% intervals, host-safety checks `a`–`e`, subagent check `f`, Landlock, and Seatbelt |
| [Dream Consolidation & Auto Mode](features/dream-and-auto.md) | M11, M12, M19 | `fyr dream`, `fyr auto` | Global binary `on \| off` `fyr auto` timer (`4h` cooldown / `--force`), all-workspace stale-conversation rating (`session_ratings`), ephemeral worktree 0d pruning, in-place `still_regressing` proposal dedup/consolidation, zero-tool `DraftsOnly` over shared `SessionDigest` (incl. dirty/non-git/`__global__` roots), gates `G1`–`G3`/`G8`/`G9`, risk rubric, and `k=3` eval-gated skill edits |

## How it works

This map is the contract for how the harness reaches a machine and for what leaves one: the dreamer's redacted prompt, the operator's `fyr eval` runs, and `fyr auto`'s isolated dreaming workers. The strict rules that keep it true are in `.agents/rules/delivery-map.md`; `TestHarnessMap` fails when the map names a command the binary does not have.

### Delivery, Release & Local Loop

```mermaid
flowchart LR
    subgraph checkoutLane["Your harness checkout"]
        edit["Edit context/, skills/, agents/, commands/, rules/"]
        ship["gofmt, vet, test -race\ncommit and push main"]
        preflight["/fyr-preflight vX.Y.Z\ntag, binaries and zip, no key"]
        retroHarness["/fyr-retro --harness\napplies the proposals queued on this machine"]
    end
    subgraph githubLane["GitHub"]
        harnessRepo[("fyr\nmain: what checkout places follow\ntags: release markers")]
        releases[("Releases\nplatform binaries + release zip + SHA256SUMS")]
    end
    subgraph placeLane["Each place"]
        checkoutPlace["Checkout\nthe normal path"]
        binaryPlace["Release binary\nharness built in, no repository"]
        zipPlace["Zip place\nrepo zip downloaded by hand, no tree kept\na development build leaves by install --repo"]
        installer["fyr install / fyr update"]
        homeFiles["~/.gemini/config\ninstalled files, no repository"]
    end
    edit --> ship
    ship -- "git push" --> harnessRepo
    ship --> preflight
    preflight -- "tag marks a version" --> harnessRepo
    preflight -- "binaries, zip and SHA256SUMS" --> releases
    harnessRepo -- "clone once, update pulls" --> checkoutPlace
    releases -- "update downloads and checks" --> binaryPlace
    harnessRepo -. "zip, downloaded by hand; update --from-zip repeats it" .-> zipPlace
    releases -. "release zip + SHA256SUMS, by hand; checked" .-> zipPlace
    checkoutPlace --> installer
    binaryPlace --> installer
    zipPlace -.-> installer
    installer -- "writes files" --> homeFiles
    homeFiles -. "~/.config/fyr/ proposals (same machine only)" .-> retroHarness
    retroHarness --> edit
    linkStyle 0,1,5,6,7,8 stroke:#0B7A74,stroke-width:2px
    linkStyle 2,3,4 stroke:#8A3A78,stroke-width:2px
    linkStyle 9,10,11,12 stroke:#3B4FB8,stroke-width:2px
    linkStyle 13,14 stroke:#A8660F,stroke-width:2px
```

### On-Machine Sessions, Dream, Eval & Auto

```mermaid
flowchart LR
    homeFiles["~/.gemini/config\ninstalled files, no repository"]
    sessions["Antigravity sessions\nfyr-feedback-log skill logs, /fyr-retro queues"]
    configDir["~/.config/fyr/\nbrain.db, metrics.db, search.db, state.json\nstays on this machine"]
    dreamer["fyr dream --all\non demand or inside fyr auto\na CLI with no tool granted"]
    evalRun["fyr eval rows\ntyped by you, each row an OS-isolated clone and HOME"]
    autoRun["fyr auto run\nsingle hourly user timer, when turned on\ndream + one OS-isolated dreaming worker: skills/, distilled skills, skills.sh asks, derived memory cards"]
    provider[("the CLI's model provider")]
    homeFiles -- "read at session start" --> sessions
    sessions -- "logs" --> configDir
    sessions -- "memory, trail, transcripts" --> dreamer
    dreamer -- "proposal records, session_ratings (metrics.db)" --> configDir
    dreamer -. "redacted prompt, no tool granted" .-> provider
    dreamer -. "one conversation per project, in /resume" .-> sessions
    evalRun -- "testing record, metrics.db" --> configDir
    evalRun -. "a sandbox's prompts and the files its agent reads, unredacted" .-> provider
    autoRun -- "report records, runs.json; on clean harness checkout, eval-gated skills/ commit" --> configDir
    autoRun -. "a dreaming worker's prompts and files, unredacted; redacted learning inputs; skills.sh asks" .-> provider
    linkStyle 0 stroke:#3B4FB8,stroke-width:2px
    linkStyle 1 stroke:#A8660F,stroke-width:2px
    linkStyle 2,3,4,5,6,7,8,9 stroke:#5B7F1E,stroke-width:2px
```

| Colour | Path | What it carries |
|---|---|---|
| Teal | **update** | a place moving forward |
| Indigo | **install** | a place writing the installed files |
| Amber | **the loop on this machine** | corrections and proposals stay in `~/.config/fyr/` and reach this repository only through `/fyr-retro --harness`, run by you on the machine that logged them (ADR-062), or an eval-gated `fyr auto` commit to this checkout's local branch (R9, ADR-090), which you push |
| Plum | **release** | `/fyr-preflight` tagging `main` and publishing the release |
| Olive | **learn** | `fyr dream` (on demand or inside `fyr auto`), `fyr eval` and `fyr auto`: on the machine, not ways in. Their dashed lines to the CLI's model provider are the things fyr sends off the machine by design: the dreamer's redacted prompt (ADR-049); the prompts of an eval sandbox you started and the files its agent reads, unredacted as in any session (ADR-065); the same for a `fyr auto` dreaming worker (`skills/`, distilled skills, `skills.sh` asks, `derived` memory cards) started by the hourly timer when you turned auto on (`fyr auto` and `fyr eval` isolate sessions under Landlock on Linux / Seatbelt on macOS, failing closed when unavailable; ADR-076, ADR-090, ADR-522, ADR-523), plus a learning worker's redacted conversations, memory, proposals and trajectory/metric digests and a missing-skill worker's search on skills.sh and the npm registry: fyr sends nothing else, but its agent runs with `command(*)` and the network, so a misled agent could send what it reads elsewhere; OS isolation keeps it off your files, not off the network. The dreamer's line to the sessions, one conversation per project that `/resume` lists, is dashed because only Linux has run it end to end; the operator's macOS run turns it solid (ADR-073). |

A dashed line is a gap: done by hand or limited to one machine. The design around the map is `SDD.md` § Deployment; what a release delivers to each kind of place is stated once, in `SPEC-fyr-delivery` § The model.

## What you get

Installing `fyr` deploys seven managed surfaces into `~/.gemini/config/` and `~/.local/bin/fyr` without writing to any vendor `settings.json` (`PRD.md` Non-goals). See [Delivery, Installer & Customization](features/delivery-and-customization.md) for allowlists and size budgets.

| Surface | Installed path & details |
|---|---|
| **Global context** | `context/AGENTS.md` + `context/dev-setup.md`, rendered and deployed as `~/.gemini/config/rules/AGENTS.md` |
| **Skills (35)** | `skills/<name>/SKILL.md` → `~/.gemini/config/skills/<name>/`; auto-loaded by description or invoked as `/name` |
| **Subagents (7)** | `agents/<name>/agent.md` → `~/.gemini/config/agents/<name>/agent.md` (`builder`, `documentation`, `dreamer`, `explorer`, `reviewer`, `security`, `verifier`) |
| **Rules (5)** | `rules/*.md` → `~/.gemini/config/rules/*.md` (`agy-dispatch`, `code-modularity`, `critical-thinking`, `fyr-features`, `session-sizing`) |
| **Workflow scripts (3)** | `workflows/*.py` → `~/.gemini/config/workflows/*.py` (`phase_loop.py`, `review_parallel.py`, `preflight_gate.py`) for native `run_workflow` parallel orchestration |
| **Lifecycle hooks** | `~/.gemini/config/hooks.json` invoking `fyr search --hook` (`session-start`, `pre-invocation`, `pre-tool-use`, `post-tool-use`, `stop`) with `< 250 ms` fail-open timeout (`< 80 ms` warm `PreInvocation`, `< 15 ms` mid-turn dedup), Bash multi-line/pipe read ledger, `[SEARCH_NUDGE]`, `[KICKOFF_GUARD]`, and Watchdog breakers (`watchdog_ring`, interactive-stdin/loop/budget guards) |
| **Binary (`fyr`)** | `~/.local/bin/fyr` + local stores under `~/.config/fyr/` (`brain.db`, `metrics.db`, `search.db`, `state.json`, `0700` mode) |

## Quickstart: Three Ways In

Ensure `~/.local/bin` is on your `PATH` and the Antigravity CLI (`agy`) is installed before choosing a delivery path. What each kind of place fetches when moving forward is defined in `SPEC-fyr-delivery` § The model.

<details>
<summary>PATH setup for non-login shells and Linux systemd user daemons</summary>

1. **Interactive & SSH shells (`~/.bashrc` / `~/.config/shells/env.sh`):** Export `$HOME/.local/bin` (and `$(go env GOPATH)/bin` when building from source) **before** any non-interactive return guard (`case $- in *i*) ;; *) return;; esac`) so non-login subshells still resolve `fyr`:
   ```bash
   export PATH="$HOME/.local/bin:/usr/local/go/bin:$PATH"
   ```
2. **Linux `systemd --user` daemon subshells (such as `antigravity-cli-daemon`):** User services and headless subshells launched by `systemd --user` do not source `~/.bashrc`. Add `~/.config/environment.d/10-local-bin.conf` (`$HOME` and `$PATH` expand per `environment.d(5)`; `%h` is only for unit files), then run `systemctl --user daemon-reload` and restart the user service:
   ```ini
   PATH=$HOME/.local/bin:$PATH
   ```

</details>

### 1. From a source checkout

Recommended when you customize skills, rules, or global context (`go` 1.24.4+ and `git` required):

```bash
git clone https://github.com/kesokaj/fyr.git ~/workspace/fyr   # or SSH: git clone git@github.com:kesokaj/fyr.git ~/workspace/fyr
cd ~/workspace/fyr
go run ./cmd/fyr install          # builds the binary, installs ~/.local/bin/fyr, deploys ~/.gemini/config/
fyr status                        # ends with "drift none"
```

Keep a checkout place current with `fyr update` (`fyr update` refuses a checkout with uncommitted local changes; `fyr update --check` only reports and exits `2` when behind).

### 2. Install without cloning

Release binaries (`fyr-linux-amd64`, `fyr-darwin-arm64`, `fyr-linux-arm64`) embed all six content directories with no git or Go toolchain required. Pin `V` from a trusted out-of-band source (not the Releases page itself) and verify against `SHA256SUMS` over HTTPS (ADR-053, ADR-515):

```bash
V=vX.Y.Z && ASSET=fyr-linux-amd64 \
&& D=$(mktemp -d) && cd "$D" \
&& curl -fsSLO "https://github.com/kesokaj/fyr/releases/download/$V/$ASSET" \
&& curl -fsSLO "https://github.com/kesokaj/fyr/releases/download/$V/SHA256SUMS" \
&& grep -qxF "# fyr $V" SHA256SUMS \
&& grep -v '^#' SHA256SUMS | sha256sum -c - --ignore-missing \
&& chmod +x "$ASSET" \
&& test "$("./$ASSET" version)" = "fyr $V" \
&& mkdir -p ~/.local/bin && mv "$ASSET" ~/.local/bin/fyr && cd ~ && rm -r "$D"
```

Then deploy the embedded harness into `~/.gemini/config/` without touching any vendor `settings.json`:

```bash
hash -r         # bash (zsh: rehash) so the shell resolves ~/.local/bin/fyr
fyr version     # verify the expected version is first on PATH (BUG-078)
fyr install     # installs the harness built into the binary
fyr status      # "source embedded vX.Y.Z" and "drift none"
```

Update a release-binary machine anytime with `fyr update`, which checks GitHub Releases over HTTPS against `SHA256SUMS` with no `gh` CLI required.

<details>
<summary>Alternative binary download methods (gh, go install, macOS browser)</summary>

With GitHub CLI (`gh`):

```bash
V=vX.Y.Z \
&& D=$(mktemp -d) && cd "$D" \
&& gh release download "$V" --repo github.com/kesokaj/fyr --clobber \
     --pattern 'fyr-linux-amd64' --pattern 'SHA256SUMS' --dir "$D" \
&& grep -qxF "# fyr $V" SHA256SUMS \
&& grep -v '^#' SHA256SUMS | sha256sum -c - --ignore-missing \
&& chmod +x fyr-linux-amd64 \
&& test "$(./fyr-linux-amd64 version)" = "fyr $V" \
&& mv fyr-linux-amd64 ~/.local/bin/fyr && cd ~ && rm -r "$D"
```

With `go install`:

```bash
GOBIN="$HOME/.local/bin" go install github.com/kesokaj/fyr/cmd/fyr@vX.Y.Z
```

From a browser download on macOS (verifying checksum and lifting quarantine):

```bash
V=vX.Y.Z \
&& ls ~/Downloads/fyr-darwin-arm64 ~/Downloads/SHA256SUMS >/dev/null \
&& D=$(mktemp -d) && mv ~/Downloads/fyr-darwin-arm64 ~/Downloads/SHA256SUMS "$D" && cd "$D" \
&& grep -qxF "# fyr $V" SHA256SUMS \
&& grep -v '^#' SHA256SUMS | shasum -a 256 -c - --ignore-missing \
&& chmod +x fyr-darwin-arm64 && { xattr -d com.apple.quarantine fyr-darwin-arm64 2>/dev/null || true; } \
&& test "$(./fyr-darwin-arm64 version)" = "fyr $V" \
&& mv fyr-darwin-arm64 ~/.local/bin/fyr && cd ~ && rm -r "$D"
```

</details>

### 3. Install from a zip (no SSH, no gh)

Download `fyr-vX.Y.Z.zip` and `SHA256SUMS` beside your `fyr` binary and run `fyr update --from-zip`:

```bash
fyr update --from-zip ~/Downloads/fyr-vX.Y.Z.zip   # verifies against SHA256SUMS when beside the zip
fyr status                                         # "installed … from zip sha256:<hex> (verified vX.Y.Z)"
```

`fyr` unpacks the zip into a temporary directory inside `~/.config/fyr/`, installs the files, and deletes the temporary directory (`SPEC-fyr-delivery` § The model).

## First 5 Minutes in Any Repository

Once `fyr install` reports `drift none`, initialize indexing and query the AST graph inside any project repository:

1. **Choose your indexing tier:** Run `fyr index` for zero-scaffold `.agents/scratch/search.db` AST/FTS5 indexing (also auto-initialized on Turn 1 by `pre-invocation`), or run `/fyr-init-project` to scaffold `AGENTS.md`, `PRD.md`, `SDD.md`, `TDD.md`, `TODO.md`, `GUIDE-testing`, and `DEPLOY-local`.
2. **Start every session with `fyr resume`:**
   ```bash
   fyr resume   # prints AGENTS.md, open records, newest handoff, TODO.md Now, open plans, recent changes, and semantic memory
   ```
3. **Query the AST graph and trail before reading files:**
   ```bash
   fyr search --slice "handlePreInvocation"   # tiered AST context capsule + linked records
   fyr search --affected "CheckAndRecordRead" # callers, importers, and blast radius
   fyr search --path "run" --to "Install"     # shortest call/import path between symbols
   fyr search --skeleton internal/search/vet.go
   fyr search --map                           # architectural God Nodes, package deps, and cycles
   fyr graph --web                            # interactive AST/package dependency graph preview
   ```
4. **Drive work through `/fyr-*` workflows:** Use `/fyr-feature` for phased plans with pre-build interviews, `/fyr-bug` for symptom-only diagnosis and red-first tests, `/fyr-review` for parallel multi-lens review, and `/fyr-handoff` (or `fyr close`) at completion — see [Workflow.md](Workflow.md) for the full lifecycle and Definition of Done.

## Second-Brain Architecture

`fyr` pairs `< 250 ms` fail-open CLI hooks with three local SQLite stores and OS-native process sandboxing so sessions stay fast, accurate, and measurable:

- **`< 250 ms` fail-open CLI hooks (`internal/search/hook.go`, `ADR-511`, `ADR-525`, `ADR-526`, `ADR-528`, `ADR-530`, `ADR-532`, `ADR-534`):** Serve `session-start` (`<= 1,500 B` capsule + fail-open `DailyAlert` from `~/.config/fyr/reports/latest-daily-digest.json`), `pre-invocation` (`< 80 ms` warm Turn-0 / `< 15 ms` mid-turn dedup with parallel `<= 90 ms` slice / `<= 50 ms` brain / `<= 25 ms` skill sub-deadlines, lightweight subagent fast path, append-only `CHECKPOINT` compaction detection, timeout fallback stamp, Git reftable fallback, bounded `CROSS JOIN` hop-1 scoping, `MATERIALIZED` FTS5 CTE, 9-mode intent vetter, 6-anchor score, `ImprovedPrompt`, dynamic installed-catalog BM25F `SkillDigests`, explicit-target deterministic `Subtasks` with `COORDINATE_SUBTASKS`, Hop-0 PageRank AST slice, on-demand `procedural`/`episodic` memory, calibrated `[40%..60%]` per-model context-knee warnings, cryptographic `=====<18-hex>=====` data fence, and zero-token `{}` on `PASS_THROUGH`), `pre-tool-use` (`[CLAMPED]` dual-gate `>= 400`-line / `>= 16 KiB` unranged read guard with `-5` `[CLAMPED]` row retention, out-of-root/unindexed `read_file` bypass, SHA-256 `read_ledger` deduplication + hardened multi-line/pipe `ParseBashFileReads` enforcement, `[SEARCH_NUDGE]` soft search nudge, `[KICKOFF_GUARD]` `ask_question` guard on autonomous Kickoff/worker turns, `invoke_subagent` / `run_workflow` (dual-root workspace + installed workflows) / `swarm` quota routing with `needsGuardedProUpgrade` and `[CROSS_FAMILY_NOTICE]`, fenced `<= 1,200 B` `[FYR_WORKER_CAPSULE]` worker prompt injection, `[COORDINATOR_GUARD]` direct-edit protection, `[INTERACTIVE_STDIN]` prompt guard, and Watchdog `[POLL_LOOP]`/`[ERROR_LOOP]`/`[SUBAGENT_BUDGET]` breakers), `post-tool-use` (`watchdog_ring` tool-error marking), and `stop` (`co_activity` + `[VERIFY_STAMP]` check), with `.agents/scratch/search.db` at `schemaVersion = "12"` (`v11 -> v12` in-place migration adding `session_meta.{last_invocation_num, last_checkpoint_offset}` while preserving all 6 behavioral tables and `-5` `[CLAMPED]` retention, camelCase/snake_case `chunks.tokens` FTS5 indexing, `refs_oid` index, `Options.IncludeDocs`, on-demand `fyr graph --include-vendored`, canonical repo-dir package paths, Go/TS resolution, Tarjan SCC, Louvain coalescing, and read-only `StructuralSignals` with `co_edit` vs `co_read` hidden-coupling split). See [CLI Hooks, Prompt Vetting & AST Graph](features/hooks-and-ast-graph.md).
- **Brain store (`~/.config/fyr/brain.db`, `internal/brain`):** Holds 15 project and global record kinds (`plan`, `review`, `testing`, `change`, `handoff`, `bug`, `adr`, `retro`, `learning`, `spec`, `guide`, `deploy`, `memory`, `feedback`, `proposal`) in `brain.db` v5 (`repo_id`, `operator` provenance guard, plus isolated global SIM optimizer ledger `sim_problems`/`sim_aliases`/`sim_attempts`/`sim_fts`) with single-open turn-record loading (`turn_records.go`), deterministic memory validity scoring (`>= 0.40`), fail-open `fyr resume` daily health brief, and on-demand `.agents/trail.jsonl` export/sync. See [Brain Store, Trail & Memory](features/brain-and-memory.md).
- **Session telemetry & trajectory (`~/.config/fyr/metrics.db`, `internal/metrics`):** Stores shape-only tool, token, `model`/`tool`/`wait`/`idle` wall-time splits, trajectory anomalies (`KNEE_EXCEEDED`, `REDUNDANT_READ`, `RETRY_LOOP`), stale-conversation `session_ratings`, post-gate `judge_verdicts`, `background_runs` (`halt_reason`), `orchestration_runs`, and `daily_digests` (`metrics.db` v14 with `orchestration_runs`, `background_runs.halt_reason`, `turn_vets.suggested_skills`, `trials.{policy_model,skill,arm,tokens,defects}`, `DailyDigest` recovery tracking & informational `model_shift_audit`, `repo_id`, `worker_model`, `f(3,28)` model IDs, and extended `turn_vets`), orchestration/quota/health signals, rate-normalized `DiagnosticReport` signals (including `pre_invocation_timeouts`, `bash_read_bypass`, `structural_regression`), `--since-tag` cohorts, `AgentRoles`, `PreInvocation` KPIs, `TopBashVerbs`, Section `0. Scorecard`, `Background Autonomy`, per-skill/SIM/orchestration sections, top `"Daily Direction & Actions"` banner, daily HTML reports, `fyr auto log`, and HTML/Markdown/`--json`/`--daily-digest`/foreground `--web` preview reports (via `internal/webpreview`) through `fyr report`. See [Telemetry, Trajectories & Reports](features/metrics-and-reports.md).
- **Sandboxed evaluation (`internal/isolate`, `internal/eval`):** Runs `fyr eval` and `fyr auto` workers inside Linux Landlock or macOS Seatbelt confinement (failing closed when unavailable) with host-safety checks `a`–`e` and subagent check `f`. See [Behavioural Eval & OS Isolation](features/eval-and-isolation.md).
- **Idle-time consolidation & dreaming (`internal/dream`, `internal/auto`):** Runs `fyr auto` as a binary `on` / `off` global switch (`--conversation-idle` default `1h`), discovers all Antigravity workspaces and conversations (including non-git directories and `search.GlobalWorkspace`) without registration, rates stale conversations in tool-free plan mode into `session_ratings` and `target: harness` proposals (`G4`–`G7` retired for dreaming), and executes eval-gated `skills/` workers on clean checkouts under `G1`–`G3`/`G8`/`G9`, a strict risk rubric, and a `k=3` behavioral eval gate (`ADR-090`, `ADR-522`, `ADR-523`, `ADR-524`). See [Dream Consolidation & Auto Mode](features/dream-and-auto.md).

## Customizing the Harness for Your Stack

`context/AGENTS.md` and `context/dev-setup.md` configure a default stack (**Go** backend, **TypeScript/Tailwind** frontend, **Google Cloud / Cloud Run** infrastructure), while project rules always override global context (`context/AGENTS.md` line 3). You can tailor `fyr` either per project (`<project>/AGENTS.md`, `.agents/rules/*.md`, `.agents/skills/<name>/SKILL.md`) or globally inside your `fyr` checkout (`context/*.md`, `rules/*.md`, `skills/*/SKILL.md`, `agents/*/agent.md`, `workflows/*.py`, or `fyr adopt`), then run `fyr install`.

To prevent Antigravity's `24,000`-byte global rules block from silently dropping instructions, `internal/harness/size_test.go` pins hand-written `context/*.md` under `13,000` bytes, always-on context plus `always_on` rules under `9,000` bytes, repository `AGENTS.md` under `4,500` bytes, and the combined deployed rules block under `19,500` bytes (`PRD.md` `22,000`-byte cap), ending the rendered block with the canary word `GANNET` so truncation is immediately visible. Global rules also enforce that temporary/scratch files belong in `.agents/scratch/` and never `/tmp`, and commits carry no co-author trailer (`check-trailers.sh`). See [Delivery, Installer & Customization](features/delivery-and-customization.md) for frontmatter allowlists and `knownTools` rules.

## Daily CLI Reference

Use these ten commands in daily workflows, and expand the full reference below for all 30 primary `fyr` commands (31 registered verbs including the `sync` alias; leading global flags `-C`/`--dir`/`--home`/`--repo` work before any verb, all `31/31` verbs support `--json`, `fyr help <verb>` prints full flags, and `SPEC-fyr-*` holds complete contracts).

| Command | What it does |
|---|---|
| `fyr install` | Deploy context, skills, agents, rules, workflows, and hooks to `~/.gemini/config/` |
| `fyr update` | Move a checkout, release binary, or `--from-zip` place forward |
| `fyr status` | Check installed version, drift, auto/dream state, and PATH shadowing |
| `fyr resume` | Print seven-step read-only session resume (`AGENTS.md`, plans, handoff, changes, memory) |
| `fyr search <words>` | Search `brain.db` + FTS5/AST graph (`--slice`, `--contracts`, `--skeleton`, `--path`, `--affected`, `--map`, `--include-docs`, `--vet`, `--hook`) |
| `fyr record add` / `get` / `edit` / `list` | Manage project and `--global` trail records (`export` / `sync` for `.agents/trail.jsonl`) |
| `fyr memory add` / `show` | Save or view validity-scored `semantic`, `procedural`, and `episodic` memories |
| `fyr verify` | Run `gofmt`, `go vet ./...`, `go test -race ./...`, and `fyr check`, writing `.agents/scratch/verify.stamp` |
| `fyr report` | Render the `Daily Direction & Actions` banner, Scorecard, `--since-tag` cohorts, `Agent Roles`, diagnostics, `Background Autonomy`, `Trigger coverage`, and `--daily-digest` in HTML, `--md`, `--json`, or `--web` |
| `fyr auto on` / `off` / `status` / `run` / `log` / `sim` | Global binary on/off switch, log viewer, and SIM ledger CLI for hourly stale-conversation rating, dreaming, daily HTML reports, and eval-gated skill improvement |

<details>
<summary>Complete CLI command reference (30 primary commands, 31 verbs with sync)</summary>

| Command | What it does |
|---|---|
| `fyr status` | What is installed, from which commit, and any drift; exit 1 on drift |
| `fyr update` | Moves this place forward: a checkout pulls, a release binary downloads the newest release, a zip place passes the zip with `--from-zip` |
| `fyr install` (or `sync`) | Deploys the harness and removes what the repository no longer has |
| `fyr diff` | A unified diff of every drifted file |
| `fyr uninstall` | Removes exactly what it installed plus `fyr auto`'s timer and any legacy dream timer; the metrics store and `fyr auto`'s config and state stay |
| `fyr list` | Prints the skill, subagent, rule, and workflow script inventory |
| `fyr render` | Prints the assembled `AGENTS.md` context file as `fyr install` deploys it |
| `fyr record add` / `get` / `edit` / `list` / `export` / `sync` / `import` | The brain store: this project's trail records and the global ones (`export` writes `.agents/trail.jsonl` on demand; `sync` loads it into `brain.db`) |
| `fyr resume` | Prints this checkout's seven-step session resume (`AGENTS.md`, open records, newest handoff, `TODO.md` Now, open plans, recent changes, memory) in one read-only call |
| `fyr search <words>` | Matching records first, then ranked files (`--slice`, `--contracts`, `--skeleton`, `--path`, `--to`, `--affected`, `--map`, `--refs`, `--open`, `--include-docs`, `--vet`, `--hook`) |
| `fyr index [--rebuild] [--register]` | Reconciles or clean-rebuilds `.agents/scratch/search.db` (and `~/.config/fyr/search.db` with `--global`); `--register` opts the project into `fyr search --global`, `fyr record import --global`, and the metrics join (skipping ephemeral worktree/smoke/sandbox/scratch paths) |
| `fyr graph [--out FILE] [--force] [--dot] [--md] [--json] [--web] [--listen ADDR] [--include-tests] [--include-docs] [--include-vendored]` | Exports the AST package/symbol dependency graph, Tarjan SCCs, Louvain communities, split `co_edit`/`co_read` structural signals, and optional in-memory vendored skill nodes (`--include-vendored`) as DOT/Markdown/JSON or serves a foreground `--web` preview |
| `fyr memory add` / `show [--limit N]` | Project and `--global` memory (`semantic`, `procedural`, `episodic`), validity-scored (`--include-low` shows `< 0.40`, `--limit` caps rows) |
| `fyr metrics ingest` / `show` / `report` / `evals` / `trajectory` / `export` / `record-run` | The shape of every session: stored, summarised, charted, trended (`trajectory`), backed up, and orchestration runs recorded (`record-run --json-in`) |
| `fyr report [--project [DIR] \| --global] [--include-sandboxes] [--no-ingest] [--since YYYY-MM-DD \| --since-tag TAG] [--until YYYY-MM-DD] [--daily-digest] [--md \| --json \| --web [--listen ADDR]] [--out FILE]` | Incrementally auto-ingests by default (`--no-ingest` opts out) and renders the project or global usage report (`Daily Direction & Actions` banner, `0. Scorecard`, `1. Executive Pulse`, `2. Second-Brain Leverage`, `3. Quality & Learning Loop`, `4. Autonomy & Model Routing`, `Background Autonomy`, `Trigger coverage`, per-skill/SIM/orchestration views, `5. Per-Project / Project Breakdown`, `Agent Roles`, `Tag Cohort`, or `--daily-digest`) as HTML, Markdown (`--md`), JSON (`--json`), or foreground `--web` preview |
| `fyr dream --all` / `--project DIR` | One-shot dreamer (also invoked hourly by `fyr auto run`): discovers git, non-git, and `__global__` workspaces without registration, rates stale conversations (`session_ratings`), and proposes memory, trail, and skill improvements in plan mode with zero tools granted |
| `fyr auto on` / `off` / `status` / `run` / `log [--limit N]` / `sim` / `--dry-run` | Single hourly background runner with global binary switch (`--conversation-idle D` defaults to `1h`; `on`/`off` also take `--dry-run` and retire any legacy `fyr-dream` timer), `log` viewer (`--lines`/`--limit`), and `sim` (`list`/`show`/`record`/`merge`) optimizer ledger CLI, running `dream`, rating stale conversations, writing daily HTML reports, free checkout checks, and OS-isolated dreaming workers with `k=3` eval gates |
| `fyr check` | Read-only drift and dual-track docs harness check (`docs-style`, root `AGENTS.md` budget, `spec-structure`, specs, `PRD.md`, `SDD.md`/`TDD.md` traces, CLI source-AST/undocumented flags, context/rules byte budgets, skill frontmatter, and advisory `docs-style-note`) |
| `fyr git-check` | Runs the three post-dispatch git checks (`git status --short --untracked-files=all`, `git log --branches --not --remotes --oneline`, `git tag`) in one read-only call |
| `fyr watch --check [--conv ID] [--idle-sec N] [--json]` | Out-of-band `< 25 ms` read-only hang, interactive-stdin, stale-artifact, and subagent-budget check (`[INTERACTIVE_WAIT]`, `[STUCK_NO_PROGRESS]`, `[BUDGET_NEAR_LIMIT]`) |
| `fyr verify` | Runs `gofmt`, `go vet ./...`, `go test -race ./...`, and `fyr check`, guarding against mid-run tree mutation and writing `.agents/scratch/verify.stamp` (`0600`) |
| `fyr gate [--base REF] [--lease GLOB] [--lease-file FILE] [--max-files N] [--max-lines N] [--judge TEXT] [--judge-model ID] [--worker-model ID] [--worker-session ID] <worktree>` | Deterministic worktree gate: `Files:` lease globs, symlink/gitlink modes, secret filenames, numstat caps, added-line secret scan, commit trailers, and optional cross-family `--judge` verdict |
| `fyr land [--lease GLOB] [--lease-file FILE] [--message TEXT] [--max-files N] [--max-lines N] [--timeout D] [--judge TEXT] [--judge-model ID] [--worker-model ID] [--worker-session ID] <branch>` | Gates `<branch>` (plus optional `--judge` verdict), squash-merges and runs `fyr verify` in a detached scratch worktree, fast-forwards the clean base branch, deletes `<branch>`, and refreshes `verify.stamp` |
| `fyr close --title TEXT [--body-file FILE]` | Stores an immutable `handoff` record (reading `--body-file` or fail-fast bounded `<= 1 MiB` piped stdin) embedding `## Verification Digest` from a fresh `verify.stamp` without mutating or deleting prior records |
| `fyr relay --plan PLAN-ID [--max-phases N] [--judge-model ID] [--dry-run] [--json]` | Opt-in hands-off fresh-session phase runner executing one isolated `agy -p` session per ready plan phase with deterministic pre/post checks, `fyr verify`, cross-family judge, anti-stuck governor, and immutable `handoff` checkpoints |
| `fyr models [--explain [ID]] [--json] [--pick ROLE] [--caller-family F] [--refresh] [--tier T] [--bench [--model ID] [--max N]]` | Lists the live `agy models` + `agy changelog` catalog cached in `~/.config/fyr/models.json`, explains model profiles (`--explain [ID]`), probes new models (`--bench`), and resolves `< 50 ms` warm role-partitioned Pareto + conservative UCB picks across 10 roles using quota-adaptive cached pool quotas |
| `fyr state export [--out FILE] [--repo ID|current] [--include-memory] [--since YYYY-MM-DD] [--json]` / `import FILE [--repo ID|current] [--include-memory] [--dry-run] [--json]` | Offline SHA-256-verified zip bundle export/import of project trail and sanitized metrics (`--include-memory` opt-in, `ADR-531`) |
| `fyr eval --home DIR [--model ID] [--runs N] [--parallel N] [--runner isolate] [--acp] [--suite-file PATH] [--json] <row>...` | Runs behavioral evaluation rows against `agy` (optionally pinning a policy `--model ID`) inside OS-native `isolate` sandboxes (or `--acp` sessions) and stores trials in `metrics.db` |
| `fyr adopt <name> [--json]` | Copies a skill installed elsewhere into `skills/` |
| `fyr version [--json]` | Prints the build version |

</details>

## Skills & Subagents

`fyr list` prints every skill, subagent, rule, and workflow script installed from the harness.

### Skills (35)

| Group | Skills |
|---|---|
| **Work protocols** | `fyr-feature` (phased plan, cold-reader check, checkpoints), `fyr-swarm` (native parallel swarm routing, `Files:` leases, and `fyr gate`/`fyr land`), `fyr-bug` (diagnose, red-first test, smallest fix, bug record), `fyr-triage` (GitHub issue/PR and local queue triage into Agent Briefs or `/fyr-swarm` Kickoffs), `fyr-review` (`--code --security --simplify --docs --prompts --tests --all --resume --judge --fix`), `review-standards`, `fyr-preflight` (release gate & publisher), `fyr-harness-audit`, `fyr-auto` (worker contract for `fyr auto`) |
| **Project shape & specs** | `fyr-init-project` (full scaffold + trail + index), `docs-ste100-80` (ASD-STE100 ~80% Markdown/HTML docs: active prose, direct steps, safety callouts, BLUF, diagrams & `<details>`), `fyr-requirements`, `fyr-spec`, `testing-strategy`, `fyr-research`, `fyr-handoff` (session checkpoint + Kickoff block) |
| **Learning loop** | `fyr-feedback-log`, `fyr-project-memory`, `fyr-retro`, `fyr-dream` |
| **Code & UI** | `golang`, `typescript`, `frontend-ui-modern` (shadcn/ui + Tailwind + React), `frontend-ui-economist` (editorial HTML/CSS/SVG), `frontend-slate-ui` (Angular 20 SlateUI components), `bash` (incl. interactive setup wizards), `json-api-style`, `docker`, `mermaid-syntax` |
| **Google Cloud** | `gcloud-setup`, `cloud-run-deploy`, `terraform`, `operate-gcp`, `data-gcp`, `api-auth-gcp` |
| **Vendored & forks** | `fyr-triage` (forked from `triage`), `frontend-slate-ui`, `bash` wizard; provenance in `skills/VENDORED.md` |

### Subagents (7)

| Subagent | Default Model | Role |
|---|---|---|
| `builder` | inherit | Implements one write-leased slice in an isolated `workspace="share"` worktree: searches first, writes red-first tests, commits on `work/<slice>` with zero trailers, and detaches `HEAD` |
| `explorer` | `flash` | Read-only codebase reconnaissance (10+ files) drafted under `.agents/scratch/agents/` and stored as a `plan` record |
| `reviewer` | inherit | Independent severity-ranked code, docs, and prompt review drafted under `.agents/scratch/agents/` (`review` record) |
| `security` | inherit | Defensive security audit of diffs touching auth, crypto, secrets, IAM, input parsing, frontmatter allowlists, or dependencies (inherits session model) |
| `verifier` | inherit | Runs browser e2e, container build/smoke, and live local HTTP smoke (`fyr verify` runs unit, vet, and `fyr check`) on the live tree with its Boundary Contract Checklist and drafts a `testing` record |
| `documentation` | `flash` | Maintains living docs (`README.md`, `SDD.md`, `TDD.md`, `SPEC-*`, `GUIDE-*`, `DEPLOY-*`) when changes exceed 30 lines |
| `dreamer` | inherit | Consolidation judgement for `/fyr-retro`, proposing `skills/` edits, new skill drafts, and memory card consolidation (project + `--global`, validity `< 0.40`, using trajectory digest) |

## Repository Layout

The repository separates immutable requirements, architecture, and verification traces (`PRD.md` → `SDD.md` → `TDD.md` → `TODO.md`) from the six embedded harness directories and per-feature documentation (`features/`).

```
PRD.md        what the harness is for and what must never drift (R0–R10)
SDD.md        how the parts fit: components M1–M27, the main flows, the decisions
TDD.md        how each part is built: sections T1–T27 and their tests
TODO.md       Now, Next and Backlog; the active plan record wins
Workflow.md   end-to-end engineering workflow (goal, plan-to-finish, feature-to-finish, DoD)
AGENTS.md     the rules of this repository, for agents
LICENSE       MIT License
SECURITY.md   vulnerability reporting and security boundary overview
features/     deep-dive feature guides with Mermaid architecture and lifecycle diagrams
context/      AGENTS.md (the global context) and dev-setup.md (environment, stack, GCP defaults)
agents/       one directory per subagent (agent.md, in Antigravity's format, installed as written)
skills/       one directory per skill (SKILL.md, references/, assets/)
rules/        always-on or path-scoped rules
commands/     markdown prompts invoked as /name (empty; prefer skills)
workflows/    native run_workflow scripts (phase_loop.py, review_parallel.py, preflight_gate.py)
cmd/fyr       the binary; internal/ its packages, each named in AGENTS.md § Layout
embed.go      the go:embed of the six content directories, pinned to what git tracks
.agents/      this repository's .agents/skills symlink, .agents/eval/ suite, and .agents/scratch/ (gitignored)
```

```mermaid
flowchart LR
  prd["PRD.md\nrequirements R0 to R10"] --> sdd["SDD.md\ncomponents M1 to M27"]
  sdd --> tdd["TDD.md\nsections T1 to T27 and their tests"]
  tdd --> todo["TODO.md\ntasks name a T section"]
  tdd --> records[("SPEC and ADR records\nthe detail")]
```

## Working on the Harness

Run formatting, static analysis, the race-enabled Go test suite, and scratch-home verification before claiming completion (`AGENTS.md` § Setup commands):

```bash
gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./... # auto: test
go run ./cmd/fyr install --home .agents/scratch/home   # exercise installer in scratch home
go run ./cmd/fyr list   # sanity-check skills and agents parse
go mod download   # cache the one module (R4) for offline builds
"$(go env GOPATH)/bin/govulncheck" ./...   # watch for NEW findings (known: DEPLOY-local § Operator steps)
```

Every stateful `fyr` command run during development must pass `--home .agents/scratch/<name>` (`cmd/fyr/realhome_guard.go`, `ADR-505`), and `internal/harness/size_test.go` must pass without raising any byte ceiling.

Where to read more: [`Workflow.md`](Workflow.md) for the end-to-end engineering workflow and DoD; [`AGENTS.md`](AGENTS.md) for repository rules; [`PRD.md`](PRD.md), [`SDD.md`](SDD.md), and [`TDD.md`](TDD.md) for requirements, architecture, and test pins; [`SECURITY.md`](SECURITY.md) for security boundaries; [Feature Guides](features/delivery-and-customization.md) for subsystem deep dives; and `fyr record list --kind spec` / `fyr record list --kind adr` for surface specs and architecture decisions.
