# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 1 — `metrics.db` v14 Schema, Portable State v14 & Shared Foundations** (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic` — 20-phase `/fyr-swarm` mitigation of `v9.10.0` findings `F-001`–`F-057`, `ADR-534`):
- [ ] Add migration #14 in `internal/metrics/store.go` (`background_runs.halt_reason`, `turn_vets.suggested_skills`, `trials.{skill,arm,tokens,defects}`, `orchestration_runs` table + `tables` export list) and `OrchestrationRunRow` CRUD in `internal/metrics/orchestration_runs.go`, plus `HaltReason` in `background_runs.go`
- [ ] Update `internal/metrics/state_io*.go`, `internal/metrics/numbers.go`, and `internal/state/{manifest,export,import}.go` (`MaxMetricsSchema = 14`, `ManifestCounts`, v13 backward-compatible import, future v15 refusal)
- [ ] Checkpoint — `metrics.db` v14 migration and `internal/state` v14 round-trip (plus v13 compatibility) pass `go test -race ./internal/metrics ./internal/state`

## Next

- Wave 2 (Phases 2–5, parallel after Phase 1): WS1a hook latency/compaction, WS4 model intelligence/changelog, WS3 auto/dream/SIM unblock, WS7a brain guard & validity (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Waves 3–10 (Phases 6–20): WS1b/WS2/WS5/WS6/WS7b/WS8, living docs, 57-repro sweep, isolated `fyr eval` & review/security close (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

None.
