# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 3 — Deterministic gates and subagent pruning (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
files: `cmd/fyr/`, `internal/gate/`, `agents/`, `internal/harness/agents_test.go`, `context/`, `skills/` (about 15 files that reference the retired agents)

- [ ] `fyr verify` (`gofmt`, `vet`, `go test -race ./...`, `fyr check`, then write the tree-hash stamp) and `fyr gate <worktree>` (lease, numstat, added-line secret scan, trailers) — verify: temp-repo tests, including a must-fail case for each check — docs: `README.md`
- [ ] `fyr land <branch>` and `fyr close` (one handoff that embeds the verify digest; no deletion of old records) — verify: a temp-repo end-to-end test — docs: `README.md`
- [ ] Retire `changelog`, `bugs` and `triage`; scope `verifier` to e2e; update `agents_test.go:131-230` and every skill or context reference; switch the DoD to 3 steps — verify: `go test -race ./...`, `fyr check`, eval rows for the changed skills
- [ ] Checkpoint

## Next

- Phase 4 — Models extraction and native orchestration (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 5 — Isolation without Docker (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 6a — One background runner (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
