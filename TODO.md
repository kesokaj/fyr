# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 2 — Wave 2: Documentation Bonsai, SSOT Generation & `PRD.md` Tightening (`D7`)** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [ ] Slice `w2-cli-gen`: build deterministic `docs/cli-reference.md` generator + `TestCLIDocsFresh` and concise `docs/glossary.md` (`E1`, `E5`, `E7`) (`who: AI`)
- [ ] Slice `w2-prd-sdd`: tighten `PRD.md` (`32 KB -> ~14 KB`, `ADR-537` D7) and shrink `SDD.md` (`49 KB -> ~16 KB`) with C4 diagrams + 1-line component table (`E2`, `E4`, `E6`) (`who: AI`)
- [ ] Slice `w2-readme-workflow`: shrink `README.md` (`43.2 KB -> ~14 KB`, `TestHarnessMap` green) and `Workflow.md` (`36.5 KB -> ~14 KB`) (`E1`, `E2`, `E5`, `E9`) (`who: AI`)
- [ ] Slice `w2-tdd-features`: shrink `TDD.md` (`119 KB -> ~28 KB`) and all 7 `features/*.md` (`104.3 KB -> ~62.5 KB`) to `>= 80%` STE on 11/11 Track 1 docs (`E1`, `E2`, `E4`, `E7`) (`who: AI`)
- [ ] Checkpoint (Phase 2): `fyr land --judge` sequentially -> promote STE threshold ratchet in `internal/check/style.go` -> `/fyr-review --docs` -> `fyr verify` -> update `TODO.md` (`who: AI`)

## Next

- Phase 3 — Wave 3a: Pinch-Point Characterization Tests, `internal/sqliteutil`, Remove `init()`s & Split `fyr search` (`D1`, `D4`) (`PLAN-2026-11-27-craft-cleanup`)
- Phase 4 — Wave 3b: Hotspot Monolith Decomposition (`search`, `metrics`, `sim`, `isolate/acp`), Complexity Reduction & `t.Parallel()` (`PLAN-2026-11-27-craft-cleanup`)
- Workstream 2 — Automatic Antigravity Conversation Retention (`fyr conversations`, `ADR-538`) + `/fyr-preflight v9.14.0`

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
