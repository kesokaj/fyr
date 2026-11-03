# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Item 10 — plan-less `/work`, of `PLAN-2026-11-02-1806-fyr-speed` (make building with fyr faster without lowering quality):

- [ ] Item 10 — plan-less `/work` — verify: three new eval rows, n of N; one real run per runner on a generated scratch repo, who: AI — expect: two disjoint tasks merged, two overlapping tasks run in two waves, a design-question task routed to the feature skill; `reviewer`, then `/review --docs`

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator.

## Next

- Item 5 — `--body-file` and `--out` for the record and memory verbs, with the handoff's `--id` (BUG-501).
- Item 6 — print-mode-only polling, held to the work and handoff rows.

## Backlog

- 2026-11-03 (item 9, ADR-501): an eval row for the `work` skill's duplicate-ID check (two workers delivering one record ID), and the `fyr eval work` run of the item 9 skill change.
- 2026-11-03 (item 4 baselines, TEST-2026-11-03-0320): preflight made a lightweight tag in 2 of 3 runs; a bug record and a preflight skill fix held to the preflight row.
- 2026-11-03 (item 4 baselines): the spec row is auto-denied one command in 2 of 3 runs; find the command before any spec-skill edit.
- 2026-11-02 (item 3, REVIEW-2026-11-02-1908): a darwin implementation of safety check e (leftover processes, O1), so eval registration runs on macOS.
- 2026-11-02 (item 3 review, finding 3): gitDirProblem has no size limit on `reftable/` tables for a reftable clone.
- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
