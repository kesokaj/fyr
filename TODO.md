# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Wave 4 — Unified Docs, Traces, Review, Security & Close (`PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`)
- [ ] Sync `README.md`, `features/dream-and-auto.md`, `SDD.md`, `TDD.md`, `PRD.md` (`R0`/`R3` `enforced by:` test names), `SPEC-fyr-pre-hook-coordinator`, and deterministic `Now` pin in `cmd/fyr/verb_search_hook_test.go`
- [ ] Run independent `reviewer` (`--code --docs`) and `security` audits over full diff, resolve findings, scratch `install`/`status` check, bare `/home/kesokaj/.local/bin/fyr verify` (`0 findings`), store `change`, push `main`, and close plan + handoff via `fyr close`

## Next

- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest 2026-11-23)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
