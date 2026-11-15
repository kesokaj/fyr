# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2a — Read engine and hook safety (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
files: `internal/search/` (hook, clamp, ledger, symbols, slice, guard files)

- [ ] Find out why the graph does not reduce reads, before any other read-engine work (from Phase 0b `RETRO-2026-11-15-0102-graph-a-b-baseline`) — verify: root cause and per-source read/token breakdown recorded in a D-row
- [ ] Give every hook entry a hard deadline (about 200 ms) that falls back to `allow`, and add a global off switch `~/.config/fyr/no-hooks` next to the per-checkout one — verify: a test with a slow stub finishes under 250 ms and returns allow
- [ ] Make the read ledger persistent and keyed by hash. A stamp survives a blocked read and is invalidated only by a content-hash change or an overlapping edit (`read_ledger.go:162-171, :234`) — verify: table tests for the 2nd and 3rd reads, an edit, and an external checkout
- [ ] Add the skeleton-first clamp from D7 (`hook_clamp.go`), a Markdown heading indexer (`symbols_md.go`) and an FTS5 fallback when `rankAndCheckConfidence` (`hook.go:375-403`) has 0 pivots — verify: a natural-language prompt gets more than 0 pivots; an unranged read of a 600-line file is denied with a skeleton, and a ranged retry is allowed
- [ ] Checkpoint — evidence: `go test -race ./internal/search/...`, then a real-home install from a clean, pushed tree, then one real session smoke test

## Next

- Phase 2b — Hook events and routing (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 3 — Deterministic gates and subagent pruning (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 4 — Models extraction and native orchestration (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
