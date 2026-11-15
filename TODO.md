# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2b — Hook events and routing (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
files: `internal/search/` (vet, route, subagent, hook files), `internal/harness/plan.go`

- [ ] Render the `SessionStart` event (a capsule of at most 1.5 KB with date, plan, Now and status) and a `run_workflow` matcher in `internal/harness/plan.go:191-198` — verify: the rendered `hooks.json` golden test; capsule ≤ 1,500 B
- [ ] Banners go through `metadata.user_markdown` with an empty `systemMessage`. `IntentQuestion` never sets `SecurityRequired` (`vet_skills.go:21`), and `vet_route.go` drops its `/fyr-work` routes — verify: `go test -race ./internal/search/...`
- [ ] Route `inferSwarmRole` by name prefix only, with the 20%/30% quota fallback (`hook_subagent.go:72-164`). Add the `Stop` stamp check from D2 — verify: `Stop` returns `continue` once on a dirty tree with a stale stamp and allows a matching stamp; latency stays under 250 ms
- [ ] Checkpoint — evidence: tests, a real-home install, one real session smoke test

## Next

- Phase 3 — Deterministic gates and subagent pruning (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 4 — Models extraction and native orchestration (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 5 — Isolation without Docker (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
