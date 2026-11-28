# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 3 — Wave 3a: Pinch-Point Characterization Tests, `internal/sqliteutil`, `init()` Removal & CLI Split (`D1`, `D4`)** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [x] Slice `w3a-sqliteutil-init`: extracted `internal/sqliteutil`, typed `search.ReadVetHistoryForIngest` (zero raw `vet_history` SQL in `metrics`), replaced `auto->isolate` `init()` with explicit `isolate.CapsuleDeps` (`da3fb281`, `2915d288`, `C1`, `C3`) (`who: AI`)
- [x] Slice `w3a-char-tests`: package char tests + `TestEvalGate`/`TestKickoffGuard*` (`b03ac226`) and 4-file `workflows_test.go` split + `areas.md` (`5b090c4c`, `D8`, `D9`) (`who: AI`)
- [ ] Slice `w3a-cli-verbs`: all 8 prod `cmd/fyr` `init()`s removed (`d3cfc2de`) and all 50+ per-verb flag fields migrated off `runtimeCtx` into per-verb option structs + `verb.writesState` (`1a3715d3`, `7aefe58c`, `69d5f1fa`); remaining: drop 1-line `type ctx = runtimeCtx` alias across 9 trivial files + cache `c.opts` nil fallback in `recordOptsOf`/`reportOptsOf`, implement `ADR-537` D1 (`fyr hook`/`ast`/`vet` + `v9.x` `fyr search` shims + `hooks.json`), mark `D4` deprecations, regenerate `docs/cli-reference.md`, sync `SDD.md`/`TDD.md` (`C4`, `A4`) (`who: AI`)
- [ ] Checkpoint (Phase 3): gate & land remaining `w3a-cli-*` slices sequentially, tighten `arch_fitness_test.go` allowlists (0 `init()` in `cmd/fyr` & `internal/auto`, `dupHelperCeilingTable`, `sqliteAllowlist`), run `fyr verify`, update `TODO.md`, commit & push (`who: AI`)

## Next

- Phase 4 — Wave 3b: Hotspot Monolith Decomposition (`search`, `metrics`, `sim`, `isolate/acp`), Complexity Reduction & `t.Parallel()` (`PLAN-2026-11-27-craft-cleanup`)
- Workstream 2 — Automatic Antigravity Conversation Retention (`fyr conversations`, `ADR-538`)
- Release Preflight — `/fyr-preflight v9.14.0` once Workstream 1 + Workstream 2 verify green

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
