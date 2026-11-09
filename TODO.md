# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 4 — Optional Live `GUIDE-eval.md` Multi-Turn Scenario Rows & Full Verification Gate (`PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj`, after: Phase 3)
- [ ] 4.1 If approved by the operator, add 3 multi-turn capability rows (`scenario-ledger-mvp`, `scenario-worker-adr-fence`, `scenario-fleet-trajectory`) to `.agents/eval/GUIDE-eval.md` (with matching pins in `internal/eval/suite_test.go` and good/bad outcomes in `internal/eval/suite_outcomes_test.go`).
- [ ] 4.2 Run the full project verification suite (`gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...`, `"$(go env GOPATH)/bin/govulncheck" ./...`, `fyr check --home ~`), dispatch `verifier` and `reviewer` (`--code --docs`), and close the plan with `/fyr-handoff`.

## Next

## Backlog
