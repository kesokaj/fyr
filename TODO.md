# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 2 — Wave 2: Documentation Bonsai, SSOT Generation & `PRD.md` Tightening (`D7`)** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [x] Slice `w2-cli-gen`: deterministic `docs/cli-reference.md` generator + `TestCLIDocsFresh` and `docs/glossary.md` (`10114501`, `E1`, `E5`, `E7`) (`who: AI`)
- [x] Slice `w2-prd-sdd`: tightened `PRD.md` (`32.8 -> 16.1 KB`, `D7`) and `SDD.md` (`49.0 -> 15.7 KB`, 100% STE, C4 L2/L3) (`0f028ab0`, `E2`, `E4`, `E6`) (`who: AI`)
- [x] Slice `w2-readme-workflow`: shrank `README.md` (`43.8 -> 15.8 KB`, `TestHarnessMap` green) and `Workflow.md` (`34.9 -> 14.6 KB`, 100% STE) (`cf8a900a`, `E1`, `E2`, `E5`, `E9`) (`who: AI`)
- [x] Slice `w2-tdd-features`: shrank `TDD.md` (`116.4 -> 27.4 KB`) and all 7 `features/*.md` (`102.8 -> 59.4 KB`) to `0` `fyr check` findings & `0` STE notes across `13/13` Track 1 docs (`9b50c045`, `fc99b50e`, `E1`, `E2`, `E4`, `E7`) (`who: AI`)
- [ ] Checkpoint (Phase 2): add Track 1 STE ratchet test (`arch_fitness_test.go`, keeping consumer advisory contract) -> `/fyr-review --docs` over `706485c0..HEAD` -> `fyr verify` -> tick Phase 2 + update `TODO.md` (`who: AI`)

## Next

- Phase 3 — Wave 3a: Pinch-Point Characterization Tests, `internal/sqliteutil`, Remove `init()`s & Split `fyr search` (`D1`, `D4`) (`PLAN-2026-11-27-craft-cleanup`)
- Phase 4 — Wave 3b: Hotspot Monolith Decomposition (`search`, `metrics`, `sim`, `isolate/acp`), Complexity Reduction & `t.Parallel()` (`PLAN-2026-11-27-craft-cleanup`)
- Workstream 2 — Automatic Antigravity Conversation Retention (`fyr conversations`, `ADR-538`) + `/fyr-preflight v9.14.0`

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
