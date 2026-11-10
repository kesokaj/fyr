# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2 — Metrics Schema v6 (`turn_vets`, `work_waves`) & Second-Brain/Wave Ingest (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- [ ] Add `ProjectRoot` (`json:"project_root,omitempty"`) to `WaveStatus` in `internal/work/progress.go:39-65` (populated from `filepath.Clean(o.Root)`) and cover in `internal/work/progress_test.go` — verify: unit, `go test -race ./internal/work -run TestProgress`, who: AI — expect: `PASS`
- [ ] Add migration 6 (`turn_vets` and `work_waves` tables) in `internal/metrics/store.go:39-82`, implement `ingestSecondBrainAndWaves` in `internal/metrics/ingest_secondbrain.go` (wired into `metrics.Ingest` when `o.Source == ""`), and implement `SecondBrain`, `WorkSummary`, and `ProjectRow` aggregations in `internal/metrics/show_types.go`, `internal/metrics/show.go`, and `internal/metrics/show_secondbrain.go` — verify: unit, `go test -race ./internal/metrics/...`, who: AI — expect: `PASS`
- [ ] Checkpoint — expect: `go test -race ./internal/work/... ./internal/metrics/...` passes with 0 failures — evidence: command and exit status, date

## Next

- Phase 3 — `fyr report --project | --global` (`--md` & HTML), `ADR-516` & Docs (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- Phase 4 — `fyr-docs`, `/fyr-triage` & Bash Wizard Skills Integration (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- Phase 5 — End-to-End Verification, Reviews & Release Readiness (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)

## Backlog
