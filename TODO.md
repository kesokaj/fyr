# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

`PLAN-2026-10-31-1636-plan-fyr-auto-mode-idle-time-self-improvement-of` Phase 6: the held-out set and the eval re-runs (`fyr eval --suite-file PATH` and the `eval-rerun` class). Phase 5 is done: `provenance` on `memory` and `feedback` records (`operator`, `derived`, `unknown`), `fyr memory add --provenance`, the `skills/retro` and `skills/project-memory` sentences, the drafts' validation and writer (`internal/auto/drafts.go`), the learning tasks' triggers and redacted inputs (`internal/auto/tasks.go`), and R3 3.4 / R8 8.3 (D49–D50).
- [ ] `fyr eval --suite-file PATH` reads a suite from a file outside every checkout, in `GUIDE-eval`'s format (D15). It refuses a file inside a checkout, and its rows are labelled `eval=holdout/<id>`.
- [ ] The eval-rerun class: Go runs `fyr eval` with its triggers and G9 between rows.
- [ ] Checkpoint.

## Next

That plan's Phase 7, then Phase 8.

## Backlog

None.
