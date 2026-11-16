# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 6a — One background runner (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
files: `internal/auto/`, `internal/dream/`, `cmd/fyr/verb_dream.go`, `cmd/fyr/verb_uninstall.go`
- [ ] Merge the dream timer into `fyr auto` (one timer). Keep metrics ingest first and the dream's plan-mode, no-tool run — verify: `TestRunIngestsMetricsFirst`, `TestDreamTimerDryRun` (or its renamed successor), and the uninstall timer tests
- [ ] Checkpoint — evidence: `fyr auto --dry-run --home .agents/scratch/p6a`, and backup row counts equal live row counts

## Next

- Phase 6b — Deletion (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 7 — Release `v9.0.0` (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
