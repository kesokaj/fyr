# Idle-Time Consolidation (`fyr dream`) & Self-Improvement (`fyr auto`)

`fyr dream` and `fyr auto` evaluate stale Antigravity conversations, consolidate memories in tool-free plan mode, check checkout health, and run eval-gated skill improvements across your entire `agy` setup behind a single binary `on` / `off` switch and OS-native confinement (`ADR-524`, `ADR-523`).

## Global switch and hourly sweep (`fyr auto` and `fyr dream`)

`fyr auto on` writes `~/.config/fyr/auto.json`, installs the single hourly OS user timer (`fyr-auto` / `com.fyr.auto`), and retires any legacy `fyr-dream` unit (`internal/auto/config.go`, `internal/auto/verbs.go`, `cmd/fyr/verb_auto.go`). It accepts an optional `--conversation-idle D` threshold (default `1h`, range `5m` to `7d`) and migrates legacy `level`, `project_idle`, and `user_idle` configs cleanly while treating `--level`, `--project-idle`, and `--user-idle` as deprecated no-op flags with a warning. `fyr auto status [--json]` (`internal/auto/status.go`) prints the switch, idle cutoff, daily budget, open asks, and an `installPending` banner (`installPendingBanner`) whenever eval-gated `skills/` commits await `fyr install`; `fyr auto log [--lines N | --limit N | --all] [--json]` (`internal/auto/log_read.go`) tails `~/.config/fyr/auto/auto.log` through `O_NOFOLLOW` (default `50` lines; prints `no auto log yet` and exits `0` when absent). Headless timer invocations resolve `agy`/`fyr` across `PATH`, sibling binary directories, and `~/.local/bin` via `dream.ResolveBinary` (`internal/dream/agy_home.go`).

```mermaid
flowchart TD
    timerTick["Hourly user timer (fyr auto run)"] --> g1Switch{"G1 switch on &\nG2 lock free?"}
    g1Switch -- "No" --> stopEarly["Log gate & exit cleanly"]
    g1Switch -- "Yes" --> ingestStep["1. Run fyr metrics ingest"]
    ingestStep --> dreamStep["2. Run fyr dream --all\n(discover all workspaces; sliding window)"]
    dreamStep --> rateStep["3. Redact & rate stale conversations\n(plan mode, zero tools -> session_ratings)"]
    rateStep --> machineGates{"4. G3 AC power & per-pool G9 quota\n(>= 50%/30%, < 4 runs/day)?"}
    machineGates -- "No" --> freeChecks["Run free git checkout checks only"]
    machineGates -- "Yes" --> workerGate{"Clean git checkout\n(G8 clean)?"}
    workerGate -- "No (non-git / global / dirty)" --> keepProposals["Skip worker; keep ratings & proposals"]
    workerGate -- "Yes" --> runWorker["5. Spawn 1 OS-isolated dreaming worker\n(30m budget via internal/isolate)"]
    freeChecks --> finishStep["6. Record background_runs (metrics.db schema 14)\n& write daily HTML report (YYYY-MM-DD-daily-metrics.html)"]
    keepProposals --> finishStep
    runWorker --> finishStep
```

During step 2, `fyr dream --all` (`internal/dream/targets.go`, `internal/search/dream.go`) discovers every Antigravity workspace and conversation without requiring `fyr index --register` (`G6` retired) or pre-existing trail records:

- **Workspace sources & shared ephemeral/refused-root 0d pruning:** Reads `~/.gemini/antigravity-cli/conversation_summaries.db` (`workspace_uris`, via `search.ListAgyWorkspaces`), `brain.db` scopes (`brain.Scopes`), `search.db` projects (`search.ListProjects`), and the resolved harness checkout (`HarnessRepo`), while excluding refused system roots (`/`, `/tmp`, `/private/tmp`, exact `$HOME`) and ephemeral worktree, smoke, eval/auto/dream sandbox, and `.agents/scratch` paths (`dream.IsEphemeralPath` / `dream.IsRefusedWorkspace` in `internal/dream/ephemeral.go`, delegated by `auto.IsEphemeralPath`, pruned at `0d` in `runs.json` and skipped by `fyr index --register`; `isSandboxProjectRoot` in `internal/metrics/project_roots.go` also filters sandbox/probe roots from metrics and diagnostics).
- **Non-git, global & remote-control sessions:** Empty `workspace_uris` map to `search.GlobalWorkspace` (`"__global__"`), non-git directories are retained as first-class workspaces alongside git checkouts (`internal/search/dream_workspace.go`), and `internal/dream/agy_process.go` recognizes `agy remote-control serve` as an active interactive session.
- **Sliding-window staleness `(since, cutoff]`:** Under `fyr auto`, `dream.Options` passes `ConversationIdle` (`1h` default) and `Session: true`. `search.AgyConversationTextInWindow` evaluates every conversation modified in `(since, now - conversation_idle]` even when another `agy` session is active (`G4`, `G5`, and `G7` retired for dreaming), advancing `st.Projects[root]` only to the latest evaluated timestamp capped at `cutoff` (`watermark`) so active conversations are re-checked once stale. Failed or incomplete runs keep a `24h` retry cooldown (`FailedAttempts`).

## Stale-conversation rating, diagnostics, and `metrics.db` schema 14

`fyr dream` scrubs conversations, optional `PLAN-*`/`HANDOFF-*` context (`internal/dream/collect_sources.go`), memories, trail records, global `feedback`, bounded `kindSIM` prompt slices (fitted alongside oversized transcripts via `trimSIMBlock` in `internal/dream/collect_fit.go`), numbers-only `DreamTrajectoryDigest` metrics, `DiagnosticReport` signals, and redacted project-local hidden-coupling hints through `internal/redact`, invokes `agy --input-format stream-json --output-format stream-json --mode plan -p ""` granting zero tools, and records structured conversation ratings alongside global `queued` `proposal` records.

- **Trajectory knee & diagnostic signal feeds:** `DreamTrajectoryDigest.KneeExceeded` (`internal/metrics/dream_digest.go`) counts distinct label-free sessions reaching their calibrated `[40%..60%]` effective context knee (`models.ContextKneeWithOverrides` / `models.CalibratedContextKnee`, capped at `180,000`/`300,000` degradation onset) or recording a `knee_exceeded` event, while `unaddressedDiagnosticSignals` (`internal/auto/diagnostics.go`) triggers `ClassRetroLite` on unaddressed high/medium `DiagnosticReport` signals and injects a redacted `## Diagnostic signals` block into worker prompts.
- **`session_ratings` & `background_runs` (`metrics.db` schema 14, `internal/metrics/store.go`, `internal/metrics/ratings.go`, `internal/metrics/background_runs.go`):** Migration 8 adds `session_ratings` (`session_id`, `project_root`, `evaluated_at`, `outcome_score` `1–5`, `token_efficiency` `1–5`, `plan_alignment` `matched|partial|missed|no-plan`, `skill_action` `none|rewrite-skill|new-skill`, `target_skill`, `summary`, plus `repo_id` in migration 11) written via `dream.Options.WriteRatings`, and migration 10 + migration 14 add `background_runs` (with `halt_reason`, written via `RecordBackgroundRun`) recording `dream` and `auto` run outcomes, halt reasons, duration, projects checked, proposals/ratings written, and worker/applied flags.
- **Reporting, daily HTML summary & proposal routing:** `fyr report` surfaces `session_ratings` in Section 3 (`Quality & Learning Loop`) and `background_runs` in `Background Autonomy`, while `autoWriteDailyReport` (`cmd/fyr/verb_auto.go`) calls `metrics.WriteDailyReport` after sweeps (`internal/auto/run.go`) to write `~/.config/fyr/reports/YYYY-MM-DD-daily-metrics.html` (`0600`, `O_EXCL`, quiet no-op if today's file exists). Whenever `skill_action` is `rewrite-skill` or `new-skill`, or either score is `<= 3`, `internal/dream/proposals_write.go` (`needsSkillProposal`) files a global `queued` `proposal` (`Target: harness   Kind: skill`, with `Sessions: <n>` set dynamically from the distinct rated session count) naming `target_skill` (or `skills/`) and the conversation rating evidence so `fyr auto` routes it directly into `harness-edit` or `distill` when a clean harness checkout exists.

## Dreaming worker classes and risk rubric

Whenever `fyr auto` is `on` (`internal/auto/run.go`, `internal/auto/worker.go`, `internal/auto/tasks.go`), `fyr auto run` runs at most one dreaming task per sweep inside an `internal/isolate` sandbox (`30`-minute timeout, maximum `4` model runs per day with a `4h` cooldown unless `--force` is passed). Zero-tool `DraftsOnly` classes (`memory-consolidation`, `retro-lite`, `memory-candidates`, `missing-skill`) run over shared `SessionDigest` summaries (`session_digest.go`, with self-log filtering and oversized transcript chunking in `collect_fit.go`) even on dirty checkouts, non-git directories, and `__global__` roots (`search.GlobalWorkspace`), pass an explicit `--model`, drain queued proposals by priority while expiring stale `"rejected"` items (`eval_proposals.go`), deduplicate recurring `still_regressing` proposals in place and TTL-retire stale ones after `14d` via `brain.Edit` without deleting rows (`proposal_dedupe.go`, `proposal_consolidate.go`), and auto-seed `holdout.md` under `o.Home` from `.agents/eval/holdout-starter.md`. Code/branch tasks still require a clean git checkout (`G8`). Every worker writes structured drafts to `.agents/scratch/agents/auto-drafts.jsonl` (`provenance: derived`), validated and committed to `brain.db` by Go (`internal/auto/drafts.go`)—never by the worker directly.

| Scope | Permitted Classes & Outcomes | Checkout Edits? |
|---|---|---|
| Clean consumer git checkout | `memory-consolidation`, `retro-lite`, `memory-candidates`, `distill` (`.agents/skills/auto-distilled-<slug>/` proposal), `missing-skill` | Zero consumer-repo edits (`OutcomeDrafts` / `OutcomeProposal`) |
| Clean harness checkout (`o.Repo`) | All classes above plus `harness-edit` (`skills/`) and harness `distill` (`skills/auto-distilled-<slug>/`) | Local `work/auto-*` branch only, behind the `k=3` eval gate (`OutcomeEvalGate`) |

The risk rubric (`internal/auto/rubric.go`, mirrored in `skills/fyr-auto/SKILL.md`) enforces hard boundaries so automated workers can never modify their own judges, guards, or instructions outside `skills/`:

- **Always denied paths:** `PRD.md`, `AGENTS.md`, `context/`, `rules/`, `.agents/rules/`, `agents/`, `<project>/.agents/agents/`, `internal/eval/`, `internal/auto/`, `internal/isolate/`, `internal/redact/`, `cmd/fyr/`, `embed.go`, `.agents/eval/`, `skills/fyr-auto/`, `skills/VENDORED.md`, `*_test.go`, CI configs, and package manifests (`go.mod`, `package.json`, lockfiles).
- **Always an ask (`proposal` record):** Any push, release, deploy, real-home install, third-party skill install, MCP server, dependency addition, or net deletion exceeding `200` lines.
- **Tamper tripwire:** Before and after every worker execution, `fyr auto` compares `auto.json` bytes and `runs.json` SHA-256 digests; any tampering disables `fyr auto` immediately (`internal/auto/worker.go`).

## The `k=3` behavioural eval gate

When a `harness-edit` or harness-checkout `distill` worker commits a skill change on `work/auto-<name>` in a clean harness checkout (`isHarnessCheckout` in `internal/auto/eval_gate.go`), `internal/auto/eval_plan.go` verifies matching eval rows exist pre-dispatch (`hasMatchingEvalRows`, preventing 0-row skills from wasting worker slots) and builds a deterministic bounded row plan (`selectGateRows`, prioritizing target rows and rotating holdout/regression subsets within `maxGateRows = maxEvalGateSessions / (2 * gateRuns)` = `24` eval sessions) before `evalGateBranch` touches your local branch:

```mermaid
sequenceDiagram
    participant Auto as "internal/auto (eval gate)"
    participant Eval as "fyr eval (internal/isolate)"
    participant WT as Detached Worktree
    participant Local as Local Checkout Branch
    Auto->>Eval: Run targeted, regression & holdout.md rows (k=3) on parent HEAD
    Auto->>Eval: Run same rows (k=3) on candidate branch (cap 24 sessions)
    alt Any row pass count drops, regression fails, or holdout fails
        Auto-->>Local: Keep work/auto-* branch & file queued proposal with both scores
    else All rows hold or improve
        Auto->>WT: Squash-merge candidate onto base HEAD
        Auto->>WT: Run # auto: test lines from AGENTS.md + fyr check
        WT-->>Auto: PASS
        Auto->>Local: Re-verify G1, G3, snapshot & fast-forward 1 commit (no push, install pending)
    end
```

`fyr auto` never pushes commits to GitHub and never writes into your real `$HOME` (`~/.gemini/config/`). Accepted harness skill changes remain one local fast-forward commit marked `install pending` until you review, run `fyr install`, and push them yourself (`R8`, `R9`). On binary or zip places without a harness git checkout, skill-rewrite findings remain queued `target: harness` `proposal` records in `brain.db`.

<details>
<summary>Active & retired gates, dreaming task triggers, and CLI flags reference</summary>

### `fyr auto` gates (`G1`–`G9`)

| Gate | Status | Checks | Scope / Timing |
|---|---|---|---|
| **G1 Switch** | Active | `~/.config/fyr/auto.json` exists, parses strictly, and `enabled: true` (`conversation_idle` valid `5m`–`7d`) | Before run, after dream, and before every workspace |
| **G2 Lock** | Active | Non-blocking `flock` on `~/.config/fyr/auto/auto.lock` | Real runs only (`--dry-run` skips lock) |
| **G3 Power** | Active | Machine is on AC power (Linux `/sys/class/power_supply`, macOS `pmset`) | Before sweep and re-checked per workspace (fails closed) |
| **G4 Presence** | Retired | Retired for conversation rating and dreaming (`ADR-524`; replaced by per-conversation `conversation_idle`) | — |
| **G5 Session** | Retired | Retired for conversation rating and dreaming (`ADR-524`; stale conversations evaluate while sessions run) | — |
| **G6 Registered** | Retired | Retired for `dream` and `auto` (`ADR-524`; all workspaces discovered automatically) | — |
| **G7 Project Idle** | Retired | Retired for `dream` and `auto` (`ADR-524`; replaced by per-conversation `conversation_idle`, default `1h`) | — |
| **G8 Clean Tree** | Scoped | Clean git working tree on a branch with upstream and `0` commits ahead | Required only for git-mutating tasks and `applyBranch`; `DraftsOnly` skips `G8` |
| **G9 Quota & Budget** | Active | `< 4` model runs today, `>= 4h` since last run (unless `--force`), and target model's `agy /usage` pool `>= 50%` (5h) / `>= 30%` (week) via `gateBudgetForModel` (`internal/auto/usage.go`) | Before running a worker (`internal/auto/usage.go`) |

### The 6 dreaming task classes

| Class | Trigger Condition | Scope | Outcome |
|---|---|---|---|
| `memory-consolidation` | `>= 10` closed memory records since last consolidation | Any root (zero-tool `DraftsOnly` via `SessionDigest`) | `OutcomeDrafts` (`add` / `retire` / `merge` in `auto-drafts.jsonl`) |
| `retro-lite` | Stale Antigravity conversations in `(since, cutoff]` or `unaddressedDiagnosticSignals` from `DiagnosticReport` | Clean git checkout | `OutcomeDrafts` (`feedback`, `proposal`, `memory` drafts) |
| `memory-candidates` | Pending memory candidates queued (`PendingMemoryCandidates > 0`) | Clean git checkout | `OutcomeDrafts` (`memory` promotion/refinement drafts) |
| `distill` | Verb/skill sequence repeated `>= 3` times (`distillRepeatThreshold`) | Clean git checkout / Harness | `OutcomeProposal` (`.agents/skills/`) or `OutcomeEvalGate` (harness `skills/auto-distilled-<slug>/`) |
| `missing-skill` | Queued `proposal` requesting a missing skill search | Clean git checkout | `OutcomeDrafts` (searches `skills.sh` and drafts quarantined `proposal`) |
| `harness-edit` | Eligible queued `target: harness` `proposal` (including rating-queued skill rewrites) | Clean harness checkout (`o.Repo`) | `OutcomeEvalGate` (`k=3` eval gate + `# auto: test` + `fyr check`) |

### `fyr dream` and `fyr auto` CLI flags

| Command | Flags | Purpose |
|---|---|---|
| `fyr dream` | `--all`, `--project DIR`, `--dry-run`, `--json` | Run plan-mode no-tool dreamer and stale-conversation rater across all discovered workspaces (`DIR` accepts checkouts, non-git dirs, or `__global__`) |
| `fyr auto on` | `--conversation-idle D`, `--dry-run`, `--json` (plus deprecated no-op `--level`, `--project-idle`, `--user-idle`) | Enable `auto.json` (`enabled: true`, `conversation_idle` default `1h`) and install the hourly OS timer |
| `fyr auto off` | `--dry-run`, `--json` | Disable `auto.json` and remove the hourly timer |
| `fyr auto status` | `--json` | Show switch state, `stale after <conversation_idle>; scope all conversations, projects & harness`, last verdict, daily model runs, open asks, and `installPending` banner |
| `fyr auto run` | `--dry-run`, `--force`, `--json` | Execute one `fyr auto` pass immediately (`--force` bypasses the `4h` cooldown; `--dry-run` previews gates) |
| `fyr auto log` | `--lines N`, `--limit N`, `--all`, `--json` | Tail `~/.config/fyr/auto/auto.log` through `O_NOFOLLOW` (default `50` lines; prints `no auto log yet` when absent) |
| `fyr auto sim` | `list`, `show`, `record`, `merge` (`--json`, `--json-in`) | Inspect and maintain the isolated global Self-Improvement Memory (SIM) optimizer ledger in `brain.db` v5 (`SPEC-fyr-sim`, `ADR-533`) |

</details>

## Next steps

Inspect your `fyr auto` state or view conversation rating averages in the usage report:

- Check your switch state, `conversation_idle` cutoff, and run history with `fyr auto status` or `fyr auto log`, or preview every gate safely with `fyr auto run --dry-run`.
- Inspect stale-conversation outcome averages, background run history, and diagnostic signals in `fyr report --web` or `fyr report --md`.
