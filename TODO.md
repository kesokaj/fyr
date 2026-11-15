# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 5 — Isolation without Docker (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
files: `internal/isolate/`, `internal/eval/`, `internal/auto/eval_gate.go`, `internal/auto/eval_exec.go`, `internal/auto/worker.go`, `cmd/fyr/eval.go`
- [ ] `internal/isolate` per D6. Pass criterion: a write outside the scratch home fails with EACCES, and the real `$HOME` is unchanged — verify: `go test -race ./internal/isolate/...`, plus a must-fail test with Landlock off
- [ ] Move `internal/eval`, `internal/auto` (eval gate, eval exec, worker) and `cmd/fyr/eval.go` off `internal/sandbox` behind a runner switch, keeping Docker as the default — verify: `TestEvalRefusesWithoutHome` and `TestEvalRefusesItsHome` still pass
- [ ] Dual-run: 5 real eval rows give the same verdict on both runners. Then make `isolate` the default — verify: 5 of 5 match
- [ ] Checkpoint

## Next

- Phase 6a — One background runner (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 6b — Deletion (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 7 — Release `v9.0.0` (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
