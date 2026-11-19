# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Wave 3 (`Phase 3a`–`3c`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19` — Dead-code deletions (`25` items), `<=350`/`<=600` LOC splits, `BUG-559` Part 2, docs & `brain.db` sync + `BUG-546`/`BUG-559` graduation:

- [ ] Phase 3a (`internal/search/`, `internal/redact/`, `internal/dream/`, `internal/brain/`): delete dead exports/helpers, unify `OpenDBWithRetry`/`IsBusy`/`BoolInt` (`SD-S16`), split `hook.go`/`ratings_test.go` and all `>350` LOC files
- [ ] Phase 3b (`internal/isolate/`, `internal/models/`, `internal/harness/`, `internal/gate/`, `internal/check/`, `internal/auto/`, `internal/metrics/`, `cmd/fyr/`): fix `BUG-559` Part 2 (`worker_judge.go`), delete retired G4/G5/G7 & dead code, unify `trail`/shared helpers, split all `>350` LOC files
- [ ] Phase 3c (`SDD.md`, `TDD.md`, `Workflow.md`, `SECURITY.md`, `features/`, `brain.db` sync): fix `C45`–`C51` doc drift, graduate `BUG-546` & `BUG-559`, sync `GUIDE-onboarding`/`SPEC-fyr-metrics`/`PLAN-2026-11-14-2337` Phase 7 heading, Checkpoint

## Next

- Wave 4 (`Phase 4a`: `ADR-527`, `SPEC-fyr-delivery`, approved `v9.3.0` behavioral upgrades) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19`
- Wave 5a (`Phase 5a`: full `-race`/`-cover`/`GOOS=darwin go vet`/`fyr check`/`TestRecall`/scratch smoke/paired `fyr eval`/`TEST-*`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19`
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

- 2026-11-19: Post-`v9.3.0` (`PLAN-2026-11-19-0852`) `fyr report` + self-improvement upgrade — Pillar 1 (`metrics.db` v10 shape-only `background_runs`, sandbox filter + `--include-sandboxes`, window deltas, `DreamTrajectoryDigest` `kneeExceeded`, expanded `Summary.Numbers()`), Pillar 2 (Section 0 Scorecard + top-5 signals, Background Autonomy panel for `dream`/`auto`/`session_ratings`/`work/auto-*`/`PROPOSAL-*`, new zero-JS SVG charts suppressing empty Cost, `fyr report --json`, `fyr auto log`, daily HTML report in `fyr auto` keeping `hooksAny` sandboxing, and foreground-only `fyr report --web` serving the static report on `127.0.0.1` with random path token until `Ctrl-C`, superseding `ADR-513` in part via a new ADR after `ADR-527`), Pillar 3 (shape-only `DiagnosticReport` in `internal/metrics/diagnostics.go` with `signal_id` dedup consumed by `fyr report`, `fyr dream`, `fyr auto` `tasks.go`/`learning_input.go`, and `/fyr-retro`) (source: operator session `dea6a27a`, superseding `PLAN-2026-11-10-1540` daily HTML follow-up).
