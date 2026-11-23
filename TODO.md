# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Wave 9 (Phase 19) -> Wave 10 (Phase 20)** (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic` — Phases 1–18 landed; `18/20` complete):
- [ ] **Phase 19 (Full `fyr verify` & 57-repro scratch-binary sweep + `T5.2` 75-prompt benchmark):** Run full verification on consolidated `main`, rebuild `.agents/scratch/bin/fyr`, re-execute isolated 57-finding scratch-binary sweep + `T5.2` 75-prompt prefetch benchmark (`Natural Top-3 >= 90%`, `terraform 100%`, `0%` neutral FP), and write `.agents/scratch/v9-10-0-mitigation-57-sweep.json` (`"resolved": 57`)

## Next

- Wave 10 (Phase 20, after Phase 19): Isolated 11-row `fyr eval` & independent `reviewer`/`security` close (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

None.
