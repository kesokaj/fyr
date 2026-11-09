# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2 — Build the 3 Mock Project Generators in `.agents/eval/fixture.sh` & Multi-Turn Scenario Proof Suite (`Mock 1` & `Mock 2`) (`PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj`, after: Phase 1)
- [ ] 2.1 Add `mock-ledger-api`, `mock-cloud-worker`, and `mock-fleet-ops` subcommands to `.agents/eval/fixture.sh`.
- [ ] 2.2 In `cmd/fyr/mock_scenarios_test.go`, write `TestMockScenario1LedgerAPIZeroScaffoldLifecycle` replaying all 3 turns of **Mock Project 1**.
- [ ] 2.3 In `cmd/fyr/mock_scenarios_test.go`, write `TestMockScenario2CloudWorkerGovernanceAndSecurity` replaying all 3 turns of **Mock Project 2**.
- [ ] 2.4 Checkpoint: run `gofmt -l cmd internal && go vet ./... && go test -race ./cmd/fyr`, store the `change` record, and run `/fyr-handoff`.

## Next

- Phase 3 (`PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj`): Multi-Turn Scenario Proof Suite (`Mock 3: mock-fleet-ops` in `cmd/fyr/mock_scenario_fleet_test.go`) — Parallel Workers, Trajectory Anomalies, Dream/Retro & Auto.
- Phase 4 (`PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj`): Full Verification Gate (and optional live `GUIDE-eval.md` multi-turn scenario rows if requested).

## Backlog
