# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 5 — End-to-End Verification, Reviews & Release Readiness (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- [x] Run full test suite (`gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...`), `fyr check`, `"$(go env GOPATH)/bin/govulncheck" ./...`, and E2E smoke test of `fyr install`, `fyr metrics ingest`, `fyr report --global --md`, `fyr report --project --out`, and `fyr report --project --global` (exit 2) against `.agents/scratch/home-e2e` — verify: e2e, `go test -race -timeout 30m ./... && go run ./cmd/fyr check --home .agents/scratch/home-e2e`, who: AI — expect: `0` findings and exit status `0`
- [ ] Dispatch `reviewer` (`--code --docs`), `security`, and `verifier` over the full feature diff, resolve any findings, run `fyr install` into `~` (`DEPLOY-local` standing permission), and close the plan and handoff — verify: e2e, `~/.local/bin/fyr status --home ~`, who: AI — expect: `drift none`
- [ ] Checkpoint — expect: `verifier` `PASS`, `reviewer` `APPROVE`, `security` `CLEAR`, `fyr status --home ~` `drift none` — evidence: command and exit status, date

## Next

## Backlog
