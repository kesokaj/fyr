# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

- [ ] Wave 3 (Phase 3b) of `PLAN-2026-11-20-1153-plan-fyr-v9-4-0-self-improvement-diagnostics-web`: `fyr report` upgrade (`Section 0` Scorecard, Background Autonomy, empty Cost SVG suppression, `--json`, `--web`, `--listen`, `--include-sandboxes`), `fyr auto log`, daily HTML report, `RecordBackgroundRun` wiring, and `help_text`/`check` updates

## Next

- [ ] Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)
- [ ] Phase 4a of `PLAN-2026-11-20-1153-plan-fyr-v9-4-0-self-improvement-diagnostics-web`: `fyr graph` core & telemetry constellation
- [ ] Phase 4b of `PLAN-2026-11-20-1153-plan-fyr-v9-4-0-self-improvement-diagnostics-web`: docs, living specs, reviews & Playwright E2E

## Backlog

- 2026-11-19: Post-`v9.3.0` (`PLAN-2026-11-19-0852`) `fyr report` + self-improvement upgrade — Pillar 1 (`metrics.db` v10 shape-only `background_runs`, sandbox filter + `--include-sandboxes`, window deltas, `DreamTrajectoryDigest` `kneeExceeded`, expanded `Summary.Numbers()`), Pillar 2 (Section 0 Scorecard + top-5 signals, Background Autonomy panel for `dream`/`auto`/`session_ratings`/`work/auto-*`/`PROPOSAL-*`, new zero-JS SVG charts suppressing empty Cost, `fyr report --json`, `fyr auto log`, daily HTML report in `fyr auto` keeping `hooksAny` sandboxing, and foreground-only `fyr report --web` serving the static report on `127.0.0.1` with random path token until `Ctrl-C`, superseding `ADR-513` in part via a new ADR after `ADR-527`), Pillar 3 (shape-only `DiagnosticReport` in `internal/metrics/diagnostics.go` with `signal_id` dedup consumed by `fyr report`, `fyr dream`, `fyr auto` `tasks.go`/`learning_input.go`, and `/fyr-retro`) (source: operator session `dea6a27a`, superseding `PLAN-2026-11-10-1540` daily HTML follow-up).
