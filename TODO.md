# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 1 — Wave 1: Zero-Risk Deletions, Retire `fyr relay` (`D2`), Deduplication & Residue Cleanup** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [x] Slice `w1-retire-relay`: retire `fyr relay` (`internal/relay/**`, `cmd/fyr/verb_relay*.go`, `31 -> 30` verbs, `ADR-537` D2, `c6d8eeee`) (`who: AI`)
- [ ] Slice `w1-dead-search`: delete 13 zero-caller unexported funcs, `ineffassign`, prod `unparam`, dead `GraphEdge` fields, and superseded `internal/search` zombies (`who: AI`)
- [ ] Slice `w1-dead-other-dedup`: delete remaining dead/zombie code across `internal/{metrics,brain,graph,auto,dream,isolate,watch,models}`, replace `runtime.GOROOT()`, unify UTF-8/`capped*` into `internal/trail` (`who: AI`)
- [ ] Slice `w1-workflows-skills-residue`: dedup `workflows/*.py`, fold `fyr-feedback-log` (`D6`), trim `agents/{verifier,reviewer,security}`, fix `SECURITY.md`, archive `TEST_PLAN.md`, prune brittle prose-pins (`who: AI`)
- [ ] Checkpoint (Phase 1): `fyr land --judge` sequentially -> tighten Wave 0 ratchets -> fix 4 `SPEC-fyr-*` Failure-modes tables -> `fyr verify` + `golangci-lint run` (`who: AI`)

## Next

- Phase 2 — Wave 2: Documentation Bonsai (`429 KB -> ~176 KB`), Generated `docs/cli-reference.md`, `11/11` STE `>= 80%`, `PRD.md` Tightening (`D7`) (`PLAN-2026-11-27-craft-cleanup`)
- Phase 3 — Wave 3a: Pinch-Point Characterization Tests, `internal/sqliteutil`, Remove `init()`s & Split `fyr search` (`D1`, `D4`) (`PLAN-2026-11-27-craft-cleanup`)
- Phase 4 — Wave 3b: Hotspot Monolith Decomposition (`search`, `metrics`, `sim`, `isolate/acp`), Complexity Reduction & `t.Parallel()` (`PLAN-2026-11-27-craft-cleanup`)

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
