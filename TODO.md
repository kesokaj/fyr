# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 3 — Wave 3a: Pinch-Point Characterization Tests, `internal/sqliteutil`, `init()` Removal & CLI Split (`D1`, `D4`)** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [x] Slice `w3a-sqliteutil-init`: extracted `internal/sqliteutil`, typed `search.ReadVetHistoryForIngest` (zero raw `vet_history` SQL in `metrics`), replaced `auto->isolate` `init()` with explicit `isolate.CapsuleDeps` (`da3fb281`, `2915d288`, `C1`, `C3`) (`who: AI`)
- [ ] Slice `w3a-char-tests`: package char tests + `TestEvalGate`/`TestKickoffGuard*` splits landed (`b03ac226`); remaining: split `workflows_test.go` + update `areas.md` (`D8`, `D9`) (`who: AI`)
- [ ] Slice `w3a-cli-verbs`: all 8 prod `cmd/fyr` `init()`s removed via lazy `allVerbs()` (`d3cfc2de`); remaining: replace `ctx` God Struct with `runtimeCtx` + per-verb opts, split `fyr search` into `search`/`hook`/`ast`/`vet` with `v9.x` shims (`D1`), mark `D4` deprecations, update `hooks.json` & regenerate `docs/cli-reference.md` (`C4`, `A4`) (`who: AI`)
- [ ] Checkpoint (Phase 3): gate & land Sub-wave 3a-2 sequentially, tighten `arch_fitness_test.go` allowlists (0 `init()` in `cmd/fyr` & `internal/auto`), run `fyr verify`, update `TODO.md`, commit & push (`who: AI`)

## Next

- Phase 4 — Wave 3b: Hotspot Monolith Decomposition (`search`, `metrics`, `sim`, `isolate/acp`), Complexity Reduction & `t.Parallel()` (`PLAN-2026-11-27-craft-cleanup`)
- Workstream 2 — Automatic Antigravity Conversation Retention (`fyr conversations`, `ADR-538`)
- Release Preflight — `/fyr-preflight v9.14.0` once Workstream 1 + Workstream 2 verify green

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
