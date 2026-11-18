# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Wave 2 of `PLAN-2026-11-18-2104-plan-v9-2-zero-token-watchdog-cross-family-gate` (`Phase 2A | Phase 2B` parallel build — `PreToolUse`/`PostToolUse` Watchdog + CLI `watch`/`gate --judge`, and `fyr report` `M6–M7` + `H1` health dashboard):

- [ ] Phase 2A — Implement `hook_bash_guard.go`, `hook_watchdog.go`, `PostToolUse` (`hooksJSON` + `hook.go`), and wire `fyr watch` + `fyr gate/land --judge` in `cmd/fyr` (`after: Phase 1A, Phase 1B, Phase 1C`)
- [ ] Phase 2B — Implement `M6`, `M7` & `H1` `v9` health metrics across HTML/MD `fyr report` (`after: Phase 1B`)

## Next

- Wave 3 of `PLAN-2026-11-18-2104-plan-v9-2-zero-token-watchdog-cross-family-gate` (`Phase 3`: `PRD.md:54` new test pins, rules/skills, living docs sync, and DoD verification)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
