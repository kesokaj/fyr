# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 0 — Wave 0: Tooling, Architecture Fitness Gates, Latent Bugs & Fast Deterministic Tests** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [ ] Slice `w0-gates`: `.golangci.yml`, 4 Go fitness tests (`arch_fitness_test.go`, `areas_test.go`, `funclen_test.go`), `areas.md` parity, per-verb `fyr check` flags (`who: AI`)
- [ ] Slice `w0-bugs`: fix unrun `testSIMEvalExecPolicyModel`, `SA4004` loop break (`collect_sources.go`), divergent `isAuditSubagentRole`, unused `ctx`/`since` (`who: AI`)
- [ ] Slice `w0-fast-tests`: fix 309s `TestRunUnreadableAnswer`, 61s `SIGHUP` test, flaky `< 5ms` `TestPersonalizedPageRank` + sub-50ms timers, duplicate `isolate`/`gate`/`search` fixture builds (`who: AI`)
- [ ] Checkpoint (Phase 0): `fyr gate` -> sequential `fyr land` -> `fyr verify` green in `< 5 min` (`who: AI`)

## Next

- Phase 1 — Wave 1: Zero-Risk Deletions, Retire `fyr relay` (`D2`), Helper/Workflow Deduplication & Residue Cleanup (`PLAN-2026-11-27-craft-cleanup`)
- Phase 2 — Wave 2: Documentation Bonsai (`429 KB -> ~176 KB`), Generated `docs/cli-reference.md`, `11/11` STE `>= 80%`, `PRD.md` Tightening (`D7`) (`PLAN-2026-11-27-craft-cleanup`)
- Phase 3 — Wave 3a: Pinch-Point Characterization Tests, `internal/sqliteutil`, Remove `init()`s & Split `fyr search` (`D1`, `D4`) (`PLAN-2026-11-27-craft-cleanup`)

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
