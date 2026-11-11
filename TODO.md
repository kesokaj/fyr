# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Wave 1 (`/fyr-work` Docker waves after Phase 1 bootstrap, `D19`, `D20`) — `PLAN-2026-11-11-0640-plan-fyr-autonomy-and-full-feature-use`: Phases 2, 5, 6, 9 in parallel.

- [ ] Phase 2 — Autonomy model in context and workflow skills (`context/AGENTS.md`, `skills/fyr-feature/`, `skills/fyr-work/`, `rules/session-sizing.md`, `GUIDE-eval`)
- [ ] Phase 5 — Trigger-coverage KPIs in `fyr report` (`internal/metrics/`, `TDD.md`, `SPEC-fyr-metrics`)
- [ ] Phase 6 — Read ledger: diagnose and fix `0` blocks saved (`internal/search/`)
- [ ] Phase 9 — Live model catalog: `fyr models` and evidence-judged routing (`cmd/fyr/`, `internal/work/`, `internal/search/process_artifact*`, `rules/agy-dispatch.md`, `README.md`, `SPEC-fyr-work`, `SPEC-fyr-delivery`)

## Next

- Wave 2 (Phases 3, 7) — Workers by default in plan template (`→ T2, T13`) and worker latency budget (`→ T10, T17, T18`)
- Wave 3 (Phase 4) — Always-on feature-routing rule (`rules/fyr-features.md`) + advisory vet routing (`internal/search/vet_route.go`)
- Phase 8 — Closing range review (`reviewer --code --docs`, `security`, `verifier`), `fyr models --refresh`, `fyr eval`, `dave` install + registration + dream timer, and `fyr report --md`

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
