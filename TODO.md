# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

No phase active: item 14 of `PLAN-2026-11-02-1806-fyr-speed` is done (`fyr work --runner auto` is the default: docker when its preflight passes, else host with the reason on the stamp line; one `work.ChooseRunner` shared with `fyr auto`; `fyr work --check --runner docker` exits 2 on a refusal, `ADR-504`); the next session does the F1 fix (Backlog, 2026-11-04, the operator's pick at the item 14 review cap) from the newest handoff's Kickoff, then the first item of Next.

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator.

## Next

- Item 17 — reviewer pre-flight: list open bugs first, skip per-file `--refs` searches when empty.
- Item 18 — no explorer on an empty repo: skip explorer dispatch on an empty scaffolded repo.
- Item 13 — Node in the worker image (its docker worker run needs an installed, versioned `fyr` and a linux `agy` at `~/.config/fyr/work/agy-linux`).

## Backlog

- 2026-11-04 (item 14 review, finding F1, `left: operator` at the cap, `REVIEW-2026-11-04-1753-review-worktree-merged`): `fyr work --check` builds no image, so a missing or stale default image reads as host although a run may build it, and a task-list `/work` runs each wave with the table's runner (`skills/work/references/tasks.md:37`), so it never reaches the auto-build; the operator picked fix (a) as its own task, the next one: `Check` predicts docker when the only obstacle is a buildable default image, with ADR-504a.
- 2026-11-04 (BUG-506 residual): a long `FAIL (<err>)` detail or eval-gate reason still comes before `branch <b> kept` on `fyr auto`'s 500-rune sweep line (`short`, `internal/auto/run.go:208`), so it can push the branch off the line.
- 2026-11-04 (item 14): the plan's measure, the same two-task run on both runners, not run (no linux `agy` at `~/.config/fyr/work/agy-linux`, and a development build always resolves to host); folded into Item 42.
- 2026-11-03 (item 10, BUG-504): `fyr eval`'s real-home watch fingerprints the whole `brain.db`, so another agy session's write fails every trial; compare per scope or per record instead (design question).
- 2026-11-03 (item 10): re-run `fyr eval work-tasks work-tasks-waves work-tasks-design --runs 3` while no other agy session works (1 of 9 by the verb, 9 of 9 on re-check, BUG-504); item 14 changed all three rows' prompts and checks (2026-11-04: the runner log), so the re-run also covers that change before a release.
- 2026-11-03 (item 10): the docker run of a task list, after item 12 builds `fyr-work:local` and the token on this Mac.
- 2026-11-03 (fyr check, REVIEW-2026-11-03-1143): pre-existing findings at `.agents/eval/GUIDE-eval.md:15`, `:24` (fixture paths) and `PRD.md:95` (R10).

- 2026-11-03 (item 9, ADR-501): an eval row for the `work` skill's duplicate-ID check (two workers delivering one record ID), and the `fyr eval work` run of the item 9 skill change.
- 2026-11-03 (item 4 baselines, TEST-2026-11-03-0320): preflight made a lightweight tag in 2 of 3 runs; a bug record and a preflight skill fix held to the preflight row.
- 2026-11-03 (item 4 baselines): the spec row is auto-denied one command in 2 of 3 runs; find the command before any spec-skill edit.
- 2026-11-02 (item 3, REVIEW-2026-11-02-1908): a darwin implementation of safety check e (leftover processes, O1), so eval registration runs on macOS.
- 2026-11-02 (item 3 review, finding 3): gitDirProblem has no size limit on `reftable/` tables for a reftable clone.
- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
