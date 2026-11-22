# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Wave 2 — Phases 2–5 (parallel after Phase 1)** (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic` — 20-phase `/fyr-swarm` mitigation of `v9.10.0` findings `F-001`–`F-057`, `ADR-534`):
- [ ] Phase 2 — WS1a: `PreInvocation` parallel latency (`brainCh <= 50ms`), incremental dirty index, `InvocationNum` mid-turn dedup, append-only `CHECKPOINT` compaction & `no-hooks` hardening (`F-001`, `F-002`, `F-003`, `F-020`, `F-024`, `F-027`, `F-057`)
- [ ] Phase 3 — WS4: `1 MiB` changelog streaming, `dev/fyr-eval` merge, role-partitioned Pareto scoring, smooth convex failover & context budgets (`F-025`, `F-034`, `F-052`, `F-053`, `F-054`, `F-055`)
- [ ] Phase 4 — WS3: `fyr auto` per-pool G9 quota & `halt_reason`, shared ephemeral filter, `GUIDE-eval` feasibility/planner, `installPending` banner, `dream` `kindSIM` `f.room` & `remote-control serve` (`F-004`, `F-005`, `F-045`, `F-046`, `F-050`)
- [ ] Phase 5 — WS7a: `unmigratedDocsDir` legacy-only check, symmetric `operator` provenance immutability, `[STALE]` display & validity curve calibration (`F-026`, `F-047`, `F-048`)

## Next

- Wave 3 (Phases 6, 7, 9, 10, 11, parallel after Wave 2): WS1b/WS6a vet & dynamic BM25 prefetch, WS2a/WS8a workflow allowlist/clamp/bash/watchdog, WS5a/b/c metrics truth, diagnostics & report KPIs (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Waves 4–10 (Phases 8, 12–20): WS2b/WS7c gate/relay/isolate, watch `<25ms`, CLI ergonomics & 31/31 `--json`, `fyr check` 8/8, skill diet, living docs, 57-repro sweep, isolated `fyr eval` & review/security close (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

None.
