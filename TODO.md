# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Wave 3 — Parallel Runtime & Hook Docs (`PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`)
#### Phase 3A — Unified Binary On/Off `fyr auto` Engine & CLI
- [ ] Switch `internal/auto/` + `cmd/fyr/` + `skills/fyr-auto/SKILL.md` to binary `on | off` (`--conversation-idle`, default `1h`; legacy `auto.json` and deprecated flag compat; retire `CheckHarness` and `G4`/`G5`/`G6`/`G7` for dreaming while retaining `G3`, `G9`, and clean-git check for branch workers; `TestAutoOnBinarySwitchAndAllProjectsSweep`)
#### Phase 3B — Hook Feature Guide, `/fyr-swarm` Docs & CLI Hook E2E Smoke
- [ ] Update `features/hooks-and-ast-graph.md` and `skills/fyr-swarm/SKILL.md` for `schemaVersion = "7"`, `skillDigests`, `subtasks`, `COORDINATE_SUBTASKS`, `[FYR_WORKER_CAPSULE]`, and `[COORDINATOR_GUARD]`, and run `< 200 ms` scratch stdin E2E hook smoke (`--home .agents/scratch/hook-smoke`)

## Next

- Wave 4 (Phase 4 unified `PRD`/`SDD`/`TDD`/`README`/`features/dream-and-auto.md` + `SPEC-fyr-pre-hook-coordinator` test trace updates, independent `reviewer --code --docs` + `security` audits, `/home/kesokaj/.local/bin/fyr verify`, `change`, push & `fyr close`) of `PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest 2026-11-23)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
