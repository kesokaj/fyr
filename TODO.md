# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 4 — Models extraction and native orchestration (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
files: `internal/models/`, `internal/work/`, `cmd/fyr/verb_models.go`, `cmd/fyr/run_dispatch.go`, `internal/auto/usage.go`, `internal/auto/eval_gate.go`, `workflows/`, `skills/fyr-swarm/`, `skills/fyr-review/`, `skills/fyr-preflight/`
- [ ] Move the model catalog and quota picker into `internal/models` and re-point every importer — verify: `go build ./... && go test -race ./...`; `go list -deps ./cmd/fyr | grep internal/work` is reduced to the `verb_work` path only
- [ ] Two workflow scripts, `review_parallel.py` and `preflight_gate.py` — verify: a real `run_workflow` run over a scratch diff returns schema-valid results
- [ ] `skills/fyr-swarm/SKILL.md` (routing table, `Files:` brief, gate and land); `/fyr-review` and `/fyr-preflight` call the workflows — verify: eval rows for the changed skills
- [ ] Checkpoint

## Next

- Phase 5 — Isolation without Docker (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 6a — One background runner (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 6b — Deletion (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
