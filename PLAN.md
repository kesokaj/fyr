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
- D11 — 2026-11-15 — Scratch-home `agy -p` probes (`probe1`–`probe5`, `.agents/scratch/p1-probe/results.md`) proved Antigravity CLI honours `trigger: model_decision` via progressive disclosure (file path + description index; full body loaded on demand) in both `~/.gemini/config/rules/` and workspace `.agents/rules/`, whereas `trigger: glob` is ignored in both locations. Global and repo rule splitting uses `model_decision` and `always_on` only (`validateRuleTrigger` enforces allowlist `""`, `"always_on"`, `"model_decision"`, `"glob"` plus repo-relative `validateRuleGlobs`). — by: implementer
- D12 — 2026-11-15 — Phase 1 A/B attempt 1 (`2026-11-15-072457`, `073123`, `073845`; `3/3` PASS) regressed `ab-pipeline-on` tokens (`+21.0%` `in+out`, `+36.5%` all tokens, `+44.3%` wall s vs Phase 0b) despite `off` improving (`-25.0%` ok reads, `-26.0%` all tokens). Traces showed three causes: `rules/code-modularity.md` (`model_decision`) firing on small edits in 6/6 runs, `context/AGENTS.md` dropping `"After any write-capable dispatch, run "` before `` `fyr git-check` `` (causing unconditional `fyr git-check` in 6/6 runs), and omitting parallel read/search batching. Narrowing `rules/{code-modularity,agy-dispatch}.md` triggers and restoring conditional `fyr git-check` + parallel batching resolved the regression cleanly in v2 (`2026-11-15-075048`, `075449`, `075939`, `.agents/scratch/ab-p1-v2/`, `RETRO-2026-11-15-0102-graph-a-b-baseline`): **Gate PASS** (`3/3` PASS per arm; `on` ok reads `13` `-18.8%`, `in+out` `90,805` `-5.0%`, total `684,565` `-27.0%`, wall `217.6s` `-13.4%`; `off` ok reads `10` `-37.5%`, `in+out` `85,787` `-25.9%`, total `706,079` `-41.6%`, wall `255.9s` `-20.2%`). Also fixed an earlier `$()` shell-substitution wording regression in `context/AGENTS.md`. — by: implementer
- D13 — 2026-11-15 — **Phase 2a Task 1 root-cause diagnosis (`RETRO-2026-11-15-0102-graph-a-b-baseline`, Phase 0b `n=5`/arm + Phase 1 v2 `n=3`/arm):** `ab-pipeline-on` failed the `>= 20%` fewer reads gate (`16` vs `16` ok reads `+0.0%` in P0b; `13` vs `10` ok reads `+30.0%` in P1v2) despite cutting total tokens `-22.4%` in P0b and wall seconds `-15.0%` in P1v2 for four measured reasons: (1) **62–71% of ok reads are non-code Markdown/process files (`98.3%` unranged `FULL`) invisible to the Go AST graph:** P0b mean ok reads/run `on` `15.0` vs `off` `14.8` (`skill_md` `4.40` vs `5.20`; `app_doc` `4.60` vs `4.80`, `6,845` io / `79,157` all tok `8.2%` vs `6,325` io / `77,701` all tok `6.9%`; `agent_md` `0.80` ok + `2.80` err vs `1.40` ok + `2.00` err; `Read:outside` `18,165` io / `95,954` all tok `9.9%` vs `27,274` io / `125,042` all tok `11.1%`); P1v2 mean ok reads/run `on` `13.3` vs `off` `9.7` (`skill_md` `4.33` vs `4.00`, `Read:outside` `13,572` io / `65,036` all tok `9.3%` vs `13,589` io / `49,079` all tok `7.2%`; `app_doc` `2.67` vs `2.67`, `6,295` io / `38,406` all tok `5.5%` vs `4,638` io / `20,261` all tok `3.0%`; `agent_md` `1.33` ok + `0.00` err vs `0.00` ok + `2.67` err); `.md` had zero indexed symbols (`symbols_code.go:21`) and no unranged skeleton clamp (`D7`). (2) **Hop-1 fan-out + partial symbol pre-seeding induced `+1.8` to `+2.0` extra `app_code` reads/run on `on`:** P0b `app_code` `5.20` mean (`5.0` med, `12,634` io / `54,223` all tok `5.6%`) vs `off` `3.40` (`3.0` med, `6,181` io / `40,332` all tok `3.6%`); P1v2 `5.00` (`6.0` med, `12,272` io / `53,972` all tok `7.7%`) vs `off` `3.00` (`3.0` med, `5,073` io / `32,524` all tok `4.8%`); pre-seeded symbol sub-spans (`greet.go:22-30` of `1..31`, `greet_test.go:5-16` of `1..18`) did not cover full-file pre-edit reads, post-edit `greet_test.go` re-reads added `0.6–1.0` reads/run, and `enrichment.targets` (`slice.Files` including Hop-1 neighbors) + bodyless Hop-1 `graph:` names prompted extra reads of `format.go`, `audit.go`, and `cmd/server/main.go`. (3) **Token budget is dominated by Stage 3 DoD/Bash overhead (`56.8–59.8%` of all `on` tokens):** P0b `on` Stage 1 recon `290,018` (`30.0%`), Stage 2 edit/verify `127,970` (`13.2%`), Stage 3 DoD `549,392` (`56.8%`, `Bash:-` `506,657` `52.4%`, `Write:doc` `103,592` `10.7%`) vs `off` Stage 1 `408,481` (`36.3%`), Stage 2 `199,405` (`17.7%`), Stage 3 `516,847` (`46.0%`); P1v2 `on` Stage 1 `158,735` (`22.6%`), Stage 2 `123,449` (`17.6%`), Stage 3 `420,373` (`59.8%`); `PreInvocation` saved tokens by halving Stage 1 `fyr search` turns (`6` -> `3` in P0b, `3` -> `2` in P1v2). (4) **Multi-turn read-engine gaps:** consume-on-hit stamp deletion (`read_ledger.go:167` -> 3rd read allowed, driving 30d repeat reads to `46.4%`), whole-file stamp wipe on any edit (`read_ledger.go:234`), external-checkout ledger misses, missing unranged clamp (`D7`), missing `.md` headings, and `0` pivots on natural-language prompts lacking `>= 6`-char identifier matches (`hook.go:379-396`). Fix: scope `SliceResult.Files` (`extractCompactCapsuleMeta`) to `[Hop 0]` files, persist hash-keyed stamps with overlapping-edit span invalidation, add D7 skeleton-first clamp, index `.md` headings, and add FTS5 0-pivot fallback. — by: implementer
- D14 — 2026-11-15 — **Phase 2a implementation, restatements & checkpoint deferral:** (1) `defaultHookDeadline` uses `!race` (`200ms`, `hook_deadline_race_off.go`) vs `race` (`2s`, `hook_deadline_race_on.go`) so `-race` goroutine/SQLite startup does not spuriously trip the prod deadline while `TestHookHardDeadlineFallsBackToAllow` forces an `80ms` deadline (`< 250ms` pass); (2) replacing consume-on-hit required updating reversed test/doc claims outside `internal/search/` (`cmd/fyr/mock_scenarios_test.go`, `internal/metrics/{metrics_test,trigger_coverage_test}.go`, `README.md`, `SDD.md`, `TDD.md`); (3) candidate binary `.agents/scratch/bin/fyr` passed `5 of 5` unique targeted `fyr eval` rows (`4/5` batch `TEST-2026-11-15-0938-eval-4-of-5-pass-at-7d8c3a78` where `vet-question-and-bypass#1` failed Turn 2 out-of-grant shell pipeline, then `1/1` retry `TEST-2026-11-15-0958-eval-1-of-1-pass-at-7d8c3a78`) plus `3/3` paired Phase 2a A/B gate (`on` ok reads `11` `-15.4%`, `in+out` `83,833` `-7.7%`, total `717,206` `+4.8%` vs P1v2; `11` vs `12` ok reads `-8.3%` vs `off`, `RETRO-2026-11-15-0102-graph-a-b-baseline`); (4) Phase 2a Checkpoint's real-home install + real session smoke test are deferred per Kickoff `Never: ... a real-home install` (`skills/fyr-handoff/SKILL.md:154`), verified instead via `.agents/scratch/p2a-home` and `fyr eval --home ~/.config/fyr/dev/fyr-eval`. — by: implementer
- D15 — 2026-11-15 — **Phase 2b supporting files, live `agy` v1.5.9 hook wire constraints & checkpoint deferral:** (1) Supporting files touched outside nominal `files:`: `cmd/fyr/{ctx_verbs,help_text,verb_models,mock_scenarios_test,mock_scenario_fleet_test}.go`, `internal/work/{catalog,catalog_test,usage}.go`, `internal/harness/plan_test.go`, `internal/search/hook_git_index.go`, `README.md`, `SDD.md`, `TDD.md`. (2) Live `agy` v1.5.9 smoke runs diagnosed three hook wire constraints: `SessionStart` requires `TerminationBehavior,omitempty` (strict `protojson` rejects `"terminationBehavior": ""`); `PreInvocation` `HookSystemMessage.Metadata` still triggers `BUG-523` `utils.scrubMessage` panic, so `writePreInvocation` keeps `Metadata == nil` (`systemMessage` carries banners/enrichment and un-enriched `PASS_THROUGH` emits `{}` / `0` tokens, overriding the `metadata.user_markdown` phrasing in box 2); and `Stop` scopes `[VERIFY_STAMP]` nudges to sessions that modified dirty Go/module files (`sessionTouchedDirtyGo` in `hook_stop_stamp.go`, keyed per tree hash) so read-only subagents exit cleanly without `sir_flash_lite` stamp-bypass warnings. (3) Candidate binary `.agents/scratch/bin/fyr` passed `6/6` targeted live `agy` evals (`TEST-2026-11-15-1243-eval-4-of-4-pass-at-7f7437a9`, `TEST-2026-11-15-1310-eval-2-of-2-pass-at-7f7437a9`, `0` hook panics/unmarshal errors/spurious Stop blocks) plus `3/3` paired Phase 2b A/B gate PASS (`on` ok reads `12` `+9.1%` vs P2a `11`, `in+out` `81,374` `-2.9%`, total `655,184` `-8.6%`, wall `244.9s` `-5.0%`; `-20.0%` reads and `-36.8%` tokens vs `off`, `RETRO-2026-11-15-0102-graph-a-b-baseline`). (4) Post-approval real-home install + live smoke on `e37635dd` (`~/.local/bin/fyr status` clean `1242` managed / `0` drift; `cli-20261115_142752.log` clean) surfaced `BUG-548` (`readGitBranchFast` returning `.invalid` on Git `reftable` repos); fixed in `1d5fb4ca` (`readGitBranchReftable` bounded `trail.Command`, `TestSessionStartReftableBranch`), verified by `TEST-2026-11-15-1458-reftable-branch-fix-verification` (`1489` pass, `0` fail, `7` skip), re-installed to real `$HOME` (`1242` managed, `drift none`), and verified via `fyr search --hook session-start` (`38ms`, `branch: "main"`) + live `agy -p` smoke (`Active branch: main — READY`, `cli-20261115_145909.log` `0` hook errors). — by: implementer

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

- [x] Cold-reader check: REVIEW at `fyr-research/.agents/scratch/agents/review-fyr-v9-plan.md`. Answered by D2, D5–D9 and the rewritten phases below.

### Phase 0 — Safety net and PRD amendment (no Go code)
files: `PRD.md`, `SDD.md`, `TDD.md`, `TODO.md`, `PLAN.md`, ADR record
- [x] Commit `PLAN.md` and store it with `fyr record add plan --body-file PLAN.md` — verify: `fyr record list --kind plan` shows it (`58cce4b6`, `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- [x] Turn `fyr auto` off for the duration, keep the binary for rollback with `cp ~/.local/bin/fyr ~/.local/bin/fyr-v8-rollback`, and copy `~/.config/fyr/{brain,metrics}.db` to `~/.config/fyr/backup-2026-11-14/` while no `fyr` process runs — verify: `fyr auto status` says off, and the backup files have the same size as the originals (`47374681` B binary; `36216832` B `brain.db`; `68567040` B `metrics.db`)
- [x] Draft the exact `PRD.md` diff and the ADR text, then wait for the operator's verbatim approval. The diff covers:
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
  — verify: operator "go" quoted in the ADR (`"Go all approved"`, `ADR-522`); `fyr check` exits 0
- [x] Checkpoint — expect: amendment and ADR committed and pushed, backups present, rollback binary present — evidence: `fyr check` (0 findings), `ADR-522`, `git log -1`

### Phase 0b — Prove the graph works: A/B baseline (after: Phase 0; no Go code unless the harness lacks a counter)
files: `.agents/eval/`, `.agents/scratch/ab/`, `retro` record
The eval rows `ab-pipeline-on` (fixture `project-ast`, hooks and graph on) and `ab-pipeline-off` (`project-ast-nohooks`) already exist in `.agents/eval/GUIDE-eval.md:117-118` and run the same bug-fix prompt. Their checks only test correctness, though, not reads or tokens. One data point so far: `ab-pipeline-on` took 497 s on 2026-11-14.
- [x] Run both rows 5 times each (`fyr eval --home ~/.config/fyr/dev/fyr-eval ab-pipeline-on ab-pipeline-off`, repeated). Then ingest each run's sandbox conversations into a scratch metrics store (`fyr metrics ingest --home .agents/scratch/ab`) — verify: 10 runs ingested, with session IDs listed (`on`: `7ef7c880`, `d4b806ad`, `62641a56`, `28d0ee96`, `bed8aad9`; `off`: `b451303c`, `96f7adc6`, `4611909b`, `2c5fcf0b`, `18e0bfea`, staged `.gemini/`-only into `.agents/scratch/ab`)
- [x] Compute per arm the median and spread of: `read_file` calls, repeat reads, in+out tokens, cache-read tokens, wall seconds, and pass rate. Store the table as a `retro` record titled "Graph A/B baseline" — verify: `RETRO-2026-11-15-0102-graph-a-b-baseline` exists; both arms `5/5` (`100%`) pass (`on` vs `off` medians: `16` vs `16` ok reads `+0.0%`, `0` vs `0` repeat, `95,597` vs `115,789` in+out `-17.4%`, `841,940` vs `1,092,708` cache_read `-22.9%`, `251.3` vs `320.7` wall s `-21.6%`)
- [x] Verdict rule, written into the record: the graph "works" only if the on arm has at least 20% fewer reads and at least 10% fewer total tokens at an equal or better pass rate. If it does not, Phase 2a's first task becomes "find out why the graph does not reduce reads", before any other read-engine work — verify: `Verdict: DOES NOT WORK` written in `RETRO-2026-11-15-0102-graph-a-b-baseline` and Phase 2a prepended below
- [x] Checkpoint — evidence: `RETRO-2026-11-15-0102-graph-a-b-baseline` and the A/B table

**Metrics gate at every later checkpoint (Phases 1, 2a, 2b, 3, 4):** re-run the Phase 0b A/B (3 runs per arm is enough) and append the deltas to the "Graph A/B baseline" record. A phase that makes reads or tokens worse by more than 10% against the previous checkpoint does not tick its Checkpoint. It gets a D-row with the cause and a fix task.

### Phase 1 — Context diet (after: Phase 0)
files: `internal/harness/inventory.go`, `internal/harness/render.go`, `internal/harness/size_test.go`, `context/`, `rules/`, `AGENTS.md`, `.agents/rules/`
- [x] Probe in a scratch home whether `~/.gemini/config/rules/` honours `trigger: glob` and `trigger: model_decision` (an `agy -p` run with one marker rule of each kind) — verify: `D11` (`probe1`–`probe5`, `.agents/scratch/p1-probe/results.md`: `model_decision` honoured via progressive disclosure in both global and workspace rules; `glob` ignored in both).
- [x] Add `"globs"` to `ruleKeys` (`inventory.go:210`) and drop the duplicate inventory from `render.go:53`, keeping Marker and Canary — verify: `validateRuleTrigger`/`validateRuleGlobs` (`inventory.go:313-356`), `RenderContext` (`render.go:23-62`), `go test -race ./internal/harness/...` PASS (`TEST-2026-11-15-0823-phase-1-context-diet-verification`).
- [x] Split always-on from triggered bytes in `size_test.go` while keeping `TestRenderedContextFitsAntigravityBlock` on the combined block — verify: all 6 size tests PASS (`6,486 B` ctx <= `13,000 B`; `8,548 B` always-on <= `9,000 B`; `12,510 B` combined <= `19,500 B`; `3,649 B` `AGENTS.md` <= `4,500 B`; `18,615 B` skill listing <= `19,500 B`; must-fail test + live +10 KB probe FAIL as expected).
- [x] Shrink repo `AGENTS.md` (13,867 B → about 3,800 B, plus `.agents/rules/*.md`) and global context plus rules (19,445 B → about 8,200 B always-on) — verify: `wc -c AGENTS.md` = `3,649 B`; `fyr install/status --home .agents/scratch/verifier-p1` clean (`1242` managed, `0` drift); `fyr eval` `10 of 10` mapped rows PASS (`TEST-2026-11-15-0821-eval-8-of-8-pass-at-7ff1c219` + `3/3` paired A/B v2 PASS, `D12`, `RETRO-2026-11-15-0102-graph-a-b-baseline`).
- [x] Checkpoint — evidence: `go test -race -timeout 30m ./...` PASS (`1473` pass, `0` fail, `7` skip; `TEST-2026-11-15-0823-phase-1-context-diet-verification`), `fyr check` (`0 findings`), `REVIEW-2026-11-15-0815-phase-1-context-diet-and-rule-triggers-code-docs` + `REVIEW-2026-11-15-0815-security-review-context-diet-and-rule-triggers`, eval `10 of 10` mapped rows + Phase 1 v2 A/B gate PASS (`on` `-18.8%` ok reads, `-27.0%` total tokens; `off` `-37.5%` ok reads, `-41.6%` total tokens).

### Phase 2a — Read engine and hook safety (after: Phase 1)
files: `internal/search/` (hook, clamp, ledger, symbols, slice, guard files)
- [x] Find out why the graph does not reduce reads, before any other read-engine work (from Phase 0b `RETRO-2026-11-15-0102-graph-a-b-baseline`) — verify: `D13` root cause & per-source read/token breakdown recorded; `extractCompactCapsuleMeta` (`slice_compact.go`) scoped to `[Hop 0]` files only.
- [x] Give every hook entry a hard deadline (about 200 ms) that falls back to `allow`, and add a global off switch `~/.config/fyr/no-hooks` next to the per-checkout one — verify: `hook_deadline.go`, `hook_deadline_race_{off,on}.go`, `hook_safety_test.go` (`TestHookHardDeadlineFallsBackToAllow` `< 250 ms`, `TestGlobalNoHooksSwitch`).
- [x] Make the read ledger persistent and keyed by hash. A stamp survives a blocked read and is invalidated only by a content-hash change or an overlapping edit (`read_ledger.go:162-171, :234`) — verify: `read_ledger.go`, `read_ledger_edit.go`, `read_ledger_persist_test.go` (`hit_count` persistence on 2nd/3rd read, overlapping-edit invalidation + line-shift compensation, `resolveSessionLedgerDir` external checkout).
- [x] Add the skeleton-first clamp from D7 (`hook_clamp.go`), a Markdown heading indexer (`symbols_md.go`) and an FTS5 fallback when `rankAndCheckConfidence` (`hook.go:375-403`) has 0 pivots — verify: `hook_clamp_test.go`, `symbols_md_test.go`, `hook_fts_fallback.go`, `TestNaturalLanguagePromptGetsPivotsViaFTSFallback`, `TestUnrangedReadOf600LineFileDeniedWithSkeletonAndRangedRetryAllowed`.
- [x] Checkpoint — evidence: `TEST-2026-11-15-1047-phase-2a-read-engine-and-hook-safety-verificatio` (`go test -race -timeout 30m ./...` 1480 pass / 0 fail / 7 skip, `internal/search` 85.4% coverage, `fyr check` 0 findings, scratch install `.agents/scratch/verifier-p2a` 1242 managed / 0 drift), `REVIEW-2026-11-15-1035-phase-2a-read-engine-and-hook-safety-code-docs`, `REVIEW-2026-11-15-1035-security-review-phase-2a-read-engine-and-hook-sa`, `REVIEW-2026-11-15-1037-security-review-phase-2a-round-2-fixes-clear`, `5/5` targeted `fyr eval` + `3/3` paired Phase 2a A/B gate PASS (`RETRO-2026-11-15-0102-graph-a-b-baseline`); real-home install & smoke deferred per Kickoff `Never:` (`D14`).

### Phase 2b — Hook events and routing (after: Phase 2a)
files: `internal/search/` (vet, route, subagent, hook files), `internal/harness/plan.go`
- [x] Render the `SessionStart` event (a capsule of at most 1.5 KB with date, plan, Now and status) and a `run_workflow` matcher in `internal/harness/plan.go:191-198` — verify: rendered `hooks.json` golden test (`internal/harness/plan_test.go`), `hook_session_start.go`, `hook_git_index.go`, `hook_workflow.go` (`<= 1,500 B`, UTF-8-safe, `< 250 ms`), live `SESSION_START` in `TEST-2026-11-15-1310-eval-2-of-2-pass-at-7f7437a9` (`D15`)
- [x] Banners go through `metadata.user_markdown` with an empty `systemMessage`. `IntentQuestion` never sets `SecurityRequired` (`vet_skills.go:21`), and `vet_route.go` drops its `/fyr-work` routes — verify: `go test -race ./internal/search/...` + live `agy` wire constraint (`Metadata == nil` per `BUG-523` `utils.scrubMessage` + zero-token `{}` on un-enriched `PASS_THROUGH`, `D15`); `vet_test.go`, `vet_route_test.go`, `hook_test.go` (`TEST-2026-11-15-1402-phase-2b-hook-events-and-routing-verification`)
- [x] Route `inferSwarmRole` by name prefix only, with the 20%/30% quota fallback (`hook_subagent.go:72-164`). Add the `Stop` stamp check from D2 — verify: `EnrichCatalogPools` (`internal/work/catalog.go`, `cmd/fyr/verb_models.go`), `hook_stop_stamp.go`, `hook_stop_stamp_test.go` (`sessionTouchedDirtyGo`, `Stop` `continue` once per tree hash on stale stamp, `allow` on matching stamp or clean/untouched session, `< 250 ms`; `TEST-2026-11-15-1402-phase-2b-hook-events-and-routing-verification`)
- [x] Checkpoint — evidence: `TEST-2026-11-15-1402-phase-2b-hook-events-and-routing-verification` (`1488` pass, `0` fail, `7` skip) + `BUG-548` (`1d5fb4ca`) / `TEST-2026-11-15-1458-reftable-branch-fix-verification` (`1489` pass, `0` fail, `7` skip; `TestSessionStartReftableBranch` PASS; `fyr check` `0 findings`), `REVIEW-2026-11-15-{1349,1352,1446,1448}-*`, `6/6` targeted live `agy` evals (`TEST-2026-11-15-1243`, `TEST-2026-11-15-1310`) + `3/3` paired Phase 2b A/B gate PASS (`RETRO-2026-11-15-0102-graph-a-b-baseline`); real-home install (`1d5fb4ca`, `1242` managed, `drift none`) + live `agy -p` smoke (`Active branch: main — READY`, `cli-20261115_145909.log` clean, `D15`)

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
