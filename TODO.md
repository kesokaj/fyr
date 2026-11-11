# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Wave 2b (`/fyr-work` Docker `-r2` fix-up after `p3` PASS, `D19`, `D25`) — `PLAN-2026-11-11-0640-plan-fyr-autonomy-and-full-feature-use`: Phase 7 (`p7-r2`).

- [x] Phase 3 — Workers by default in the plan template (`skills/fyr-feature/SKILL.md`, `.agents/eval/GUIDE-eval.md`, `internal/eval/`)
- [ ] Phase 7 — Worker latency budget (`cmd/fyr/`, `internal/work/`, `internal/sandbox/`, `internal/metrics/`, `docker/work.Dockerfile`, `SDD.md`, `TDD.md`, `SPEC-fyr-work`)

## Next

- Wave 3 (Phase 4) — Always-on feature-routing rule (`rules/fyr-features.md`) + advisory vet routing (`internal/search/vet_route.go`)
- Phase 8 — Closing range review (`reviewer --code --docs`, `security`, `verifier`), `fyr models --refresh`, `fyr eval`, `dave` install + registration + dream timer, and `fyr report --md`
- Release (`/fyr-preflight v8.6.0`, `D21`) — Gate, annotated tag, GitHub release assets + checksums, and post-release `dave` reinstall

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
