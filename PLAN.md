# PLAN — fyr v9: build control + self-improvement   (2026-11-14, revision 2)

> **Razor:** `fyr` has two jobs. **(1) Control builds:** context, routing, write leases and verification under Antigravity's own dispatchers. **(2) Improve itself:** `fyr auto` turns telemetry into eval-gated changes. Code that serves neither job is cut or folded into something else.

Revision 2 folds in a double check: my own verification plus a cold reviewer with 10 findings, 2 of them blocking (review: `fyr-research/.agents/scratch/agents/review-fyr-v9-plan.md`). It also records the operator's approval of decisions 1–5 (D1–D5 below). The evidence base is [45-telemetry-and-agents-md-audit.md](file:///home/kesokaj/Workspace/fyr-research/2026-11-14-lean-fyr-and-swarm/45-telemetry-and-agents-md-audit.md).

## Why

Data from 2026-10-15 to 2026-11-14 (3.10B global tokens, 1.16B in `fyr`):
- About 33% of tokens go to process overhead rather than work.
- 43.3% of steps run at 150k+ tokens of context.
- Repeat reads rose from 20.1% to 46.4% after ADR-511.
- `fyr work` ran 0 of 101 qualifying phases. 12 of its 13 FAILs were Docker `scanCreds` false positives.
- `verifier` ran 88 sessions and found no bug that `go test -race ./...` missed.
- Always-on rules cost 33,312 B per turn in this repo.

## Safety rules for every phase (what "do not destroy fyr" means)

1. **PRD first.** No phase changes behaviour a `PRD.md` Must describes until Phase 0's amendment and ADR are approved and committed. Those Must items are R3 (hooks, ledger), R4 (dependencies), R6, R8 and R9 (eval runner, release procedure), and the enforced-by test lists.
2. **Hooks stay under 250 ms and fail open.** A hook only reads files and stamps. It never runs `go`, `git` or the network. Every hook entry gets a hard deadline, not just the current `recover()` at `internal/search/hook.go:91`.
3. **Delete only what nothing imports.** Before any deletion, `go list -deps ./... | grep -E 'internal/(work|sandbox)'` must show nothing outside the package being deleted, and `go build ./... && go test -race ./...` must be green.
4. **Stores are never deleted.** `brain.db` and `metrics.db` are backed up in Phase 0. Existing `change` and `testing` records stay; v9 only stops writing new per-task ones.
5. **Development happens in scratch homes, with one session at a time.** Every state-writing verb carries `--home .agents/scratch/<name>` (`AGENTS.md:55`). A real-home `fyr install` happens only at a phase checkpoint, from a clean pushed checkout. No second session edits `fyr` while a phase runs.
6. **Rollback is always one command.** Phase 0 keeps the current binary. Rolling back is `cp ~/.local/bin/fyr-v8-rollback ~/.local/bin/fyr && fyr install`. Hooks can be switched off per checkout with `.agents/scratch/no-hooks` (`internal/search/hook_guard.go:44-52`), and Phase 2a adds a global switch.

## Decisions

- D1 — 2026-11-14 — The plan lives in `PLAN.md` at the repo root and is also stored with `fyr record add plan`. It is archived at `v9.0.0`. — by: operator, "Questions. Look good."
- D2 — 2026-11-14 — The `Stop` hook returns `continue` once when Go files changed in the turn and no green `fyr verify` stamp matches the current tree hash. It only compares stamps and never runs tests (R3 budget). — by: operator + cold read round 1
- D3 — 2026-11-14 — `fyr auto` keeps its eval-gated apply path to local `auto/*` branches. The `rubric.go` denied roots stay. The workers move to the new isolation instead of being deleted. — by: operator
- D4 — 2026-11-14 — Retire the `changelog`, `bugs` and `triage` subagents. `verifier` keeps only browser and container e2e. — by: operator
- D5 — 2026-11-14 — Amend `PRD.md` through an ADR, now in Phase 0 rather than last. — by: operator; moved by cold read round 1 (blocking)
- D6 — 2026-11-14 — `agy --sandbox` restricts only the terminal (`agy --help`). File isolation for `fyr eval` and `fyr auto` comes from `internal/isolate`, built on stdlib `syscall` (Linux Landlock, syscalls 444–446) with no new dependency (R4). On macOS it uses `sandbox-exec`, and if that is unavailable the eval refuses to run. — by: cold read round 1
- D7 — 2026-11-14 — The read-file clamp denies the read once and returns a skeleton; a retry with an explicit range is allowed. It does not use `overwrite`, because a buggy rewrite of read arguments would corrupt every read in every session. — by: planner
- D8 — 2026-11-14 — Cut from scope: the `PostToolUse` retry-streak hint, code for `/boost` and `/teamwork` (they stay as routing-table rows in a skill only), and more than two workflow scripts. — by: cold read round 1 (scope)
- D9 — 2026-11-14 — The 7-day benchmark runs after `v9.0.0` and feeds `fyr auto`. It does not block the tag. — by: cold read round 1 (sizing)
- D10 — 2026-11-14 — Every claim that the graph or hooks save reads or tokens is proven by a controlled A/B before it is believed. Real-world telemetry cannot prove it: in the 30-day data, sessions with slices on 80%+ of turns (n=39) median 12.0 reads and 195,828 in+out tokens per turn, while the rest (n=13) median 2.0 reads and 39,581 tokens per turn. The seeded sessions are mostly code work (82% code intent vs 62%), so the comparison is confounded. Phase 0b runs the A/B, and every later checkpoint re-runs it as a metrics gate. — by: operator, "check the metrics that the graph is actually working"

Rejected: a per-file 24,000 B rule cap. It contradicts `PRD.md:110` (BUG-085, which measured one combined block), so `TestRenderedContextFitsAntigravityBlock` stays.

## Routing after v9 (replaces `fyr work`)

| Work shape | Tool | What `fyr` controls |
|---|---|---|
| One track, 1–6 files (estimated majority) | plan → execute in the main session | turn-0 capsule, read clamp, `Stop` stamp, `fyr verify`, `fyr close` |
| Width ≤ 2, or a blind judgement | `invoke_subagent` | model pinned by name prefix with quota fallback (`hook_subagent.go:72-164`) |
| Width ≥ 3, independent pieces | `run_workflow` (`parallel()`, `workspace="share"`) | `fyr gate` on each worker, `fyr land` one at a time |
| Width ≥ 3, workers must negotiate | `/swarm` | `Files:` lease, `fyr gate`, `fyr land` |
| A hard single-surface bug | `/boost` (routing row only) | none beyond the normal gates |
| A multi-day campaign | `/teamwork` or `/goal` (routing row only) | per-milestone `fyr verify` from `PLAN-*` |

## Phases

Each phase is one fresh session. It ends at its Checkpoint with `/fyr-handoff`, and it starts from a clean tree with no other session in `fyr`.

- [ ] Cold-reader check: REVIEW at `fyr-research/.agents/scratch/agents/review-fyr-v9-plan.md`. Answered by D2, D5–D9 and the rewritten phases below.

### Phase 0 — Safety net and PRD amendment (no Go code)
files: `PRD.md`, `SDD.md`, `TDD.md`, `TODO.md`, `PLAN.md`, ADR record
- [ ] Commit `PLAN.md` and store it with `fyr record add plan --body-file PLAN.md` — verify: `fyr record list --kind plan` shows it
- [ ] Turn `fyr auto` off for the duration, keep the binary for rollback with `cp ~/.local/bin/fyr ~/.local/bin/fyr-v8-rollback`, and copy `~/.config/fyr/{brain,metrics}.db` to `~/.config/fyr/backup-2026-11-14/` while no `fyr` process runs — verify: `fyr auto status` says off, and the backup files have the same size as the originals
- [ ] Draft the exact `PRD.md` diff and the ADR text, then wait for the operator's verbatim approval. The diff covers:
  - G2 at ≤ 4,500 B.
  - R0 without `fyr work`.
  - R3: five hooks, a stamp-only `Stop` nudge, persistent hash ledger, one background runner `fyr auto` with dream merged in.
  - R4: no new dependency.
  - R6: eval refuses the real home under `internal/isolate`.
  - R7: records are never deleted.
  - R8: the runner list.
  - R9: the release procedure through parallel preflight.
  - R10: macOS `sandbox-exec`.
  - Known limits: drop the Docker fallback and keep BUG-085.
  - Pre-approval for the enforced-by edits that Phase 6b makes.
  — verify: operator "go" quoted in the ADR; `fyr check` exits 0
- [ ] Checkpoint — expect: amendment and ADR committed and pushed, backups present, rollback binary present — evidence: `fyr check`, `git log -1`

### Phase 0b — Prove the graph works: A/B baseline (after: Phase 0; no Go code unless the harness lacks a counter)
files: `.agents/eval/`, `.agents/scratch/ab/`, `retro` record
The eval rows `ab-pipeline-on` (fixture `project-ast`, hooks and graph on) and `ab-pipeline-off` (`project-ast-nohooks`) already exist in `.agents/eval/GUIDE-eval.md:117-118` and run the same bug-fix prompt. Their checks only test correctness, though, not reads or tokens. One data point so far: `ab-pipeline-on` took 497 s on 2026-11-14.
- [ ] Run both rows 5 times each (`fyr eval --home ~/.config/fyr/dev/fyr-eval ab-pipeline-on ab-pipeline-off`, repeated). Then ingest each run's sandbox conversations into a scratch metrics store (`fyr metrics ingest --home .agents/scratch/ab`) — verify: 10 runs ingested, with session IDs listed
- [ ] Compute per arm the median and spread of: `read_file` calls, repeat reads, in+out tokens, cache-read tokens, wall seconds, and pass rate. Store the table as a `retro` record titled "Graph A/B baseline" — verify: the record exists. If an arm has under 4 of 5 passes, the token comparison is invalid; fix the row first.
- [ ] Verdict rule, written into the record: the graph "works" only if the on arm has at least 20% fewer reads and at least 10% fewer total tokens at an equal or better pass rate. If it does not, Phase 2a's first task becomes "find out why the graph does not reduce reads", before any other read-engine work — verify: the verdict line is present
- [ ] Checkpoint — evidence: the `retro` record ID and the A/B table

**Metrics gate at every later checkpoint (Phases 1, 2a, 2b, 3, 4):** re-run the Phase 0b A/B (3 runs per arm is enough) and append the deltas to the "Graph A/B baseline" record. A phase that makes reads or tokens worse by more than 10% against the previous checkpoint does not tick its Checkpoint. It gets a D-row with the cause and a fix task.

### Phase 1 — Context diet (after: Phase 0)
files: `internal/harness/inventory.go`, `internal/harness/render.go`, `internal/harness/size_test.go`, `context/`, `rules/`, `AGENTS.md`, `.agents/rules/`
- [ ] Probe in a scratch home whether `~/.gemini/config/rules/` honours `trigger: glob` and `trigger: model_decision` (an `agy -p` run with one marker rule of each kind) — verify: a marker shows up only when its trigger fires. If not, fall back to shrinking text only and record a D-row.
- [ ] Add `"globs"` to `ruleKeys` (`inventory.go:210`) and drop the duplicate inventory from `render.go:53`, keeping Marker and Canary — verify: `go test -race ./internal/harness/...`
- [ ] Split always-on from triggered bytes in `size_test.go` while keeping `TestRenderedContextFitsAntigravityBlock` on the combined block — verify: the tests pass, and a must-fail run (an oversized rule) fails
- [ ] Shrink repo `AGENTS.md` (13,867 B → about 3,800 B, plus `.agents/rules/*.md`) and global context plus rules (19,445 B → about 8,200 B always-on) — verify: `wc -c AGENTS.md` ≤ 4,500; `fyr install --home .agents/scratch/p1` is clean; `fyr eval` passes on the rows mapped to `context/` and `rules/`
- [ ] Checkpoint — evidence: `go test -race ./...`, `fyr check`, eval n of N

### Phase 2a — Read engine and hook safety (after: Phase 1)
files: `internal/search/` (hook, clamp, ledger, symbols, slice, guard files)
- [ ] Give every hook entry a hard deadline (about 200 ms) that falls back to `allow`, and add a global off switch `~/.config/fyr/no-hooks` next to the per-checkout one — verify: a test with a slow stub finishes under 250 ms and returns allow
- [ ] Make the read ledger persistent and keyed by hash. A stamp survives a blocked read and is invalidated only by a content-hash change or an overlapping edit (`read_ledger.go:162-171, :234`) — verify: table tests for the 2nd and 3rd reads, an edit, and an external checkout
- [ ] Add the skeleton-first clamp from D7 (`hook_clamp.go`), a Markdown heading indexer (`symbols_md.go`) and an FTS5 fallback when `rankAndCheckConfidence` (`hook.go:375-403`) has 0 pivots — verify: a natural-language prompt gets more than 0 pivots; an unranged read of a 600-line file is denied with a skeleton, and a ranged retry is allowed
- [ ] Checkpoint — evidence: `go test -race ./internal/search/...`, then a real-home install from a clean, pushed tree, then one real session smoke test

### Phase 2b — Hook events and routing (after: Phase 2a)
files: `internal/search/` (vet, route, subagent, hook files), `internal/harness/plan.go`
- [ ] Render the `SessionStart` event (a capsule of at most 1.5 KB with date, plan, Now and status) and a `run_workflow` matcher in `internal/harness/plan.go:191-198` — verify: the rendered `hooks.json` golden test; capsule ≤ 1,500 B
- [ ] Banners go through `metadata.user_markdown` with an empty `systemMessage`. `IntentQuestion` never sets `SecurityRequired` (`vet_skills.go:21`), and `vet_route.go` drops its `/fyr-work` routes — verify: `go test -race ./internal/search/...`
- [ ] Route `inferSwarmRole` by name prefix only, with the 20%/30% quota fallback (`hook_subagent.go:72-164`). Add the `Stop` stamp check from D2 — verify: `Stop` returns `continue` once on a dirty tree with a stale stamp and allows a matching stamp; latency stays under 250 ms
- [ ] Checkpoint — evidence: tests, a real-home install, one real session smoke test

### Phase 3 — Deterministic gates and subagent pruning (after: Phase 2b)
files: `cmd/fyr/`, `internal/gate/`, `agents/`, `internal/harness/agents_test.go`, `context/`, `skills/` (about 15 files that reference the retired agents)
- [ ] `fyr verify` (`gofmt`, `vet`, `go test -race ./...`, `fyr check`, then write the tree-hash stamp) and `fyr gate <worktree>` (lease, numstat, added-line secret scan, trailers) — verify: temp-repo tests, including a must-fail case for each check — docs: `README.md`
- [ ] `fyr land <branch>` and `fyr close` (one handoff that embeds the verify digest; no deletion of old records) — verify: a temp-repo end-to-end test — docs: `README.md`
- [ ] Retire `changelog`, `bugs` and `triage`; scope `verifier` to e2e; update `agents_test.go:131-230` and every skill or context reference; switch the DoD to 3 steps — verify: `go test -race ./...`, `fyr check`, eval rows for the changed skills
- [ ] Checkpoint

### Phase 4 — Models extraction and native orchestration (after: Phase 3)
files: `internal/models/`, `internal/work/`, `cmd/fyr/verb_models.go`, `cmd/fyr/run_dispatch.go`, `internal/auto/usage.go`, `internal/auto/eval_gate.go`, `workflows/`, `skills/fyr-swarm/`, `skills/fyr-review/`, `skills/fyr-preflight/`
- [ ] Move the model catalog and quota picker into `internal/models` and re-point every importer — verify: `go build ./... && go test -race ./...`; `go list -deps ./cmd/fyr | grep internal/work` is reduced to the `verb_work` path only
- [ ] Two workflow scripts, `review_parallel.py` and `preflight_gate.py` — verify: a real `run_workflow` run over a scratch diff returns schema-valid results
- [ ] `skills/fyr-swarm/SKILL.md` (routing table, `Files:` brief, gate and land); `/fyr-review` and `/fyr-preflight` call the workflows — verify: eval rows for the changed skills
- [ ] Checkpoint

### Phase 5 — Isolation without Docker (after: Phase 4)
files: `internal/isolate/`, `internal/eval/`, `internal/auto/eval_gate.go`, `internal/auto/eval_exec.go`, `internal/auto/worker.go`, `cmd/fyr/eval.go`
- [ ] `internal/isolate` per D6. Pass criterion: a write outside the scratch home fails with EACCES, and the real `$HOME` is unchanged — verify: `go test -race ./internal/isolate/...`, plus a must-fail test with Landlock off
- [ ] Move `internal/eval`, `internal/auto` (eval gate, eval exec, worker) and `cmd/fyr/eval.go` off `internal/sandbox` behind a runner switch, keeping Docker as the default — verify: `TestEvalRefusesWithoutHome` and `TestEvalRefusesItsHome` still pass
- [ ] Dual-run: 5 real eval rows give the same verdict on both runners. Then make `isolate` the default — verify: 5 of 5 match
- [ ] Checkpoint

### Phase 6a — One background runner (after: Phase 5)
files: `internal/auto/`, `internal/dream/`, `cmd/fyr/verb_dream.go`, `cmd/fyr/verb_uninstall.go`
- [ ] Merge the dream timer into `fyr auto` (one timer). Keep metrics ingest first and the dream's plan-mode, no-tool run — verify: `TestRunIngestsMetricsFirst`, `TestDreamTimerDryRun` (or its renamed successor), and the uninstall timer tests
- [ ] Checkpoint — evidence: `fyr auto --dry-run --home .agents/scratch/p6a`, and backup row counts equal live row counts

### Phase 6b — Deletion (after: Phase 6a)
files: `internal/work/`, `internal/sandbox/`, `cmd/fyr/verb_work*`, `docker/`, `embed.go`, `internal/auto/rubric.go`, `skills/fyr-work/`, `skills/fyr-auto/`, `PRD.md` enforced-by lists (pre-approved in Phase 0)
- [ ] Confirm nothing imports the packages anymore (safety rule 3), then delete them. Remove `all:docker` from `embed.go`. Update `rubric.go` `deniedRoots` and `TestRubricMatchesSkill` — verify: `go build ./... && go test -race ./...`, `TestEmbedHoldsOnlyTrackedFiles`, `fyr check`
- [ ] Checkpoint — evidence: `git diff --stat` shows deletions only in the listed paths

### Phase 7 — Release `v9.0.0` (after: Phase 6b)
- [ ] `/fyr-preflight v9.0.0` (parallel review plus security, isolated changed-row eval, checksums) — verify: SHIP
- [ ] After the tag: a 7-day `fyr report --md` comparison stored as a `retro` record — targets: repeat reads under 10%, search:grep at 1.5 or higher, under 20% of steps at 150k+, tokens per turn down 45%

## Risks

| Risk | Mitigation |
|---|---|
| A hook change bricks every session | Deadline plus global off switch (2a) first; real-home install only at checkpoints; rollback binary (Phase 0) |
| Global rule triggers are not honoured | Phase 1 probe first; fall back to text-only shrink |
| Landlock or `sandbox-exec` misbehaves | Dual-run with Docker still the default until 5 of 5 rows match |
| Records or metrics lost in the merge | Phase 0 backup; no deletion of rows; row-count check at 6a |
| Estimates turn out wrong | Net LOC after D3 is about 19k–21k (work ~10k after extraction, sandbox 10.3k, `verb_work` 1k, minus about 1.5–2k new); re-measure at 6b |
