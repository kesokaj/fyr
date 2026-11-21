# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

- [ ] Phase 2 of `PLAN-2026-11-21-1351-plan-fyr-v9-6-2-v9-7-0-optimization-fixes-daily`: Lift `search:grep` & close Bash read bypasses (`PATH` setup, `&&`/`;` read-ledger enforcement, subagent read budgets + eval gate)

## Next

- [ ] Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

- [ ] Run cross-family judge after `verifyInScratchWorktree` (or pass fresh verify summary) in `internal/gate/land.go:65-86` so `fyr land --judge` does not fail on stale main `verify.stamp` (`BUG-560`, 2026-11-21)
