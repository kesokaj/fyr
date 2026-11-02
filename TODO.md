# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Item 4 — fresh eval suite and baselines, of `PLAN-2026-11-02-1806-fyr-speed` (make building with fyr faster without lowering quality):

- [ ] Item 4 — fresh eval suite and baselines — verify: `go test ./internal/eval`, then `fyr eval <rows> --runs 3 --home ~/sandbox/fyr-eval/home` after `agy -p "/usage"`, who: AI — expect: a tracked suite file (D2) holding the rows item 4 names, each check shown to pass and to fail; `TestSuiteHarness` pins exactly those rows; the v7.0.0 baselines stored in a `testing` record; an ADR

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator.

## Next

- Item 9 — worker records reach the `/work` lead (BUG-500); its work-skill edit is measured against item 4's work row.
- Item 5 — `--body-file` and `--out` for the record and memory verbs, with the handoff's `--id` (BUG-501).

## Backlog

- 2026-11-02 (item 3, REVIEW-2026-11-02-1908): a darwin implementation of safety check e (leftover processes, O1), so eval registration runs on macOS.
- 2026-11-02 (item 3 review, finding 3): gitDirProblem has no size limit on `reftable/` tables for a reftable clone.
- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
