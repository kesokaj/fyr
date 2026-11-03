# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Item 9 — worker records reach the `/work` lead (BUG-500), of `PLAN-2026-11-02-1806-fyr-speed` (make building with fyr faster without lowering quality):

- [ ] Item 9 — worker records reach the lead (BUG-500) — verify: unit, a fake-agy test that fails before the fix, who: AI — expect: after `fyr work` returns, every handoff and change record a worker stored can be stored in the lead and read with `fyr record get`; an ADR; BUG-500 fixed; the stale comments at work.go:6-7 and brain.go:233 corrected

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator.

## Next

- Item 5 — `--body-file` and `--out` for the record and memory verbs, with the handoff's `--id` (BUG-501).
- Item 6 — print-mode-only polling, held to the work and handoff rows.

## Backlog

- 2026-11-03 (item 4 baselines, TEST-2026-11-03-0320): preflight made a lightweight tag in 2 of 3 runs; a bug record and a preflight skill fix held to the preflight row.
- 2026-11-03 (item 4 baselines): the spec row is auto-denied one command in 2 of 3 runs; find the command before any spec-skill edit.
- 2026-11-02 (item 3, REVIEW-2026-11-02-1908): a darwin implementation of safety check e (leftover processes, O1), so eval registration runs on macOS.
- 2026-11-02 (item 3 review, finding 3): gitDirProblem has no size limit on `reftable/` tables for a reftable clone.
- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
