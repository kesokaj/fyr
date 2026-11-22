# Workflows, Subagents, Parallel Swarm & Build Control

`fyr` coordinates multi-step engineering work by pairing Antigravity's native dispatchers (`invoke_subagent`, `run_workflow`, `swarm`) with 35 reusable `SKILL.md` workflows, 7 specialized subagents (including `builder`), 3 parallel Python workflow scripts (`phase_loop.py`, `review_parallel.py`, `preflight_gate.py`), evidence-ranked model selection (`fyr models` with `<= 10m` cached pool quotas for `< 50 ms` warm `--pick`, Empirical-Bayes + UCB1 ranking, and `--bench`), per-model `60%` context knee budgets (`internal/models/context_budget.go`), opt-in hands-off phase execution (`fyr relay`), and deterministic Go build-control gates (`fyr verify`, `fyr gate`, `fyr land`, `fyr close`, `fyr check`, `fyr watch`, `fyr state`).

## End-to-end build lifecycle

Non-trivial changes follow a disciplined pipeline driven by slash-invocable skills (`/fyr-feature`, `/fyr-bug`, `/fyr-swarm`, `/fyr-review`, `/fyr-preflight`, `/fyr-handoff`) so architectural choices, write-lease scopes, and verification proofs stay intact across turns and sessions.

```mermaid
flowchart LR
    interview["1. Pre-build interview\n(/fyr-feature / /fyr-requirements)"] --> planRec["2. Phased plan record\n(cold-reader check by reviewer)"]
    planRec --> execPhase["3. Build phase\n(single session or /fyr-swarm waves)"]
    execPhase --> gateLand["4. Deterministic gate & land\n(fyr gate -> fyr land)"]
    gateLand --> reviewCode["5. Multi-lens review\n(/fyr-review + review parallel script)"]
    reviewCode --> verifyClose["6. Verify & close\n(fyr verify -> fyr close / /fyr-handoff)"]
```

Every feature workflow isolates design decisions upfront during the pre-build interview (`ADR-517`): once approved, build phases and `/fyr-swarm` waves execute autonomously without mid-build questions, halting only on a failing gate, check, or security blocker.

## Deterministic build control

Four mechanical Go verbs in `internal/gate` enforce scope discipline, secret hygiene, clean merges, and verified session closes without trusting prose assertions alone:

```mermaid
sequenceDiagram
    participant Lead as Coordinator / Main Session
    participant Gate as fyr gate
    participant Land as fyr land
    participant WT as Detached Scratch Worktree
    participant Brain as ~/.config/fyr/brain.db
    Lead->>Gate: fyr gate --lease <glob> [--judge TEXT] <worktree>
    Gate-->>Lead: PASS (lease, modes, numstat, secrets, trailers, optional judge)
    Lead->>Land: fyr land --message <text> [--judge TEXT] <branch>
    Land->>WT: squash-merge onto base HEAD
    Land->>WT: run fyr verify (gofmt, go vet, go test -race, fyr check)
    WT-->>Land: PASS
    Land->>Land: fast-forward main, delete branch, write .agents/scratch/verify.stamp
    Lead->>Brain: fyr close --title <text>
    Brain-->>Lead: immutable HANDOFF-* with ## Verification Digest
```

- **`fyr verify` (`internal/gate/verify.go`):** Runs `gofmt -l`, `go vet ./...`, `go test -race ./...`, and `fyr check`, verifies the Go source tree digest did not mutate during the test run, and writes `.agents/scratch/verify.stamp` (`0600`), deleting stale stamps on failure.
- **`fyr gate` (`internal/gate/gate.go`, `internal/gate/judge.go`):** Audits a clean worktree or branch against four mechanical rules (`Files:` lease globs plus forbidden `120000`/`160000` modes and secret filenames, numstat caps of default `50` files / `1,500` lines with no binary diffs, added-line secret scanning via `internal/redact` without printing matched values, and zero forbidden commit trailers) plus an optional post-gate cross-family LLM verdict (`--judge TEXT`, `--judge-model ID`) run inside `internal/isolate`'s confined read-only `Network: true` sandbox and logged to `metrics.db` (`judge_verdicts`, `ADR-526`).
- **`fyr land` (`internal/gate/land.go`):** Runs `fyr gate` (including optional `--judge TEXT` and `--judge-model ID`) on a worker branch, squash-merges and runs `fyr verify` inside an isolated detached scratch worktree, verifies your base branch stayed clean and unmoved, fast-forwards it, deletes the worker branch, and refreshes `.agents/scratch/verify.stamp`.
- **`fyr close` (`internal/gate/close.go`):** Validates that `.agents/scratch/verify.stamp` matches the current Go tree SHA-256 and writes an immutable `handoff` record embedding `## Verification Digest` without altering earlier records.

## Subagents, parallel workflows, and live model routing

`fyr models` (`internal/models`) maintains a cached live catalog from `agy models` and `agy changelog` at `~/.config/fyr/models.json` (`0600`), enriches each model with historical wave and eval pass rates (`Store.ModelEvidence`), caches `agy -p "/usage"` quota pools with a `<= 10m` TTL (`DefaultPoolQuotaTTL` in `internal/models/usage_cache.go`) for `< 50 ms` warm `fyr models --pick`, and picks role models without hardcoding model IDs (`ADR-519`, `ADR-520`, `ADR-532`).

```mermaid
flowchart TD
    agyCatalog["agy models + agy changelog"] --> modelsCache["~/.config/fyr/models.json"]
    metricsEvidence["metrics.db ModelEvidence\n(work waves & eval trials)"] --> modelsCache
    usageQuota["agy -p /usage pools"] --> rolePicker["fyr models --pick <role>"]
    modelsCache --> rolePicker
    rolePicker --> geminiRoles["planner / builder / mechanical\n(Gemini family)"]
    rolePicker --> judgeRoles["judge / reviewer / verifier\n(Anthropic cross-family)"]
    judgeRoles -- "< 20% weekly or < 30% 5-hour quota" --> fallbackGemini["Automatic Gemini equivalent-tier fallback"]
```

For parallel build waves, multi-lens reviews, and release gates, three native Python workflow scripts (`workflows/phase_loop.py`, `workflows/review_parallel.py`, and `workflows/preflight_gate.py`) run `builder`, `reviewer`, `security`, `verifier`, and `explorer` concurrently through Antigravity's `run_workflow` tool with cross-family routing (`phase_loop.py` enforces pairwise-disjoint `files` leases, loops back structured `DefectPacket` findings `<= 2` rounds, and lands passing slices sequentially via `fyr land`). Meanwhile, the `PreToolUse` hook automatically guards `invoke_subagent`, `run_workflow`, and prefix-named `swarm` workers so execution stays on capable models (`"pro"` / cross-family judges, emitting `[CROSS_FAMILY_NOTICE]` when tier enums cannot pin concrete cross-family IDs) while `security` and `builder` inherit the active session model (`internal/harness/agents_test.go`).

## Opt-in hands-off phase relay (`fyr relay`)

`fyr relay --plan <PLAN-ID> [--max-phases N] [--judge-model ID] [--dry-run] [--json]` (`internal/relay`, `M27`, `ADR-532`) advances ready phases of an approved `PLAN-*` record hands-off by running one isolated fresh `agy -p` session per phase (`--disable-slash-commands --output-format json --print-timeout`, never `--dangerously-skip-permissions`, `< 120 KiB` Kickoff prompt). Before each phase it verifies clean git state and cached Gemini quota floors (`20%` 5h / `15%` week); after each phase it enforces `HEAD` progress (`ZERO_PROGRESS`), clean tree (`DIRTY_TREE`), zero `PRD.md` edits (`PRD_MODIFIED`), zero new tags (`TAG_CREATED`), lease/secret/trailer gate (`LEASE_OR_GATE_FAIL`), `fyr verify`, and cross-family alignment judge (`gate.Gate --judge`), halting immediately on any `FAIL` or repeating normalized error hash (`REPEATING_ERROR_HASH`) while recording immutable checkpoint or redacted diagnostic `handoff` records in `brain.db` without ever pushing or tagging.

## Drift and trace checking

`fyr check` (`internal/check`) runs four read-only consistency rules across every tracked Markdown file (excluding `TODO.md`, `ROADMAP.md`, `PLAN.md`, `.fyr-noindex` trees, and `skills/VENDORED.md` upstream directories) plus every `current` `spec`, `guide`, and `deploy` record:

1. **Docs parity (rule `a`):** Verifies every `fyr <verb> [subverb] [--flag]` command in code spans/fences exists on the binary and every repo-relative file path (with optional `:line` range) exists on disk within its line count.
2. **Spec freshness (rule `b`):** Flags any `current` `SPEC-*` record that names no code files (`spec-unlinked`) or whose named code received git commits after the commit carrying the spec's current `trail.jsonl` line (`spec-stale`).
3. **Requirement enforcement (rule `c`):** Verifies every active `Must` and `Should` item in `PRD.md` carries an `<!-- enforced by: ... -->` comment whose named `Test*` functions, record IDs, or file paths exist.
4. **R -> M -> T trace completeness (rule `d`):** Verifies every `PRD.md` requirement maps to a component in `SDD.md` `## Trace` and every `SDD.md` component maps to a row in `TDD.md` `## Trace`.

Beside `fyr check`, `fyr git-check` (`ADR-508`) runs `git status --short --untracked-files=all`, `git log --branches --not --remotes --oneline`, and `git tag` in a single read-only command after any write-capable subagent dispatch.

## Out-of-band hang inspection (`fyr watch`)

`fyr watch --check` (`internal/watch/watch.go`, `ADR-526`) inspects Antigravity session databases, subagent scratch artifacts, and live child processes out-of-band in `< 30 ms` without opening live stores directly or invoking any LLM. It copies target stores into a scratch temp directory, discovers active targets via `conversation_summaries.db`, inspects `.agents/scratch/search.db` alongside Linux `/proc` or macOS `ps`, and exits `0` when clear, `1` on findings, or `2` on invalid `--conv` IDs or store-copy failures:

- **`[INTERACTIVE_WAIT]`:** Flags child processes blocked waiting on interactive stdin.
- **`[STUCK_NO_PROGRESS]`:** Flags sessions idle past `--idle-sec` with stale scratch artifacts.
- **`[BUDGET_NEAR_LIMIT]`:** Warns when subagent or session tool calls reach `>= 80%` of the `40`/`120` tool-call budget.

<details>
<summary>Complete inventory of 35 skills, 7 subagents, and build-control CLI flags</summary>

### Subagents (`agents/<name>/agent.md`)

| Subagent | Default Model | Role |
|---|---|---|
| `builder` | inherit | Implements one write-leased slice in an isolated `workspace="share"` worktree: searches first, writes red-first tests, commits on `work/<slice>` with zero trailers, and detaches `HEAD` |
| `explorer` | `flash` | Read-only codebase reconnaissance (10+ files) drafted under `.agents/scratch/agents/` for a `plan` record |
| `reviewer` | inherit | Independent severity-ranked code, docs, simplify, and cold-reader review drafted under `.agents/scratch/agents/` |
| `security` | inherit | Defensive security review of diffs touching auth, crypto, secrets, IAM, parsing, frontmatter allowlists, or dependencies (inherits session model) |
| `verifier` | inherit | Live browser e2e, container smoke, and local HTTP smoke with Boundary Contract Checklist (`fyr verify` runs unit, vet, and `fyr check`) |
| `documentation` | `flash` | Living docs maintainer (`README.md`, `SDD.md`, `TDD.md`, `SPEC-*`, `GUIDE-*`, `DEPLOY-*`) for edits over 30 lines |
| `dreamer` | inherit | Consolidation judgement for `/fyr-retro`, proposing skill edits and memory consolidations |

### Skills by group (35)

| Group | Skills |
|---|---|
| **Work & release protocols (9)** | `fyr-feature`, `fyr-swarm`, `fyr-bug`, `fyr-triage`, `fyr-review`, `review-standards`, `fyr-preflight`, `fyr-harness-audit`, `fyr-auto` |
| **Project shape, specs & handoffs (7)** | `fyr-init-project`, `fyr-docs`, `fyr-requirements`, `fyr-spec`, `testing-strategy`, `fyr-research`, `fyr-handoff` |
| **Learning loop (4)** | `fyr-feedback-log`, `fyr-project-memory`, `fyr-retro`, `fyr-dream` |
| **Language, shell, APIs & UI (9)** | `golang`, `typescript`, `frontend-ui-modern`, `frontend-ui-economist`, `frontend-slate-ui`, `bash`, `json-api-style`, `docker`, `mermaid-syntax` |
| **Google Cloud (6)** | `gcloud-setup`, `cloud-run-deploy`, `terraform`, `operate-gcp`, `data-gcp`, `api-auth-gcp` |

### Build-control and model CLI flags

| Command | Flags | Purpose |
|---|---|---|
| `fyr verify` | `--json`, `--timeout D` | Run `gofmt`, `go vet ./...`, `go test -race ./...`, and `fyr check`, writing `.agents/scratch/verify.stamp` |
| `fyr gate <worktree>` | `--base REF`, `--lease GLOB`, `--lease-file FILE`, `--max-files N`, `--max-lines N`, `--judge TEXT`, `--judge-model ID`, `--json` | Run 4 deterministic checks plus optional confined cross-family `--judge` verdict on a worktree or branch |
| `fyr land <branch>` | `--message TEXT`, `--lease GLOB`, `--lease-file FILE`, `--max-files N`, `--max-lines N`, `--timeout D`, `--judge TEXT`, `--judge-model ID`, `--json` | Gate (with optional `--judge`), squash-verify in detached worktree, fast-forward `main`, and delete branch |
| `fyr close` | `--title TEXT`, `--id ID`, `--body-file FILE`, `--json` | Write immutable `handoff` record embedding fresh verification digest |
| `fyr relay` | `--plan PLAN-ID`, `--max-phases N`, `--judge-model ID`, `--dry-run`, `--json` | Run ready plan phases hands-off in isolated fresh `agy -p` sessions with deterministic safety, `fyr verify`, cross-family judge, and checkpoint handoffs |
| `fyr check` | (global flags only) | Run read-only drift and trace checks; exit `1` on findings |
| `fyr git-check` | (global flags only) | Run post-dispatch `git status`, unpushed commits, and `git tag` check |
| `fyr watch` | `--check`, `--conv ID`, `--idle-sec N`, `--json` | Inspect session DBs, scratch artifacts, and child processes out-of-band (`< 30 ms`) for stdin blocks, stalls, and subagent budgets |
| `fyr models` | `--bench`, `--explain ID`, `--json`, `--max N`, `--model ID`, `--pick ROLE`, `--refresh`, `--tier T` | Inspect `~/.config/fyr/models.json`, refresh catalog/evidence, probe models, and pick role models |
| `fyr state` | `export [--out FILE] [--since DATE] [--include-memory] [--json]`, `import [--dry-run] [--include-memory] [--json] FILE` | Export or import offline state archives across hosts (`internal/state`) |

</details>

## Next steps

Review the full engineering lifecycle, inspect live model picks, or check telemetry reports:

- Read the [Workflow Guide](../Workflow.md) for the complete end-to-end walkthrough from plan to finish, feature to finish, goal, and step-by-step Definition of Done.
- Inspect your available models and live picks with `fyr models --refresh`.
- Read [Telemetry, Trajectories & Reports](metrics-and-reports.md) to see how wave pass rates and session shapes feed back into model evidence and usage reports.
