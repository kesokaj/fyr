# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 3 — Wave 3a: Pinch-Point Characterization Tests, `internal/sqliteutil`, `init()` Removal & CLI Split (`D1`, `D4`)** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [ ] Slice `w3a-char-tests`: Feathers pinch-point characterization tests (`graph`, `isolate`, `metrics`, `search`) + split `workflows_test.go` & `TestEvalGate` (`D8`, `D9`) (`who: AI`)
- [ ] Slice `w3a-sqliteutil-init`: extract `internal/sqliteutil`, typed `search.ReadVetHistory` (zero cross-DB raw SQL), replace `auto->isolate` `init()` with `isolate.CapsuleDeps` (`C1`, `C3`) (`who: AI`)
- [ ] Slice `w3a-cli-verbs`: replace `ctx` God Struct + 9 `cmd/fyr` `init()`s with `runtimeCtx` + `buildVerbs()`; split `fyr search` into `search`/`hook`/`ast`/`vet` with `v9.x` shims (`D1`), mark `D4` deprecations, update `hooks.json` & regenerate `docs/cli-reference.md` (`C4`, `A4`) (`who: AI`)
- [ ] Checkpoint (Phase 3): gate & land sequentially, tighten `arch_fitness_test.go` allowlists, run `fyr verify`, update `TODO.md`, commit & push (`who: AI`)

## Next

- Phase 4 — Wave 3b: Hotspot Monolith Decomposition (`search`, `metrics`, `sim`, `isolate/acp`), Complexity Reduction & `t.Parallel()` (`PLAN-2026-11-27-craft-cleanup`)
- Workstream 2 — Automatic Antigravity Conversation Retention (`fyr conversations`, `ADR-538`)
- Release Preflight — `/fyr-preflight v9.14.0` once Workstream 1 + Workstream 2 verify green

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
