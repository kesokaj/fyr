# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Wave 2 (`Phase 2a`–`2c`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19` — Core CLI/gate/check/brain/metrics fixes, workflow/agent/context/rule alignment & skill/eval fixes:

- [ ] Phase 2a (`internal/brain/`, `internal/check/`, `internal/gate/`, `internal/metrics/`, `cmd/fyr/`): CLI shortHash/models/record/zip fixes (`IN65`–`IN68`, `IN70`–`IN71`, `IN74`–`IN78`, `TR27`), gate/check fixes & `BUG-559` Part 1 (`IN64`, `IN87`–`IN91`, `IN105`), brain fixes (`SD106`–`SD113`), metrics fixes & N+1 elimination (`SD122`–`SD139`)
- [ ] Phase 2b (`workflows/`, `workflows_test.go`, `agents/`, `context/`, `rules/`, `.agents/rules/`): harden `preflight_gate.py`/`review_parallel.py` (`AG30`, `AG33`–`AG35`, `AG-S2`), align `agents/*/agent.md` (`AG31`–`AG32`, `AG38`–`AG39`, `AG-S3`), recover `TestAlwaysOnContextBudget` headroom & fix `.agents/rules/` (`C53`, `C-S9`–`C-S10`)
- [ ] Phase 2c (`after: Phase 2b`; `skills/`, `.agents/eval/`, `internal/eval/`, `areas_test.go`, `skills_text_test.go`, `ui_skills_test.go`): workflow & technical skill fixes (`WS55`–`WS67`, `WS-S8`–`WS-S10`, `TS45`–`TS52`, `TS-S3`–`TS-S4`), `VENDORED.md`/`areas.md` sync (`AG37`), `GUIDE-eval.md` `dreamer` row (`AG36`), Checkpoint

## Next

- Wave 3 (`Phase 3a`–`3c`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19` (`25` dead-code deletions, `>350`/`>600` LOC splits, `BUG-559` Part 2, doc/record sync)
- Wave 4–5a (`Phase 4a`, `Phase 5a`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19` (`v9.3.0` behavioral upgrades, `ADR-527`, full `-race`/`-cover`/`TestRecall`/paired `fyr eval` verification)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

- 2026-11-19: Post-`v9.3.0` (`PLAN-2026-11-19-0852`) `fyr report` + self-improvement upgrade — Pillar 1 (`metrics.db` v10 shape-only `background_runs`, sandbox filter + `--include-sandboxes`, window deltas, `DreamTrajectoryDigest` `kneeExceeded`, expanded `Summary.Numbers()`), Pillar 2 (Section 0 Scorecard + top-5 signals, Background Autonomy panel for `dream`/`auto`/`session_ratings`/`work/auto-*`/`PROPOSAL-*`, new zero-JS SVG charts suppressing empty Cost, `fyr report --json`, `fyr auto log`, daily HTML report in `fyr auto` keeping `hooksAny` sandboxing, and foreground-only `fyr report --web` serving the static report on `127.0.0.1` with random path token until `Ctrl-C`, superseding `ADR-513` in part via a new ADR after `ADR-527`), Pillar 3 (shape-only `DiagnosticReport` in `internal/metrics/diagnostics.go` with `signal_id` dedup consumed by `fyr report`, `fyr dream`, `fyr auto` `tasks.go`/`learning_input.go`, and `/fyr-retro`) (source: operator session `dea6a27a`, superseding `PLAN-2026-11-10-1540` daily HTML follow-up).
