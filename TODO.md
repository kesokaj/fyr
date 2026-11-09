# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 3 — Multi-Turn Scenario Proof Suite (`Mock 3: mock-fleet-ops`) — Parallel Workers, Trajectory Anomalies, Dream/Retro & Auto (`PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj`, after: Phase 2)
- [ ] 3.1 In `cmd/fyr/mock_scenario_fleet_test.go`, write `TestMockScenario3FleetOpsWorkersTrajectoryAndLearningLoop` replaying **Mock Project 3**.
- [ ] 3.2 Checkpoint: run `gofmt -l cmd internal && go vet ./... && go test -race ./cmd/fyr`, store the `change` record, and run `/fyr-handoff`.

## Next

- Phase 4 (`PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj`): Full Verification Gate (and optional live `GUIDE-eval.md` multi-turn scenario rows if requested).

## Backlog
