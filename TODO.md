# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 0 — `PRD.md` amendment, `ADR-523`, and build prep (`PLAN-2026-11-16-1307-plan-dreaming-only-fyr-auto-and-validity-scored`)
- [x] On operator approval (`go all approved` / `go v9.1.0`), store `ADR-523` (`fyr record add adr`), apply the approved `PRD.md` edits (`R0`, `R3`, `R7`, `R8`, `R9`, keeping `enforced by:` HTML comments on currently-declared test names until Phase 3b), turn `fyr auto off` in `~` during build, and confirm `fyr check` exits 0
- [x] Checkpoint — `PRD.md`, `TODO.md`, and `ADR-523` stored and verified (`fyr check` exit 0, `fyr verify` green)

## Next

- Phase 1–4 of `PLAN-2026-11-16-1307-plan-dreaming-only-fyr-auto-and-validity-scored` (multi-part validity-scored memory, progressive `PreInvocation` + trajectory digest, dreaming-only `fyr auto`, `/fyr-preflight v9.0.1`)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro (earliest 2026-11-23)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
